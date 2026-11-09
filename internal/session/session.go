// Package session manages server-side login sessions and pending Google
// logins. The cookie holds a random token; the database holds its SHA-256.
package session

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/tiliavir/allmend/internal/id"
)

// Lifetimes.
const (
	TTL     = 30 * 24 * time.Hour // sliding
	FlowTTL = 10 * time.Minute
	Cookie  = "allmend_session"
)

// ErrNoSession means no valid session (or flow) exists.
var ErrNoSession = errors.New("no session")

// Flow is a pending Google login (state, nonce and PKCE verifier).
type Flow struct {
	State, Nonce, Verifier string
	Created                time.Time
}

// Repository stores sessions and flows.
type Repository interface {
	Create(ctx context.Context, tokenHash, accountID string, now, expires time.Time) error
	Lookup(ctx context.Context, tokenHash string, now, expires time.Time) (string, error)
	Delete(ctx context.Context, tokenHash string) error
	DeleteAccount(ctx context.Context, accountID string) error
	DeleteExpired(ctx context.Context, now time.Time) error
	SaveFlow(ctx context.Context, f Flow) error
	TakeFlow(ctx context.Context, state string) (Flow, error)
}

// Manager issues and checks session cookies.
type Manager struct {
	Repo   Repository
	Secure bool
	Now    func() time.Time
}

func (m *Manager) now() time.Time {
	if m.Now != nil {
		return m.Now().UTC()
	}
	return time.Now().UTC()
}

// Start creates a session for accountID and sets the cookie.
func (m *Manager) Start(ctx context.Context, w http.ResponseWriter, accountID string) error {
	tok := id.Secret()
	now := m.now()
	if err := m.Repo.Create(ctx, id.Hash(tok), accountID, now, now.Add(TTL)); err != nil {
		return err
	}
	//nolint:gosec // Secure is config-driven (false only for plain-HTTP development)
	http.SetCookie(w, &http.Cookie{
		Name: Cookie, Value: tok, Path: "/", HttpOnly: true, Secure: m.Secure,
		SameSite: http.SameSiteLaxMode, MaxAge: int(TTL.Seconds()),
	})
	return nil
}

// Account returns the account id of the request's session.
func (m *Manager) Account(r *http.Request) (string, error) {
	c, err := r.Cookie(Cookie)
	if err != nil || c.Value == "" {
		return "", ErrNoSession
	}
	now := m.now()
	return m.Repo.Lookup(r.Context(), id.Hash(c.Value), now, now.Add(TTL))
}

// End deletes the request's session and clears the cookie.
func (m *Manager) End(w http.ResponseWriter, r *http.Request) error {
	//nolint:gosec // Secure is config-driven (false only for plain-HTTP development)
	http.SetCookie(w, &http.Cookie{Name: Cookie, Value: "", Path: "/", HttpOnly: true, Secure: m.Secure, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	c, err := r.Cookie(Cookie)
	if err != nil || c.Value == "" {
		return nil
	}
	return m.Repo.Delete(r.Context(), id.Hash(c.Value))
}

// NewFlow creates and stores a pending Google login.
func (m *Manager) NewFlow(ctx context.Context) (Flow, error) {
	f := Flow{State: id.Secret(), Nonce: id.Secret(), Verifier: id.Secret(), Created: m.now()}
	return f, m.Repo.SaveFlow(ctx, f)
}

// TakeFlow returns and consumes the flow for state if it is still fresh.
func (m *Manager) TakeFlow(ctx context.Context, state string) (Flow, error) {
	if state == "" {
		return Flow{}, ErrNoSession
	}
	f, err := m.Repo.TakeFlow(ctx, state)
	if err != nil {
		return f, err
	}
	if m.now().Sub(f.Created) > FlowTTL {
		return Flow{}, ErrNoSession
	}
	return f, nil
}
