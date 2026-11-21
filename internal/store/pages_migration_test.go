package store_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/tiliavir/allmend/internal/db"
	"github.com/tiliavir/allmend/internal/migrations"
)

// v15Setup migrates to v14 and stores pages; each page is
// "id parent owner read_all write_all readers writers" with "-" for empty and
// comma-separated lists.
func v15Setup(t *testing.T, pages ...string) (*sql.DB, func() error) {
	t.Helper()
	dir := t.TempDir()
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(dir, "allmend.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	opt := db.Options{BackupDir: filepath.Join(dir, "backup")}
	all := migrations.All()
	if _, err := db.Migrate(ctx, d, all[:14], opt); err != nil {
		t.Fatal(err)
	}
	now := "2026-01-01T00:00:00Z"
	for _, a := range []string{"anna", "ben", "mia"} {
		if _, err := d.ExecContext(ctx, `INSERT INTO accounts (id, email, status, created_at, updated_at) VALUES (?, ?, 'active', ?, ?)`, a, a+"@example.org", now, now); err != nil {
			t.Fatal(err)
		}
	}
	list := func(s string) []string {
		if s == "-" {
			return nil
		}
		return strings.Split(s, ",")
	}
	for i, spec := range pages {
		f := strings.Fields(spec)
		var parent any
		if f[1] != "-" {
			parent = f[1]
		}
		if _, err := d.ExecContext(ctx, `INSERT INTO pages (id, slug, parent_id, position, title, body, version, owner_id, read_all, write_all, created_at, updated_at, updated_by)
VALUES (?, ?, ?, ?, ?, '', 1, ?, ?, ?, ?, ?, ?)`, f[0], f[0], parent, i, f[0], f[2], f[3], f[4], now, now, f[2]); err != nil {
			t.Fatal(err)
		}
		for kind, ids := range map[string][]string{"read": list(f[5]), "write": list(f[6])} {
			for _, id := range ids {
				if _, err := d.ExecContext(ctx, `INSERT INTO page_access (page_id, account_id, kind) VALUES (?, ?, ?)`, f[0], id, kind); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	runs := 0
	return d, func() error {
		runs++ // one backup per run, also within the same second
		opt.BackupDir = filepath.Join(dir, "backup", strconv.Itoa(runs))
		_, err := db.Migrate(ctx, d, all, opt)
		return err
	}
}

func rights(t *testing.T, d *sql.DB) []string {
	t.Helper()
	return strs(t, d, `SELECT p.id || ' ' || p.read_all || p.write_all || ' r:' || COALESCE((SELECT group_concat(account_id) FROM (SELECT account_id FROM page_access WHERE page_id = p.id AND kind = 'read' ORDER BY 1)), '')
 || ' w:' || COALESCE((SELECT group_concat(account_id) FROM (SELECT account_id FROM page_access WHERE page_id = p.id AND kind = 'write' ORDER BY 1)), '') FROM pages p ORDER BY p.id`)
}

// TestPageRightsMigration: subpages readable by more than their parent are
// narrowed top down; fitting pages stay untouched.
func TestPageRightsMigration(t *testing.T) {
	t.Parallel()
	d, migrate := v15Setup(t,
		"a1 - anna 0 0 ben -",       // Geschenke: anna, ben
		"a2 a1 ben 1 1 - -",         // public below it: everyone → anna (edit), ben owns
		"a3 a2 mia 1 0 - -",         // mia's page below: mia not in a1 → would lock out
		"b1 - anna 1 0 - -",         // public read
		"b2 b1 ben 1 1 - -",         // fits
		"c1 - anna 0 0 ben,mia ben", // anna, ben, mia
		"c2 c1 ben 0 0 anna,mia mia",
		"c3 c2 anna 1 0 - ben", // read all → ben, mia; ben edits
	)
	err := migrate()
	if err == nil || !strings.Contains(err.Error(), "[a3]") {
		t.Fatalf("want abort naming a3, got %v", err)
	}
	before := rights(t, d)
	if !slices.Contains(before, "a2 11 r: w:") {
		t.Fatalf("data changed despite the abort: %v", before)
	}
	if _, err := d.Exec(`UPDATE pages SET parent_id = NULL WHERE id = 'a3'`); err != nil {
		t.Fatal(err)
	}
	if err := migrate(); err != nil {
		t.Fatal(err)
	}
	got := rights(t, d)
	want := []string{
		"a1 00 r:ben w:",
		"a2 00 r: w:anna",
		"a3 10 r: w:",
		"b1 10 r: w:",
		"b2 11 r: w:",
		"c1 00 r:ben,mia w:ben",
		"c2 00 r:anna,mia w:mia",
		"c3 00 r:ben,mia w:ben",
	}
	if !slices.Equal(got, want) {
		t.Errorf("rights\n got %v\nwant %v\n(before %v)", got, want, before)
	}
}
