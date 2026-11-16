// Package webui is the HTTP layer: routing, middleware and handlers that
// render html/template pages. It knows domain services, never the store.
package webui

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/tiliavir/allmend/internal/account"
	"github.com/tiliavir/allmend/internal/ausgaben"
	"github.com/tiliavir/allmend/internal/commute"
	"github.com/tiliavir/allmend/internal/finanzen"
	"github.com/tiliavir/allmend/internal/google"
	"github.com/tiliavir/allmend/internal/links"
	"github.com/tiliavir/allmend/internal/offline"
	"github.com/tiliavir/allmend/internal/pages"
	"github.com/tiliavir/allmend/internal/places"
	"github.com/tiliavir/allmend/internal/schiffe"
	"github.com/tiliavir/allmend/internal/session"
	"github.com/tiliavir/allmend/internal/velo"
)

// Server holds the handler dependencies.
type Server struct {
	Accounts *account.Service
	Links    *links.Service
	Pages    *pages.Service
	Commute  *commute.Service
	Velo     *velo.Service
	Finanzen *finanzen.Service
	// FinRefresh starts a market data refresh in the background; false if
	// one is running. nil hides the button.
	FinRefresh func() bool
	Schiffe    *schiffe.Service
	Ausgaben   *ausgaben.Service
	Offline    *offline.Service
	Places     *places.Service
	Sessions   *session.Manager
	Google     *google.Provider // nil: Google login disabled
	Templates  Templates
	Static     fs.FS // rooted at the static directory
	Location   *time.Location
	// BaseCurrency is the default currency for new amounts (ISO 4217).
	BaseCurrency string
	// Demo renders the pages for the static demo (allmend demo).
	Demo bool
	// AppName is the displayed name and the name of the installed app;
	// empty means "Allmend".
	AppName string

	hub hub
	sw  swCache
}

type ctxKey struct{}

func currentAccount(r *http.Request) *account.Account {
	a, _ := r.Context().Value(ctxKey{}).(*account.Account)
	return a
}

// Handler returns the full HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := s.routes()
	csrf := http.NewCrossOriginProtection()
	return logRequests(securityHeaders(csrf.Handler(s.loadAccount(s.replay(mux)))))
}

// routes registers every route.
func (s *Server) routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.StripPrefix("/static/", staticFiles(s.Static)))
	mux.HandleFunc("GET /static/"+manifestFile, s.manifest)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })

	mux.HandleFunc("GET /{$}", s.start)
	mux.HandleFunc("GET /anmelden", s.loginPage)
	mux.HandleFunc("POST /anmelden", s.loginLocal)
	mux.HandleFunc("GET /anmelden/google", s.loginGoogle)
	mux.HandleFunc("GET /anmelden/google/zurueck", s.loginGoogleCallback)
	mux.HandleFunc("POST /abmelden", s.logout)

	mux.Handle("GET /konto", s.auth(s.konto))
	mux.Handle("POST /konto/passwort", s.auth(s.changePassword))
	mux.Handle("POST /konto/darstellung", s.auth(s.changeTheme))

	mux.Handle("GET /verwaltung", s.admin(s.adminIndex))
	mux.Handle("GET /verwaltung/konten", s.admin(s.adminAccounts))
	mux.Handle("POST /verwaltung/konten", s.admin(s.adminInvite))
	mux.Handle("GET /verwaltung/konten/{id}", s.admin(s.adminAccount))
	mux.Handle("POST /verwaltung/konten/{id}", s.admin(s.adminAccountSave))
	mux.Handle("POST /verwaltung/konten/{id}/passwort", s.admin(s.adminResetPassword))
	mux.Handle("POST /verwaltung/konten/{id}/passwort/entfernen", s.admin(s.adminRemovePassword))
	mux.Handle("POST /verwaltung/konten/{id}/loeschen", s.admin(s.adminAccountDelete))
	mux.Handle("GET /verwaltung/links", s.admin(s.adminLinks))
	mux.Handle("POST /verwaltung/links", s.admin(s.adminLinkCreate))
	mux.Handle("GET /verwaltung/links/{id}", s.admin(s.adminLink))
	mux.Handle("POST /verwaltung/links/{id}", s.admin(s.adminLinkSave))
	mux.Handle("POST /verwaltung/links/{id}/hoch", s.admin(s.adminLinkMove(-1)))
	mux.Handle("POST /verwaltung/links/{id}/runter", s.admin(s.adminLinkMove(1)))
	mux.Handle("POST /verwaltung/links/{id}/loeschen", s.admin(s.adminLinkDelete))

	s.seitenRoutes(mux)
	s.arbeitswegeRoutes(mux)
	s.veloRoutes(mux)
	s.schiffeRoutes(mux)
	s.orteRoutes(mux)
	s.finanzenRoutes(mux)
	s.ausgabenRoutes(mux)
	s.offlineRoutes(mux)

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { s.status(w, r, http.StatusNotFound) })
	return mux
}

// loadAccount puts the session's account into the request context.
func (s *Server) loadAccount(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := s.Sessions.Account(r)
		if err != nil && !errors.Is(err, session.ErrNoSession) {
			s.fail(w, r, err)
			return
		}
		if id != "" {
			a, err := s.Accounts.Repo.Get(r.Context(), id)
			switch {
			case errors.Is(err, account.ErrNotFound):
			case err != nil:
				s.fail(w, r, err)
				return
			case a.Status == account.Active:
				r = r.WithContext(context.WithValue(r.Context(), ctxKey{}, &a))
			}
		}
		next.ServeHTTP(w, r)
	})
}

