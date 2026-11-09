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

// Rates provides exchange rates (Frankfurter).
type Rates interface {
	Latest(ctx context.Context, currency, base string) (Rate, error)
}

// Refresher loads market data into the Market store. It is run once a day
// and on demand by an admin.
type Refresher struct {
	Market     Market
	Quotes     Quotes // nil: no API key, prices are not fetched
	Rates      Rates
	Base       string
	Now        func() time.Time
	Pause      time.Duration // between provider calls (rate limit)
	Budget     int           // provider calls per run (free tier: 25 a day)
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
	calls := 0
	pause := func() bool {
		if calls > 0 && r.Pause > 0 {
			select {
			case <-ctx.Done():
				return false
			case <-time.After(r.Pause):
			}
		}
		calls++
		return true
	}
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
	if r.Quotes == nil && len(syms) > 0 {
		errs = append(errs, "Kein Alpha-Vantage-Schlüssel (ALLMEND_ALPHAVANTAGE_KEY), Kurse werden nicht geladen")
		syms = nil
	}
	budget := r.Budget
	if budget <= 0 {
		budget = 25
	}
	if len(syms) > budget {
		errs = append(errs, fmt.Sprintf("%d Kurssymbole, aber nur %d Abfragen pro Tag möglich", len(syms), budget))
	}
	for _, s := range syms {
		if calls >= budget || !pause() {
			break
		}
		ps, err := r.Quotes.Daily(ctx, s.Symbol)
		if err == nil {
			var n int
			n, err = r.Market.AddPrices(ctx, s.Symbol, ps, r.now())
			newPrices += n
		}
		if err != nil {
			errs = append(errs, s.Symbol+": "+err.Error())
		}
	}
	// Profiles change slowly: refresh the oldest with what is left of the budget.
	for _, s := range syms {
		if calls >= budget {
			break
		}
		p, err := r.Market.Profile(ctx, s.Symbol)
		if err == nil && r.now().Sub(p.FetchedAt) < r.ProfileAge {
			continue
		}
		if err != nil && !errors.Is(err, ErrNotFound) {
			errs = append(errs, s.Symbol+": "+err.Error())
			continue
		}
		if !pause() {
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
		slog.Info("finanzen: market data refreshed", "prices", newPrices, "calls", calls)
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
