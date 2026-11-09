// Package finanzen tracks an account's wealth: assets (accounts, building
// savings, pensions, other holdings), securities with their buys, sells and
// dividends, and a yearly snapshot. All of it is private to its owner.
// Market data (prices, exchange rates, fund profiles) is shared: it belongs to
// a symbol or currency, not to a person.
package finanzen

import (
	"context"
	"errors"
	"math"
	"regexp"
	"strings"
	"time"
)

// AssetKind groups assets. Konto and Vorsorge do not count towards the
// total gain (their "gain" is mostly what was paid in).
type AssetKind string

// Asset kinds.
const (
	Konto    AssetKind = "konto"
	Bauspar  AssetKind = "bauspar"
	Vorsorge AssetKind = "vorsorge"
	Sachwert AssetKind = "sachwert"
)

// AssetKinds in display order.
var AssetKinds = []struct {
	ID    AssetKind
	Label string
}{{Konto, "Konto"}, {Bauspar, "Bausparvertrag"}, {Vorsorge, "Vorsorge"}, {Sachwert, "Sachwert"}}

// Label is the German name of k.
func (k AssetKind) Label() string {
	for _, x := range AssetKinds {
		if x.ID == k {
			return x.Label
		}
	}
	return string(k)
}

// CountsGain reports whether gains of this kind add to the total gain.
func (k AssetKind) CountsGain() bool { return k != Konto && k != Vorsorge }

