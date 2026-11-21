package finanzen

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// Quotes provides prices and fund profiles (Alpha Vantage).
type Quotes interface {
	Daily(ctx context.Context, symbol string) ([]Price, error) // recent closes, ascending
	Profile(ctx context.Context, symbol, kind string) ([]Weight, error)
}

// DailySource provides daily closes from a start date on (EODHD, for SIX
// titles; ADR six-prices-eodhd).
type DailySource interface {
	DailySince(ctx context.Context, symbol, from string) ([]Price, error) // ascending
}

// SIXSuffix marks symbols of the SIX Swiss Exchange. Their prices come from
// the SIX source, not from Quotes.
const SIXSuffix = ".SW"

// IsSIX reports whether symbol is a SIX title.
func IsSIX(symbol string) bool { return strings.HasSuffix(symbol, SIXSuffix) }

// Rates provides exchange rates (Frankfurter).
type Rates interface {
	Latest(ctx context.Context, currency, base string) (Rate, error)
}

// Refresher loads market data into the Market store. It is run once a day
// and on demand by an admin.
type Refresher struct {
	Market     Market
	Quotes     Quotes      // nil: no API key, prices are not fetched
	SIX        DailySource // nil: no EODHD key, SIX prices are not fetched
	Rates      Rates
	Base       string
	Now        func() time.Time
	Pause      time.Duration // between calls to one provider (rate limit)
	Budget     int           // Quotes calls per run (free tier: 25 a day)
	SIXBudget  int           // SIX calls per run (free tier: 20 a day)
	ProfileAge time.Duration // refresh profiles older than this

	mu sync.Mutex // one run at a time
}

// Start runs a refresh in the background; false if one is running.
func (r *Refresher) Start(ctx context.Context) bool {
	if !r.mu.TryLock() {
		return false
	}
	go func() {
		defer r.mu.Unlock()
		r.Run(context.WithoutCancel(ctx))
	}()
	return true
}

// limiter counts the calls to one provider and pauses between them.
type limiter struct {
	calls, budget int
	pause         time.Duration
}

// take reports whether one more call may be made, after the pause.
func (l *limiter) take(ctx context.Context) bool {
	if l.calls >= l.budget {
		return false
	}
	if l.calls > 0 && l.pause > 0 {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(l.pause):
		}
	}
	l.calls++
	return true
}

func orDefault(n, d int) int {
	if n <= 0 {
		return d
	}
	return n
}

func (r *Refresher) now() time.Time {
	if r.Now != nil {
		return r.Now().UTC()
	}
	return time.Now().UTC()
}

