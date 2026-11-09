// Package quotes fetches market data over HTTP: daily prices and fund
// profiles from Alpha Vantage, exchange rates from Frankfurter. Responses are
// parsed strictly; anything unexpected is an error and nothing is stored.
package quotes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tiliavir/allmend/internal/finanzen"
)

var client = &http.Client{Timeout: 30 * time.Second}

func get(ctx context.Context, c *http.Client, u string, limit int64) ([]byte, error) {
	if c == nil {
		c = client
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		// The URL may carry the API key: report only the error kind.
		var ue *url.Error
		if errors.As(err, &ue) {
			return nil, fmt.Errorf("request failed: %w", ue.Err)
		}
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, errors.New("response too large")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return body, nil
}

// ── Alpha Vantage ─────────────────────────────────────

// AlphaVantage implements finanzen.Quotes.
type AlphaVantage struct {
	BaseURL string // default https://www.alphavantage.co
	Key     string
	Client  *http.Client
}

func (a *AlphaVantage) query(ctx context.Context, function, symbol string, extra url.Values) (map[string]json.RawMessage, error) {
	base := a.BaseURL
	if base == "" {
		base = "https://www.alphavantage.co"
	}
	q := url.Values{"function": {function}, "symbol": {symbol}, "apikey": {a.Key}}
	for k, v := range extra {
		q[k] = v
	}
	body, err := get(ctx, a.Client, base+"/query?"+q.Encode(), 8<<20)
	if err != nil {
		return nil, err
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("unexpected response: %w", err)
	}
	for _, k := range []string{"Note", "Information", "Error Message"} {
		if raw, ok := m[k]; ok {
			var s string
			_ = json.Unmarshal(raw, &s)
			return nil, fmt.Errorf("Alpha Vantage: %s", s)
		}
	}
	return m, nil
}

// Daily returns the last ~100 daily closes (compact output, free tier).
func (a *AlphaVantage) Daily(ctx context.Context, symbol string) ([]finanzen.Price, error) {
	m, err := a.query(ctx, "TIME_SERIES_DAILY", symbol, url.Values{"outputsize": {"compact"}})
	if err != nil {
		return nil, err
	}
	raw, ok := m["Time Series (Daily)"]
	if !ok {
		return nil, errors.New("response has no daily time series")
	}
	var series map[string]map[string]string
	if err := json.Unmarshal(raw, &series); err != nil {
		return nil, fmt.Errorf("unexpected time series: %w", err)
	}
	return ParseDaily(series)
}

// ParseDaily validates and sorts a daily series (date → fields with "4. close").
func ParseDaily(series map[string]map[string]string) ([]finanzen.Price, error) {
	if len(series) == 0 {
		return nil, errors.New("empty time series")
	}
	out := make([]finanzen.Price, 0, len(series))
	for d, f := range series {
		if !finanzen.ValidDate(d) {
			return nil, fmt.Errorf("invalid date %q", d)
		}
		s, ok := f["4. close"]
		if !ok {
			return nil, fmt.Errorf("%s: no close", d)
		}
		c, err := strconv.ParseFloat(s, 64)
		if err != nil || !(c > 0) || c > 1e9 {
			return nil, fmt.Errorf("%s: invalid close %q", d, s)
		}
		out = append(out, finanzen.Price{Date: d, Close: c})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out, nil
}

// number accepts a JSON number or a numeric string, as Alpha Vantage mixes both.
type number float64

func (n *number) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fmt.Errorf("not a number: %s", b)
	}
	*n = number(f)
	return nil
}

// Profile returns the composition: ETF_PROFILE for ETFs and funds (sectors,
// holdings), OVERVIEW for stocks (sector, country, currency).
func (a *AlphaVantage) Profile(ctx context.Context, symbol, kind string) ([]finanzen.Weight, error) {
	if kind == finanzen.Stock {
		m, err := a.query(ctx, "OVERVIEW", symbol, nil)
		if err != nil {
			return nil, err
		}
		return ParseOverview(m)
	}
	m, err := a.query(ctx, "ETF_PROFILE", symbol, nil)
	if err != nil {
		return nil, err
	}
	return ParseETFProfile(m)
}

