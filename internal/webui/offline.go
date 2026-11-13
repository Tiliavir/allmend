package webui

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/tiliavir/allmend/internal/account"
	"github.com/tiliavir/allmend/internal/offline"
)

// Offline mode (ADR offline): the service worker ui/static/sw.js shows cached
// pages while the server is unreachable and keeps the forms below in an
// outbox. It sends them again later with these headers.
const (
	hdrKey     = "Allmend-Vorgang" // UUID of the outbox entry
	hdrAccount = "Allmend-Konto"   // account the entry was made with; also set on every page
)

// offlineWrites are the only forms that work offline. Every other POST and
// Schiffe versenken need a connection; every other page is readable offline.
var offlineWrites = []string{
	"POST /ausgaben/{g}/eintraege", // Ausgabe erfassen
	"POST /arbeitswege/{jahr}",     // Arbeitsweg ändern (versioned)
	"POST /velo/touren",            // Velo-Tour erfassen
}

// offlineNeverPrefixes are paths the service worker neither caches nor serves offline.
var offlineNeverPrefixes = []string{"/schiffe", "/anmelden", "/abmelden", "/sw.js", "/health"}

func (s *Server) offlineRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /sw.js", s.serviceWorker)
	mux.Handle("GET /offline", s.auth(s.offlineOutbox))
	mux.Handle("GET /offline/{art}", s.auth(s.offlineStatus))
}

// replay guards requests from the outbox: only the listed forms, only for
// the account that made them, and never twice (the key is remembered after
// the redirect that ends a successful POST).
func (s *Server) replay(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key, owner := r.Header.Get(hdrKey), r.Header.Get(hdrAccount)
		if key == "" && owner == "" {
			mux.ServeHTTP(w, r)
			return
		}
		_, pattern := mux.Handler(r)
		if !slices.Contains(offlineWrites, pattern) || !offline.ValidKey(key) {
			s.status(w, r, http.StatusBadRequest)
			return
		}
		a := currentAccount(r)
		if a == nil || a.PasswordMustChange {
			s.offlineError(w, r, http.StatusUnauthorized, "Nicht angemeldet", "Melde dich an; danach werden die offline erfassten Einträge übertragen.")
			return
		}
		if owner != a.ID {
			s.offlineError(w, r, http.StatusConflict, "Anderes Konto", "Dieser Eintrag wurde mit einem anderen Konto erfasst.")
			return
		}
		prev, err := s.Offline.Applied(r.Context(), a.ID, key)
		switch {
		case err == nil:
			// prev.Location came from our own redirect (offline.Record checks it is a path).
			http.Redirect(w, r, prev.Location, http.StatusSeeOther)
			return
		case errors.Is(err, offline.ErrForeign):
			s.offlineError(w, r, http.StatusConflict, "Anderes Konto", "Dieser Eintrag wurde mit einem anderen Konto erfasst.")
			return
		case !errors.Is(err, offline.ErrNotFound):
			s.fail(w, r, err)
			return
		}
		mux.ServeHTTP(&replayWriter{ResponseWriter: w, done: func(location string) {
			// Not in the handler's transaction: a crash right here lets the
			// entry be applied again (accepted, #24).
			if err := s.Offline.Record(r.Context(), a.ID, key, pattern, location); err != nil {
				slog.Error("record offline request", "account", a.ID, "route", pattern, "err", err)
			}
		}}, r)
	})
}

// replayWriter calls done once when the handler redirects (POST/redirect/GET).
type replayWriter struct {
	http.ResponseWriter
	done   func(location string)
	called bool
}

func (w *replayWriter) WriteHeader(code int) {
	if code == http.StatusSeeOther && !w.called {
		w.called = true
		w.done(w.Header().Get("Location"))
	}
	w.ResponseWriter.WriteHeader(code)
}

func (s *Server) offlineError(w http.ResponseWriter, r *http.Request, code int, heading, text string) {
	s.render(w, r, code, "error", Page{Title: heading, Data: errorData{heading, text}})
}

// ── service worker ────────────────────────────────────

type swConfig struct {
	Version string   `json:"version"`
	Writes  []string `json:"writes"` // regular expressions over the path
	Never   []string `json:"never"`  // path prefixes
	CSP     string   `json:"csp"`
	Pages   []string `json:"pages"`  // offline pages cached at install
	Assets  []string `json:"assets"` // own static files cached at install
}

