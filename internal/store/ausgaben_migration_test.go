package store_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tiliavir/allmend/internal/ausgaben"
	"github.com/tiliavir/allmend/internal/db"
	"github.com/tiliavir/allmend/internal/migrations"
)

// v12Setup migrates to v11 and stores expenses with the given categories.
// It returns the database and a function that runs the remaining steps.
func v12Setup(t *testing.T, cats ...string) (*sql.DB, func() error) {
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
	if _, err := db.Migrate(ctx, d, all[:11], opt); err != nil {
		t.Fatal(err)
	}
	now := "2026-01-01T00:00:00Z"
	stmts := []string{
		`INSERT INTO accounts (id, email, status, created_at, updated_at) VALUES ('a','a@example.org','active','` + now + `','` + now + `')`,
		`INSERT INTO expense_groups (id, name, created_by, created_at, updated_at) VALUES ('g','G','a','` + now + `','` + now + `')`,
		`INSERT INTO expense_members (group_id, account_id, position) VALUES ('g','a',0)`,
		`INSERT INTO expense_recurring (id, group_id, description, category, amount, currency, paid_by, day, next_date, version, created_by, created_at, updated_at)
		 VALUES ('r','g','Strom','Strom',100,'EUR','a',1,'2026-02-01',1,'a','` + now + `','` + now + `')`,
		`INSERT INTO expense_entries (id, group_id, kind, date, description, category, amount, currency, paid_by, version, created_by, created_at, updated_by, updated_at)
		 VALUES ('p','g','zahlung','2026-01-02','','',100,'EUR','a',1,'a','` + now + `','a','` + now + `')`,
	}
	for i, c := range cats {
		id := string(rune('A' + i))
		stmts = append(stmts, `INSERT INTO expense_entries (id, group_id, kind, date, description, category, amount, currency, paid_by, version, created_by, created_at, updated_by, updated_at)
		 VALUES ('`+id+`','g','ausgabe','2026-01-02','x','`+c+`',100,'EUR',NULL,1,'a','`+now+`','a','`+now+`')`)
	}
	for _, s := range stmts {
		if _, err := d.ExecContext(ctx, s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	return d, func() error { _, err := db.Migrate(ctx, d, all, opt); return err }
}

func strs(t *testing.T, d *sql.DB, q string) []string {
	t.Helper()
	rows, err := d.QueryContext(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	return out
}

// TestExpenseCategoriesMigration: old free-text categories are mapped to the
// fixed keys; the database refuses unknown ones afterwards.
func TestExpenseCategoriesMigration(t *testing.T) {
	d, migrate := v12Setup(t, "Groceries", "Entertainment", "lebensmittel", "", "Medical expenses", " Wohnen ")
	if err := migrate(); err != nil {
		t.Fatal(err)
	}
	got := strs(t, d, `SELECT category FROM expense_entries WHERE kind = 'ausgabe' ORDER BY id`)
	want := []string{"groceries", "entertainment_other", "groceries", "general", "medical", "home_other"}
	if !slices.Equal(got, want) {
		t.Errorf("entries %v, want %v", got, want)
	}
	if got := strs(t, d, `SELECT category FROM expense_recurring`); !slices.Equal(got, []string{"electricity"}) {
		t.Errorf("recurring %v", got)
	}
	if got := strs(t, d, `SELECT category FROM expense_entries WHERE kind = 'zahlung'`); !slices.Equal(got, []string{""}) {
		t.Errorf("payment %v", got)
	}
	// The table matches the catalog of the domain.
	var keys []string
	for _, m := range ausgaben.Mains {
		for _, s := range m.Subs {
			keys = append(keys, s.Key+"/"+m.Key)
		}
	}
	slices.Sort(keys)
	if got := strs(t, d, `SELECT key || '/' || main FROM expense_categories ORDER BY 1`); !slices.Equal(got, keys) {
		t.Errorf("expense_categories %v\nwant %v", got, keys)
	}
	for name, stmt := range map[string]string{
		"free text":   `UPDATE expense_entries SET category = 'Essen' WHERE id = 'A'`,
		"empty":       `UPDATE expense_entries SET category = '' WHERE id = 'A'`,
		"payment cat": `UPDATE expense_entries SET category = 'groceries' WHERE id = 'p'`,
		"recurring":   `UPDATE expense_recurring SET category = 'Strom'`,
	} {
		if _, err := d.Exec(stmt); err == nil || !strings.Contains(err.Error(), "invalid category") {
			t.Errorf("%s: not refused (%v)", name, err)
		}
	}
	if _, err := d.Exec(`UPDATE expense_entries SET category = 'rent' WHERE id = 'A'`); err != nil {
		t.Errorf("valid key refused: %v", err)
	}
}

// TestExpenseCategoriesMigrationAborts: unmapped values abort and are named.
func TestExpenseCategoriesMigrationAborts(t *testing.T) {
	d, migrate := v12Setup(t, "Groceries", "Znüni", "Hobby")
	err := migrate()
	if err == nil || !strings.Contains(err.Error(), `["Hobby" "Znüni"]`) {
		t.Fatalf("want abort naming the values, got %v", err)
	}
	if got := strs(t, d, `SELECT category FROM expense_entries WHERE kind = 'ausgabe' ORDER BY id`); !slices.Equal(got, []string{"Groceries", "Znüni", "Hobby"}) {
		t.Errorf("data changed despite the abort: %v", got)
	}
}
