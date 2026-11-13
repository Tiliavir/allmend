package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/tiliavir/allmend/internal/offline"
)

// Offline implements offline.Repository.
type Offline struct{ s *Store }

// Offline returns the repository of applied outbox requests.
func (s *Store) Offline() *Offline { return &Offline{s} }

// Get loads the request with key.
func (r *Offline) Get(ctx context.Context, key string) (offline.Request, error) {
	var q offline.Request
	var c string
	err := r.s.db.QueryRowContext(ctx, `SELECT key, account_id, route, location, created_at FROM offline_requests WHERE key = ?`, key).
		Scan(&q.Key, &q.AccountID, &q.Route, &q.Location, &c)
	if errors.Is(err, sql.ErrNoRows) {
		return q, offline.ErrNotFound
	}
	if err != nil {
		return q, fmt.Errorf("store.Offline.Get: %w", err)
	}
	q.CreatedAt, err = parseTime(c)
	return q, err
}

// Insert stores an applied request; a known key is offline.ErrInvalid.
func (r *Offline) Insert(ctx context.Context, q offline.Request) error {
	_, err := r.s.db.ExecContext(ctx, `INSERT INTO offline_requests (key, account_id, route, location, created_at) VALUES (?, ?, ?, ?, ?)`,
		q.Key, q.AccountID, q.Route, q.Location, fmtTime(q.CreatedAt))
	if err != nil && strings.Contains(err.Error(), "constraint failed") {
		return fmt.Errorf("%w: %w", offline.ErrInvalid, err)
	}
	if err != nil {
		return fmt.Errorf("store.Offline.Insert: %w", err)
	}
	return nil
}
