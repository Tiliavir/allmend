// Package db opens the Allmend SQLite database and applies its migrations.
//
// The SQLite driver is registered by the binary (cmd/allmend imports
// modernc.org/sqlite); this package only uses database/sql.
package db

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
)

// DriverName is the database/sql driver registered by modernc.org/sqlite.
const DriverName = "sqlite"

// Open opens (or creates) the database file at path with WAL, foreign keys
// and a busy timeout. One connection: SQLite writes are serialised anyway and
// a single connection keeps PRAGMAs and ATTACHed databases consistent.
func Open(ctx context.Context, path string) (*sql.DB, error) {
	q := url.Values{}
	for _, p := range []string{"foreign_keys(1)", "journal_mode(WAL)", "busy_timeout(5000)", "synchronous(NORMAL)"} {
		q.Add("_pragma", p)
	}
	d, err := sql.Open(DriverName, "file:"+path+"?"+q.Encode())
	if err != nil {
		return nil, fmt.Errorf("db.Open: %w", err)
	}
	d.SetMaxOpenConns(1)
	d.SetConnMaxLifetime(0)
	if err := d.PingContext(ctx); err != nil {
		_ = d.Close()
		return nil, fmt.Errorf("db.Open ping: %w", err)
	}
	return d, nil
}
