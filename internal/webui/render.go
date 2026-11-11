package webui

import (
	"bytes"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/tiliavir/allmend/internal/account"
	"github.com/tiliavir/allmend/internal/finanzen"
	"github.com/tiliavir/allmend/internal/links"
	"github.com/tiliavir/allmend/internal/velo"
)

// Page is what every template receives.
type Page struct {
	Title   string
	Module  account.Module // colours the page; empty for start/admin
	Account *account.Account
	Nav     []NavItem
	Flash   string
	Error   string
	Scripts []Script
	Data    any
	Demo    bool // static demo export: banner, forms handled by demo.js
}

// Script is a page-specific script (always same-origin, deferred).
type Script struct {
	Src    string
	Module bool
}

// Initial is the avatar letter.
func (p Page) Initial() string {
	if p.Account == nil {
		return ""
	}
	r := []rune(p.Account.DisplayName())
	if len(r) == 0 {
		return "?"
	}
	return strings.ToUpper(string(r[0]))
}

// DarkMedia is the media attribute of the dark stylesheet: always for
// "dark", never for "light" (empty: not linked), else the device setting.
func (p Page) DarkMedia() string {
	if p.Account == nil {
		return "(prefers-color-scheme: dark)"
	}
	switch p.Account.Theme {
	case account.ThemeDark:
		return "all"
	case account.ThemeLight:
		return ""
	}
	return "(prefers-color-scheme: dark)"
}

// NavItem is one module in the header.
type NavItem struct {
	Name, Path string
	Current    bool
}

// num formats a number German style; 0 shows as a dash.
func num(f float64) string {
	if f == 0 {
		return "–"
	}
	return strings.ReplaceAll(strconv.FormatFloat(f, 'f', -1, 64), ".", ",")
}

// eur formats euro cents German style: "1.234,56 €".
func eur(cents int64) string {
	neg := cents < 0
	if neg {
		cents = -cents
	}
	whole := strconv.FormatInt(cents/100, 10)
	var b strings.Builder
	for i, c := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(c)
	}
	out := fmt.Sprintf("%s,%02d €", b.String(), cents%100)
	if neg {
		return "−" + out
	}
	return out
}

// Templates holds one parsed set per page.
type Templates map[string]*template.Template

var funcs = template.FuncMap{
	"num":  num,
	"num1": func(f float64) string { return strings.ReplaceAll(strconv.FormatFloat(f, 'f', 1, 64), ".", ",") },
	"input": func(f float64) string {
		if f == 0 {
			return ""
		}
		return strconv.FormatFloat(f, 'f', -1, 64)
	},
	"inputp": func(f *float64) string {
		if f == nil {
			return ""
		}
		return strconv.FormatFloat(*f, 'f', -1, 64)
	},
	"pct": func(f float64) string {
		if f == 0 {
			return "–"
		}
		return strconv.Itoa(int(f*100+0.5)) + " %"
	},
	"eur": eur,
	"dict": func(kv ...any) map[string]any {
		m := map[string]any{}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i].(string)] = kv[i+1]
		}
		return m
	},
	"div":  func(a float64, b int) float64 { return a / float64(b) },
	"sub":  func(a, b int) int { return a - b },
	"subf": func(a, b float64) float64 { return a - b },
	"seq": func(n int) []int {
		out := make([]int, n)
		for i := range out {
			out[i] = i
		}
		return out
	},
	"monthShort": func(i int) string { return monthShort[i] },
	"deref":      func(p *float64) float64 { return *p },
	"money":      money,
	"moneyInput": moneyInput,
	"km":         func(f float64) string { return velo.FmtInt(int(math.Round(f))) },
	"int":        velo.FmtInt,
	"day":        velo.DayLabel,
	"monthName":  func(m int) string { return velo.MonthNames[m-1] },
	"signed": func(f float64) string {
		if f >= 0 {
			return "+" + velo.FmtInt(int(math.Round(f)))
		}
		return "−" + velo.FmtInt(int(math.Round(-f)))
	},
	"icon":      icon,
	"linkIcons": func() []string { return links.Icons },
	"iconLabel": func(s string) string { return iconLabels[s] },
	"gaincls": func(c int64) string {
		switch {
		case c > 0:
			return "pos"
		case c < 0:
			return "neg"
		}
		return ""
	},
	"qty": func(f float64) string {
		return strings.ReplaceAll(strconv.FormatFloat(f, 'f', -1, 64), ".", ",")
	},
	"pct1": func(f float64) string {
		return strings.ReplaceAll(strconv.FormatFloat(f*100, 'f', 1, 64), ".", ",") + " %"
	},
	"kindLabel": finanzen.KindLabel,
	"afterTax":  finanzen.AfterTax,
	"whole": func(c int64, cur string) string { // whole units, no currency: for wide tables
		out := money(int64(math.Round(float64(c)/100))*100, cur)
		return strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(out, " "+cur), ",00"), ".00")
	},
	"eurf": func(f float64, cur string) string { return money(int64(math.Round(f*100)), cur) },
	"dateDE": func(d string) string {
		if len(d) != 10 {
			return d
		}
		return d[8:10] + "." + d[5:7] + "." + d[:4]
	},
	"statusLabel": func(s account.Status) string {
		switch s {
		case account.Invited:
			return "eingeladen"
		case account.Active:
			return "aktiv"
		case account.Disabled:
			return "gesperrt"
		}
		return string(s)
	},
	"themeLabel": func(t account.Theme) string {
		switch t {
		case account.ThemeAuto:
			return "Automatisch (wie das Gerät)"
		case account.ThemeLight:
			return "Hell"
		case account.ThemeDark:
			return "Dunkel"
		}
		return string(t)
	},
}

