// Package store implements the module repositories on SQLite (database/sql).
// Only cmd/allmend and tests may import it.
package store

import (
	"database/sql"
	"fmt"
	"time"
)

// Store bundles the repositories over one database.
type Store struct {
	db *sql.DB
}

// New wraps an open database (see internal/db.Open).
func New(d *sql.DB) *Store { return &Store{db: d} }

// DB returns the underlying database.
func (s *Store) DB() *sql.DB { return s.db }

const ts = time.RFC3339Nano

func fmtTime(t time.Time) string { return t.UTC().Format(ts) }

func parseTime(s string) (time.Time, error) {
	t, err := time.Parse(ts, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("store: bad timestamp %q: %w", s, err)
	}
	return t, nil
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
