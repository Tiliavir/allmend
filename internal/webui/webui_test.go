package webui_test

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"io/fs"
	"math/big"
	mrand "math/rand/v2"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/tiliavir/allmend/internal/account"
	"github.com/tiliavir/allmend/internal/ausgaben"
	"github.com/tiliavir/allmend/internal/commute"
	"github.com/tiliavir/allmend/internal/db"
	"github.com/tiliavir/allmend/internal/finanzen"
	"github.com/tiliavir/allmend/internal/google"
	"github.com/tiliavir/allmend/internal/links"
	"github.com/tiliavir/allmend/internal/migrations"
	"github.com/tiliavir/allmend/internal/pages"
	"github.com/tiliavir/allmend/internal/password"
	"github.com/tiliavir/allmend/internal/schiffe"
	"github.com/tiliavir/allmend/internal/session"
	"github.com/tiliavir/allmend/internal/store"
	"github.com/tiliavir/allmend/internal/velo"
	"github.com/tiliavir/allmend/internal/webui"
	"github.com/tiliavir/allmend/ui"
)

type hasher struct{}

func (hasher) Hash(pw string) string           { return password.Hash(pw) }
func (hasher) Verify(pw, encoded string) error { return password.Verify(pw, encoded) }

type env struct {
	t        *testing.T
	srv      *httptest.Server
	st       *store.Store
	accounts *account.Service
	idp      *fakeGoogle
}

const adminEmail = "admin@example.org"

func newEnv(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "allmend.db")
	d, err := db.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if _, err := db.Migrate(ctx, d, path, migrations.All(), db.Options{BackupDir: dir}); err != nil {
		t.Fatal(err)
	}
	st := store.New(d)
	e := &env{t: t, st: st}
	e.accounts = &account.Service{Repo: st.Accounts(), Hasher: hasher{}, AdminEmail: adminEmail, BreakGlassHash: password.Hash("break-glass-password")}
	if _, err := e.accounts.EnsureAdmin(ctx); err != nil {
		t.Fatal(err)
	}
	tpl, err := webui.ParseTemplates(ui.Templates)
	if err != nil {
		t.Fatal(err)
	}
	static, _ := fs.Sub(ui.Static, "static")
	e.idp = newFakeGoogle(t)
	s := &webui.Server{
		Accounts: e.accounts, Links: &links.Service{Repo: st.Links()}, Pages: &pages.Service{Repo: st.Pages()}, Commute: &commute.Service{Repo: st.Commute()}, Velo: &velo.Service{Repo: st.Velo(), Loc: time.UTC}, Schiffe: &schiffe.Service{Repo: st.Schiffe(), Accounts: st.Accounts()}, Ausgaben: &ausgaben.Service{Repo: st.Ausgaben(), Accounts: st.Accounts(), Loc: time.UTC}, Places: st.Places(), BaseCurrency: "EUR",
		Finanzen:  &finanzen.Service{Repo: st.Finanzen(), Market: st.Finanzen(), Base: "EUR"},
		Sessions:  &session.Manager{Repo: st.Sessions()},
		Templates: tpl, Static: static, Location: time.UTC,
	}
	e.srv = httptest.NewServer(nil)
	s.Google = e.idp.provider(e.srv.URL + "/anmelden/google/zurueck")
	e.srv.Config.Handler = s.Handler()
	t.Cleanup(e.srv.Close)
	return e
}