// Asset is a manually maintained position (formerly accounts, Bauspar,
// pension funds and manual assets).
type Asset struct {
	ID, OwnerID        string
	Name               string
	Kind               AssetKind
	Category           string // free text, e.g. "Reka", "Grundstück"
	Currency           string
	ValueCents         int64
	AsOf               string // YYYY-MM-DD or ""
	MaturityDate       string // Bauspar only, YYYY-MM-DD or ""
	TaxAllowanceCents  int64
	CostCents          int64
	PaidInCents        int64
	DistributionsCents int64
	Comment            string
	Version            int
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// GainCents is value − cost − paid in + distributions.
func (a Asset) GainCents() int64 {
	return a.ValueCents - a.CostCents - a.PaidInCents + a.DistributionsCents
}

// Security kinds.
const (
	Stock = "stock"
	ETF   = "etf"
	Fund  = "fund"
)

// SecurityKinds in display order.
var SecurityKinds = []struct{ ID, Label string }{{Stock, "Aktie"}, {ETF, "ETF"}, {Fund, "Fonds"}}

// KindLabel is the German name of a security kind.
func KindLabel(k string) string {
	for _, x := range SecurityKinds {
		if x.ID == k {
			return x.Label
		}
	}
	return k
}

// Composition dimensions.
const (
	Sector   = "sector"
	Country  = "country"
	Currency = "currency"
	Holdings = "holding"
)

// Dimensions in display order.
var Dimensions = []struct{ ID, Label string }{{Sector, "Sektoren"}, {Country, "Länder"}, {Currency, "Währungen"}, {Holdings, "Top Holdings"}}

// Weight is one share of a composition (0 < Weight ≤ 1).
type Weight struct {
	Dimension string
	Label     string
	Weight    float64
}

// Security is a traded instrument.
type Security struct {
	ID, OwnerID  string
	Name         string
	ISIN         string
	Ticker       string
	Kind         string
	Currency     string // trading currency
	Symbol       string // Alpha Vantage symbol; "" = no prices
	Archived     bool
	ArchivedAt   string // YYYY-MM-DD or ""
	RiskOverride int    // 0 = derived from prices; 1–7 = manual SRRI class
	Weights      []Weight
	Version      int
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Transaction types.
const (
	Buy  = "buy"
	Sell = "sell"
)

// Transaction is a buy or sell of a security. Amounts are in the security's
// currency.
type Transaction struct {
	ID, OwnerID, SecurityID string
	Date                    string
	Type                    string
	Quantity                float64
	PriceCents              int64 // per unit
	FeesCents               int64
	TaxesCents              int64 // e.g. tax on employee share grants
	Version                 int
	CreatedAt               time.Time
	UpdatedAt               time.Time
}

// Dividend is a payout in the security's currency.
type Dividend struct {
	ID, OwnerID, SecurityID string
	Date                    string
	AmountCents             int64
	Version                 int
	CreatedAt               time.Time
	UpdatedAt               time.Time
}

// Year is the manually recorded yearly snapshot, in the base currency.
type Year struct {
	OwnerID         string
	Year            int
	IncomeCents     int64
	TaxCents        int64
	PensionCents    int64
	CashCents       int64
	BausparCents    int64
	StocksCents     int64
	FundsCents      int64
	RealEstateCents int64
	Comment         string
	Version         int
	UpdatedAt       time.Time
}

// TotalCents is the wealth of the year (all categories except income and tax).
func (y Year) TotalCents() int64 {
	return y.PensionCents + y.CashCents + y.BausparCents + y.StocksCents + y.FundsCents + y.RealEstateCents
}

// Price is a daily closing price.
type Price struct {
	Date  string
	Close float64
}

// Rate converts one unit of Currency into Base.
type Rate struct {
	Currency, Base, Date string
	Rate                 float64
}

// Profile is the provider's composition of a symbol.
type Profile struct {
	Symbol    string
	FetchedAt time.Time
	Weights   []Weight
}

// Run is one market-data refresh.
type Run struct {
	At      time.Time
	OK      bool
	Message string
}

// SymbolUse is a symbol some active security needs prices for.
type SymbolUse struct{ Symbol, Kind string }

// Errors.
var (
	ErrNotFound = errors.New("finanzen: not found")
	ErrConflict = errors.New("finanzen: changed meanwhile")
	ErrInUse    = errors.New("finanzen: security has transactions or dividends")
)

// Repository stores an owner's finance data. Every method is scoped to one owner.
type Repository interface {
	Assets(ctx context.Context, owner string) ([]Asset, error) // by kind, name
	Asset(ctx context.Context, owner, id string) (Asset, error)
	InsertAsset(ctx context.Context, a Asset) error
	UpdateAsset(ctx context.Context, a Asset, want int) error
	DeleteAsset(ctx context.Context, owner, id string) error

	Securities(ctx context.Context, owner string) ([]Security, error) // by name, with weights
	Security(ctx context.Context, owner, id string) (Security, error)
	InsertSecurity(ctx context.Context, s Security) error
	UpdateSecurity(ctx context.Context, s Security, want int) error
	DeleteSecurity(ctx context.Context, owner, id string) error // ErrInUse if referenced

	Transactions(ctx context.Context, owner string) ([]Transaction, error) // by date, then created
	Transaction(ctx context.Context, owner, id string) (Transaction, error)
	InsertTransaction(ctx context.Context, t Transaction) error
	UpdateTransaction(ctx context.Context, t Transaction, want int) error
	DeleteTransaction(ctx context.Context, owner, id string) error

	Dividends(ctx context.Context, owner string) ([]Dividend, error) // by date
	Dividend(ctx context.Context, owner, id string) (Dividend, error)
	InsertDividend(ctx context.Context, d Dividend) error
	UpdateDividend(ctx context.Context, d Dividend, want int) error
	DeleteDividend(ctx context.Context, owner, id string) error

	Years(ctx context.Context, owner string) ([]Year, error) // ascending
	Year(ctx context.Context, owner string, year int) (Year, error)
	InsertYear(ctx context.Context, y Year) error
	UpdateYear(ctx context.Context, y Year, want int) error
	DeleteYear(ctx context.Context, owner string, year int) error
}

// Market stores shared market data. Stored prices and rates are never
// changed: a day's close is final.
type Market interface {
	Prices(ctx context.Context, symbol string) ([]Price, error) // ascending
	AddPrices(ctx context.Context, symbol string, ps []Price, at time.Time) (int, error)
	Rate(ctx context.Context, currency, base string) (Rate, error) // latest; ErrNotFound if none
	AddRate(ctx context.Context, r Rate, at time.Time) error
	Profile(ctx context.Context, symbol string) (Profile, error) // ErrNotFound if never fetched
	SetProfile(ctx context.Context, p Profile) error
	Symbols(ctx context.Context) ([]SymbolUse, error)              // of all active securities
	Currencies(ctx context.Context, base string) ([]string, error) // in use, except base
	AddRun(ctx context.Context, r Run) error
	LastRun(ctx context.Context) (Run, error) // ErrNotFound if none
}

// ── validation ────────────────────────────────────────

var (
	dateRe     = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	currencyRe = regexp.MustCompile(`^[A-Z]{3}$`)
	isinRe     = regexp.MustCompile(`^[A-Z]{2}[A-Z0-9]{9}[0-9]$`)
	symbolRe   = regexp.MustCompile(`^[A-Za-z0-9.\-:^=]{1,30}$`)
)

// ValidDate reports whether s is a real calendar date "YYYY-MM-DD".
func ValidDate(s string) bool {
	if !dateRe.MatchString(s) {
		return false
	}
	_, err := time.Parse(time.DateOnly, s)
	return err == nil
}

// ValidCurrency reports whether s looks like an ISO 4217 code.
func ValidCurrency(s string) bool { return currencyRe.MatchString(s) }

// Problems lists what is wrong with user input (German).
type Problems []string

func (p *Problems) add(cond bool, msg string) {
	if cond {
		*p = append(*p, msg)
	}
}

// Err returns nil or an error carrying the problems.
func (p Problems) Err() error {
	if len(p) == 0 {
		return nil
	}
	return &InvalidError{p}
}

// InvalidError lists input problems.
type InvalidError struct{ Problems Problems }

func (e *InvalidError) Error() string { return "finanzen: invalid: " + strings.Join(e.Problems, "; ") }

const maxCents = 1e13 // 100 billion: anything above is a typo

func badCents(cs ...int64) bool {
	for _, c := range cs {
		if c < 0 || c > maxCents {
			return true
		}
	}
	return false
}

// CheckAsset validates an asset.
func CheckAsset(a Asset) error {
	var p Problems
	p.add(strings.TrimSpace(a.Name) == "" || len([]rune(a.Name)) > 80, "Name fehlt oder ist zu lang")
	p.add(a.Kind.Label() == string(a.Kind), "Art ist unbekannt")
	p.add(len([]rune(a.Category)) > 60, "Kategorie ist zu lang")
	p.add(!ValidCurrency(a.Currency), "Währung muss ein ISO-Code wie EUR sein")
	p.add(badCents(a.ValueCents, a.TaxAllowanceCents, a.CostCents, a.PaidInCents, a.DistributionsCents), "Beträge müssen zwischen 0 und 100 Mrd. liegen")
	p.add(a.AsOf != "" && !ValidDate(a.AsOf), "Stand ist kein gültiges Datum")
	p.add(a.MaturityDate != "" && !ValidDate(a.MaturityDate), "Fälligkeit ist kein gültiges Datum")
	p.add(a.MaturityDate != "" && a.Kind != Bauspar, "Fälligkeit gibt es nur bei Bausparverträgen")
	p.add(len(a.Comment) > 5000, "Kommentar ist zu lang")
	return p.Err()
}

// CheckSecurity validates a security including its manual composition.
func CheckSecurity(s Security) error {
	var p Problems
	p.add(strings.TrimSpace(s.Name) == "" || len([]rune(s.Name)) > 80, "Name fehlt oder ist zu lang")
	p.add(KindLabel(s.Kind) == s.Kind, "Art ist unbekannt")
	p.add(!ValidCurrency(s.Currency), "Währung muss ein ISO-Code wie EUR sein")
	p.add(s.ISIN != "" && !isinRe.MatchString(s.ISIN), "ISIN hat nicht das Format DE0001234567")
	p.add(len(s.Ticker) > 20, "Ticker ist zu lang")
	p.add(s.Symbol != "" && !symbolRe.MatchString(s.Symbol), "Kurssymbol enthält unerlaubte Zeichen")
	p.add(s.RiskOverride < 0 || s.RiskOverride > 7, "Risikoklasse muss 1–7 sein (leer = aus Kursen)")
	p.add(!s.Archived && s.ArchivedAt != "", "Archivdatum ohne Archivierung")
	p.add(s.ArchivedAt != "" && !ValidDate(s.ArchivedAt), "Archivdatum ist ungültig")
	p = append(p, checkWeights(s.Weights)...)
	return p.Err()
}

func checkWeights(ws []Weight) Problems {
	var p Problems
	sum := map[string]float64{}
	seen := map[string]bool{}
	for _, w := range ws {
		name := dimensionLabel(w.Dimension)
		if name == "" {
			p = append(p, "Unbekannte Zusammensetzung "+w.Dimension)
			continue
		}
		key := w.Dimension + "\x00" + strings.ToLower(w.Label)
		p.add(seen[key], name+": "+w.Label+" kommt doppelt vor")
		seen[key] = true
		p.add(strings.TrimSpace(w.Label) == "" || len([]rune(w.Label)) > 80, name+": Bezeichnung fehlt oder ist zu lang")
		p.add(!(w.Weight > 0 && w.Weight <= 1), name+": Anteil von "+w.Label+" muss über 0 % und höchstens 100 % sein")
		sum[w.Dimension] += w.Weight
	}
	for _, d := range Dimensions {
		p.add(sum[d.ID] > 1.0001, d.Label+": Anteile ergeben mehr als 100 %")
	}
	return p
}

func dimensionLabel(id string) string {
	for _, d := range Dimensions {
		if d.ID == id {
			return d.Label
		}
	}
	return ""
}

// CheckTransaction validates a transaction on its own.
func CheckTransaction(t Transaction) error {
	var p Problems
	p.add(!ValidDate(t.Date), "Datum fehlt oder ist ungültig")
	p.add(t.Type != Buy && t.Type != Sell, "Typ muss Kauf oder Verkauf sein")
	p.add(!(t.Quantity > 0 && t.Quantity < 1e9) || math.IsNaN(t.Quantity), "Stückzahl muss grösser als 0 sein")
	p.add(badCents(t.PriceCents, t.FeesCents, t.TaxesCents), "Beträge müssen zwischen 0 und 100 Mrd. liegen")
	return p.Err()
}

// CheckDividend validates a dividend.
func CheckDividend(d Dividend) error {
	var p Problems
	p.add(!ValidDate(d.Date), "Datum fehlt oder ist ungültig")
	p.add(d.AmountCents <= 0 || d.AmountCents > maxCents, "Betrag muss grösser als 0 sein")
	return p.Err()
}

// CheckYear validates a yearly snapshot.
func CheckYear(y Year) error {
	var p Problems
	p.add(y.Year < 1900 || y.Year > 2200, "Jahr ist ungültig")
	p.add(badCents(y.IncomeCents, y.TaxCents, y.PensionCents, y.CashCents, y.BausparCents, y.StocksCents, y.FundsCents, y.RealEstateCents),
		"Beträge müssen zwischen 0 und 100 Mrd. liegen")
	p.add(len(y.Comment) > 20000, "Kommentar ist zu lang")
	return p.Err()
}

// CheckHoldings verifies that no sell exceeds the quantity held at its date.
// txs must be the security's transactions in the order Transactions returns.
func CheckHoldings(txs []Transaction) error {
	var held float64
	for _, t := range txs {
		if t.Type == Buy {
			held += t.Quantity
			continue
		}
		held -= t.Quantity
		if held < -1e-9 {
			return (&Problems{"Am " + t.Date + " wird mehr verkauft als im Bestand ist"}).Err()
		}
	}
	return nil
}