// ParseETFProfile reads "sectors" and "holdings" of an ETF_PROFILE response.
func ParseETFProfile(m map[string]json.RawMessage) ([]finanzen.Weight, error) {
	var sectors []struct {
		Sector string `json:"sector"`
		Weight number `json:"weight"`
	}
	var holdings []struct {
		Symbol      string `json:"symbol"`
		Description string `json:"description"`
		Weight      number `json:"weight"`
	}
	if raw, ok := m["sectors"]; ok {
		if err := json.Unmarshal(raw, &sectors); err != nil {
			return nil, fmt.Errorf("sectors: %w", err)
		}
	}
	if raw, ok := m["holdings"]; ok {
		if err := json.Unmarshal(raw, &holdings); err != nil {
			return nil, fmt.Errorf("holdings: %w", err)
		}
	}
	if len(sectors) == 0 && len(holdings) == 0 {
		return nil, errors.New("profile has neither sectors nor holdings")
	}
	var ws []finanzen.Weight
	for _, s := range sectors {
		ws = append(ws, finanzen.Weight{Dimension: finanzen.Sector, Label: title(s.Sector), Weight: float64(s.Weight)})
	}
	// Several share classes of one company share a name: add them up.
	byName := map[string]float64{}
	var names []string
	for _, h := range holdings {
		label := h.Description
		if label == "" || label == "n/a" {
			label = h.Symbol
		}
		label = title(label)
		if _, ok := byName[label]; !ok {
			names = append(names, label)
		}
		byName[label] += float64(h.Weight)
	}
	// The top holdings only: the rest is noise on a portfolio page.
	sort.SliceStable(names, func(i, j int) bool { return byName[names[i]] > byName[names[j]] })
	for i, n := range names {
		if i == 25 {
			break
		}
		ws = append(ws, finanzen.Weight{Dimension: finanzen.Holdings, Label: n, Weight: byName[n]})
	}
	return ws, check(ws)
}

// ParseOverview reads Sector, Country and Currency of an OVERVIEW response.
func ParseOverview(m map[string]json.RawMessage) ([]finanzen.Weight, error) {
	field := func(k string) (string, error) {
		raw, ok := m[k]
		if !ok {
			return "", nil
		}
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return "", fmt.Errorf("%s: %w", k, err)
		}
		if s == "None" || s == "-" {
			return "", nil
		}
		return s, nil
	}
	var ws []finanzen.Weight
	for _, f := range []struct{ key, dim string }{{"Sector", finanzen.Sector}, {"Country", finanzen.Country}, {"Currency", finanzen.Currency}} {
		v, err := field(f.key)
		if err != nil {
			return nil, err
		}
		if v != "" {
			ws = append(ws, finanzen.Weight{Dimension: f.dim, Label: title(v), Weight: 1})
		}
	}
	if len(ws) == 0 {
		return nil, errors.New("overview has no sector, country or currency")
	}
	return ws, check(ws)
}

// check rejects weights outside (0, 1] and duplicate labels.
func check(ws []finanzen.Weight) error {
	seen := map[string]bool{}
	sums := map[string]float64{}
	for _, w := range ws {
		if strings.TrimSpace(w.Label) == "" {
			return fmt.Errorf("%s without a name", w.Dimension)
		}
		if !(w.Weight > 0 && w.Weight <= 1) {
			return fmt.Errorf("%s %q: weight %v outside (0, 1]", w.Dimension, w.Label, w.Weight)
		}
		k := w.Dimension + "\x00" + strings.ToLower(w.Label)
		if seen[k] {
			return fmt.Errorf("%s %q twice", w.Dimension, w.Label)
		}
		seen[k] = true
		sums[w.Dimension] += w.Weight
		if sums[w.Dimension] > 1.01 { // provider rounding
			return fmt.Errorf("%s weights add up to more than 100 %%", w.Dimension)
		}
	}
	return nil
}

// title turns "INFORMATION TECHNOLOGY" into "Information Technology"; mixed
// case stays as it is.
func title(s string) string {
	s = strings.TrimSpace(s)
	if s != strings.ToUpper(s) {
		return s
	}
	words := strings.Fields(strings.ToLower(s))
	for i, w := range words {
		if w != "" {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}

// ── Frankfurter ───────────────────────────────────────

// Frankfurter implements finanzen.Rates.
type Frankfurter struct {
	BaseURL string // default https://api.frankfurter.dev/v1
	Client  *http.Client
}

// Latest returns the newest rate for one unit of currency in base.
func (f *Frankfurter) Latest(ctx context.Context, currency, base string) (finanzen.Rate, error) {
	u := f.BaseURL
	if u == "" {
		u = "https://api.frankfurter.dev/v1"
	}
	q := url.Values{"base": {currency}, "symbols": {base}}
	body, err := get(ctx, f.Client, strings.TrimSuffix(u, "/")+"/latest?"+q.Encode(), 1<<20)
	if err != nil {
		return finanzen.Rate{}, err
	}
	return ParseFrankfurter(body, currency, base)
}

// ParseFrankfurter validates a /latest response.
func ParseFrankfurter(body []byte, currency, base string) (finanzen.Rate, error) {
	var r struct {
		Base  string             `json:"base"`
		Date  string             `json:"date"`
		Rates map[string]float64 `json:"rates"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return finanzen.Rate{}, fmt.Errorf("unexpected response: %w", err)
	}
	rate, ok := r.Rates[base]
	switch {
	case r.Base != currency:
		return finanzen.Rate{}, fmt.Errorf("asked for %s, got base %q", currency, r.Base)
	case !finanzen.ValidDate(r.Date):
		return finanzen.Rate{}, fmt.Errorf("invalid date %q", r.Date)
	case !ok || !(rate > 0) || rate > 1e6:
		return finanzen.Rate{}, fmt.Errorf("no valid %s rate", base)
	}
	return finanzen.Rate{Currency: currency, Base: base, Date: r.Date, Rate: rate}, nil
}