func (e *env) client() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func (e *env) post(c *http.Client, path string, form url.Values) *http.Response {
	e.t.Helper()
	req, _ := http.NewRequest(http.MethodPost, e.srv.URL+path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := c.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp
}

func (e *env) get(c *http.Client, path string) (*http.Response, string) {
	e.t.Helper()
	resp, err := c.Get(e.srv.URL + path)
	if err != nil {
		e.t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	return resp, string(b)
}

func (e *env) loginAdmin() *http.Client {
	c := e.client()
	resp := e.post(c, "/anmelden", url.Values{"email": {adminEmail}, "password": {"break-glass-password"}})
	if resp.StatusCode != http.StatusSeeOther {
		e.t.Fatalf("admin login: %d", resp.StatusCode)
	}
	return c
}

func TestPublicStartAndGuards(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	if r, body := e.get(c, "/"); r.StatusCode != 200 || !strings.Contains(body, "Anmelden") {
		t.Fatalf("start: %d", r.StatusCode)
	}
	for _, p := range []string{"/konto", "/verwaltung", "/verwaltung/konten", "/verwaltung/links"} {
		if r, _ := e.get(c, p); r.StatusCode != http.StatusSeeOther || r.Header.Get("Location") != "/anmelden" {
			t.Errorf("%s anonymous: %d", p, r.StatusCode)
		}
	}
	if r, _ := e.get(c, "/gibts-nicht"); r.StatusCode != 404 {
		t.Errorf("404: %d", r.StatusCode)
	}
	r, _ := e.get(c, "/")
	if !strings.Contains(r.Header.Get("Content-Security-Policy"), "script-src 'self'") {
		t.Error("CSP missing")
	}
}

func TestLocalLoginFailure(t *testing.T) {
	e := newEnv(t)
	resp := e.post(e.client(), "/anmelden", url.Values{"email": {adminEmail}, "password": {"wrong"}})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", resp.StatusCode)
	}
}

func TestCrossOriginPostRejected(t *testing.T) {
	e := newEnv(t)
	c := e.loginAdmin()
	req, _ := http.NewRequest(http.MethodPost, e.srv.URL+"/verwaltung/links", strings.NewReader("name=x&url=https://x.example&icon=link"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	resp, _ := c.Do(req)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-site POST: %d", resp.StatusCode)
	}
}

func TestInviteGoogleLoginAndAccess(t *testing.T) {
	e := newEnv(t)
	admin := e.loginAdmin()
	resp := e.post(admin, "/verwaltung/konten", url.Values{"email": {"Kim@Example.org"}, "name": {"Kim"}, "module": {"velo"}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("invite: %d", resp.StatusCode)
	}
	// Kim logs in with Google.
	e.idp.next = map[string]any{"sub": "g-kim", "email": "kim@example.org", "email_verified": true, "name": "Kim G."}
	kim := e.client()
	r, _ := e.get(kim, "/anmelden/google")
	loc, _ := url.Parse(r.Header.Get("Location"))
	q := loc.Query()
	e.idp.nonce = q.Get("nonce")
	r, _ = e.get(kim, "/anmelden/google/zurueck?state="+url.QueryEscape(q.Get("state"))+"&code=good")
	if r.StatusCode != http.StatusSeeOther {
		t.Fatalf("callback: %d", r.StatusCode)
	}
	a, err := e.st.Accounts().ByEmail(context.Background(), "kim@example.org")
	if err != nil || a.Status != account.Active || a.GoogleSub != "g-kim" || a.Name != "Kim" {
		t.Fatalf("kim after login: %+v %v", a, err)
	}
	if r, _ := e.get(kim, "/verwaltung"); r.StatusCode != http.StatusForbidden {
		t.Errorf("non-admin admin page: %d", r.StatusCode)
	}
	// Replayed state is rejected.
	if r, _ := e.get(e.client(), "/anmelden/google/zurueck?state="+url.QueryEscape(q.Get("state"))+"&code=good"); r.StatusCode != http.StatusUnauthorized {
		t.Errorf("replayed state: %d", r.StatusCode)
	}
	// Disabling ends Kim's session.
	resp = e.post(admin, "/verwaltung/konten/"+a.ID, url.Values{"email": {a.Email}, "name": {"Kim"}, "status": {"disabled"}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("disable: %d", resp.StatusCode)
	}
	if r, _ := e.get(kim, "/konto"); r.StatusCode != http.StatusSeeOther {
		t.Errorf("disabled account still logged in: %d", r.StatusCode)
	}
}

func TestGoogleUnknownAccountDenied(t *testing.T) {
	e := newEnv(t)
	e.idp.next = map[string]any{"sub": "g-x", "email": "stranger@example.org", "email_verified": true}
	c := e.client()
	r, _ := e.get(c, "/anmelden/google")
	loc, _ := url.Parse(r.Header.Get("Location"))
	e.idp.nonce = loc.Query().Get("nonce")
	if r, _ := e.get(c, "/anmelden/google/zurueck?state="+url.QueryEscape(loc.Query().Get("state"))+"&code=good"); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("stranger: %d", r.StatusCode)
	}
}

func TestOneTimePasswordMustBeChanged(t *testing.T) {
	e := newEnv(t)
	admin := e.loginAdmin()
	e.post(admin, "/verwaltung/konten", url.Values{"email": {"lou@example.org"}})
	lou, _ := e.st.Accounts().ByEmail(context.Background(), "lou@example.org")
	adminAcc, _ := e.st.Accounts().ByEmail(context.Background(), adminEmail)
	otp, err := e.accounts.ResetPassword(context.Background(), adminAcc, lou.ID)
	if err != nil {
		t.Fatal(err)
	}
	c := e.client()
	if r := e.post(c, "/anmelden", url.Values{"email": {"lou@example.org"}, "password": {otp}}); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("otp login: %d", r.StatusCode)
	}
	if r, _ := e.get(c, "/verwaltung"); r.Header.Get("Location") != "/konto" {
		t.Errorf("must change: redirected to %q", r.Header.Get("Location"))
	}
	if r := e.post(c, "/konto/passwort", url.Values{"next": {"kurz"}}); r.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("short password: %d", r.StatusCode)
	}
	if r := e.post(c, "/konto/passwort", url.Values{"next": {"ein-langes-passwort"}}); r.StatusCode != http.StatusSeeOther {
		t.Errorf("change: %d", r.StatusCode)
	}
	if r := e.post(e.client(), "/anmelden", url.Values{"email": {"lou@example.org"}, "password": {otp}}); r.StatusCode != http.StatusUnauthorized {
		t.Errorf("old otp still valid: %d", r.StatusCode)
	}
}

func TestLastAdminProtected(t *testing.T) {
	e := newEnv(t)
	admin := e.loginAdmin()
	a, _ := e.st.Accounts().ByEmail(context.Background(), adminEmail)
	if r := e.post(admin, "/verwaltung/konten/"+a.ID, url.Values{"email": {adminEmail}, "status": {"active"}}); r.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("removing last admin: %d", r.StatusCode)
	}
}

func TestLinksVisibility(t *testing.T) {
	e := newEnv(t)
	admin := e.loginAdmin()
	e.post(admin, "/verwaltung/links", url.Values{"name": {"Öffentlich"}, "url": {"https://pub.example"}, "icon": {"link"}, "public": {"1"}})
	e.post(admin, "/verwaltung/links", url.Values{"name": {"Intern"}, "url": {"https://int.example"}, "icon": {"server"}})
	if r := e.post(admin, "/verwaltung/links", url.Values{"name": {"Böse"}, "url": {"javascript:alert(1)"}, "icon": {"link"}}); r.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("javascript url: %d", r.StatusCode)
	}
	_, anon := e.get(e.client(), "/")
	_, logged := e.get(admin, "/")
	if !strings.Contains(anon, "pub.example") || strings.Contains(anon, "int.example") {
		t.Error("anonymous sees wrong links")
	}
	if !strings.Contains(logged, "int.example") {
		t.Error("logged-in misses internal link")
	}
}

