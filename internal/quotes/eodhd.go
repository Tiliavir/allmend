package quotes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/tiliavir/allmend/internal/finanzen"
)

// ── EODHD ─────────────────────────────────────────────

// EODHD implements finanzen.DailySource with the End-of-Day API. It serves
// the SIX titles Alpha Vantage does not cover (ADR six-prices-eodhd).
type EODHD struct {
	BaseURL string // default https://eodhd.com
	Key     string
	Client  *http.Client
}

// DailySince returns the unadjusted daily closes from the date from
// (inclusive) to today.
func (e *EODHD) DailySince(ctx context.Context, symbol, from string) ([]finanzen.Price, error) {
	if !finanzen.ValidDate(from) {
		return nil, fmt.Errorf("invalid start date %q", from)
	}
	base := e.BaseURL
	if base == "" {
		base = "https://eodhd.com"
	}
	q := url.Values{"api_token": {e.Key}, "fmt": {"json"}, "period": {"d"}, "from": {from}}
	body, err := get(ctx, e.Client, base+"/api/eod/"+url.PathEscape(symbol)+"?"+q.Encode(), 4<<20)
	if err != nil {
		return nil, fmt.Errorf("EODHD: %w", err)
	}
	return ParseEODHD(body)
}

// ParseEODHD reads an End-of-Day response: an array of bars with "date" and
// the unadjusted "close". An empty array is an error (renamed symbol, or
// nothing traded since the start date).
func ParseEODHD(body []byte) ([]finanzen.Price, error) {
	var bars []struct {
		Date  string   `json:"date"`
		Close *float64 `json:"close"`
	}
	if err := json.Unmarshal(body, &bars); err != nil {
		return nil, fmt.Errorf("EODHD: unexpected response: %w", err)
	}
	ps := make([]finanzen.Price, 0, len(bars))
	for _, b := range bars {
		if b.Close == nil {
			return nil, fmt.Errorf("EODHD: %s: no close", b.Date)
		}
		ps = append(ps, finanzen.Price{Date: b.Date, Close: *b.Close})
	}
	ps, err := checkPrices(ps)
	if err != nil {
		return nil, fmt.Errorf("EODHD: %w", err)
	}
	return ps, nil
}
