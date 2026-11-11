// Command allmend runs the Allmend family workbench.
//
//	allmend                     start the server (migrates first)
//	allmend migrate             apply pending migrations and exit
//	allmend migrate --dry-run   run migrations on a copy and print counts
//	allmend hashpw              read a password from stdin, print its hash
//	allmend health              check a running server (container healthcheck)
//	allmend demo [-base /p/] DIR  write the static demo site to DIR
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // distroless has no zoneinfo

	_ "modernc.org/sqlite"

	"github.com/tiliavir/allmend/internal/account"
	"github.com/tiliavir/allmend/internal/ausgaben"
	"github.com/tiliavir/allmend/internal/commute"
	"github.com/tiliavir/allmend/internal/db"
	"github.com/tiliavir/allmend/internal/env"
	"github.com/tiliavir/allmend/internal/finanzen"
	"github.com/tiliavir/allmend/internal/google"
	"github.com/tiliavir/allmend/internal/links"
	"github.com/tiliavir/allmend/internal/migrations"
	"github.com/tiliavir/allmend/internal/pages"
	"github.com/tiliavir/allmend/internal/password"
	"github.com/tiliavir/allmend/internal/quotes"
	"github.com/tiliavir/allmend/internal/schiffe"
	"github.com/tiliavir/allmend/internal/session"
	"github.com/tiliavir/allmend/internal/store"
	"github.com/tiliavir/allmend/internal/velo"
	"github.com/tiliavir/allmend/internal/webui"
	"github.com/tiliavir/allmend/ui"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	if err := run(os.Args[1:]); err != nil {
		slog.Error("allmend", "err", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	cmd := "serve"
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "serve":
		return serve()
	case "migrate":
		fl := flag.NewFlagSet("migrate", flag.ContinueOnError)
		dry := fl.Bool("dry-run", false, "run on a copy and report record counts")
		if err := fl.Parse(args); err != nil {
			return err
		}
		return migrate(*dry)
	case "hashpw":
		return hashpw()
	case "health":
		return health()
	case "demo":
		fl := flag.NewFlagSet("demo", flag.ContinueOnError)
		base := fl.String("base", "/", "URL path the site is served below, e.g. /allmend/")
		if err := fl.Parse(args); err != nil {
			return err
		}
		if fl.NArg() != 1 {
			return errors.New("usage: allmend demo [-base /path/] DIR")
		}
		// Request logs of the crawl are noise here.
		slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelWarn})))
		n, err := buildDemo(context.Background(), fl.Arg(0), *base, time.Now())
		if err == nil {
			fmt.Printf("demo: %d pages written to %s\n", n, fl.Arg(0))
		}
		return err
	}
	return fmt.Errorf("unknown command %q", cmd)
}

type hasher struct{}

func (hasher) Hash(pw string) string           { return password.Hash(pw) }
func (hasher) Verify(pw, encoded string) error { return password.Verify(pw, encoded) }

func loadConfig() (env.Config, error) {
	cfg, err := env.Load(os.Environ())
	if err != nil {
		return cfg, err
	}
	if cfg.AdminPWHash != "" {
		if err := password.Valid(cfg.AdminPWHash); err != nil {
			return cfg, fmt.Errorf("ALLMEND_ADMIN_PASSWORD_HASH: %w", err)
		}
	}
	return cfg, os.MkdirAll(cfg.DataDir, 0o750)
}

func serve() error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	d, err := db.Open(ctx, cfg.DBPath())
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	res, err := db.Migrate(ctx, d, migrations.All(), db.Options{BackupDir: cfg.BackupDir()})
	if err != nil {
		return err
	}
	if len(res.Applied) > 0 {
		slog.Info("migrated", "from", res.From, "to", res.To, "applied", res.Applied, "backup", res.BackupPath)
	}

	st := store.New(d)
	accounts := &account.Service{Repo: st.Accounts(), Hasher: hasher{}, AdminEmail: cfg.AdminEmail, BreakGlassHash: cfg.AdminPWHash}
	if _, err := accounts.EnsureAdmin(ctx); err != nil {
		return err
	}
	srv, err := newServer(st, accounts, cfg.BaseCurrency, cfg.SecureCookie)
	if err != nil {
		return err
	}
	fin := st.Finanzen()
	refresher := &finanzen.Refresher{Market: fin, Rates: &quotes.Frankfurter{BaseURL: cfg.FrankfurterURL}, Base: cfg.BaseCurrency,
		Pause: 2 * time.Second, Budget: 25, ProfileAge: 30 * 24 * time.Hour}
	if cfg.AlphaVantageKey != "" {
		refresher.Quotes = &quotes.AlphaVantage{Key: cfg.AlphaVantageKey}
	}
	srv.FinRefresh = func() bool { return refresher.Start(ctx) }
	if cfg.GoogleEnabled() {
		srv.Google = google.New(cfg.GoogleClientID, cfg.GoogleClientSecret, cfg.BaseURL.String()+"/anmelden/google/zurueck")
	}

	go cleanup(ctx, st.Sessions())
	go refresher.Schedule(ctx, srv.Location, 5)

	hs := &http.Server{
		Addr: cfg.Addr, Handler: srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 120 * time.Second,
	}
	errc := make(chan error, 1)
	go func() { errc <- hs.ListenAndServe() }()
	slog.Info("listening", "addr", cfg.Addr, "google", cfg.GoogleEnabled())
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shut, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return hs.Shutdown(shut)
}

