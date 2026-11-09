package webui_test

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/tiliavir/allmend/internal/ausgaben"
)

// agLogin invites email with the given modules and logs in with a password.
func (e *env) agLogin(admin *http.Client, email string, modules ...string) (*http.Client, string) {
	e.t.Helper()
	ctx := context.Background()
	e.post(admin, "/verwaltung/konten", url.Values{"email": {email}, "name": {strings.Split(email, "@")[0]}, "module": modules})
	adminAcc, _ := e.st.Accounts().ByEmail(ctx, adminEmail)
	acc, err := e.st.Accounts().ByEmail(ctx, email)
	if err != nil {
		e.t.Fatal(err)
	}
	pw, err := e.accounts.ResetPassword(ctx, adminAcc, acc.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	c := e.client()
	e.post(c, "/anmelden", url.Values{"email": {email}, "password": {pw}})
	e.post(c, "/konto/passwort", url.Values{"next": {"ein-langes-passwort"}})
	return c, acc.ID
}

func (e *env) upload(c *http.Client, path string, fields map[string]string, file string) *http.Response {
	e.t.Helper()
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	fw, _ := mw.CreateFormFile("datei", "export.csv")
	_, _ = fw.Write([]byte(file))
	_ = mw.Close()
	req, _ := http.NewRequest(http.MethodPost, e.srv.URL+path, &b)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := c.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp
}

func flashOf(r *http.Response) string {
	for _, c := range r.Cookies() {
		if c.Name == "allmend_flash" {
			v, _ := url.QueryUnescape(c.Value)
			return v
		}
	}
	return ""
}

// agSeed creates a group with entries, a payment and a recurring expense and
// returns the pages to render.
func agSeed(t *testing.T, e *env, admin *http.Client) map[string]string {
	t.Helper()
	_, kimID := e.agLogin(admin, "kim.ag@example.org", "ausgaben")
	r := e.post(admin, "/ausgaben", url.Values{"name": {"Haushalt"}, "person": {kimID}})
	if r.StatusCode != http.StatusSeeOther {
		t.Fatalf("create group: %d", r.StatusCode)
	}
	g := r.Header.Get("Location")
	aid := adminID(e)
	if r := e.post(admin, g+"/eintraege", url.Values{"art": {"ausgabe"}, "date": {"2026-01-10"}, "description": {"Wochenmarkt"}, "category": {"Essen"},
		"amount": {"18,39"}, "currency": {"EUR"}, "paid_by": {kimID}, "aufteilung": {"gleich"}, "fuer": {aid, kimID}}); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("expense: %d", r.StatusCode)
	}
	if r := e.post(admin, g+"/eintraege", url.Values{"art": {"ausgabe"}, "date": {"2026-01-12"}, "description": {"Velo"}, "amount": {"96.04"}, "currency": {"CHF"},
		"paid_by": {aid}, "aufteilung": {"betraege"}, "anteil_" + aid: {"60"}, "anteil_" + kimID: {"36.04"}}); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("fixed shares: %d", r.StatusCode)
	}
	if r := e.post(admin, g+"/eintraege", url.Values{"art": {"zahlung"}, "date": {"2025-12-01"}, "amount": {"5"}, "currency": {"EUR"},
		"paid_by": {aid}, "to": {kimID}}); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("payment: %d", r.StatusCode)
	}
	if r := e.post(admin, g+"/wiederkehrend", url.Values{"description": {"Strom"}, "category": {"Energie"}, "amount": {"90"}, "currency": {"EUR"},
		"paid_by": {aid}, "aufteilung": {"gleich"}, "fuer": {aid, kimID}, "next": {time.Now().UTC().AddDate(0, 0, 3).Format(time.DateOnly)}}); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("recurring: %d", r.StatusCode)
	}
	gid := strings.TrimPrefix(g, "/ausgaben/")
	es, err := e.st.Ausgaben().Entries(context.Background(), aid, gid)
	if err != nil || len(es) != 3 {
		t.Fatalf("entries: %d %v", len(es), err)
	}
	rs, _ := e.st.Ausgaben().Recurrings(context.Background(), aid, gid)
	return map[string]string{
		"ausgaben":              "/ausgaben",
		"ausgaben-gruppe":       g,
		"ausgaben-gruppe-alle":  g + "?jahr=",
		"ausgaben-neu":          g + "/neu",
		"ausgaben-zahlung":      g + "/neu?art=zahlung&von=" + kimID + "&an=" + aid + "&betrag=9.20&waehrung=EUR",
		"ausgaben-eintrag":      g + "/eintraege/" + es[len(es)-2].ID,
		"ausgaben-wiederkehr":   g + "/wiederkehrend/" + rs[0].ID,
		"ausgaben-wiederk-neu":  g + "/wiederkehrend/neu",
		"ausgaben-zahlung-edit": g + "/eintraege/" + es[len(es)-1].ID,
	}
}

