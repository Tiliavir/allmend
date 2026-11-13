package webui_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

const vorgang = "0b8c3f9e-1d2a-4c5b-8e7f-123456789abc"

// replayPost sends form like the service worker does from the outbox.
func (e *env) replayPost(c *http.Client, path, key, konto string, form url.Values) *http.Response {
	e.t.Helper()
	req, _ := http.NewRequest(http.MethodPost, e.srv.URL+path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	if key != "" {
		req.Header.Set("Allmend-Vorgang", key)
	}
	if konto != "" {
		req.Header.Set("Allmend-Konto", konto)
	}
	resp, err := c.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp
}

func TestOfflineReplay(t *testing.T) {
	e := newEnv(t)
	admin := e.loginAdmin()
	me := adminID(e)
	ctx := context.Background()
	e.post(admin, "/velo/velos", url.Values{"name": {"Gravel"}, "purchase": {"2500"}, "color": {"#5c7a3e"}})
	bikes, _ := e.st.Velo().Bikes(ctx, me)
	tour := url.Values{"date": {"2026-11-12"}, "km": {"30"}, "bike": {bikes[0].ID}, "title": {"Offline"}}
	tours := func() int {
		ts, err := e.st.Velo().Tours(ctx, me)
		if err != nil {
			t.Fatal(err)
		}
		return len(ts)
	}

	// Applied once; the same entry again only gets the same redirect.
	r := e.replayPost(admin, "/velo/touren", vorgang, me, tour)
	if r.StatusCode != http.StatusSeeOther || tours() != 1 {
		t.Fatalf("first replay: %d, %d tours", r.StatusCode, tours())
	}
	loc := r.Header.Get("Location")
	r = e.replayPost(admin, "/velo/touren", vorgang, me, tour)
	if r.StatusCode != http.StatusSeeOther || r.Header.Get("Location") != loc || tours() != 1 {
		t.Fatalf("second replay: %d %q, %d tours", r.StatusCode, r.Header.Get("Location"), tours())
	}

	// Rejected by the handler: nothing stored, the key stays usable.
	bad := url.Values{"date": {"2026-11-12"}, "km": {"viel"}, "bike": {bikes[0].ID}}
	const other = "1b8c3f9e-1d2a-4c5b-8e7f-123456789abc"
	if r := e.replayPost(admin, "/velo/touren", other, me, bad); r.StatusCode != http.StatusUnprocessableEntity || tours() != 1 {
		t.Fatalf("invalid entry: %d, %d tours", r.StatusCode, tours())
	}
	if r := e.replayPost(admin, "/velo/touren", other, me, tour); r.StatusCode != http.StatusSeeOther || tours() != 2 {
		t.Fatalf("corrected entry: %d, %d tours", r.StatusCode, tours())
	}

	const fresh = "2b8c3f9e-1d2a-4c5b-8e7f-123456789abc"
	for _, c := range []struct {
		name       string
		client     *http.Client
		path       string
		key, konto string
		want       int
	}{
		{"form not offline", admin, "/velo/velos", fresh, me, http.StatusBadRequest},
		{"bad key", admin, "/velo/touren", "nope", me, http.StatusBadRequest},
		{"key missing", admin, "/velo/touren", "", me, http.StatusBadRequest},
		{"not logged in", e.client(), "/velo/touren", fresh, me, http.StatusUnauthorized},
		{"other account in header", admin, "/velo/touren", fresh, "someone-else", http.StatusConflict},
	} {
		if r := e.replayPost(c.client, c.path, c.key, c.konto, tour); r.StatusCode != c.want {
			t.Errorf("%s: %d, want %d", c.name, r.StatusCode, c.want)
		}
	}
	if tours() != 2 {
		t.Fatalf("guards changed data: %d tours", tours())
	}
}

// TestOfflineReplayForeignKey: a key applied for one account is no
// redirect for another.
func TestOfflineReplayForeignKey(t *testing.T) {
	e := newEnv(t)
	admin := e.loginAdmin()
	me := adminID(e)
	e.post(admin, "/arbeitswege", awForm(2026, nil))
	y := awForm(2026, map[string]string{"workdays_0": "20", "base": "1"})
	if r := e.replayPost(admin, "/arbeitswege/2026", vorgang, me, y); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("replay: %d", r.StatusCode)
	}
	// Stale version from the outbox: rejected as a conflict, never overwrites.
	if r := e.replayPost(admin, "/arbeitswege/2026", "3b8c3f9e-1d2a-4c5b-8e7f-123456789abc", me, y); r.StatusCode != http.StatusConflict {
		t.Fatalf("stale version: %d", r.StatusCode)
	}
	kim, kimID := e.agLogin(admin, "kim@example.org", "arbeitswege")
	if r := e.replayPost(kim, "/arbeitswege/2026", vorgang, kimID, y); r.StatusCode != http.StatusConflict {
		t.Fatalf("foreign key: %d", r.StatusCode)
	}
}

