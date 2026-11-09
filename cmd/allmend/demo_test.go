package main

import (
	"context"
	"html"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestDemo builds the demo site and checks that every link and asset in it
// resolves to an exported file, as it must on GitHub Pages.
func TestDemo(t *testing.T) {
	out := filepath.Join(t.TempDir(), "site")
	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	n, err := buildDemo(context.Background(), out, "/demo/", now)
	if err != nil {
		t.Fatal(err)
	}
	if n < 50 {
		t.Errorf("only %d pages exported", n)
	}
	for _, f := range []string{"index.html", "404.html", "velo/index.html", "seiten/heimnetz/index.html", "static/demo.js", "static/vendor/mermaid-12.3.0.min.js"} {
		if _, err := os.Stat(filepath.Join(out, f)); err != nil {
			t.Error(err)
		}
	}
	ref := regexp.MustCompile(`\b(href|src)="(/[^"]*)"`)
	checked := 0
	err = filepath.WalkDir(out, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".html") {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		body := string(b)
		if !strings.Contains(body, `class="demo-banner"`) {
			t.Errorf("%s: no demo banner", p)
		}
		for _, m := range ref.FindAllStringSubmatch(body, -1) {
			u, _, _ := strings.Cut(html.UnescapeString(m[2]), "#")
			rel, ok := strings.CutPrefix(u, "/demo/")
			if !ok {
				t.Errorf("%s: %s outside the base path", p, u)
				continue
			}
			target := filepath.Join(out, filepath.FromSlash(rel))
			if rel == "" || strings.HasSuffix(rel, "/") {
				target = filepath.Join(target, "index.html")
			}
			if _, err := os.Stat(target); err != nil {
				t.Errorf("%s: broken link %s", p, u)
			}
			checked++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 1000 {
		t.Errorf("only %d references checked", checked)
	}
	if _, err := buildDemo(context.Background(), out, "/demo/", now); err == nil {
		t.Error("export into a non-empty directory must fail")
	}
}
