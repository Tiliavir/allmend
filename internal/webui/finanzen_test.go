package webui_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/tiliavir/allmend/internal/finanzen"
)

// finSeed fills the admin's finances and returns the pages to render.
func finSeed(t *testing.T, e *env, admin *http.Client) map[string]string {
	t.Helper()
	ctx := context.Background()
	aid := adminID(e)
	must := func(r *http.Response, what string) {
		t.Helper()
		if r.StatusCode != http.StatusSeeOther {
			t.Fatalf("%s: %d", what, r.StatusCode)
		}
	}
	must(e.post(admin, "/finanzen/anlagen", url.Values{"name": {"Girokonto"}, "kind": {"konto"}, "currency": {"EUR"}, "value": {"1234,50"}, "as_of": {"2026-10-31"}}), "konto")
	must(e.post(admin, "/finanzen/anlagen", url.Values{"name": {"Säule 3a"}, "kind": {"vorsorge"}, "currency": {"CHF"}, "value": {"20000"}, "paid_in": {"18000"}}), "vorsorge")
	must(e.post(admin, "/finanzen/anlagen", url.Values{"name": {"Wohnung"}, "kind": {"sachwert"}, "category": {"Grundstück"}, "currency": {"EUR"}, "value": {"300000"}, "cost": {"250000"}}), "sachwert")
	must(e.post(admin, "/finanzen/wertpapiere", url.Values{"name": {"Welt-ETF"}, "kind": {"etf"}, "currency": {"EUR"}, "isin": {"IE00BK5BQT80"}, "symbol": {"VWCE.DEX"},
		"w_sector": {"Technologie = 25\nFinanzen = 15"}, "w_country": {"USA = 62,5"}}), "etf")
	must(e.post(admin, "/finanzen/wertpapiere", url.Values{"name": {"Firma AG"}, "kind": {"stock"}, "currency": {"USD"}, "symbol": {"FIRMA"}, "risk": {"6"}}), "aktie")
	secs, err := e.st.Finanzen().Securities(ctx, aid)
	if err != nil || len(secs) != 2 {
		t.Fatalf("securities: %v %d", err, len(secs))
	}
	firma, etf := secs[0], secs[1]
	must(e.post(admin, "/finanzen/wertpapiere/"+etf.ID+"/transaktionen", url.Values{"date": {"2025-01-10"}, "type": {"buy"}, "quantity": {"10"}, "price": {"100"}, "fees": {"5"}}), "kauf")
	must(e.post(admin, "/finanzen/wertpapiere/"+etf.ID+"/transaktionen", url.Values{"date": {"2025-06-10"}, "type": {"sell"}, "quantity": {"4"}, "price": {"120"}}), "verkauf")
	must(e.post(admin, "/finanzen/wertpapiere/"+firma.ID+"/transaktionen", url.Values{"date": {"2025-03-01"}, "type": {"buy"}, "quantity": {"2,5"}, "price": {"40"}}), "kauf usd")
	must(e.post(admin, "/finanzen/wertpapiere/"+etf.ID+"/dividenden", url.Values{"date": {"2025-09-01"}, "amount": {"12,30"}, "currency": {"EUR"}}), "dividende")
	must(e.post(admin, "/finanzen/verlauf", url.Values{"year": {"2023"}, "currency": {"CHF"}, "cash": {"8000"}}), "jahr chf")
	must(e.post(admin, "/finanzen/verlauf", url.Values{"year": {"2024"}, "currency": {"EUR"}, "income": {"90000"}, "cash": {"10000"}, "stocks": {"5000"}, "comment": {"**Gutes** Jahr"}}), "jahr")
	at := time.Now()
	var ps []finanzen.Price
	for d := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC); d.Before(time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC)); d = d.AddDate(0, 0, 3) {
		ps = append(ps, finanzen.Price{Date: d.Format(time.DateOnly), Close: 100 + float64(d.YearDay()%40)})
	}
	if _, err := e.st.Finanzen().AddPrices(ctx, "VWCE.DEX", ps, at); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.Finanzen().AddPrices(ctx, "FIRMA", ps, at); err != nil {
		t.Fatal(err)
	}
	if err := e.st.Finanzen().AddRate(ctx, finanzen.Rate{Currency: "USD", Base: "EUR", Date: "2025-12-30", Rate: 0.9}, at); err != nil {
		t.Fatal(err)
	}
	if err := e.st.Finanzen().AddRun(ctx, finanzen.Run{At: at, OK: false, Message: "1 neue Kurse. Fehler: XYZ: HTTP 500"}); err != nil {
		t.Fatal(err)
	}
	txs, _ := e.st.Finanzen().Transactions(ctx, aid)
	divs, _ := e.st.Finanzen().Dividends(ctx, aid)
	assets, _ := e.st.Finanzen().Assets(ctx, aid)
	return map[string]string{
		"fin":               "/finanzen",
		"fin-anlage-neu":    "/finanzen/anlagen/neu?art=bauspar",
		"fin-anlage":        "/finanzen/anlagen/" + assets[0].ID,
		"fin-wertpapiere":   "/finanzen/wertpapiere",
		"fin-archiv":        "/finanzen/wertpapiere?archiv=1",
		"fin-wp-neu":        "/finanzen/wertpapiere/neu",
		"fin-wp":            "/finanzen/wertpapiere/" + etf.ID,
		"fin-wp-usd":        "/finanzen/wertpapiere/" + firma.ID,
		"fin-wp-edit":       "/finanzen/wertpapiere/" + etf.ID + "/bearbeiten",
		"fin-tx-neu":        "/finanzen/wertpapiere/" + etf.ID + "/transaktionen/neu?typ=verkauf",
		"fin-tx":            "/finanzen/transaktionen/" + txs[0].ID,
		"fin-div-neu":       "/finanzen/wertpapiere/" + etf.ID + "/dividenden/neu",
		"fin-div":           "/finanzen/dividenden/" + divs[0].ID,
		"fin-entwicklung":   "/finanzen/entwicklung?zeitraum=alle",
		"fin-entwicklung-1": "/finanzen/entwicklung?wertpapier=" + etf.ID + "&zeitraum=1m",
		"fin-divers":        "/finanzen/diversifikation",
		"fin-divers-ohne":   "/finanzen/diversifikation?ohne_art=konto&ohne=" + firma.ID,
		"fin-verlauf":       "/finanzen/verlauf",
		"fin-verlauf-neu":   "/finanzen/verlauf/neu",
		"fin-verlauf-2024":  "/finanzen/verlauf/2024",
	}
}