func TestOfflineHeadersAndWorker(t *testing.T) {
	e := newEnv(t)
	admin := e.loginAdmin()
	me := adminID(e)
	if r, _ := e.get(admin, "/"); r.Header.Get("Allmend-Konto") != me || r.Header.Get("Cache-Control") != "" {
		t.Fatalf("page headers: %v", r.Header)
	}
	if r, _ := e.get(e.client(), "/"); r.Header.Get("Allmend-Konto") != "" {
		t.Fatalf("anonymous page carries an account")
	}
	// A page with a flash message is not kept as an offline copy.
	e.post(admin, "/velo/velos", url.Values{"name": {"Gravel"}, "purchase": {"2500"}, "color": {"#5c7a3e"}})
	if r, body := e.get(admin, "/velo/velos"); !strings.Contains(body, `class="flash"`) || r.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("flash page: %q", r.Header.Get("Cache-Control"))
	}
	r, js := e.get(admin, "/sw.js")
	if r.StatusCode != 200 || r.Header.Get("Cache-Control") != "no-cache" || !strings.HasPrefix(r.Header.Get("Content-Type"), "text/javascript") {
		t.Fatalf("sw.js: %d %v", r.StatusCode, r.Header)
	}
	for _, want := range []string{`"writes":["^/ausgaben/[^/]+/eintraege$","^/arbeitswege/[^/]+$","^/velo/touren$"]`, `"/offline/schiffe"`, `"/static/offline.js"`, `"/static/outbox.js"`, `"/schiffe"`, `"version":"`, "script-src 'self'"} {
		if !strings.Contains(js, want) {
			t.Errorf("sw.js lacks %s", want)
		}
	}
	if strings.Contains(js, "const CONFIG = {};") {
		t.Error("sw.js config not filled in")
	}
	if r, _ := e.get(e.client(), "/offline"); r.StatusCode != http.StatusSeeOther {
		t.Errorf("offline page without login: %d", r.StatusCode)
	}
	if r, _ := e.get(admin, "/offline/unbekannt"); r.StatusCode != http.StatusNotFound {
		t.Errorf("unknown offline page: %d", r.StatusCode)
	}
	if r := e.post(admin, "/abmelden", nil); r.Header.Get("Clear-Site-Data") != `"cache", "storage"` {
		t.Errorf("logout: %v", r.Header)
	}
}

func TestOfflineReplayAusgaben(t *testing.T) {
	e := newEnv(t)
	admin := e.loginAdmin()
	aid := adminID(e)
	_, kimID := e.agLogin(admin, "kim@example.org", "ausgaben")
	g := e.post(admin, "/ausgaben", url.Values{"name": {"Wohnung"}, "person": {kimID}}).Header.Get("Location")
	gid := strings.TrimPrefix(g, "/ausgaben/")
	entry := url.Values{"art": {"ausgabe"}, "date": {"2026-11-12"}, "description": {"Markt"}, "category": {"groceries"}, "amount": {"18.39"},
		"currency": {"EUR"}, "paid_by": {aid}, "aufteilung": {"gleich"}, "fuer": {aid, kimID}}
	for range 2 {
		if r := e.replayPost(admin, g+"/eintraege", vorgang, aid, entry); r.StatusCode != http.StatusSeeOther {
			t.Fatalf("replay: %d", r.StatusCode)
		}
	}
	es, err := e.st.Ausgaben().Entries(context.Background(), aid, gid)
	if err != nil || len(es) != 1 {
		t.Fatalf("entries: %d %v", len(es), err)
	}
}