// ── fake Google ───────────────────────────────────────

type fakeGoogle struct {
	t     *testing.T
	key   *rsa.PrivateKey
	srv   *httptest.Server
	next  map[string]any
	nonce string
}

func newFakeGoogle(t *testing.T) *fakeGoogle {
	k, _ := rsa.GenerateKey(rand.Reader, 2048)
	f := &fakeGoogle{t: t, key: k}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /certs", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{"kty": "RSA", "kid": "k", "use": "sig",
			"n": base64.RawURLEncoding.EncodeToString(k.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(k.E)).Bytes())}}})
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, _ *http.Request) {
		c := map[string]any{"iss": "https://accounts.google.com", "aud": "cid", "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(), "nonce": f.nonce}
		for k, v := range f.next {
			c[k] = v
		}
		h, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "k"})
		p, _ := json.Marshal(c)
		in := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(p)
		sum := sha256.Sum256([]byte(in))
		sig, _ := rsa.SignPKCS1v15(rand.Reader, k, crypto.SHA256, sum[:])
		_ = json.NewEncoder(w).Encode(map[string]string{"id_token": in + "." + base64.RawURLEncoding.EncodeToString(sig)})
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeGoogle) provider(redirect string) *google.Provider {
	p := google.New("cid", "secret", redirect)
	p.TokenURL, p.JWKSURL, p.HTTP = f.srv.URL+"/token", f.srv.URL+"/certs", f.srv.Client()
	return p
}

