package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/tiliavir/allmend/internal/session"
)

// Sessions implements session.Repository.
type Sessions struct{ s *Store }

// Sessions returns the session repository.
func (s *Store) Sessions() *Sessions { return &Sessions{s} }

// Create stores a session.
func (r *Sessions) Create(ctx context.Context, tokenHash, accountID string, now, expires time.Time) error {
	_, err := r.s.db.ExecContext(ctx, `INSERT INTO sessions (token_hash, account_id, created_at, expires_at) VALUES (?,?,?,?)`,
		tokenHash, accountID, fmtTime(now), fmtTime(expires))
	if err != nil {
		return fmt.Errorf("store.Sessions.Create: %w", err)
	}
	return nil
}

// Lookup returns the account id of a valid session and extends it to expires.
func (r *Sessions) Lookup(ctx context.Context, tokenHash string, now, expires time.Time) (string, error) {
	var acc, exp string
	err := r.s.db.QueryRowContext(ctx, `SELECT account_id, expires_at FROM sessions WHERE token_hash = ?`, tokenHash).Scan(&acc, &exp)
	if errors.Is(err, sql.ErrNoRows) {
		return "", session.ErrNoSession
	}
	if err != nil {
		return "", fmt.Errorf("store.Sessions.Lookup: %w", err)
	}
	e, err := parseTime(exp)
	if err != nil {
		return "", err
	}
	if !now.Before(e) {
		_, _ = r.s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
		return "", session.ErrNoSession
	}
	// Extend at most once an hour to keep writes rare.
	if e.Sub(now) < expires.Sub(now)-time.Hour {
		if _, err := r.s.db.ExecContext(ctx, `UPDATE sessions SET expires_at = ? WHERE token_hash = ?`, fmtTime(expires), tokenHash); err != nil {
			return "", fmt.Errorf("store.Sessions.Lookup extend: %w", err)
		}
	}
	return acc, nil
}

// Delete removes one session.
func (r *Sessions) Delete(ctx context.Context, tokenHash string) error {
	_, err := r.s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
	return err
}

// DeleteAccount removes all sessions of an account.
func (r *Sessions) DeleteAccount(ctx context.Context, accountID string) error {
	_, err := r.s.db.ExecContext(ctx, `DELETE FROM sessions WHERE account_id = ?`, accountID)
	return err
}

// DeleteExpired removes expired sessions and stale login flows.
func (r *Sessions) DeleteExpired(ctx context.Context, now time.Time) error {
	if _, err := r.s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, fmtTime(now)); err != nil {
		return err
	}
	_, err := r.s.db.ExecContext(ctx, `DELETE FROM login_flows WHERE created_at <= ?`, fmtTime(now.Add(-session.FlowTTL)))
	return err
}

// SaveFlow stores a pending OIDC login.
func (r *Sessions) SaveFlow(ctx context.Context, f session.Flow) error {
	_, err := r.s.db.ExecContext(ctx, `INSERT INTO login_flows (state, nonce, code_verifier, created_at) VALUES (?,?,?,?)`,
		f.State, f.Nonce, f.Verifier, fmtTime(f.Created))
	return err
}

// TakeFlow loads and deletes a pending OIDC login (single use).
func (r *Sessions) TakeFlow(ctx context.Context, state string) (session.Flow, error) {
	var f session.Flow
	var created string
	err := r.s.db.QueryRowContext(ctx, `DELETE FROM login_flows WHERE state = ? RETURNING state, nonce, code_verifier, created_at`, state).
		Scan(&f.State, &f.Nonce, &f.Verifier, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return f, session.ErrNoSession
	}
	if err != nil {
		return f, fmt.Errorf("store.Sessions.TakeFlow: %w", err)
	}
	f.Created, err = parseTime(created)
	return f, err
}
