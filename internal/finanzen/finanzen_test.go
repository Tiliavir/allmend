package finanzen

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestApplyAverageCost(t *testing.T) {
	txs := []Transaction{
		{Date: "2025-01-01", Type: Buy, Quantity: 10, PriceCents: 10000, FeesCents: 500},
		{Date: "2025-02-01", Type: Buy, Quantity: 10, PriceCents: 12000},
		{Date: "2025-03-01", Type: Sell, Quantity: 5, PriceCents: 15000, FeesCents: 100},
	}
	h := Apply(txs, "")
	// cost 100500 + 120000 = 220500 for 20 → 11025 each; 5 sold: 55125.
	if h.Held != 15 || h.CostCents != 165375 || h.RealizedCents != 75000-100-55125 || h.FeesCents != 500 {
		t.Fatalf("%+v", h)
	}
	if got := Apply(txs, "2025-01-15"); got.Held != 10 || got.CostCents != 100500 {
		t.Fatalf("until: %+v", got)
	}
	all := append(txs, Transaction{Date: "2025-04-01", Type: Sell, Quantity: 15, PriceCents: 1})
	if h := Apply(all, ""); h.Held != 0 || h.CostCents != 0 {
		t.Fatalf("sold out: %+v", h)
	}
}

func TestCheckHoldings(t *testing.T) {
	ok := []Transaction{{Date: "2025-01-01", Type: Buy, Quantity: 1.5}, {Date: "2025-01-02", Type: Sell, Quantity: 1.5}}
	if err := CheckHoldings(ok); err != nil {
		t.Fatal(err)
	}
	bad := []Transaction{{Date: "2025-01-01", Type: Sell, Quantity: 1}, {Date: "2025-01-02", Type: Buy, Quantity: 5}}
	if err := CheckHoldings(bad); err == nil {
		t.Fatal("short sell accepted")
	}
}

func TestPriceAtAndRisk(t *testing.T) {
	ps := []Price{{"2025-01-02", 10}, {"2025-01-03", 11}, {"2025-01-06", 12}}
	if p, ok := PriceAt(ps, "2025-01-05"); !ok || p != 11 {
		t.Fatal(p, ok)
	}
	if _, ok := PriceAt(ps, "2025-01-01"); ok {
		t.Fatal("price before the first")
	}
	if RiskClass(ps) != 0 {
		t.Fatal("too few prices must give 0")
	}
	var flat, wild []Price
	for i := 0; i < 300; i++ {
		d := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, i).Format(time.DateOnly)
		flat = append(flat, Price{d, 100 + float64(i%2)*0.001})
		wild = append(wild, Price{d, 100 + float64(i%2)*20})
	}
	if c := RiskClass(flat); c != 1 {
		t.Errorf("flat: %d", c)
	}
	if c := RiskClass(wild); c != 7 {
		t.Errorf("wild: %d", c)
	}
}

func TestEffectivePrefersProvider(t *testing.T) {
	manual := []Weight{{Sector, "A", 0.5}, {Country, "CH", 1}}
	prov := []Weight{{Sector, "Tech", 0.3}, {Sector, "Health", 0.6}}
	e := Effective(manual, prov)
	if len(e[Sector]) != 2 || e[Sector][0].Label != "Health" || e[Country][0].Label != "CH" {
		t.Fatalf("%+v", e)
	}
}

func TestChecks(t *testing.T) {
	good := Security{Name: "X", Kind: ETF, Currency: "EUR", ISIN: "IE00BK5BQT80", Symbol: "VWCE.DEX"}
	if err := CheckSecurity(good); err != nil {
		t.Fatal(err)
	}
	for i, s := range []Security{
		{Name: "X", Kind: "bond", Currency: "EUR"},
		{Name: "X", Kind: ETF, Currency: "eur"},
		{Name: "X", Kind: ETF, Currency: "EUR", RiskOverride: 8},
		{Name: "X", Kind: ETF, Currency: "EUR", ArchivedAt: "2025-01-01"},
		{Name: "X", Kind: ETF, Currency: "EUR", Symbol: "A B"},
		{Name: "X", Kind: ETF, Currency: "EUR", Weights: []Weight{{Sector, "A", 0}}},
		{Name: "X", Kind: ETF, Currency: "EUR", Weights: []Weight{{Sector, "A", 0.5}, {Sector, "a", 0.1}}},
		{Name: "X", Kind: ETF, Currency: "EUR", Weights: []Weight{{"region", "A", 0.5}}},
	} {
		if CheckSecurity(s) == nil {
			t.Errorf("security %d accepted", i)
		}
	}
	if CheckAsset(Asset{Name: "B", Kind: Konto, Currency: "EUR", MaturityDate: "2030-01-01"}) == nil {
		t.Error("maturity on an account accepted")
	}
}