// TestPagesRender renders every page. With ALLMEND_HTML_DUMP set it writes
// them there for the W3C validator (make validate, CI).
func TestPagesRender(t *testing.T) {
	e := newEnv(t)
	admin := e.loginAdmin()
	e.post(admin, "/verwaltung/links", url.Values{"name": {"Haken"}, "url": {"https://haken.example"}, "icon": {"check"}, "public": {"1"}, "description": {"Aufgaben"}})
	e.post(admin, "/verwaltung/konten", url.Values{"email": {"kim@example.org"}, "module": {"velo", "seiten", "schiffe"}})
	e.post(admin, "/seiten", url.Values{"title": {"Netzwerk"}, "body": {"# Netz\n\n- [ ] Router\n- [x] NAS\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\n```mermaid\ngraph LR; A-->B\n```\n"}})
	e.post(admin, "/arbeitswege", awForm(2026, map[string]string{"workdays_0": "20", "car_train_0": "13", "ho_0": "7"}))
	e.post(admin, "/velo/velos", url.Values{"name": {"Gravel"}, "purchase": {"2500"}, "color": {"#5c7a3e"}})
	bikes, _ := e.st.Velo().Bikes(context.Background(), adminID(e))
	e.post(admin, "/velo/touren", url.Values{"date": {"2025-05-03"}, "km": {"42.5"}, "hm": {"610"}, "bike": {bikes[0].ID}, "title": {"Blauen"}})
	e.post(admin, "/velo/velos/"+bikes[0].ID+"/reparaturen", url.Values{"date": {"2025-06-01"}, "title": {"Kette"}, "cost": {"45.90"}})
	e.post(admin, "/velo/jahre/2025/kommentar", url.Values{"comment": {"**Gutes** Jahr"}})
	tours, _ := e.st.Velo().Tours(context.Background(), adminID(e))
	reps, _ := e.st.Velo().Repairs(context.Background(), adminID(e), bikes[0].ID)
	ls, _ := e.st.Links().List(context.Background())
	acc, _ := e.st.Accounts().ByEmail(context.Background(), "kim@example.org")
	pages := map[string]pageCase{
		"start-anonymous": {e.client(), "/", 200},
		"start":           {admin, "/", 200},
		"login":           {e.client(), "/anmelden", 200},
		"konto":           {admin, "/konto", 200},
		"admin":           {admin, "/verwaltung", 200},
		"accounts":        {admin, "/verwaltung/konten", 200},
		"account":         {admin, "/verwaltung/konten/" + acc.ID, 200},
		"links":           {admin, "/verwaltung/links", 200},
		"link":            {admin, "/verwaltung/links/" + ls[0].ID, 200},
		"notfound":        {admin, "/nix", 404},
		"seiten":          {admin, "/seiten", 200},
		"velo":            {admin, "/velo", 200},
		"schiffe":         {admin, "/schiffe", 200},
		"orte":            {admin, "/orte", 200},
		"orte-zeitachse":  {admin, "/orte/zeitachse", 200},
		"velo-2025":       {admin, "/velo?jahr=2025", 200},
		"velo-touren":     {admin, "/velo/touren?jahr=2025", 200},
		"velo-tour-neu":   {admin, "/velo/touren/neu", 200},
		"velo-velos":      {admin, "/velo/velos", 200},
		"velo-velo-neu":   {admin, "/velo/velos/neu", 200},
		"velo-kommentar":  {admin, "/velo/jahre/2025/kommentar", 200},
		"velo-ziel":       {admin, "/velo/ziel/2025/5", 200},
		"aw":              {admin, "/arbeitswege", 200},
		"aw-neu":          {admin, "/arbeitswege/neu", 200},
		"aw-edit":         {admin, "/arbeitswege/2026/bearbeiten", 200},
		"seite-neu":       {admin, "/seiten/neu", 200},
		"seite":           {admin, "/seiten/netzwerk", 200},
		"seite-edit":      {admin, "/seiten/netzwerk/bearbeiten", 200},
		"seite-rechte":    {admin, "/seiten/netzwerk/rechte", 200},
		"seite-versionen": {admin, "/seiten/netzwerk/versionen", 200},
		"seite-unter":     {admin, "/seiten/neu?unter=netzwerk", 200},
		"seite-move":      {admin, "/seiten/netzwerk/verschieben", 200},
		"seite-version":   {admin, "/seiten/netzwerk/versionen/1", 200},
	}
	pages["velo-tour"] = pageCase{admin, "/velo/touren/" + tours[0].ID, 200}
	pages["velo-velo"] = pageCase{admin, "/velo/velos/" + bikes[0].ID, 200}
	pages["velo-velo-edit"] = pageCase{admin, "/velo/velos/" + bikes[0].ID + "/bearbeiten", 200}
	pages["velo-reparatur"] = pageCase{admin, "/velo/reparaturen/" + reps[0].ID, 200}
	kimAcc, _ := e.st.Accounts().ByEmail(context.Background(), "kim@example.org")
	_ = e.st.Accounts().Update(context.Background(), func() account.Account { kimAcc.Status = account.Active; return kimAcc }())
	r := e.post(admin, "/schiffe", url.Values{"gegner": {kimAcc.ID}, "variante": {"salve"}, "wasser": {"1"}})
	game := strings.SplitN(r.Header.Get("Location"), "?", 2)[0]
	pages["schiffe-aufstellung"] = pageCase{admin, game, 200}
	aid := adminID(e)
	if _, err := e.st.DB().ExecContext(context.Background(), `INSERT INTO places VALUES ('p1', ?, 'Basel', 'Rheinknie', 47.5596, 7.5886, '2024-01-01T00:00:00Z', '2024-01-01T00:00:00Z');
INSERT INTO place_lists VALUES ('l1', ?, 'Rhein', '', '2024-05-01', '2024-05-04', 'trip.json', '2024-01-01T00:00:00Z', '2024-01-01T00:00:00Z');
INSERT INTO place_list_places VALUES ('l1', 'p1');
INSERT INTO place_visits VALUES ('v1', ?, NULL, 47.5, 7.6, '2024-05-03T10:00:00.000Z', NULL, 'trip.json');
INSERT INTO place_lists VALUES ('l2', ?, 'Weg', '', '', '', 'weg.kmz', '2024-01-01T00:00:00Z', '2024-01-01T00:00:00Z');
INSERT INTO place_visits VALUES ('v2', ?, NULL, 46.1, 7.8, NULL, 0, 'weg.kmz'), ('v3', ?, NULL, 46.2, 7.8, NULL, 1, 'weg.kmz');`, aid, aid, aid, aid, aid, aid); err != nil {
		t.Fatal(err)
	}
	pages["orte-liste"] = pageCase{admin, "/orte/listen/l1", 200}
	pages["orte-liste-ohne-zeit"] = pageCase{admin, "/orte/listen/l2", 200}
	pages["orte-tag"] = pageCase{admin, "/orte/zeitachse?tag=2024-05-03", 200}
	for name, path := range finSeed(t, e, admin) {
		pages[name] = pageCase{admin, path, 200}
	}
	for name, path := range agSeed(t, e, admin) {
		pages[name] = pageCase{admin, path, 200}
	}
	dump := os.Getenv("ALLMEND_HTML_DUMP")
	for name, p := range pages {
		r, body := e.get(p.c, p.path)
		if r.StatusCode != p.code {
			t.Errorf("%s: status %d", name, r.StatusCode)
		}
		if strings.Contains(body, "style=") {
			t.Errorf("%s: inline style violates the CSP", name)
		}
		if name == "schiffe-aufstellung" && dump != "" {
			// Also dump the running game: both fleets placed, a salvo fired.
			gid := strings.TrimPrefix(game, "/schiffe/")
			g, _ := e.st.Schiffe().Get(context.Background(), gid)
			_ = g.Place(g.PlayerA, schiffe.RandomFleet(mrand.New(mrand.NewPCG(1, 1)), 10, false))
			_ = g.Place(g.PlayerB, schiffe.RandomFleet(mrand.New(mrand.NewPCG(2, 2)), 10, false))
			g.Version++
			_ = e.st.Schiffe().Save(context.Background(), g, g.Version-1)
			_, running := e.get(p.c, game)
			_ = os.WriteFile(filepath.Join(dump, "schiffe-spiel.html"), []byte(running), 0o600)
		}
		if dump != "" {
			if err := os.WriteFile(filepath.Join(dump, name+".html"), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestSeiten(t *testing.T) {
	e := newEnv(t)
	admin := e.loginAdmin()
	e.post(admin, "/verwaltung/konten", url.Values{"email": {"kim@example.org"}, "module": {"seiten"}})
	e.post(admin, "/verwaltung/konten", url.Values{"email": {"lou@example.org"}})
	ctx := context.Background()
	adminAcc, _ := e.st.Accounts().ByEmail(ctx, adminEmail)
	kimAcc, _ := e.st.Accounts().ByEmail(ctx, "kim@example.org")
	louAcc, _ := e.st.Accounts().ByEmail(ctx, "lou@example.org")
	kimPW, _ := e.accounts.ResetPassword(ctx, adminAcc, kimAcc.ID)
	louPW, _ := e.accounts.ResetPassword(ctx, adminAcc, louAcc.ID)
	login := func(email, otp string) *http.Client {
		c := e.client()
		e.post(c, "/anmelden", url.Values{"email": {email}, "password": {otp}})
		e.post(c, "/konto/passwort", url.Values{"next": {"ein-langes-passwort"}})
		return c
	}
	kim, lou := login("kim@example.org", kimPW), login("lou@example.org", louPW)

	if r := e.post(kim, "/seiten", url.Values{"title": {"Einkauf"}, "body": {"- [ ] Milch\n- [ ] Brot"}}); r.Header.Get("Location") != "/seiten/einkauf" {
		t.Fatalf("create: %d %s", r.StatusCode, r.Header.Get("Location"))
	}
	if r, _ := e.get(lou, "/seiten"); r.StatusCode != http.StatusForbidden {
		t.Errorf("no module access: %d", r.StatusCode)
	}
	// Admin ticks Milch, Kim's stale tick on Brot (version 1) still finds it by text.
	if r := e.post(admin, "/seiten/einkauf/haken", url.Values{"version": {"1"}, "line": {"0"}, "text": {"Milch"}}); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("tick: %d", r.StatusCode)
	}
	e.post(kim, "/seiten/einkauf/haken", url.Values{"version": {"1"}, "line": {"1"}, "text": {"Brot"}})
	p, _ := e.st.Pages().BySlug(ctx, "einkauf")
	if p.Body != "- [x] Milch\n- [x] Brot" || p.Version != 3 {
		t.Fatalf("after ticks: %q v%d", p.Body, p.Version)
	}
	// Stale edit is a conflict and keeps the user's text.
	r := e.post(kim, "/seiten/einkauf/bearbeiten", url.Values{"base": {"1"}, "title": {"Einkauf"}, "body": {"meins"}})
	if r.StatusCode != http.StatusConflict {
		t.Fatalf("stale edit: %d", r.StatusCode)
	}
	// A private page is invisible to everyone else, admins included.
	e.post(kim, "/seiten/einkauf/rechte", url.Values{"read": {"some"}, "write": {"some"}})
	if r, _ := e.get(admin, "/seiten/einkauf"); r.StatusCode != http.StatusNotFound {
		t.Errorf("private page readable by admin: %d", r.StatusCode)
	}
	if r := e.post(admin, "/seiten/einkauf/haken", url.Values{"version": {"3"}, "line": {"0"}, "text": {"Milch"}}); r.StatusCode != http.StatusNotFound {
		t.Errorf("private page writable: %d", r.StatusCode)
	}
	// Restore version 1 and delete with confirmation.
	e.post(kim, "/seiten/einkauf/versionen/1/wiederherstellen", nil)
	p, _ = e.st.Pages().BySlug(ctx, "einkauf")
	if p.Body != "- [ ] Milch\n- [ ] Brot" {
		t.Errorf("restore: %q", p.Body)
	}
	if r := e.post(kim, "/seiten/einkauf/loeschen", nil); r.StatusCode != http.StatusBadRequest {
		t.Errorf("delete without confirm: %d", r.StatusCode)
	}
	e.post(kim, "/seiten/einkauf/loeschen", url.Values{"confirm": {"1"}})
	if r, _ := e.get(kim, "/seiten/einkauf"); r.StatusCode != http.StatusNotFound {
		t.Errorf("deleted page: %d", r.StatusCode)
	}
}

func TestPageTreeMove(t *testing.T) {
	e := newEnv(t)
	admin := e.loginAdmin()
	ctx := context.Background()
	e.post(admin, "/seiten", url.Values{"title": {"Haus"}, "body": {"x"}})
	haus, _ := e.st.Pages().BySlug(ctx, "haus")
	if r := e.post(admin, "/seiten", url.Values{"title": {"Heizung"}, "body": {"y"}, "parent": {haus.ID}}); r.Header.Get("Location") != "/seiten/heizung" {
		t.Fatalf("create below: %d %s", r.StatusCode, r.Header.Get("Location"))
	}
	e.post(admin, "/seiten", url.Values{"title": {"Garten"}, "body": {"z"}})
	heiz, _ := e.st.Pages().BySlug(ctx, "heizung")
	garten, _ := e.st.Pages().BySlug(ctx, "garten")
	if heiz.ParentID != haus.ID {
		t.Fatalf("parent: %q", heiz.ParentID)
	}
	_, body := e.get(admin, "/seiten")
	if !strings.Contains(body, `data-parent="`+haus.ID+`"`) || !strings.Contains(body, "seiten-baum.js") {
		t.Error("tree list or script missing")
	}
	if _, body := e.get(admin, "/seiten/heizung"); !strings.Contains(body, `href="/seiten/haus"`) {
		t.Error("breadcrumb missing")
	}
	// Move Garten before Haus (top level), then Heizung below Garten.
	if r := e.post(admin, "/seiten/garten/verschieben", url.Values{"ziel": {"/" + haus.ID}, "zurueck": {"liste"}}); r.StatusCode != http.StatusSeeOther || r.Header.Get("Location") != "/seiten#seite-"+garten.ID {
		t.Fatalf("move: %d %s", r.StatusCode, r.Header.Get("Location"))
	}
	if r := e.post(admin, "/seiten/heizung/verschieben", url.Values{"ziel": {garten.ID + "/"}, "zurueck": {"seite"}}); r.Header.Get("Location") != "/seiten/heizung" {
		t.Fatalf("reparent: %d %s", r.StatusCode, r.Header.Get("Location"))
	}
	heiz, _ = e.st.Pages().BySlug(ctx, "heizung")
	if heiz.ParentID != garten.ID {
		t.Fatalf("parent after move: %q", heiz.ParentID)
	}
	// Bad input is an error, a loop is refused without changing anything.
	for name, v := range map[string]url.Values{
		"no separator": {"ziel": {"abc"}, "zurueck": {"liste"}},
		"bad id":       {"ziel": {"XYZ/"}, "zurueck": {"liste"}},
		"bad return":   {"ziel": {"/"}, "zurueck": {"anderswo"}},
		"missing":      {},
	} {
		if r := e.post(admin, "/seiten/garten/verschieben", v); r.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: %d", name, r.StatusCode)
		}
	}
	if r := e.post(admin, "/seiten/garten/verschieben", url.Values{"ziel": {heiz.ID + "/"}, "zurueck": {"liste"}}); r.Header.Get("Location") != "/seiten" {
		t.Errorf("loop: %d %s", r.StatusCode, r.Header.Get("Location"))
	}
	garten, _ = e.st.Pages().BySlug(ctx, "garten")
	if garten.ParentID != "" {
		t.Errorf("loop moved the page: %q", garten.ParentID)
	}
	if r := e.post(admin, "/seiten", url.Values{"title": {"Neu"}, "body": {"n"}, "parent": {"nicht-hex"}}); r.StatusCode != http.StatusBadRequest {
		t.Errorf("bad parent: %d", r.StatusCode)
	}
}

func TestMermaidCSPAndGzipAsset(t *testing.T) {
	e := newEnv(t)
	admin := e.loginAdmin()
	e.post(admin, "/seiten", url.Values{"title": {"Plain"}, "body": {"Hallo"}})
	e.post(admin, "/seiten", url.Values{"title": {"Diagramm"}, "body": {"```mermaid\ngraph LR; A-->B\n```"}})
	r, body := e.get(admin, "/seiten/plain")
	if strings.Contains(r.Header.Get("Content-Security-Policy"), "unsafe-inline") || strings.Contains(body, "mermaid") {
		t.Error("plain page relaxed CSP or loads mermaid")
	}
	r, body = e.get(admin, "/seiten/diagramm")
	if !strings.Contains(r.Header.Get("Content-Security-Policy"), "style-src 'self' 'unsafe-inline'") || !strings.Contains(r.Header.Get("Content-Security-Policy"), "script-src 'self';") || !strings.Contains(body, "mermaid-12.3.0.min.js") {
		t.Errorf("mermaid page: %s", r.Header.Get("Content-Security-Policy"))
	}
	req, _ := http.NewRequest(http.MethodGet, e.srv.URL+"/static/vendor/mermaid-12.3.0.min.js", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Encoding") != "gzip" || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/javascript") {
		t.Errorf("asset: %d %v", resp.StatusCode, resp.Header)
	}
}

func awForm(year int, extra map[string]string) url.Values {
	v := url.Values{"year": {strconv.Itoa(year)}, "car_dist_km": {"7.6"}, "train_dist_km": {"15"}, "bike_dist_km": {"18"}}
	for k, x := range extra {
		v.Set(k, x)
	}
	return v
}

func TestArbeitswege(t *testing.T) {
	e := newEnv(t)
	admin := e.loginAdmin()
	ctx := context.Background()
	e.post(admin, "/verwaltung/konten", url.Values{"email": {"kim@example.org"}, "module": {"arbeitswege"}})
	adminAcc, _ := e.st.Accounts().ByEmail(ctx, adminEmail)
	kimAcc, _ := e.st.Accounts().ByEmail(ctx, "kim@example.org")
	pw, _ := e.accounts.ResetPassword(ctx, adminAcc, kimAcc.ID)
	kim := e.client()
	e.post(kim, "/anmelden", url.Values{"email": {"kim@example.org"}, "password": {pw}})
	e.post(kim, "/konto/passwort", url.Values{"next": {"ein-langes-passwort"}})

	if r := e.post(admin, "/arbeitswege", awForm(2026, map[string]string{"workdays_0": "20", "car_train_0": "13", "ho_0": "7"})); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("create: %d", r.StatusCode)
	}
	if r := e.post(admin, "/arbeitswege", awForm(2026, nil)); r.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("duplicate: %d", r.StatusCode)
	}
	if r := e.post(admin, "/arbeitswege", awForm(2027, map[string]string{"workdays_0": "40"})); r.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("41 days: %d", r.StatusCode)
	}
	if r := e.post(admin, "/arbeitswege", awForm(2028, map[string]string{"rate_low_cents": "30"})); r.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("one rate only: %d", r.StatusCode)
	}
	_, body := e.get(admin, "/arbeitswege")
	if !strings.Contains(body, "108,68 €") {
		t.Errorf("pendler not shown")
	}
	// Kim reads the admin's year but has no edit controls and cannot write it.
	r, body := e.get(kim, "/arbeitswege?person="+adminAcc.ID)
	if r.StatusCode != 200 || !strings.Contains(body, "108,68 €") || strings.Contains(body, "bearbeiten") {
		t.Errorf("kim reads admin: %d", r.StatusCode)
	}
	if r := e.post(kim, "/arbeitswege/2026", awForm(2026, map[string]string{"base": "1"})); r.StatusCode != http.StatusNotFound {
		t.Errorf("kim writes admin's year: %d", r.StatusCode)
	}
	// Stale save keeps the input.
	e.post(admin, "/arbeitswege/2026", awForm(2026, map[string]string{"base": "1", "workdays_0": "21"}))
	if r := e.post(admin, "/arbeitswege/2026", awForm(2026, map[string]string{"base": "1", "workdays_0": "22"})); r.StatusCode != http.StatusConflict {
		t.Errorf("stale save: %d", r.StatusCode)
	}
	if r := e.post(admin, "/arbeitswege/2026/loeschen", nil); r.StatusCode != http.StatusBadRequest {
		t.Errorf("delete without confirm: %d", r.StatusCode)
	}
	if r := e.post(admin, "/arbeitswege/2026/loeschen", url.Values{"confirm": {"1"}}); r.StatusCode != http.StatusSeeOther {
		t.Errorf("delete: %d", r.StatusCode)
	}
}

