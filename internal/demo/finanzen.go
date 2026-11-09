package demo

import (
	"math"
	"strconv"
	"time"

	"github.com/tiliavir/allmend/internal/finanzen"
)

// finanzen gives Anna a few assets, three securities with monthly savings,
// a sale and dividends, about three years of made-up prices and yearly
// snapshots. Market data goes straight into the market store, as the daily
// job would put it there.
func (s *seeder) finanzen(f *family) error {
	fin := s.Finanzen
	for _, a := range []finanzen.Asset{
		{Name: "Girokonto", Kind: finanzen.Konto, Currency: "EUR", ValueCents: 412_530, AsOf: s.date(9)},
		{Name: "Tagesgeld", Kind: finanzen.Konto, Currency: "EUR", ValueCents: 1_850_000, AsOf: s.date(9)},
		{Name: "Bausparvertrag", Kind: finanzen.Bauspar, Currency: "EUR", ValueCents: 1_420_000, PaidInCents: 1_300_000, AsOf: s.date(40), MaturityDate: s.today.AddDate(4, 0, 0).Format(time.DateOnly)},
		{Name: "Säule 3a", Kind: finanzen.Vorsorge, Currency: "CHF", ValueCents: 3_150_000, PaidInCents: 2_900_000, AsOf: s.date(40)},
		{Name: "Reka-Guthaben", Kind: finanzen.Sachwert, Category: "Reka", Currency: "CHF", ValueCents: 65_000, CostCents: 60_000},
	} {
		if _, err := fin.SaveAsset(s.ctx, f.anna, a); err != nil {
			return err
		}
	}
	type sec struct {
		finanzen.Security
		start, drift, vol float64
	}
	secs := []sec{
		{finanzen.Security{Name: "Welt-ETF", Kind: finanzen.ETF, Currency: "EUR", ISIN: "IE0000000018", Symbol: "WELT.DEMO",
			Weights: []finanzen.Weight{{Dimension: finanzen.Sector, Label: "Technologie", Weight: 0.26}, {Dimension: finanzen.Sector, Label: "Finanzen", Weight: 0.16},
				{Dimension: finanzen.Sector, Label: "Gesundheit", Weight: 0.11}, {Dimension: finanzen.Country, Label: "USA", Weight: 0.62},
				{Dimension: finanzen.Country, Label: "Japan", Weight: 0.06}, {Dimension: finanzen.Currency, Label: "USD", Weight: 0.64},
				{Dimension: finanzen.Currency, Label: "EUR", Weight: 0.1}, {Dimension: finanzen.Holdings, Label: "Grosser Chiphersteller", Weight: 0.045}}}, 95, 0.0004, 0.008},
		{finanzen.Security{Name: "Schwellenländer-ETF", Kind: finanzen.ETF, Currency: "EUR", ISIN: "IE0000000026", Symbol: "EMER.DEMO",
			Weights: []finanzen.Weight{{Dimension: finanzen.Country, Label: "China", Weight: 0.25}, {Dimension: finanzen.Country, Label: "Indien", Weight: 0.19}}}, 28, 0.0002, 0.011},
		{finanzen.Security{Name: "Uhrenfabrik AG", Kind: finanzen.Stock, Currency: "CHF", Symbol: "UHR.DEMO",
			Weights: []finanzen.Weight{{Dimension: finanzen.Sector, Label: "Industrie", Weight: 1}, {Dimension: finanzen.Country, Label: "Schweiz", Weight: 1}}}, 240, 0.0001, 0.014},
	}
	at := s.today
	first := s.today.AddDate(-3, 0, 0)
	for i := range secs {
		saved, err := fin.SaveSecurity(s.ctx, f.anna, secs[i].Security)
		if err != nil {
			return err
		}
		secs[i].Security = saved
		var ps []finanzen.Price
		p := secs[i].start
		for d := first; !d.After(s.today); d = d.AddDate(0, 0, 1) {
			if d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
				continue
			}
			p *= math.Exp(secs[i].drift + secs[i].vol*s.rnd.NormFloat64())
			ps = append(ps, finanzen.Price{Date: d.Format(time.DateOnly), Close: math.Round(p*100) / 100})
		}
		if _, err := fin.Market.AddPrices(s.ctx, saved.Symbol, ps, at); err != nil {
			return err
		}
	}
	buy := func(sec finanzen.Security, date string, qty float64, price int64) error {
		_, err := fin.SaveTransaction(s.ctx, f.anna, finanzen.Transaction{SecurityID: sec.ID, Date: date, Type: finanzen.Buy, Quantity: qty, PriceCents: price, FeesCents: 100})
		return err
	}
	for m := 34; m >= 1; m-- {
		d := s.today.AddDate(0, -m, 0).Format(time.DateOnly)
		if err := buy(secs[0].Security, d, 4, int64(9500+(34-m)*120)); err != nil {
			return err
		}
		if m%3 == 0 {
			if err := buy(secs[1].Security, d, 10, int64(2800+(34-m)*10)); err != nil {
				return err
			}
		}
	}
	if err := buy(secs[2].Security, s.date(900), 15, 24550); err != nil {
		return err
	}
	if _, err := fin.SaveTransaction(s.ctx, f.anna, finanzen.Transaction{SecurityID: secs[2].ID, Date: s.date(300), Type: finanzen.Sell, Quantity: 5, PriceCents: 26800, FeesCents: 1500}); err != nil {
		return err
	}
	for y := 2; y >= 0; y-- {
		for _, dv := range []finanzen.Dividend{
			{SecurityID: secs[0].ID, Date: s.today.AddDate(-y, -2, 0).Format(time.DateOnly), Currency: secs[0].Currency, AmountCents: int64(6000 + (2-y)*3500)},
			{SecurityID: secs[2].ID, Date: s.today.AddDate(-y, -6, 0).Format(time.DateOnly), Currency: secs[2].Currency, AmountCents: int64(9600 + (2-y)*800)},
		} {
			if _, err := fin.SaveDividend(s.ctx, f.anna, dv); err != nil {
				return err
			}
		}
	}
	if err := fin.Market.AddRate(s.ctx, finanzen.Rate{Currency: "CHF", Base: "EUR", Date: s.date(1), Rate: 1.07}, at); err != nil {
		return err
	}
	for i, y := 0, s.today.Year()-5; y < s.today.Year(); i, y = i+1, y+1 {
		k := int64(i)
		yr := finanzen.Year{Year: y, Currency: "EUR", IncomeCents: (7_800_000 + k*300_000), TaxCents: (1_560_000 + k*60_000), PensionCents: 2_000_000 + k*400_000,
			CashCents: 2_800_000 + k*100_000, BausparCents: 900_000 + k*130_000, StocksCents: 300_000 + k*250_000, FundsCents: 800_000 + k*600_000}
		if i == 3 {
			yr.Comment = "Bausparvertrag aufgestockt, Sparplan auf " + strconv.Itoa(4) + " Anteile im Monat erhöht."
		}
		if _, err := fin.SaveYear(s.ctx, f.anna, yr); err != nil {
			return err
		}
	}
	return fin.Market.AddRun(s.ctx, finanzen.Run{At: at.Add(5 * time.Hour), OK: true, Message: "3 neue Kurse, 1 von 1 Wechselkursen"})
}
