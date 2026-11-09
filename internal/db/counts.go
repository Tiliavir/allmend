package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sort"
)

// Counts returns row counts of every table in the main database and, if
// legacyPath exists, of the legacy database (keys prefixed "legacy.").
func Counts(ctx context.Context, d *sql.DB, legacyPath string) (map[string]int, error) {
	out := map[string]int{}
	if err := countSchema(ctx, d, "main", out); err != nil {
		return nil, err
	}
	if legacyPath == "" {
		return out, nil
	}
	if _, err := os.Stat(legacyPath); errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if _, err := d.ExecContext(ctx, `ATTACH DATABASE ? AS legacy_count`, "file:"+legacyPath+"?mode=ro"); err != nil {
		return nil, fmt.Errorf("db.Counts attach: %w", err)
	}
	defer func() { _, _ = d.ExecContext(context.WithoutCancel(ctx), `DETACH DATABASE legacy_count`) }()
	tmp := map[string]int{}
	if err := countSchema(ctx, d, "legacy_count", tmp); err != nil {
		return nil, err
	}
	for k, v := range tmp {
		out["legacy."+k] = v
	}
	return out, nil
}

func countSchema(ctx context.Context, d *sql.DB, schema string, out map[string]int) error {
	//nolint:gosec // schema is "main" or our own attach alias, never input
	rows, err := d.QueryContext(ctx, `SELECT name FROM `+schema+`.sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		return fmt.Errorf("db.Counts %s: %w", schema, err)
	}
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			_ = rows.Close()
			return err
		}
		names = append(names, n)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, n := range names {
		var c int
		if err := d.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+schema+`."`+n+`"`).Scan(&c); err != nil {
			return fmt.Errorf("db.Counts %s.%s: %w", schema, n, err)
		}
		out[n] = c
	}
	return nil
}

// SortedKeys returns the union of keys, sorted.
func SortedKeys(ms ...map[string]int) []string {
	set := map[string]bool{}
	for _, m := range ms {
		for k := range m {
			set[k] = true
		}
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