type pageCase struct {
	c    *http.Client
	path string
	code int
}

func adminID(e *env) string {
	a, err := e.st.Accounts().ByEmail(context.Background(), adminEmail)
	if err != nil {
		e.t.Fatal(err)
	}
	return a.ID
}

func TestVeloPrivate(t *testing.T) {
	e := newEnv(t)
	admin := e.loginAdmin()
	ctx := context.Background()
	e.post(admin, "/verwaltung/konten", url.Values{"email": {"kim@example.org"}, "module": {"velo"}})
	adminAcc, _ := e.st.Accounts().ByEmail(ctx, adminEmail)
	kimAcc, _ := e.st.Accounts().ByEmail(ctx, "kim@example.org")
	pw, _ := e.accounts.ResetPassword(ctx, adminAcc, kimAcc.ID)
	kim := e.client()
	e.post(kim, "/anmelden", url.Values{"email": {"kim@example.org"}, "password": {pw}})
	e.post(kim, "/konto/passwort", url.Values{"next": {"ein-langes-passwort"}})

	e.post(admin, "/velo/velos", url.Values{"name": {"Gravel"}, "color": {"#5c7a3e"}})
	bikes, _ := e.st.Velo().Bikes(ctx, adminAcc.ID)
	if r := e.post(admin, "/velo/touren", url.Values{"date": {"2025-05-03"}, "km": {"42,5"}, "bike": {bikes[0].ID}}); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("tour: %d", r.StatusCode)
	}
	if r := e.post(admin, "/velo/touren", url.Values{"date": {"2025-02-30"}, "km": {"10"}}); r.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("bad date: %d", r.StatusCode)
	}
	tours, _ := e.st.Velo().Tours(ctx, adminAcc.ID)
	// Kim sees none of it and cannot use the admin's bike.
	if r, _ := e.get(kim, "/velo/touren/"+tours[0].ID); r.StatusCode != http.StatusNotFound {
		t.Errorf("foreign tour: %d", r.StatusCode)
	}
	if r, _ := e.get(kim, "/velo/velos/"+bikes[0].ID); r.StatusCode != http.StatusNotFound {
		t.Errorf("foreign bike: %d", r.StatusCode)
	}
	if r := e.post(kim, "/velo/touren", url.Values{"date": {"2025-05-03"}, "km": {"5"}, "bike": {bikes[0].ID}}); r.StatusCode != http.StatusNotFound {
		t.Errorf("tour on foreign bike: %d", r.StatusCode)
	}
	if r := e.post(kim, "/velo/touren/"+tours[0].ID+"/loeschen", url.Values{"confirm": {"1"}}); r.StatusCode != http.StatusNotFound {
		t.Errorf("delete foreign tour: %d", r.StatusCode)
	}
	if _, body := e.get(kim, "/velo?jahr=2025"); strings.Contains(body, "42,5") || strings.Contains(body, "Gravel") {
		// 2025 is not in Kim's year list, so this must be a 404 page anyway.
		t.Error("kim sees admin's data")
	}
	// Bike in use cannot be deleted; stale tour edit conflicts.
	if r := e.post(admin, "/velo/velos/"+bikes[0].ID+"/loeschen", url.Values{"confirm": {"1"}}); r.Header.Get("Location") != "/velo/velos/"+bikes[0].ID {
		t.Errorf("delete bike in use: %d %s", r.StatusCode, r.Header.Get("Location"))
	}
	e.post(admin, "/velo/touren/"+tours[0].ID, url.Values{"version": {"1"}, "date": {"2025-05-03"}, "km": {"43"}})
	if r := e.post(admin, "/velo/touren/"+tours[0].ID, url.Values{"version": {"1"}, "date": {"2025-05-03"}, "km": {"44"}}); r.StatusCode != http.StatusConflict {
		t.Errorf("stale tour: %d", r.StatusCode)
	}
}