func TestAusgaben(t *testing.T) {
	e := newEnv(t)
	admin := e.loginAdmin()
	kim, kimID := e.agLogin(admin, "kim@example.org", "ausgaben")
	mia, _ := e.agLogin(admin, "mia@example.org", "ausgaben")
	lou, _ := e.agLogin(admin, "lou@example.org", "seiten")
	aid := adminID(e)

	if r, _ := e.get(lou, "/ausgaben"); r.StatusCode != http.StatusForbidden {
		t.Errorf("no module access: %d", r.StatusCode)
	}
	if r := e.post(admin, "/ausgaben", url.Values{"name": {"Allein"}}); r.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("group without others: %d", r.StatusCode)
	}
	r := e.post(admin, "/ausgaben", url.Values{"name": {"Wohnung"}, "person": {kimID}})
	g := r.Header.Get("Location")
	if r.StatusCode != http.StatusSeeOther || !strings.HasPrefix(g, "/ausgaben/") {
		t.Fatalf("create: %d %s", r.StatusCode, g)
	}
	// Mia has access to the module but is no member.
	if r, body := e.get(mia, "/ausgaben"); r.StatusCode != 200 || strings.Contains(body, "Wohnung") {
		t.Errorf("mia's list shows the group: %d", r.StatusCode)
	}
	if r, _ := e.get(mia, g); r.StatusCode != http.StatusNotFound {
		t.Errorf("mia opens: %d", r.StatusCode)
	}
	if r := e.post(mia, g+"/eintraege", url.Values{"art": {"ausgabe"}}); r.StatusCode != http.StatusNotFound {
		t.Errorf("mia posts: %d", r.StatusCode)
	}
	// Unexpected input is refused, never guessed.
	base := url.Values{"art": {"ausgabe"}, "date": {"2026-01-10"}, "description": {"Markt"}, "amount": {"18.39"}, "currency": {"EUR"}, "paid_by": {kimID}}
	for name, mod := range map[string]func(url.Values){
		"sum":      func(v url.Values) { v.Set("aufteilung", "betraege"); v.Set("anteil_"+aid, "10") },
		"nobody":   func(v url.Values) { v.Set("aufteilung", "gleich") },
		"stranger": func(v url.Values) { v.Set("aufteilung", "gleich"); v["fuer"] = []string{"nobody"} },
		"amount": func(v url.Values) {
			v.Set("aufteilung", "gleich")
			v["fuer"] = []string{aid}
			v.Set("amount", "12.345")
		},
		"currency": func(v url.Values) { v.Set("aufteilung", "gleich"); v["fuer"] = []string{aid}; v.Set("currency", "eur") },
		"mode":     func(v url.Values) { v["fuer"] = []string{aid} },
		"payer":    func(v url.Values) { v.Set("aufteilung", "gleich"); v["fuer"] = []string{aid}; v.Set("paid_by", "") },
	} {
		v := url.Values{}
		for k, x := range base {
			v[k] = append([]string(nil), x...)
		}
		mod(v)
		if r := e.post(admin, g+"/eintraege", v); r.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d", name, r.StatusCode)
		}
	}
	if r := e.post(admin, g+"/eintraege", url.Values{"art": {"geschenk"}}); r.StatusCode != http.StatusBadRequest {
		t.Errorf("unknown kind: %d", r.StatusCode)
	}
	ok := url.Values{}
	for k, x := range base {
		ok[k] = x
	}
	ok.Set("aufteilung", "gleich")
	ok["fuer"] = []string{aid, kimID}
	if r := e.post(admin, g+"/eintraege", ok); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("expense: %d", r.StatusCode)
	}
	// Kim paid 18.39 for both: admin owes 9.20.
	_, body := e.get(kim, g)
	if !strings.Contains(body, "9,20 EUR") || !strings.Contains(body, "Als bezahlt erfassen") {
		t.Errorf("balance or settle proposal missing")
	}
	// Settle up: the proposal's payment brings the balance to zero.
	if r := e.post(admin, g+"/eintraege", url.Values{"art": {"zahlung"}, "date": {"2026-01-11"}, "amount": {"9.20"}, "currency": {"EUR"}, "paid_by": {aid}, "to": {kimID}}); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("payment: %d", r.StatusCode)
	}
	if _, body := e.get(kim, g); !strings.Contains(body, "Alles ausgeglichen") {
		t.Error("not settled")
	}
	// A payment to oneself is refused.
	if r := e.post(admin, g+"/eintraege", url.Values{"art": {"zahlung"}, "date": {"2026-01-11"}, "amount": {"1"}, "currency": {"EUR"}, "paid_by": {aid}, "to": {aid}}); r.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("self payment: %d", r.StatusCode)
	}
	// Edit with a stale version conflicts; delete needs the current version.
	gid := strings.TrimPrefix(g, "/ausgaben/")
	es, _ := e.st.Ausgaben().Entries(context.Background(), aid, gid)
	exp := es[len(es)-1]
	ok.Set("version", "1")
	ok.Set("description", "Markt Basel")
	if r := e.post(kim, g+"/eintraege/"+exp.ID, ok); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("edit: %d", r.StatusCode)
	}
	if r := e.post(admin, g+"/eintraege/"+exp.ID, ok); r.StatusCode != http.StatusConflict {
		t.Errorf("stale edit: %d", r.StatusCode)
	}
	if r := e.post(admin, g+"/eintraege/"+exp.ID+"/loeschen", url.Values{"version": {"2"}, "confirm": {"1"}}); r.StatusCode != http.StatusSeeOther || flashOf(r) != "Gelöscht." {
		t.Errorf("delete: %d %q", r.StatusCode, flashOf(r))
	}
	// A recurring expense that started in the past is booked on opening.
	start := time.Now().UTC().AddDate(0, -2, 0)
	start = time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, time.UTC)
	if r := e.post(kim, g+"/wiederkehrend", url.Values{"description": {"Miete"}, "amount": {"1200"}, "currency": {"CHF"}, "paid_by": {kimID},
		"aufteilung": {"gleich"}, "fuer": {aid, kimID}, "next": {start.Format(time.DateOnly)}}); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("recurring: %d", r.StatusCode)
	}
	e.get(admin, g)
	es, _ = e.st.Ausgaben().Entries(context.Background(), aid, gid)
	n := 0
	for _, x := range es {
		if x.RecurringID != "" {
			n++
		}
	}
	if n != 3 {
		t.Errorf("booked %d months, want 3", n)
	}

	// Import into a fresh group; the group with entries refuses it.
	r = e.post(kim, "/ausgaben", url.Values{"name": {"Ferien"}, "person": {aid}})
	g2 := r.Header.Get("Location")
	csv := "Date,Description,Category,Cost,Currency,Kim K,Admin A\n\n2026-01-02,Brot,Groceries,4.00,EUR,2.00,-2.00\n\n2026-03-01,Total balance, , ,EUR,2.00,-2.00\n"
	names := map[string]string{"name_" + kimID: "Kim K", "name_" + aid: "Admin A"}
	if r := e.upload(kim, g2+"/import", names, strings.Replace(csv, "-2.00\n\n2026", "-1.00\n\n2026", 1)); !strings.HasPrefix(flashOf(r), "Import abgebrochen, nichts gespeichert") {
		t.Errorf("bad file: %q", flashOf(r))
	}
	if r := e.upload(kim, g2+"/import", names, csv); flashOf(r) != "1 Einträge importiert." {
		t.Errorf("import: %q", flashOf(r))
	}
	if r := e.upload(kim, g2+"/import", names, csv); !strings.Contains(flashOf(r), "schon Einträge") {
		t.Errorf("second import: %q", flashOf(r))
	}
	if r := e.upload(mia, g2+"/import", names, csv); r.StatusCode != http.StatusNotFound {
		t.Errorf("mia imports: %d", r.StatusCode)
	}
	kimAcc, _ := e.st.Accounts().Get(context.Background(), kimID)
	v, err := (&ausgaben.Service{Repo: e.st.Ausgaben(), Accounts: e.st.Accounts()}).Open(context.Background(), kimAcc, strings.TrimPrefix(g2, "/ausgaben/"))
	if err != nil || len(v.Entries) != 1 || v.Entries[0].PaidBy != kimID {
		t.Fatalf("imported: %+v %v", v.Entries, err)
	}
}
