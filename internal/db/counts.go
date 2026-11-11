package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
)

// Counts returns the row count of every table in the database.
func Counts(ctx context.Context, d *sql.DB) (map[string]int, error) {
	rows, err := d.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		return nil, fmt.Errorf("db.Counts: %w", err)
	}
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			_ = rows.Close()
			return nil, err
		}
		names = append(names, n)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	out := map[string]int{}
	for _, n := range names {
		var c int
		if err := d.QueryRowContext(ctx, `SELECT COUNT(*) FROM "`+n+`"`).Scan(&c); err != nil {
			return nil, fmt.Errorf("db.Counts %s: %w", n, err)
		}
		out[n] = c
	}
	return out, nil
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
