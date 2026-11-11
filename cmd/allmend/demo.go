package main

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"time"

	"github.com/tiliavir/allmend/internal/account"
	"github.com/tiliavir/allmend/internal/db"
	"github.com/tiliavir/allmend/internal/demo"
	"github.com/tiliavir/allmend/internal/migrations"
	"github.com/tiliavir/allmend/internal/store"
	"github.com/tiliavir/allmend/ui"
)

// buildDemo seeds a fresh database in a temporary directory and exports the
// app, as seen by the demo admin, as a static site to out (must not exist
// or be empty). It returns the number of pages.
func buildDemo(ctx context.Context, out, base string, now time.Time) (int, error) {
	if entries, err := os.ReadDir(out); err == nil && len(entries) > 0 {
		return 0, fmt.Errorf("demo: %s is not empty", out)
	}
	tmp, err := os.MkdirTemp("", "allmend-demo-")
	if err != nil {
		return 0, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	path := filepath.Join(tmp, "allmend.db")
	d, err := db.Open(ctx, path)
	if err != nil {
		return 0, err
	}
	defer func() { _ = d.Close() }()
	if _, err := db.Migrate(ctx, d, migrations.All(), db.Options{BackupDir: filepath.Join(tmp, "backup")}); err != nil {
		return 0, err
	}
	st := store.New(d)
	accounts := &account.Service{Repo: st.Accounts(), Hasher: hasher{}, AdminEmail: demo.AdminEmail}
	if _, err := accounts.EnsureAdmin(ctx); err != nil {
		return 0, err
	}
	srv, err := newServer(st, accounts, "EUR", false)
	if err != nil {
		return 0, err
	}
	srv.Demo = true
	for _, f := range []*func() time.Time{&srv.Accounts.Now, &srv.Links.Now, &srv.Pages.Now, &srv.Commute.Now, &srv.Velo.Now, &srv.Finanzen.Now, &srv.Schiffe.Now, &srv.Ausgaben.Now} {
		*f = func() time.Time { return now }
	}
	admin, err := demo.Seed(ctx, demo.Deps{
		Accounts: srv.Accounts, Links: srv.Links, Pages: srv.Pages, Commute: srv.Commute,
		Velo: srv.Velo, Finanzen: srv.Finanzen, Schiffe: srv.Schiffe, Ausgaben: srv.Ausgaben, Places: srv.Places,
	}, now, srv.Location)
	if err != nil {
		return 0, err
	}
	w := httptest.NewRecorder()
	if err := srv.Sessions.Start(ctx, w, admin.ID); err != nil {
		return 0, err
	}
	var cookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		cookie = c
	}
	if cookie == nil {
		return 0, fmt.Errorf("demo: no session cookie")
	}
	static, err := fs.Sub(ui.Static, "static")
	if err != nil {
		return 0, err
	}
	return demo.Export(srv.Handler(), cookie, static, out, base)
}