// Run fetches what is missing and records the outcome. Stored prices are
// never overwritten; a failed symbol keeps its old data.
func (r *Refresher) Run(ctx context.Context) Run {
	var errs, notes []string
	newPrices := 0
	av := &limiter{budget: orDefault(r.Budget, 25), pause: r.Pause}
	six := &limiter{budget: orDefault(r.SIXBudget, 20), pause: r.Pause}
	curs, err := r.Market.Currencies(ctx, r.Base)
	if err != nil {
		errs = append(errs, "Währungen: "+err.Error())
	}
	newRates := 0
	for _, c := range curs {
		rate, err := r.Rates.Latest(ctx, c, r.Base)
		if err == nil {
			err = r.Market.AddRate(ctx, rate, r.now())
		}
		if err != nil {
			errs = append(errs, c+": "+err.Error())
			continue
		}
		newRates++
	}
	syms, err := r.Market.Symbols(ctx)
	if err != nil {
		errs = append(errs, "Wertpapiere: "+err.Error())
	}
	var avSyms, sixSyms []SymbolUse
	for _, s := range syms {
		if IsSIX(s.Symbol) {
			sixSyms = append(sixSyms, s)
		} else {
			avSyms = append(avSyms, s)
		}
	}
	if r.Quotes == nil && len(avSyms) > 0 {
		errs = append(errs, "Kein Alpha-Vantage-Schlüssel (ALLMEND_ALPHAVANTAGE_KEY), Kurse werden nicht geladen")
		avSyms = nil
	}
	if r.SIX == nil && len(sixSyms) > 0 {
		errs = append(errs, fmt.Sprintf("Kein EODHD-Schlüssel (ALLMEND_EODHD_KEY), Kurse der %d SIX-Titel (%s) werden nicht geladen", len(sixSyms), SIXSuffix))
		sixSyms = nil
	}
	if len(avSyms) > av.budget {
		errs = append(errs, fmt.Sprintf("%d Kurssymbole, aber nur %d Abfragen pro Tag möglich", len(avSyms), av.budget))
	}
	if len(sixSyms) > six.budget {
		errs = append(errs, fmt.Sprintf("%d SIX-Kurssymbole, aber nur %d EODHD-Abfragen pro Tag möglich", len(sixSyms), six.budget))
	}
	store := func(symbol string, ps []Price, err error) {
		if err == nil {
			var n int
			n, err = r.Market.AddPrices(ctx, symbol, ps, r.now())
			newPrices += n
		}
		if err != nil {
			errs = append(errs, symbol+": "+err.Error())
		}
	}
	for _, s := range avSyms {
		if !av.take(ctx) {
			break
		}
		ps, err := r.Quotes.Daily(ctx, s.Symbol)
		store(s.Symbol, ps, err)
	}
	// SIX: from the last stored close on; a new symbol gets the year of
	// history the free plan offers.
	for _, s := range sixSyms {
		have, err := r.Market.Prices(ctx, s.Symbol)
		if err != nil {
			errs = append(errs, s.Symbol+": "+err.Error())
			continue
		}
		from := r.now().AddDate(-1, 0, 0).Format(time.DateOnly)
		if len(have) > 0 {
			from = have[len(have)-1].Date
		}
		if !six.take(ctx) {
			break
		}
		ps, err := r.SIX.DailySince(ctx, s.Symbol, from)
		store(s.Symbol, ps, err)
	}
	// Profiles change slowly: refresh the oldest with what is left of the budget.
	// Only ETFs and funds from Alpha Vantage: a stock is its own composition
	// and its overview is a paid endpoint; EODHD gives no composition.
	for _, s := range avSyms {
		if av.calls >= av.budget {
			break
		}
		if s.Kind == Stock {
			continue
		}
		p, err := r.Market.Profile(ctx, s.Symbol)
		if err == nil && r.now().Sub(p.FetchedAt) < r.ProfileAge {
			continue
		}
		if err != nil && !errors.Is(err, ErrNotFound) {
			errs = append(errs, s.Symbol+": "+err.Error())
			continue
		}
		if !av.take(ctx) {
			break
		}
		ws, err := r.Quotes.Profile(ctx, s.Symbol, s.Kind)
		if err == nil {
			err = r.Market.SetProfile(ctx, Profile{Symbol: s.Symbol, FetchedAt: r.now(), Weights: ws})
		}
		if err != nil {
			errs = append(errs, s.Symbol+" (Zusammensetzung): "+err.Error())
		} else {
			notes = append(notes, "Zusammensetzung "+s.Symbol)
		}
	}
	run := Run{At: r.now(), OK: len(errs) == 0}
	msg := fmt.Sprintf("%d neue Kurse, %d von %d Wechselkursen", newPrices, newRates, len(curs))
	if len(notes) > 0 {
		msg += ", " + strings.Join(notes, ", ")
	}
	if len(errs) > 0 {
		msg += ". Fehler: " + strings.Join(errs, "; ")
	}
	run.Message = msg
	if err := r.Market.AddRun(context.WithoutCancel(ctx), run); err != nil {
		slog.Error("finanzen: store run", "err", err)
	}
	if run.OK {
		slog.Info("finanzen: market data refreshed", "prices", newPrices, "calls", av.calls, "six_calls", six.calls)
	} else {
		slog.Warn("finanzen: market data refresh had errors", "detail", msg)
	}
	return run
}

// Schedule runs r every day at hour:00 in loc until ctx ends.
func (r *Refresher) Schedule(ctx context.Context, loc *time.Location, hour int) {
	for {
		now := time.Now().In(loc)
		next := time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, loc)
		if !next.After(now) {
			next = time.Date(now.Year(), now.Month(), now.Day()+1, hour, 0, 0, 0, loc)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(next)):
			r.mu.Lock()
			r.Run(ctx)
			r.mu.Unlock()
		}
	}
}