// newServer wires the domain services and the web layer on st.
func newServer(st *store.Store, accounts *account.Service, baseCurrency string, secureCookie bool) (*webui.Server, error) {
	tpl, err := webui.ParseTemplates(ui.Templates)
	if err != nil {
		return nil, err
	}
	static, err := fs.Sub(ui.Static, "static")
	if err != nil {
		return nil, err
	}
	loc, err := time.LoadLocation("Europe/Zurich")
	if err != nil {
		return nil, err
	}
	return &webui.Server{
		Accounts:     accounts,
		Links:        &links.Service{Repo: st.Links()},
		Pages:        &pages.Service{Repo: st.Pages()},
		Commute:      &commute.Service{Repo: st.Commute()},
		Velo:         &velo.Service{Repo: st.Velo(), Loc: loc},
		Finanzen:     &finanzen.Service{Repo: st.Finanzen(), Market: st.Finanzen(), Base: baseCurrency},
		Schiffe:      &schiffe.Service{Repo: st.Schiffe(), Accounts: st.Accounts()},
		Ausgaben:     &ausgaben.Service{Repo: st.Ausgaben(), Accounts: st.Accounts(), Loc: loc},
		Places:       st.Places(),
		BaseCurrency: baseCurrency,
		Sessions:     &session.Manager{Repo: st.Sessions(), Secure: secureCookie},
		Templates:    tpl,
		Static:       static,
		Location:     loc,
	}, nil
}

func cleanup(ctx context.Context, r *store.Sessions) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		if err := r.DeleteExpired(ctx, time.Now().UTC()); err != nil && ctx.Err() == nil {
			slog.Error("delete expired sessions", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func migrate(dry bool) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	ctx := context.Background()
	path := cfg.DBPath()
	backupDir := cfg.BackupDir()
	if dry {
		tmp, err := os.MkdirTemp("", "allmend-dry-run-")
		if err != nil {
			return err
		}
		defer func() { _ = os.RemoveAll(tmp) }()
		copyPath := filepath.Join(tmp, "allmend.db")
		if _, err := os.Stat(path); err == nil {
			src, err := db.Open(ctx, path)
			if err != nil {
				return err
			}
			_, err = src.ExecContext(ctx, `VACUUM main INTO ?`, copyPath)
			_ = src.Close()
			if err != nil {
				return fmt.Errorf("copy database: %w", err)
			}
		}
		path, backupDir = copyPath, filepath.Join(tmp, "backup")
	}
	d, err := db.Open(ctx, path)
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	before, err := db.Counts(ctx, d)
	if err != nil {
		return err
	}
	res, err := db.Migrate(ctx, d, migrations.All(), db.Options{BackupDir: backupDir})
	if err != nil {
		return err
	}
	after, err := db.Counts(ctx, d)
	if err != nil {
		return err
	}
	var w strings.Builder
	if dry {
		fmt.Fprintln(&w, "DRY RUN — nothing was changed.")
	}
	fmt.Fprintf(&w, "schema version %d → %d, applied %d step(s)\n", res.From, res.To, len(res.Applied))
	for _, a := range res.Applied {
		fmt.Fprintln(&w, "  "+a)
	}
	fmt.Fprintf(&w, "\n%-40s %10s %10s\n", "table", "before", "after")
	for _, k := range db.SortedKeys(before, after) {
		fmt.Fprintf(&w, "%-40s %10s %10s\n", k, count(before, k), count(after, k))
	}
	_, err = os.Stdout.WriteString(w.String())
	return err
}

func count(m map[string]int, k string) string {
	if v, ok := m[k]; ok {
		return fmt.Sprint(v)
	}
	return "-"
}

func hashpw() error {
	fmt.Fprint(os.Stderr, "Passwort: ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && !errors.Is(err, os.ErrClosed) && line == "" {
		return err
	}
	pw := strings.TrimRight(line, "\r\n")
	if len([]rune(pw)) < account.MinPasswordLength {
		return fmt.Errorf("password must have at least %d characters", account.MinPasswordLength)
	}
	fmt.Println(password.Hash(pw))
	return nil
}

func health() error {
	addr := os.Getenv("ALLMEND_ADDR")
	if addr == "" || strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
		if addr == "127.0.0.1" {
			addr += ":8080"
		}
	}
	c := http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get("http://" + addr + "/health") //nolint:gosec // address comes from our own config, not from a request
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health: status %d", resp.StatusCode)
	}
	return nil
}