func TestFinanzen(t *testing.T) {
	e := newEnv(t)
	admin := e.loginAdmin()
	ctx := context.Background()
	finSeed(t, e, admin)
	aid := adminID(e)
	secs, _ := e.st.Finanzen().Securities(ctx, aid)
	firma, etf := secs[0], secs[1]

	// Overview: values, conversion and gain.
	_, body := e.get(admin, "/finanzen")
	for _, want := range []string{"Girokonto", "Säule 3a", "Kein Wechselkurs CHF → EUR", "Wertpapiere", "HTTP 500"} {
		if !strings.Contains(body, want) {
			t.Errorf("overview lacks %q", want)
		}
	}
	// Average cost: 10 × 100 + 5 = 1005, 4 sold at 120 → realised 480 − 402 = 78.
	_, body = e.get(admin, "/finanzen/wertpapiere/"+etf.ID)
	for _, want := range []string{"78,00 EUR", "603,00 EUR", "Technologie", "62,5 %", "12,30 EUR"} {
		if !strings.Contains(body, want) {
			t.Errorf("detail lacks %q", want)
		}
	}
	// Years keep their currency; the chart needs a rate for mixed ones.
	_, body = e.get(admin, "/finanzen/verlauf")
	for _, want := range []string{"<td>CHF</td>", "Kein Wechselkurs CHF → EUR (betroffene Jahre"} {
		if !strings.Contains(body, want) {
			t.Errorf("years lack %q", want)
		}
	}
	_, body = e.get(admin, "/finanzen/wertpapiere/"+firma.ID)
	if !strings.Contains(body, "6 / 7") || !strings.Contains(body, "manuell") {
		t.Error("manual risk class not shown")
	}

	// Unexpected input is rejected, nothing is stored.
	bad := []struct {
		path string
		form url.Values
	}{
		{"/finanzen/wertpapiere/" + etf.ID + "/transaktionen", url.Values{"date": {"2025-07-01"}, "type": {"sell"}, "quantity": {"7"}, "price": {"1"}}},
		{"/finanzen/wertpapiere/" + etf.ID + "/transaktionen", url.Values{"date": {"2025-07-01"}, "type": {"buy"}, "quantity": {"1e3"}, "price": {"1"}}},
		{"/finanzen/wertpapiere/" + etf.ID + "/transaktionen", url.Values{"date": {"2025-02-30"}, "type": {"buy"}, "quantity": {"1"}, "price": {"1"}}},
		{"/finanzen/anlagen", url.Values{"name": {"X"}, "kind": {"aktie"}, "currency": {"EUR"}, "value": {"1"}}},
		{"/finanzen/anlagen", url.Values{"name": {"X"}, "kind": {"konto"}, "currency": {"EURO"}, "value": {"1"}}},
		{"/finanzen/anlagen", url.Values{"name": {"X"}, "kind": {"konto"}, "currency": {"EUR"}, "value": {"-5"}}},
		{"/finanzen/wertpapiere", url.Values{"name": {"X"}, "kind": {"etf"}, "currency": {"EUR"}, "isin": {"DE123"}}},
		{"/finanzen/wertpapiere", url.Values{"name": {"X"}, "kind": {"etf"}, "currency": {"EUR"}, "w_sector": {"A = 70\nB = 40"}}},
		{"/finanzen/wertpapiere", url.Values{"name": {"X"}, "kind": {"etf"}, "currency": {"EUR"}, "w_sector": {"nur Text"}}},
		{"/finanzen/verlauf", url.Values{"year": {"2024"}}},
		{"/finanzen/verlauf", url.Values{"year": {"2022"}, "currency": {"EURO"}}},
		{"/finanzen/wertpapiere/" + etf.ID + "/dividenden", url.Values{"date": {"2025-09-02"}, "amount": {"1"}}},
	}
	for i, b := range bad {
		if r := e.post(admin, b.path, b.form); r.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("bad input %d: %d", i, r.StatusCode)
		}
	}
	// Deleting the buy would leave the sell uncovered.
	txs, _ := e.st.Finanzen().Transactions(ctx, aid)
	e.post(admin, "/finanzen/transaktionen/"+txs[0].ID+"/loeschen", url.Values{"confirm": {"1"}})
	if after, _ := e.st.Finanzen().Transactions(ctx, aid); len(after) != len(txs) {
		t.Error("buy deleted although a sell needs it")
	}
	if r := e.post(admin, "/finanzen/wertpapiere/"+etf.ID+"/loeschen", url.Values{"confirm": {"1"}}); r.StatusCode != http.StatusSeeOther {
		t.Errorf("delete in use: %d", r.StatusCode)
	}
	if _, err := e.st.Finanzen().Security(ctx, aid, etf.ID); err != nil {
		t.Error("security with transactions was deleted")
	}

	// Archive: out of the overview, back again.
	e.post(admin, "/finanzen/wertpapiere/"+firma.ID+"/archiv", url.Values{"archived": {"1"}})
	if s, _ := e.st.Finanzen().Security(ctx, aid, firma.ID); !s.Archived || s.ArchivedAt == "" {
		t.Error("not archived")
	}
	if syms, _ := e.st.Finanzen().Symbols(ctx); len(syms) != 1 {
		t.Errorf("archived symbol still fetched: %v", syms)
	}

	// Private: another account with the module sees nothing of it.
	e.post(admin, "/verwaltung/konten", url.Values{"email": {"kim@example.org"}, "module": {"finanzen"}})
	adminAcc, _ := e.st.Accounts().ByEmail(ctx, adminEmail)
	kimAcc, _ := e.st.Accounts().ByEmail(ctx, "kim@example.org")
	pw, _ := e.accounts.ResetPassword(ctx, adminAcc, kimAcc.ID)
	kim := e.client()
	e.post(kim, "/anmelden", url.Values{"email": {"kim@example.org"}, "password": {pw}})
	e.post(kim, "/konto/passwort", url.Values{"next": {"ein-langes-passwort"}})
	if r, body := e.get(kim, "/finanzen"); r.StatusCode != 200 || strings.Contains(body, "Girokonto") {
		t.Errorf("kim overview: %d", r.StatusCode)
	} else if strings.Contains(body, `id="kurse"`) || strings.Contains(body, "XYZ") {
		t.Error("kim sees the price refresh log (names all accounts' symbols)")
	}
	if _, body := e.get(admin, "/finanzen"); !strings.Contains(body, "XYZ: HTTP 500") {
		t.Error("admin does not see the price refresh log")
	}
	for _, p := range []string{"/finanzen/wertpapiere/" + etf.ID, "/finanzen/transaktionen/" + txs[0].ID, "/finanzen/verlauf/2024"} {
		if r, _ := e.get(kim, p); r.StatusCode != http.StatusNotFound {
			t.Errorf("kim %s: %d", p, r.StatusCode)
		}
	}
	if r := e.post(kim, "/finanzen/wertpapiere/"+etf.ID+"/transaktionen", url.Values{"date": {"2025-07-01"}, "type": {"buy"}, "quantity": {"1"}, "price": {"1"}}); r.StatusCode != http.StatusNotFound {
		t.Errorf("kim buys on admin's security: %d", r.StatusCode)
	}
	if r := e.post(kim, "/finanzen/kurse", nil); r.StatusCode != http.StatusForbidden {
		t.Errorf("kim refresh: %d", r.StatusCode)
	}
}