func fleetForm(ships []schiffe.Ship) url.Values {
	v := url.Values{}
	for _, s := range ships {
		v.Add("start", schiffe.Coord(s.X, s.Y))
		d := "v"
		if s.Horizontal {
			d = "h"
		}
		v.Add("richtung", d)
	}
	return v
}

func TestSchiffe(t *testing.T) {
	e := newEnv(t)
	admin := e.loginAdmin()
	ctx := context.Background()
	adminAcc, _ := e.st.Accounts().ByEmail(ctx, adminEmail)
	login := func(email string, modules ...string) (*http.Client, string) {
		e.post(admin, "/verwaltung/konten", url.Values{"email": {email}, "module": modules})
		acc, _ := e.st.Accounts().ByEmail(ctx, email)
		pw, _ := e.accounts.ResetPassword(ctx, adminAcc, acc.ID)
		c := e.client()
		e.post(c, "/anmelden", url.Values{"email": {email}, "password": {pw}})
		e.post(c, "/konto/passwort", url.Values{"next": {"ein-langes-passwort"}})
		return c, acc.ID
	}
	kim, kimID := login("kim@example.org", "schiffe")
	lou, _ := login("lou@example.org", "schiffe")

	// Lou challenges Kim.
	r := e.post(lou, "/schiffe", url.Values{"gegner": {kimID}, "variante": {"klassisch"}, "wasser": {"1"}})
	if r.StatusCode != http.StatusSeeOther {
		t.Fatalf("challenge: %d", r.StatusCode)
	}
	gamePath := strings.SplitN(r.Header.Get("Location"), "?", 2)[0]
	if r := e.post(lou, "/schiffe", url.Values{"gegner": {"nobody"}, "variante": {"klassisch"}}); r.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("unknown opponent: %d", r.StatusCode)
	}
	if r, _ := e.get(admin, gamePath); r.StatusCode != http.StatusNotFound {
		t.Errorf("outsider (admin with module) sees game: %d", r.StatusCode)
	}
	fleet := []schiffe.Ship{{Size: 5, X: 0, Y: 0, Horizontal: true}, {Size: 4, X: 0, Y: 2, Horizontal: true}, {Size: 4, X: 6, Y: 2, Horizontal: true},
		{Size: 3, X: 0, Y: 4, Horizontal: true}, {Size: 3, X: 4, Y: 4, Horizontal: true}, {Size: 3, X: 8, Y: 4},
		{Size: 2, X: 0, Y: 6, Horizontal: true}, {Size: 2, X: 3, Y: 6, Horizontal: true}, {Size: 2, X: 0, Y: 8, Horizontal: true}, {Size: 2, X: 3, Y: 8, Horizontal: true}}
	bad := fleetForm(fleet)
	bad.Set("start", "A1")
	if r := e.post(lou, gamePath+"/aufstellung", bad); r.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("overlapping fleet: %d", r.StatusCode)
	}
	e.post(lou, gamePath+"/aufstellung", fleetForm(fleet))
	e.post(kim, gamePath+"/aufstellung", fleetForm(fleet))
	// Lou (challenger) starts; Kim must wait.
	if r := e.post(kim, gamePath+"/schuss", url.Values{"c": {"A1"}}); r.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("out of turn: %d", r.StatusCode)
	}
	if r := e.post(lou, gamePath+"/schuss", url.Values{"c": {"A1"}}); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("fire: %d", r.StatusCode)
	}
	// Kim's view: own sea shows ships, target (Lou's sea) must not reveal any.
	_, body := e.get(kim, gamePath)
	target := body[strings.Index(body, `class="sea target"`):strings.Index(body, "Dein Meer")]
	if strings.Contains(target, `class="ship"`) {
		t.Error("opponent's ships leak into the target sea")
	}
	if !strings.Contains(body, "Du bist am Zug") {
		t.Error("turn not passed to Kim")
	}
	// SSE delivers the current version immediately.
	req, _ := http.NewRequest(http.MethodGet, e.srv.URL+gamePath+"/ereignisse", nil)
	cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	resp, err := kim.Do(req.WithContext(cctx))
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64)
	n, _ := resp.Body.Read(buf)
	_ = resp.Body.Close()
	if resp.Header.Get("Content-Type") != "text/event-stream" || !strings.Contains(string(buf[:n]), "event: update") {
		t.Errorf("sse: %q %v", buf[:n], resp.Header)
	}
}