// auth requires a logged-in account; a one-time password must be replaced first.
func (s *Server) auth(h func(http.ResponseWriter, *http.Request, account.Account)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a := currentAccount(r)
		if a == nil {
			http.Redirect(w, r, "/anmelden", http.StatusSeeOther)
			return
		}
		if a.PasswordMustChange && r.URL.Path != "/konto" && r.URL.Path != "/konto/passwort" {
			s.redirect(w, r, "/konto", "")
			return
		}
		h(w, r, *a)
	})
}

// admin requires an active admin.
func (s *Server) admin(h func(http.ResponseWriter, *http.Request, account.Account)) http.Handler {
	return s.auth(func(w http.ResponseWriter, r *http.Request, a account.Account) {
		if !a.IsAdmin {
			s.status(w, r, http.StatusForbidden)
			return
		}
		h(w, r, a)
	})
}

// Module guards a module route: login plus access to m.
func (s *Server) Module(m account.Module, h func(http.ResponseWriter, *http.Request, account.Account)) http.Handler {
	return s.auth(func(w http.ResponseWriter, r *http.Request, a account.Account) {
		if !a.Can(m) {
			s.status(w, r, http.StatusForbidden)
			return
		}
		h(w, r, a)
	})
}

const (
	cspHeader = "Content-Security-Policy"
	csp       = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'"
	// cspDiagram only adds inline <style> (Mermaid's and Markmap's SVG output).
	cspDiagram = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'"
)

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set(cspHeader, csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) { w.status = code; w.ResponseWriter.WriteHeader(code) }

// Flush keeps Server-Sent Events working through the wrapper.
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// logRequests logs method, path (never the query) and status — no PII.
func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		if r.URL.Path == "/health" || len(r.URL.Path) > 7 && r.URL.Path[:8] == "/static/" {
			return
		}
		slog.Info("request", "method", r.Method, "path", r.URL.Path, "status", sw.status, "ms", time.Since(start).Milliseconds())
	})
}

// vendorFiles are the libraries in ui/static/vendor the pages refer to.
var vendorFiles = []string{"mermaid.min.js", "chart.umd.min.js", "leaflet.js", "leaflet.css", "leaflet-heat.js", "markmap.js", "LICENSES.txt"}

// CheckVendor reports libraries missing from static. They are not in git
// but built by `make vendor` (ADR vendor-build); a binary built without them
// would serve pages without charts, maps and diagrams.
func CheckVendor(static fs.FS) error {
	var missing []string
	for _, f := range vendorFiles {
		_, err := fs.Stat(static, "vendor/"+f)
		if errors.Is(err, fs.ErrNotExist) {
			_, err = fs.Stat(static, "vendor/"+f+".gz")
		}
		if errors.Is(err, fs.ErrNotExist) {
			missing = append(missing, f)
		} else if err != nil {
			return fmt.Errorf("webui: vendor/%s: %w", f, err)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("webui: ui/static/vendor lacks %s; run `make vendor` before building", strings.Join(missing, ", "))
	}
	return nil
}

// staticFiles serves ui/static, including the libraries in vendor/ (built by
// make vendor). Big files are stored gzipped only (name + ".gz"). Files keep
// their names across releases, so the browser must revalidate them
// (no-cache + ETag of the content; 304 when unchanged): with max-age a
// release's changed script was not used until the old copy expired.
func staticFiles(fsys fs.FS) http.Handler {
	files := http.FileServerFS(fsys)
	// computed on first use: routes are also built without files (tests)
	etags := sync.OnceValue(func() map[string]string {
		m := map[string]string{}
		err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			b, err := fs.ReadFile(fsys, p)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(b)
			m[strings.TrimSuffix(p, ".gz")] = `"` + hex.EncodeToString(sum[:8]) + `"`
			return nil
		})
		if err != nil { // embedded files: not expected; without ETags they are just sent in full
			slog.Error("static etags", "err", err)
		}
		return m
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if p == "" || strings.HasSuffix(p, "/") || strings.HasSuffix(p, ".gz") {
			http.NotFound(w, r)
			return
		}
		if strings.HasSuffix(p, ".webmanifest") { // not in Go's built-in MIME table
			w.Header().Set("Content-Type", "application/manifest+json")
		}
		w.Header().Set("Cache-Control", "no-cache")
		if tag, ok := etags()[p]; ok {
			w.Header().Set("ETag", tag)
			if r.Header.Get("If-None-Match") == tag {
				w.WriteHeader(http.StatusNotModified)
				return
			}
		}
		if gz, err := fs.ReadFile(fsys, p+".gz"); err == nil {
			if ct := mime.TypeByExtension(path.Ext(p)); ct != "" {
				w.Header().Set("Content-Type", ct)
			}
			w.Header().Set("Vary", "Accept-Encoding")
			if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
				w.Header().Set("Content-Encoding", "gzip")
				_, _ = w.Write(gz) //nolint:gosec // our own embedded asset, not request data
				return
			}
			zr, err := gzip.NewReader(bytes.NewReader(gz))
			if err != nil {
				http.Error(w, "broken asset", http.StatusInternalServerError)
				return
			}
			_, _ = io.Copy(w, zr) //nolint:gosec // our own embedded asset, size known
			return
		}
		files.ServeHTTP(w, r)
	})
}
