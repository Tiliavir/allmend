package demo

import (
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"html"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Export crawls the app as the logged-in account (session cookie) starting at
// "/", following links and the options of GET filter forms, and writes every
// page as static HTML below out, plus the static assets. Links are rewritten
// to absolute paths below base (e.g. "/allmend/"), so the site works on
// GitHub Pages. Every crawled page must answer 200 and every referenced
// asset must exist; anything else is an error.
//
// A URL with a query becomes a directory: /velo?jahr=2025 → velo/~jahr=2025/.
// ui/static/demo.js maps submitted filter forms the same way.
func Export(h http.Handler, cookie *http.Cookie, static fs.FS, out, base string) (pages int, err error) {
	if !strings.HasPrefix(base, "/") || !strings.HasSuffix(base, "/") {
		return 0, fmt.Errorf("demo: base %q must start and end with /", base)
	}
	e := &exporter{h: h, cookie: cookie, static: static, out: out, base: base, seen: map[string]bool{}}
	e.enqueue("/")
	for len(e.queue) > 0 {
		u := e.queue[0]
		e.queue = e.queue[1:]
		if err := e.page(u); err != nil {
			return len(e.seen), err
		}
	}
	if err := e.notFound(); err != nil {
		return len(e.seen), err
	}
	if err := e.assets(); err != nil {
		return len(e.seen), err
	}
	return len(e.seen), nil
}

type exporter struct {
	h      http.Handler
	cookie *http.Cookie
	static fs.FS
	out    string
	base   string
	seen   map[string]bool
	queue  []string
}

var safe = regexp.MustCompile(`^[A-Za-z0-9_.-]*$`)

// canonical returns "/path" or "/path?k=v&…" with sorted keys; query parts
// must be plain (ids, years, dates) so they map to a directory name.
func canonical(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("demo: bad URL %q: %w", raw, err)
	}
	if u.Scheme != "" || u.Host != "" || !strings.HasPrefix(u.Path, "/") {
		return "", fmt.Errorf("demo: not an internal URL %q", raw)
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return "", fmt.Errorf("demo: bad query in %q: %w", raw, err)
	}
	if len(q) == 0 {
		return u.Path, nil
	}
	keys := make([]string, 0, len(q))
	for k, vs := range q {
		if len(vs) != 1 || !safe.MatchString(k) || !safe.MatchString(vs[0]) || k == "" {
			return "", fmt.Errorf("demo: query of %q cannot be exported", raw)
		}
		keys = append(keys, k)
	}
	slices.Sort(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + "=" + q.Get(k)
	}
	return u.Path + "?" + strings.Join(parts, "&"), nil
}

// dir is the directory (relative, "" for the root) a canonical URL is written to.
func dir(c string) string {
	p, q, _ := strings.Cut(c, "?")
	d := strings.Trim(p, "/")
	if q != "" {
		d = path.Join(d, "~"+strings.ReplaceAll(q, "&", ","))
	}
	return d
}

func (e *exporter) href(c string) string {
	if d := dir(c); d != "" {
		return e.base + d + "/"
	}
	return e.base
}

func (e *exporter) enqueue(c string) {
	if !e.seen[c] {
		e.seen[c] = true
		e.queue = append(e.queue, c)
	}
}

func (e *exporter) get(c string) (int, string) {
	r := httptest.NewRequest(http.MethodGet, c, nil)
	r.AddCookie(e.cookie)
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	return w.Code, w.Body.String()
}

func (e *exporter) page(c string) error {
	code, body := e.get(c)
	if code != http.StatusOK {
		return fmt.Errorf("demo: GET %s: status %d", c, code)
	}
	if err := e.forms(c, body); err != nil {
		return err
	}
	out, err := e.rewrite(c, body, true)
	if err != nil {
		return err
	}
	return e.write(path.Join(dir(c), "index.html"), []byte(out))
}

func (e *exporter) notFound() error {
	code, body := e.get("/diese-seite-gibt-es-nicht")
	if code != http.StatusNotFound {
		return fmt.Errorf("demo: not-found page: status %d", code)
	}
	out, err := e.rewrite("/", body, false)
	if err != nil {
		return err
	}
	return e.write("404.html", []byte(out))
}