// ── refresher ─────────────────────────────────────────

type fakeMarket struct {
	prices   map[string][]Price
	rates    []Rate
	profiles map[string]Profile
	syms     []SymbolUse
	curs     []string
	runs     []Run
}

func (m *fakeMarket) Prices(_ context.Context, s string) ([]Price, error) { return m.prices[s], nil }
func (m *fakeMarket) AddPrices(_ context.Context, s string, ps []Price, _ time.Time) (int, error) {
	if m.prices == nil {
		m.prices = map[string][]Price{}
	}
	m.prices[s] = append(m.prices[s], ps...)
	return len(ps), nil
}
func (m *fakeMarket) Rate(context.Context, string, string) (Rate, error) { return Rate{}, ErrNotFound }
func (m *fakeMarket) AddRate(_ context.Context, r Rate, _ time.Time) error {
	m.rates = append(m.rates, r)
	return nil
}
func (m *fakeMarket) Profile(_ context.Context, s string) (Profile, error) {
	p, ok := m.profiles[s]
	if !ok {
		return p, ErrNotFound
	}
	return p, nil
}
func (m *fakeMarket) SetProfile(_ context.Context, p Profile) error {
	if m.profiles == nil {
		m.profiles = map[string]Profile{}
	}
	m.profiles[p.Symbol] = p
	return nil
}
func (m *fakeMarket) Symbols(context.Context) ([]SymbolUse, error)         { return m.syms, nil }
func (m *fakeMarket) Currencies(context.Context, string) ([]string, error) { return m.curs, nil }
func (m *fakeMarket) AddRun(_ context.Context, r Run) error                { m.runs = append(m.runs, r); return nil }
func (m *fakeMarket) LastRun(context.Context) (Run, error)                 { return m.runs[len(m.runs)-1], nil }

type fakeQuotes struct{ calls []string }

func (q *fakeQuotes) Daily(_ context.Context, s string) ([]Price, error) {
	q.calls = append(q.calls, "daily "+s)
	if s == "BAD" {
		return nil, errors.New("HTTP 500")
	}
	return []Price{{"2025-01-02", 1}}, nil
}
func (q *fakeQuotes) Profile(_ context.Context, s, _ string) ([]Weight, error) {
	q.calls = append(q.calls, "profile "+s)
	return []Weight{{Sector, "Tech", 1}}, nil
}

type fakeRates struct{}

func (fakeRates) Latest(_ context.Context, c, b string) (Rate, error) {
	return Rate{Currency: c, Base: b, Date: "2025-01-02", Rate: 0.9}, nil
}

func TestRefresherBudgetAndErrors(t *testing.T) {
	m := &fakeMarket{syms: []SymbolUse{{"A", ETF}, {"BAD", Stock}, {"C", ETF}}, curs: []string{"USD"},
		profiles: map[string]Profile{"C": {Symbol: "C", FetchedAt: time.Now()}}}
	q := &fakeQuotes{}
	r := &Refresher{Market: m, Quotes: q, Rates: fakeRates{}, Base: "EUR", Budget: 4, ProfileAge: 24 * time.Hour}
	run := r.Run(context.Background())
	want := "daily A,daily BAD,daily C,profile A"
	if got := strings.Join(q.calls, ","); got != want {
		t.Fatalf("calls %s, want %s", got, want)
	}
	if run.OK || !strings.Contains(run.Message, "BAD: HTTP 500") || len(m.rates) != 1 || len(m.prices["A"]) != 1 {
		t.Fatalf("%+v", run)
	}
	// Without a key: rates only, and the run says why.
	m2 := &fakeMarket{syms: []SymbolUse{{"A", ETF}}}
	run = (&Refresher{Market: m2, Rates: fakeRates{}, Base: "EUR"}).Run(context.Background())
	if run.OK || !strings.Contains(run.Message, "ALLMEND_ALPHAVANTAGE_KEY") {
		t.Fatalf("%+v", run)
	}
}