// ParseTemplates parses every page with the layout and partials. Errors are
// fatal at startup, never at request time.
func ParseTemplates(fsys fs.FS) (Templates, error) {
	out := Templates{}
	err := fs.WalkDir(fsys, "templates", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || path.Ext(p) != ".html" {
			return err
		}
		name := strings.TrimSuffix(strings.TrimPrefix(p, "templates/"), ".html")
		if name == "layout" || name == "partials" {
			return nil
		}
		t, err := template.New("layout").Funcs(funcs).ParseFS(fsys, "templates/layout.html", "templates/partials.html", p)
		if err != nil {
			return fmt.Errorf("webui: parse %s: %w", p, err)
		}
		out[name] = t
		return nil
	})
	return out, err
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, status int, name string, p Page) {
	t, ok := s.Templates[name]
	if !ok {
		s.fail(w, r, fmt.Errorf("webui: no template %q", name))
		return
	}
	if p.Account == nil {
		p.Account = currentAccount(r)
	}
	if p.Account != nil {
		p.Nav = nav(*p.Account, r.URL.Path)
	}
	p.Demo = s.Demo
	if f := takeFlash(w, r); f != "" && p.Flash == "" {
		p.Flash = f
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "layout", p); err != nil {
		s.fail(w, r, fmt.Errorf("webui: execute %s: %w", name, err))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}

func nav(a account.Account, current string) []NavItem {
	var out []NavItem
	for _, m := range account.Modules {
		if m.Ready && a.Can(m.ID) {
			out = append(out, NavItem{Name: m.Name, Path: m.Path, Current: strings.HasPrefix(current, m.Path)})
		}
	}
	return out
}

// fail logs an unexpected error and shows a generic error page.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	slog.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusInternalServerError)
	if t, ok := s.Templates["error"]; ok {
		_ = t.ExecuteTemplate(w, "layout", Page{Title: "Fehler", Data: errorData{"Etwas ist schiefgelaufen", "Der Fehler wurde protokolliert. Deine Daten sind unverändert."}})
	}
}

type errorData struct{ Heading, Text string }

func (s *Server) status(w http.ResponseWriter, r *http.Request, code int) {
	d := errorData{"Nicht gefunden", "Diese Seite gibt es nicht."}
	if code == http.StatusForbidden {
		d = errorData{"Kein Zugriff", "Dafür fehlen dir die Rechte. Ein Admin kann sie dir geben."}
	}
	if code == http.StatusBadRequest {
		d = errorData{"Ungültige Anfrage", "Die Anfrage war unvollständig oder fehlerhaft."}
	}
	s.render(w, r, code, "error", Page{Title: d.Heading, Data: d})
}

// redirect implements POST/redirect/GET with an optional flash message.
// to is always a path built by the handler, never request input.
func (s *Server) redirect(w http.ResponseWriter, r *http.Request, to, flash string) {
	if flash != "" {
		//nolint:gosec // Secure is config-driven (false only for plain-HTTP development)
		http.SetCookie(w, &http.Cookie{Name: "allmend_flash", Value: url.QueryEscape(flash), Path: "/", HttpOnly: true, Secure: s.Sessions.Secure, SameSite: http.SameSiteLaxMode, MaxAge: 60})
	}
	http.Redirect(w, r, to, http.StatusSeeOther) //nolint:gosec // see above: internal paths only
}

func takeFlash(w http.ResponseWriter, r *http.Request) string {
	c, err := r.Cookie("allmend_flash")
	if err != nil {
		return ""
	}
	//nolint:gosec // clears the cookie; nothing to protect
	http.SetCookie(w, &http.Cookie{Name: "allmend_flash", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	v, err := url.QueryUnescape(c.Value)
	if err != nil {
		return ""
	}
	return v
}