var (
	attrRe   = regexp.MustCompile(`\b(href|src|action)="([^"]*)"`)
	formRe   = regexp.MustCompile(`(?s)<form\b([^>]*)>(.*?)</form>`)
	methodRe = regexp.MustCompile(`\bmethod="([a-z]+)"`)
	actionRe = regexp.MustCompile(`\baction="([^"]*)"`)
	selectRe = regexp.MustCompile(`(?s)<select\b[^>]*\bname="([^"]+)"[^>]*>(.*?)</select>`)
	optionRe = regexp.MustCompile(`<option\b[^>]*\bvalue="([^"]*)"`)
	inputRe  = regexp.MustCompile(`<input\b[^>]*>`)
	nameRe   = regexp.MustCompile(`\bname="([^"]+)"`)
	valueRe  = regexp.MustCompile(`\bvalue="([^"]*)"`)

	checkableRe = regexp.MustCompile(`\btype="(checkbox|radio)"`)
	checkedRe   = regexp.MustCompile(`\schecked\b`)
)

// rewrite points internal URLs at the export; with follow, linked pages are queued.
func (e *exporter) rewrite(from, body string, follow bool) (string, error) {
	var errs []error
	out := attrRe.ReplaceAllStringFunc(body, func(m string) string {
		sub := attrRe.FindStringSubmatch(m)
		attr, raw := sub[1], html.UnescapeString(sub[2])
		if raw == "" || strings.HasPrefix(raw, "#") || !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") {
			return m // fragment or external
		}
		if p, ok := strings.CutPrefix(raw, "/static/"); ok {
			if _, err := fs.Stat(e.static, p); err != nil {
				if _, gzErr := fs.Stat(e.static, p+".gz"); gzErr != nil {
					errs = append(errs, fmt.Errorf("demo: %s references missing asset %s", from, raw))
				}
			}
			return attr + `="` + html.EscapeString(e.base+"static/"+p) + `"`
		}
		target, frag, _ := strings.Cut(raw, "#")
		c, err := canonical(target)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", from, err))
			return m
		}
		if follow && attr == "href" {
			e.enqueue(c)
		}
		h := e.href(c)
		if frag != "" {
			h += "#" + frag
		}
		return attr + `="` + html.EscapeString(h) + `"`
	})
	return out, errors.Join(errs...)
}

// forms queues every combination of the select options of GET forms; other
// fields keep their current value. demo.js submits them to the same place.
func (e *exporter) forms(from, body string) error {
	for _, f := range formRe.FindAllStringSubmatch(body, -1) {
		attrs, inner := f[1], f[2]
		if m := methodRe.FindStringSubmatch(attrs); m == nil || m[1] != "get" {
			continue
		}
		a := actionRe.FindStringSubmatch(attrs)
		if a == nil {
			return fmt.Errorf("demo: %s: GET form without action", from)
		}
		action := html.UnescapeString(a[1])
		combos := []url.Values{{}}
		for _, in := range inputRe.FindAllString(inner, -1) {
			n := nameRe.FindStringSubmatch(in)
			if n == nil {
				continue
			}
			// Like the browser: an unchecked box is not submitted.
			if checkableRe.MatchString(in) && !checkedRe.MatchString(in) {
				continue
			}
			v := ""
			if m := valueRe.FindStringSubmatch(in); m != nil {
				v = html.UnescapeString(m[1])
			}
			for _, c := range combos {
				c.Set(n[1], v)
			}
		}
		for _, s := range selectRe.FindAllStringSubmatch(inner, -1) {
			opts := optionRe.FindAllStringSubmatch(s[2], -1)
			if len(opts) == 0 {
				return fmt.Errorf("demo: %s: select %s without options", from, s[1])
			}
			var next []url.Values
			for _, c := range combos {
				for _, o := range opts {
					n := url.Values{}
					for k, v := range c {
						n[k] = slices.Clone(v)
					}
					n.Set(s[1], html.UnescapeString(o[1]))
					next = append(next, n)
				}
			}
			if combos = next; len(combos) > 500 {
				return fmt.Errorf("demo: %s: too many filter combinations", from)
			}
		}
		for _, q := range combos {
			c, err := canonical(action + "?" + q.Encode())
			if err != nil {
				return fmt.Errorf("%s: %w", from, err)
			}
			e.enqueue(c)
		}
	}
	return nil
}

// assets copies ui/static; files stored only gzipped are written unpacked.
func (e *exporter) assets() error {
	return fs.WalkDir(e.static, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(e.static, p)
		if err != nil {
			return err
		}
		if strings.HasSuffix(p, ".gz") {
			zr, err := gzip.NewReader(bytes.NewReader(b))
			if err != nil {
				return fmt.Errorf("demo: %s: %w", p, err)
			}
			if b, err = io.ReadAll(zr); err != nil {
				return fmt.Errorf("demo: %s: %w", p, err)
			}
			p = strings.TrimSuffix(p, ".gz")
		}
		return e.write(path.Join("static", p), b)
	})
}

func (e *exporter) write(rel string, b []byte) error {
	p := filepath.Join(e.out, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil { //nolint:gosec // public site
		return err
	}
	return os.WriteFile(p, b, 0o644) //nolint:gosec // public site
}
