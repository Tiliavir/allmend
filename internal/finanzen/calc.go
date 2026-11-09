package finanzen

import (
	"math"
	"sort"
)

// TaxPercent is the flat capital gains tax used for "after tax" figures
// (Kapitalertragssteuer, as in the old app).
const TaxPercent = 25

// AfterTax deducts the capital gains tax from a positive amount; losses stay.
func AfterTax(c int64) int64 {
	if c <= 0 {
		return c
	}
	return c * (100 - TaxPercent) / 100
}

// Holding is the state of one security after its transactions (average cost
// method: a sell takes the average cost of the units held).
type Holding struct {
	Held          float64
	CostCents     int64 // cost of the units still held, fees and taxes included
	FeesCents     int64 // all buy fees (part of the cost)
	TaxesCents    int64 // all buy taxes (part of the cost)
	RealizedCents int64 // gain of all sells
}

// Apply runs txs (sorted by date) up to and including until ("" = all).
func Apply(txs []Transaction, until string) Holding {
	var h Holding
	for _, t := range txs {
		if until != "" && t.Date > until {
			break
		}
		gross := int64(math.Round(t.Quantity * float64(t.PriceCents)))
		if t.Type == Buy {
			h.CostCents += gross + t.FeesCents + t.TaxesCents
			h.FeesCents += t.FeesCents
			h.TaxesCents += t.TaxesCents
			h.Held += t.Quantity
			continue
		}
		if h.Held > 0 {
			alloc := int64(math.Round(t.Quantity * float64(h.CostCents) / h.Held))
			h.RealizedCents += gross - t.FeesCents - alloc
			h.CostCents -= alloc
		}
		h.Held -= t.Quantity
	}
	if math.Abs(h.Held) < 1e-9 {
		h.Held, h.CostCents = 0, 0
	}
	return h
}

// PriceAt is the close on date or the last one before it.
func PriceAt(ps []Price, date string) (float64, bool) {
	i := sort.Search(len(ps), func(i int) bool { return ps[i].Date > date })
	if i == 0 {
		return 0, false
	}
	return ps[i-1].Close, true
}

// Latest is the newest price.
func Latest(ps []Price) (Price, bool) {
	if len(ps) == 0 {
		return Price{}, false
	}
	return ps[len(ps)-1], true
}

// Cents converts a quantity at a price into minor units.
func Cents(qty, price float64) int64 { return int64(math.Round(qty * price * 100)) }

// RiskClass derives an SRRI-like class 1–7 from the annualised volatility of
// daily returns over the last five years; 0 if there are too few prices.
func RiskClass(ps []Price) int {
	if len(ps) > 1260 {
		ps = ps[len(ps)-1260:]
	}
	var rs []float64
	for i := 1; i < len(ps); i++ {
		if ps[i-1].Close > 0 {
			rs = append(rs, (ps[i].Close-ps[i-1].Close)/ps[i-1].Close)
		}
	}
	if len(rs) < 20 {
		return 0
	}
	var sum float64
	for _, r := range rs {
		sum += r
	}
	mean := sum / float64(len(rs))
	var v float64
	for _, r := range rs {
		v += (r - mean) * (r - mean)
	}
	vol := math.Sqrt(v/float64(len(rs)-1)) * math.Sqrt(252) * 100
	for i, limit := range []float64{0.5, 2, 5, 10, 15, 25} {
		if vol < limit {
			return i + 1
		}
	}
	return 7
}

// Converter turns amounts into the base currency with stored rates.
type Converter struct {
	Base  string
	Rates map[string]Rate // by currency
}

// ToBase converts c minor units of cur; ok is false if no rate is known.
func (cv Converter) ToBase(c int64, cur string) (int64, bool) {
	if cur == cv.Base {
		return c, true
	}
	r, ok := cv.Rates[cur]
	if !ok {
		return 0, false
	}
	return int64(math.Round(float64(c) * r.Rate)), true
}

// Factor is the multiplier from cur into the base currency.
func (cv Converter) Factor(cur string) (float64, bool) {
	if cur == cv.Base {
		return 1, true
	}
	r, ok := cv.Rates[cur]
	return r.Rate, ok
}

// Effective merges a security's manual weights with the provider profile:
// a dimension the provider delivers replaces the manual one.
func Effective(manual, provider []Weight) map[string][]Weight {
	out := map[string][]Weight{}
	for _, w := range provider {
		out[w.Dimension] = append(out[w.Dimension], w)
	}
	for _, w := range manual {
		if _, fromProvider := providerHas(provider, w.Dimension); !fromProvider {
			out[w.Dimension] = append(out[w.Dimension], w)
		}
	}
	for d := range out {
		sort.SliceStable(out[d], func(i, j int) bool { return out[d][i].Weight > out[d][j].Weight })
	}
	return out
}

func providerHas(ws []Weight, dim string) (int, bool) {
	for i, w := range ws {
		if w.Dimension == dim {
			return i, true
		}
	}
	return 0, false
}
