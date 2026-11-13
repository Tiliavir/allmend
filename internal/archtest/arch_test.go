// Package archtest enforces the layer rules of Allmend.
package archtest

import (
	"go/build"
	"strings"
	"testing"
)

const mod = "github.com/tiliavir/allmend/"

// forbidden lists, per package, imports it must not have. Allmend packages
// are checked transitively, other packages (stdlib, modules) directly.
var forbidden = map[string][]string{
	"internal/account":    {"internal/store", "internal/webui", "database/sql", "net/http"},
	"internal/links":      {"internal/store", "internal/webui", "database/sql", "net/http"},
	"internal/pages":      {"internal/store", "internal/webui", "database/sql", "net/http"},
	"internal/markdown":   {"internal/"},
	"internal/commute":    {"internal/store", "internal/webui", "database/sql", "net/http"},
	"internal/velo":       {"internal/store", "internal/webui", "database/sql", "net/http"},
	"internal/schiffe":    {"internal/store", "internal/webui", "database/sql", "net/http"},
	"internal/ausgaben":   {"internal/store", "internal/webui", "database/sql", "net/http"},
	"internal/places":     {"internal/store", "internal/webui", "database/sql", "net/http"},
	"internal/offline":    {"internal/store", "internal/webui", "database/sql", "net/http"},
	"internal/finanzen":   {"internal/store", "internal/webui", "internal/quotes", "database/sql", "net/http"},
	"internal/quotes":     {"internal/store", "internal/webui", "database/sql"},
	"internal/session":    {"internal/store", "internal/webui", "database/sql"},
	"internal/webui":      {"internal/store", "internal/db", "internal/migrations", "database/sql", "modernc.org/sqlite"},
	"internal/store":      {"internal/webui", "net/http"},
	"internal/db":         {"modernc.org/sqlite", "internal/webui", "internal/store"},
	"internal/migrations": {"internal/store", "internal/webui"},
	"internal/env":        {"internal/"},
	"internal/demo":       {"internal/store", "internal/db", "internal/webui", "database/sql"},
}

func TestLayers(t *testing.T) {
	for pkg, deny := range forbidden {
		deps := map[string]bool{}
		collect(t, mod+pkg, deps)
		p, err := build.Import(mod+pkg, "", 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range deny {
			if !strings.HasPrefix(f, "internal/") {
				for _, d := range p.Imports {
					if d == f || strings.HasPrefix(d, f+"/") {
						t.Errorf("%s must not import %s", pkg, f)
					}
				}
				continue
			}
			target := mod + f
			for d := range deps {
				if d == target || strings.HasPrefix(d, strings.TrimSuffix(target, "/")+"/") {
					t.Errorf("%s must not import %s (via %s)", pkg, f, d)
				}
			}
		}
	}
}

func collect(t *testing.T, path string, seen map[string]bool) {
	t.Helper()
	p, err := build.Import(path, "", 0)
	if err != nil {
		t.Fatalf("import %s: %v", path, err)
	}
	for _, imp := range p.Imports {
		if seen[imp] {
			continue
		}
		seen[imp] = true
		if strings.HasPrefix(imp, mod) {
			collect(t, imp, seen)
		}
	}
}
