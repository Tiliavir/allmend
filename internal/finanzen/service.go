package finanzen

import (
	"context"
	"errors"
	"math"
	"slices"
	"sort"
	"strconv"
	"time"

	"github.com/tiliavir/allmend/internal/account"
	"github.com/tiliavir/allmend/internal/id"
)

// Service implements the finance use cases for the acting account (always
// the owner).
type Service struct {
	Repo   Repository
	Market Market
	Base   string // base currency (ISO 4217)
	Now    func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// book is everything of one owner, loaded once per page.
type book struct {
	assets []Asset
	secs   []Security
	txs    map[string][]Transaction
	divs   map[string][]Dividend
	prices map[string][]Price // by symbol
	conv   Converter
	miss   []string // missing market data, German
}

func (s *Service) load(ctx context.Context, owner string, withPrices bool) (*book, error) {
	b := &book{txs: map[string][]Transaction{}, divs: map[string][]Dividend{}, prices: map[string][]Price{},
		conv: Converter{Base: s.Base, Rates: map[string]Rate{}}}
	var err error
	if b.assets, err = s.Repo.Assets(ctx, owner); err != nil {
		return nil, err
	}
	if b.secs, err = s.Repo.Securities(ctx, owner); err != nil {
		return nil, err
	}
	txs, err := s.Repo.Transactions(ctx, owner)
	if err != nil {
		return nil, err
	}
	for _, t := range txs {
		b.txs[t.SecurityID] = append(b.txs[t.SecurityID], t)
	}
	divs, err := s.Repo.Dividends(ctx, owner)
	if err != nil {
		return nil, err
	}
	for _, d := range divs {
		b.divs[d.SecurityID] = append(b.divs[d.SecurityID], d)
	}
	curs := map[string]bool{}
	for _, a := range b.assets {
		curs[a.Currency] = true
	}
	for _, sec := range b.secs {
		curs[sec.Currency] = true
		if withPrices && sec.Symbol != "" {
			if _, done := b.prices[sec.Symbol]; done {
				continue
			}
			if b.prices[sec.Symbol], err = s.Market.Prices(ctx, sec.Symbol); err != nil {
				return nil, err
			}
		}
	}
	for _, c := range sortedKeys(curs) {
		if c == s.Base {
			continue
		}
		r, err := s.Market.Rate(ctx, c, s.Base)
		switch {
		case errors.Is(err, ErrNotFound):
			b.miss = append(b.miss, "Kein Wechselkurs "+c+" → "+s.Base)
		case err != nil:
			return nil, err
		default:
			b.conv.Rates[c] = r
		}
	}
	return b, nil
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ── overview ──────────────────────────────────────────

// Position is one row of the wealth overview.
type Position struct {
	ID                 string // "" for the securities row
	Name               string
	Kind               string // asset kind or "wertpapiere"
	KindLabel          string
	Currency           string
	ValueCents         int64
	BaseCents          int64
	HasBase            bool
	AsOf               string
	TaxAllowanceCents  int64
	CostCents          int64
	PaidInCents        int64
	DistributionsCents int64
	GainCents          int64
	CountsGain         bool
}

// GainAfterTaxCents is the gain after capital gains tax.
func (p Position) GainAfterTaxCents() int64 { return AfterTax(p.GainCents) }

// Overview is the wealth page.
type Overview struct {
	Positions  []Position
	TotalCents int64
	GainCents  int64
	Base       string
	Missing    []string
	Securities []Summary
}

// GainAfterTaxCents is the total gain after tax.
func (o Overview) GainAfterTaxCents() int64 { return AfterTax(o.GainCents) }

// Summary is one security with its current state.
type Summary struct {
	Security
	Holding
	PriceCents     int64
	PriceDate      string
	HasPrice       bool
	ValueCents     int64 // security currency
	DividendsCents int64
	GainCents      int64 // value − cost + dividends (unrealised view)
}

// GainAfterTaxCents is the gain after tax.
func (m Summary) GainAfterTaxCents() int64 { return AfterTax(m.GainCents) }

func (b *book) summary(sec Security) Summary {
	m := Summary{Security: sec, Holding: Apply(b.txs[sec.ID], "")}
	for _, d := range b.divs[sec.ID] {
		m.DividendsCents += d.AmountCents
	}
	if p, ok := Latest(b.prices[sec.Symbol]); ok && sec.Symbol != "" {
		m.HasPrice, m.PriceDate = true, p.Date
		m.PriceCents = Cents(1, p.Close)
		m.ValueCents = Cents(m.Held, p.Close)
	}
	m.GainCents = m.ValueCents - m.CostCents + m.DividendsCents
	return m
}

// Overview builds the wealth overview of a.
func (s *Service) Overview(ctx context.Context, a account.Account) (Overview, error) {
	b, err := s.load(ctx, a.ID, true)
	if err != nil {
		return Overview{}, err
	}
	o := Overview{Base: s.Base, Missing: b.miss}
	for _, as := range b.assets {
		p := Position{ID: as.ID, Name: as.Name, Kind: string(as.Kind), KindLabel: as.Kind.Label(), Currency: as.Currency,
			ValueCents: as.ValueCents, AsOf: as.AsOf, TaxAllowanceCents: as.TaxAllowanceCents, CostCents: as.CostCents,
			PaidInCents: as.PaidInCents, DistributionsCents: as.DistributionsCents, GainCents: as.GainCents(), CountsGain: as.Kind.CountsGain()}
		if as.Category != "" {
			p.KindLabel += " · " + as.Category
		}
		p.BaseCents, p.HasBase = b.conv.ToBase(as.ValueCents, as.Currency)
		o.Positions = append(o.Positions, p)
	}
	sp := Position{Name: "Wertpapiere", Kind: "wertpapiere", KindLabel: "Wertpapiere", Currency: s.Base, HasBase: true, CountsGain: true}
	held := false
	for _, sec := range b.secs {
		if sec.Archived {
			continue
		}
		m := b.summary(sec)
		if m.Held == 0 {
			continue
		}
		o.Securities = append(o.Securities, m)
		held = true
		if sec.Symbol != "" && !m.HasPrice {
			o.Missing = append(o.Missing, "Noch kein Kurs für "+sec.Name)
		}
		f, ok := b.conv.Factor(sec.Currency)
		if !ok {
			sp.HasBase = false
			continue
		}
		sp.BaseCents += int64(math.Round(float64(m.ValueCents) * f))
		sp.CostCents += int64(math.Round(float64(m.CostCents) * f))
		sp.DistributionsCents += int64(math.Round(float64(m.DividendsCents) * f))
	}
	if held {
		sp.ValueCents = sp.BaseCents
		sp.GainCents = sp.BaseCents - sp.CostCents + sp.DistributionsCents
		o.Positions = append(o.Positions, sp)
	}
	for _, p := range o.Positions {
		if !p.HasBase {
			continue
		}
		o.TotalCents += p.BaseCents
		if p.CountsGain {
			g := p.GainCents
			if p.Kind != "wertpapiere" {
				g, _ = b.conv.ToBase(p.GainCents, p.Currency)
			}
			o.GainCents += g
		}
	}
	return o, nil
}

// Securities lists a's securities (active or archived) with their state.
func (s *Service) Securities(ctx context.Context, a account.Account, archived bool) ([]Summary, error) {
	b, err := s.load(ctx, a.ID, true)
	if err != nil {
		return nil, err
	}
	var out []Summary
	for _, sec := range b.secs {
		if sec.Archived == archived {
			out = append(out, b.summary(sec))
		}
	}
	return out, nil
}

// ── security detail ───────────────────────────────────

// DividendRow is a dividend with its per-unit amount.
type DividendRow struct {
	Dividend
	PerUnitCents int64 // 0 if nothing was held
}

// YearAmount is a sum per calendar year.
type YearAmount struct {
	Year  int
	Cents int64
}

// Detail is the page of one security.
type Detail struct {
	Summary
	UnrealizedCents int64
	Transactions    []Transaction // newest first
	Dividends       []DividendRow // newest first
	DividendYears   []YearAmount  // newest first
	RiskClass       int
	RiskDerived     bool
	Composition     map[string][]Weight
	Prices          []Price
}

// Security builds the detail page of one of a's securities.
func (s *Service) Security(ctx context.Context, a account.Account, secID string) (Detail, error) {
	sec, err := s.Repo.Security(ctx, a.ID, secID)
	if err != nil {
		return Detail{}, err
	}
	b, err := s.load(ctx, a.ID, false)
	if err != nil {
		return Detail{}, err
	}
	if sec.Symbol != "" {
		if b.prices[sec.Symbol], err = s.Market.Prices(ctx, sec.Symbol); err != nil {
			return Detail{}, err
		}
	}
	d := Detail{Summary: b.summary(sec), Prices: b.prices[sec.Symbol]}
	d.UnrealizedCents = d.ValueCents - d.CostCents
	txs := b.txs[sec.ID]
	for i := len(txs) - 1; i >= 0; i-- {
		d.Transactions = append(d.Transactions, txs[i])
	}
	years := map[int]int64{}
	divs := b.divs[sec.ID]
	for i := len(divs) - 1; i >= 0; i-- {
		dv := divs[i]
		row := DividendRow{Dividend: dv}
		if held := Apply(txs, dv.Date).Held; held > 0 {
			row.PerUnitCents = int64(math.Round(float64(dv.AmountCents) / held))
		}
		d.Dividends = append(d.Dividends, row)
		y, _ := strconv.Atoi(dv.Date[:4])
		years[y] += dv.AmountCents
	}
	for y, c := range years {
		d.DividendYears = append(d.DividendYears, YearAmount{y, c})
	}
	sort.Slice(d.DividendYears, func(i, j int) bool { return d.DividendYears[i].Year > d.DividendYears[j].Year })
	d.RiskClass = sec.RiskOverride
	if d.RiskClass == 0 {
		d.RiskClass, d.RiskDerived = RiskClass(d.Prices), true
	}
	prov, err := s.profile(ctx, sec)
	if err != nil {
		return Detail{}, err
	}
	d.Composition = Effective(sec.Weights, prov)
	return d, nil
}

func (s *Service) profile(ctx context.Context, sec Security) ([]Weight, error) {
	if sec.Symbol == "" {
		return nil, nil
	}
	p, err := s.Market.Profile(ctx, sec.Symbol)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	return p.Weights, err
}

// ── diversification ───────────────────────────────────

// Share is a label with its part of a whole (0–1).
type Share struct {
	Label string
	Share float64
}

// Diversification is the composition and risk of the portfolio.
type Diversification struct {
	Dimensions       map[string][]Share
	Risk             []Share // by class
	WeightedRisk     float64 // 0 = unknown
	AssetClasses     []Share
	SecuritiesCents  int64
	TotalCents       int64
	Base             string
	Missing          []string
	Securities       []Security // active ones, for the filter
	ExcludedSecurity map[string]bool
	ExcludedKind     map[string]bool
}

// AssetClassLabels names the asset classes of the diversification page.
var AssetClassLabels = map[string]string{
	string(Konto): "Barmittel", string(Bauspar): "Bauspar", string(Vorsorge): "Vorsorge",
	string(Sachwert): "Sachanlagen", "wertpapiere": "Wertpapiere",
}

// Diversification aggregates the active securities of a (minus the excluded
// ones) by value, and all wealth by asset class (minus excluded kinds).
func (s *Service) Diversification(ctx context.Context, a account.Account, exSec, exKind []string) (Diversification, error) {
	b, err := s.load(ctx, a.ID, true)
	if err != nil {
		return Diversification{}, err
	}
	dv := Diversification{Dimensions: map[string][]Share{}, Base: s.Base, Missing: b.miss,
		ExcludedSecurity: map[string]bool{}, ExcludedKind: map[string]bool{}}
	for _, x := range exSec {
		dv.ExcludedSecurity[x] = true
	}
	for _, x := range exKind {
		dv.ExcludedKind[x] = true
	}
	type entry struct {
		value int64
		comp  map[string][]Weight
		risk  int
	}
	var es []entry
	for _, sec := range b.secs {
		if sec.Archived {
			continue
		}
		dv.Securities = append(dv.Securities, sec)
		if dv.ExcludedSecurity[sec.ID] || dv.ExcludedKind["wertpapiere"] {
			continue
		}
		m := b.summary(sec)
		if m.Held == 0 || !m.HasPrice {
			continue
		}
		v, ok := b.conv.ToBase(m.ValueCents, sec.Currency)
		if !ok {
			continue
		}
		prov, err := s.profile(ctx, sec)
		if err != nil {
			return dv, err
		}
		risk := sec.RiskOverride
		if risk == 0 {
			risk = RiskClass(b.prices[sec.Symbol])
		}
		es = append(es, entry{v, Effective(sec.Weights, prov), risk})
		dv.SecuritiesCents += v
	}
	classes := map[string]int64{}
	if dv.SecuritiesCents > 0 {
		classes["wertpapiere"] = dv.SecuritiesCents
		sums := map[string]map[string]float64{}
		risk := map[int]float64{}
		var rn, rd float64
		for _, e := range es {
			w := float64(e.value) / float64(dv.SecuritiesCents)
			for dim, ws := range e.comp {
				if sums[dim] == nil {
					sums[dim] = map[string]float64{}
				}
				for _, x := range ws {
					sums[dim][x.Label] += w * x.Weight
				}
			}
			if e.risk > 0 {
				risk[e.risk] += w
				rn += w * float64(e.risk)
				rd += w
			}
		}
		for dim, m := range sums {
			dv.Dimensions[dim] = shares(m)
			if dim == Holdings && len(dv.Dimensions[dim]) > 10 {
				dv.Dimensions[dim] = dv.Dimensions[dim][:10]
			}
		}
		for c := 1; c <= 7; c++ {
			if risk[c] > 0 {
				dv.Risk = append(dv.Risk, Share{"Klasse " + strconv.Itoa(c), risk[c]})
			}
		}
		if rd > 0 {
			dv.WeightedRisk = rn / rd
		}
	}
	for _, as := range b.assets {
		if dv.ExcludedKind[string(as.Kind)] {
			continue
		}
		if v, ok := b.conv.ToBase(as.ValueCents, as.Currency); ok {
			classes[string(as.Kind)] += v
		}
	}
	m := map[string]float64{}
	for k, v := range classes {
		dv.TotalCents += v
		m[AssetClassLabels[k]] = float64(v)
	}
	if dv.TotalCents > 0 {
		for k := range m {
			m[k] /= float64(dv.TotalCents)
		}
		dv.AssetClasses = shares(m)
	}
	return dv, nil
}

func shares(m map[string]float64) []Share {
	out := make([]Share, 0, len(m))
	for k, v := range m {
		if v > 0 {
			out = append(out, Share{k, v})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Share != out[j].Share {
			return out[i].Share > out[j].Share
		}
		return out[i].Label < out[j].Label
	})
	return out
}

// ── development (charts) ──────────────────────────────

// Filter restricts the development charts.
type Filter struct {
	SecurityID string
	Kind       string
	From, To   string // YYYY-MM-DD or ""
}

// Development holds the chart series, in the base currency. Foreign
// currencies are converted with today's rate (no history is stored).
type Development struct {
	Dates      []string
	Value      []float64 // portfolio value
	Unrealized []float64
	Realized   []float64
	Dividends  []float64
	Indexed    []IndexedSeries // price per security, 100 = average buy price
	Securities []Security      // all, for the filter
	Base       string
	Missing    []string
}

// IndexedSeries is one security's price relative to its average buy price.
type IndexedSeries struct {
	Name   string
	Values []*float64 // aligned with Development.Dates; nil = no price yet
}

// Development computes value and gain over time for a's securities.
func (s *Service) Development(ctx context.Context, a account.Account, f Filter) (Development, error) {
	b, err := s.load(ctx, a.ID, true)
	if err != nil {
		return Development{}, err
	}
	dev := Development{Base: s.Base, Securities: b.secs, Missing: b.miss}
	type sel struct {
		sec    Security
		factor float64
	}
	var picked []sel
	dates := map[string]bool{}
	for _, sec := range b.secs {
		if (f.SecurityID != "" && sec.ID != f.SecurityID) || (f.Kind != "" && sec.Kind != f.Kind) || sec.Symbol == "" {
			continue
		}
		if len(b.txs[sec.ID]) == 0 || len(b.prices[sec.Symbol]) == 0 {
			continue
		}
		fac, ok := b.conv.Factor(sec.Currency)
		if !ok {
			continue
		}
		picked = append(picked, sel{sec, fac})
		first := b.txs[sec.ID][0].Date
		for _, p := range b.prices[sec.Symbol] {
			if p.Date >= first && (f.From == "" || p.Date >= f.From) && (f.To == "" || p.Date <= f.To) {
				dates[p.Date] = true
			}
		}
	}
	dev.Dates = sortedKeys(dates)
	n := len(dev.Dates)
	dev.Value, dev.Unrealized, dev.Realized, dev.Dividends = make([]float64, n), make([]float64, n), make([]float64, n), make([]float64, n)
	for _, p := range picked {
		txs, divs, ps := b.txs[p.sec.ID], b.divs[p.sec.ID], b.prices[p.sec.Symbol]
		avg := averageBuy(txs)
		ix := IndexedSeries{Name: p.sec.Name, Values: make([]*float64, n)}
		di := 0
		var divSum int64
		for i, date := range dev.Dates {
			for di < len(divs) && divs[di].Date <= date {
				divSum += divs[di].AmountCents
				di++
			}
			price, ok := PriceAt(ps, date)
			if !ok {
				continue
			}
			h := Apply(txs, date)
			value := h.Held * price * p.factor
			dev.Value[i] += value
			dev.Unrealized[i] += value - float64(h.CostCents)/100*p.factor
			dev.Realized[i] += float64(h.RealizedCents) / 100 * p.factor
			dev.Dividends[i] += float64(divSum) / 100 * p.factor
			if avg > 0 {
				v := math.Round(price/avg*1000) / 10
				ix.Values[i] = &v
			}
		}
		if avg > 0 {
			dev.Indexed = append(dev.Indexed, ix)
		}
	}
	for i := range dev.Value {
		dev.Value[i] = math.Round(dev.Value[i])
		dev.Unrealized[i] = math.Round(dev.Unrealized[i])
		dev.Realized[i] = math.Round(dev.Realized[i])
		dev.Dividends[i] = math.Round(dev.Dividends[i])
	}
	return dev, nil
}

// averageBuy is the quantity-weighted average buy price (major units).
func averageBuy(txs []Transaction) float64 {
	var q, c float64
	for _, t := range txs {
		if t.Type == Buy {
			q += t.Quantity
			c += t.Quantity * float64(t.PriceCents) / 100
		}
	}
	if q == 0 {
		return 0
	}
	return c / q
}

// ── writes ────────────────────────────────────────────

// SaveAsset creates (ID empty) or updates an asset of a.
func (s *Service) SaveAsset(ctx context.Context, a account.Account, as Asset) (Asset, error) {
	if err := CheckAsset(as); err != nil {
		return as, err
	}
	as.OwnerID, as.UpdatedAt = a.ID, s.now()
	if as.ID == "" {
		as.ID, as.Version, as.CreatedAt = id.New(), 1, as.UpdatedAt
		return as, s.Repo.InsertAsset(ctx, as)
	}
	old, err := s.Repo.Asset(ctx, a.ID, as.ID)
	if err != nil {
		return as, err
	}
	want := as.Version
	as.CreatedAt, as.Version = old.CreatedAt, want+1
	return as, s.Repo.UpdateAsset(ctx, as, want)
}

// SaveSecurity creates (ID empty) or updates a security of a.
func (s *Service) SaveSecurity(ctx context.Context, a account.Account, sec Security) (Security, error) {
	if err := CheckSecurity(sec); err != nil {
		return sec, err
	}
	sec.OwnerID, sec.UpdatedAt = a.ID, s.now()
	if sec.ID == "" {
		sec.ID, sec.Version, sec.CreatedAt = id.New(), 1, sec.UpdatedAt
		return sec, s.Repo.InsertSecurity(ctx, sec)
	}
	old, err := s.Repo.Security(ctx, a.ID, sec.ID)
	if err != nil {
		return sec, err
	}
	want := sec.Version
	sec.CreatedAt, sec.Version = old.CreatedAt, want+1
	return sec, s.Repo.UpdateSecurity(ctx, sec, want)
}

// SetArchived archives (with today's date) or restores a security.
func (s *Service) SetArchived(ctx context.Context, a account.Account, secID string, archived bool, today string) error {
	sec, err := s.Repo.Security(ctx, a.ID, secID)
	if err != nil {
		return err
	}
	sec.Archived, sec.ArchivedAt = archived, ""
	if archived {
		sec.ArchivedAt = today
	}
	_, err = s.SaveSecurity(ctx, a, sec)
	return err
}

// SaveTransaction creates or updates a transaction on one of a's securities.
// A sell may never exceed the holding at its date, also after edits.
func (s *Service) SaveTransaction(ctx context.Context, a account.Account, t Transaction) (Transaction, error) {
	if err := CheckTransaction(t); err != nil {
		return t, err
	}
	if _, err := s.Repo.Security(ctx, a.ID, t.SecurityID); err != nil {
		return t, err
	}
	all, err := s.Repo.Transactions(ctx, a.ID)
	if err != nil {
		return t, err
	}
	t.OwnerID, t.UpdatedAt = a.ID, s.now()
	var old Transaction
	if t.ID != "" {
		if old, err = s.Repo.Transaction(ctx, a.ID, t.ID); err != nil {
			return t, err
		}
		if old.SecurityID != t.SecurityID {
			return t, ErrNotFound
		}
	}
	var list []Transaction
	for _, x := range all {
		if x.SecurityID == t.SecurityID && x.ID != t.ID {
			list = append(list, x)
		}
	}
	list = append(list, t)
	sortTransactions(list)
	if err := CheckHoldings(list); err != nil {
		return t, err
	}
	if t.ID == "" {
		t.ID, t.Version, t.CreatedAt = id.New(), 1, t.UpdatedAt
		return t, s.Repo.InsertTransaction(ctx, t)
	}
	want := t.Version
	t.CreatedAt, t.Version = old.CreatedAt, want+1
	return t, s.Repo.UpdateTransaction(ctx, t, want)
}

// DeleteTransaction removes a transaction unless a later sell needs it.
func (s *Service) DeleteTransaction(ctx context.Context, a account.Account, txID string) (Transaction, error) {
	t, err := s.Repo.Transaction(ctx, a.ID, txID)
	if err != nil {
		return t, err
	}
	all, err := s.Repo.Transactions(ctx, a.ID)
	if err != nil {
		return t, err
	}
	var list []Transaction
	for _, x := range all {
		if x.SecurityID == t.SecurityID && x.ID != t.ID {
			list = append(list, x)
		}
	}
	if err := CheckHoldings(list); err != nil {
		return t, err
	}
	return t, s.Repo.DeleteTransaction(ctx, a.ID, txID)
}

// sortTransactions orders like the repository: by date, buys before sells
// on the same day, then by creation.
func sortTransactions(ts []Transaction) {
	slices.SortStableFunc(ts, func(x, y Transaction) int {
		if x.Date != y.Date {
			if x.Date < y.Date {
				return -1
			}
			return 1
		}
		if x.Type != y.Type {
			if x.Type == Buy {
				return -1
			}
			return 1
		}
		return x.CreatedAt.Compare(y.CreatedAt)
	})
}

// SaveDividend creates or updates a dividend on one of a's securities.
func (s *Service) SaveDividend(ctx context.Context, a account.Account, d Dividend) (Dividend, error) {
	if err := CheckDividend(d); err != nil {
		return d, err
	}
	if _, err := s.Repo.Security(ctx, a.ID, d.SecurityID); err != nil {
		return d, err
	}
	d.OwnerID, d.UpdatedAt = a.ID, s.now()
	if d.ID == "" {
		d.ID, d.Version, d.CreatedAt = id.New(), 1, d.UpdatedAt
		return d, s.Repo.InsertDividend(ctx, d)
	}
	old, err := s.Repo.Dividend(ctx, a.ID, d.ID)
	if err != nil {
		return d, err
	}
	if old.SecurityID != d.SecurityID {
		return d, ErrNotFound
	}
	want := d.Version
	d.CreatedAt, d.Version = old.CreatedAt, want+1
	return d, s.Repo.UpdateDividend(ctx, d, want)
}

// SaveYear creates (Version 0) or updates a yearly snapshot of a.
func (s *Service) SaveYear(ctx context.Context, a account.Account, y Year) (Year, error) {
	if err := CheckYear(y); err != nil {
		return y, err
	}
	y.OwnerID, y.UpdatedAt = a.ID, s.now()
	if y.Version == 0 {
		if _, err := s.Repo.Year(ctx, a.ID, y.Year); err == nil {
			return y, (&Problems{"Für " + strconv.Itoa(y.Year) + " gibt es schon einen Eintrag"}).Err()
		} else if !errors.Is(err, ErrNotFound) {
			return y, err
		}
		y.Version = 1
		return y, s.Repo.InsertYear(ctx, y)
	}
	want := y.Version
	y.Version = want + 1
	return y, s.Repo.UpdateYear(ctx, y, want)
}
