// Package webui is the HTTP layer: routing, middleware and handlers that
// render html/template pages. It knows domain services, never the store.
package webui

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"path"
	"strings"
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
	Places     places.Repository
	Sessions   *session.Manager
	Google     *google.Provider // nil: Google login disabled
	Templates  Templates
	Static     fs.FS // rooted at the static directory
	Location   *time.Location
	// BaseCurrency is the default currency for new amounts (ISO 4217).
	BaseCurrency string
	// Demo renders the pages for the static demo (allmend demo).
	Demo bool

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
	// cspMermaid only adds inline <style> (Mermaid's SVG output).
	cspMermaid = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'"
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

// staticFiles serves ui/static. Vendored files (versioned names) are cached
// for a year; big ones are stored gzipped only (name + ".gz").
func staticFiles(fsys fs.FS) http.Handler {
	files := http.FileServerFS(fsys)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if p == "" || strings.HasSuffix(p, "/") || strings.HasSuffix(p, ".gz") {
			http.NotFound(w, r)
			return
		}
		if strings.HasPrefix(p, "vendor/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=3600")
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