// swCache holds the built worker script (built once per server).
type swCache struct {
	once sync.Once
	body []byte
	err  error
}

var patternParam = regexp.MustCompile(`\{[^}]+\}`)

// writeRegexps turns the offlineWrites patterns into path expressions for the worker.
func writeRegexps() []string {
	out := make([]string, 0, len(offlineWrites))
	for _, p := range offlineWrites {
		path := strings.TrimPrefix(p, "POST ")
		parts := patternParam.Split(path, -1)
		for i := range parts {
			parts[i] = regexp.QuoteMeta(parts[i])
		}
		out = append(out, "^"+strings.Join(parts, "[^/]+")+"$")
	}
	return out
}

// staticVersion hashes every static file, so each release gets new caches.
func staticVersion(fsys fs.FS) (string, error) {
	h := sha256.New()
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(h, "%s\x00%d\x00", p, len(b))
		_, _ = h.Write(b)
		return nil
	})
	return hex.EncodeToString(h.Sum(nil))[:12], err
}

func (s *Server) buildServiceWorker() ([]byte, error) {
	src, err := fs.ReadFile(s.Static, "sw.js")
	if err != nil {
		return nil, err
	}
	v, err := staticVersion(s.Static)
	if err != nil {
		return nil, err
	}
	pages := []string{"/offline"}
	for _, a := range offlineArten {
		pages = append(pages, "/offline/"+a.Key)
	}
	var assets []string
	entries, err := fs.ReadDir(s.Static, ".")
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		// vendor files are big and cached when a page first needs them
		if !e.IsDir() && e.Name() != "sw.js" && e.Name() != "demo.js" && !strings.HasSuffix(e.Name(), ".gz") {
			assets = append(assets, "/static/"+e.Name())
		}
	}
	cfg, err := json.Marshal(swConfig{Version: v, Writes: writeRegexps(), Never: offlineNeverPrefixes, CSP: csp, Pages: pages, Assets: assets})
	if err != nil {
		return nil, err
	}
	const marker = "const CONFIG = {};"
	if !strings.Contains(string(src), marker) {
		return nil, errors.New("webui: sw.js has no CONFIG marker")
	}
	return []byte(strings.Replace(string(src), marker, "const CONFIG = "+string(cfg)+";", 1)), nil
}

func (s *Server) serviceWorker(w http.ResponseWriter, r *http.Request) {
	s.sw.once.Do(func() { s.sw.body, s.sw.err = s.buildServiceWorker() })
	if s.sw.err != nil {
		s.fail(w, r, s.sw.err)
		return
	}
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(s.sw.body)
}

// ── offline pages (cached by the worker at install) ───

type offlineArt struct {
	Key, Heading, Text string
}

// offlineArten are the pages the worker answers with while offline.
var offlineArten = []offlineArt{
	{"gespeichert", "Offline gespeichert", "Der Eintrag wird übertragen, sobald Allmend wieder erreichbar ist."},
	{"nicht-moeglich", "Offline – Änderung nicht möglich", "Diese Änderung braucht eine Verbindung und wurde nicht gespeichert. Mit „Zurück“ findest du deine Eingaben meist noch im Formular."},
	{"nicht-verfuegbar", "Offline nicht verfügbar", "Diese Seite hast du auf diesem Gerät noch nicht geöffnet, darum gibt es keine gespeicherte Fassung."},
	{"schiffe", "Keine Verbindung", "Schiffe versenken braucht eine Verbindung."},
}

func (s *Server) offlineStatus(w http.ResponseWriter, r *http.Request, _ account.Account) {
	i := slices.IndexFunc(offlineArten, func(a offlineArt) bool { return a.Key == r.PathValue("art") })
	if i < 0 {
		s.status(w, r, http.StatusNotFound)
		return
	}
	a := offlineArten[i]
	s.render(w, r, http.StatusOK, "offline/status", Page{Title: a.Heading, Data: a})
}

func (s *Server) offlineOutbox(w http.ResponseWriter, r *http.Request, _ account.Account) {
	s.render(w, r, http.StatusOK, "offline/outbox", Page{Title: "Nicht übertragen", Scripts: []Script{{Src: "/static/outbox.js", Module: true}}})
}