func TestOrte(t *testing.T) {
	e := newEnv(t)
	admin := e.loginAdmin()
	ctx := context.Background()
	adminAcc, _ := e.st.Accounts().ByEmail(ctx, adminEmail)
	e.post(admin, "/verwaltung/konten", url.Values{"email": {"kim@example.org"}, "module": {"orte"}})
	kimAcc, _ := e.st.Accounts().ByEmail(ctx, "kim@example.org")
	pw, _ := e.accounts.ResetPassword(ctx, adminAcc, kimAcc.ID)
	kim := e.client()
	e.post(kim, "/anmelden", url.Values{"email": {"kim@example.org"}, "password": {pw}})
	e.post(kim, "/konto/passwort", url.Values{"next": {"ein-langes-passwort"}})

	upload := func(c *http.Client, body string) *http.Response {
		var b strings.Builder
		b.WriteString("--x\r\nContent-Disposition: form-data; name=\"datei\"; filename=\"Records.json\"\r\nContent-Type: application/json\r\n\r\n")
		b.WriteString(body + "\r\n--x--\r\n")
		req, _ := http.NewRequest(http.MethodPost, e.srv.URL+"/orte/import", strings.NewReader(b.String()))
		req.Header.Set("Content-Type", "multipart/form-data; boundary=x")
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp
	}
	if r := upload(admin, `{"locations":[{"latitudeE7":475596000,"longitudeE7":75886000,"timestamp":"2024-05-03T10:00:00Z"}]}`); r.Header.Get("Location") != "/orte/zeitachse" {
		t.Fatalf("import: %d %s", r.StatusCode, r.Header.Get("Location"))
	}
	if r := upload(admin, `{"locations":[{"latitudeE7":1}]}`); r.Header.Get("Location") != "/orte" {
		t.Errorf("bad import not rejected: %s", r.Header.Get("Location"))
	}
	// Kim reads the admin's timeline (with map CSP), but cannot import into it.
	r, body := e.get(kim, "/orte/zeitachse?person="+adminAcc.ID)
	if r.StatusCode != 200 || !strings.Contains(body, "1 Punkte am 2024-05-03") || !strings.Contains(r.Header.Get("Content-Security-Policy"), "tile.openstreetmap.org") {
		t.Errorf("kim timeline: %d", r.StatusCode)
	}
	if strings.Contains(body, "Standortverlauf importieren") {
		t.Error("import offered for someone else's data")
	}
	if r, _ := e.get(kim, "/orte?person=nobody"); r.StatusCode != http.StatusNotFound {
		t.Errorf("unknown person: %d", r.StatusCode)
	}
}
