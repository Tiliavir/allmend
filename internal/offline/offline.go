// Package offline keeps the requests that were sent again from the offline
// outbox, so a replay never applies a change twice (ADR offline).
package offline

import (
	"context"
	"errors"
	"regexp"
	"time"
)

// Request is one outbox entry the server has applied.
type Request struct {
	Key       string // client-generated UUID (header Allmend-Vorgang)
	AccountID string
	Route     string // route pattern, e.g. "POST /velo/touren"
	Location  string // where the first answer redirected to
	CreatedAt time.Time
}

// Errors.
var (
	ErrNotFound = errors.New("offline request not found")
	ErrInvalid  = errors.New("offline request invalid")
	// ErrForeign: the key was applied for another account.
	ErrForeign = errors.New("offline request of another account")
)

// Repository stores applied requests.
type Repository interface {
	Get(ctx context.Context, key string) (Request, error)
	Insert(ctx context.Context, r Request) error
}

// Service implements the replay rules.
type Service struct {
	Repo Repository
	Now  func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

var keyPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// ValidKey reports whether k is a lowercase UUID v4 (crypto.randomUUID).
func ValidKey(k string) bool { return keyPattern.MatchString(k) }

// Applied returns the earlier request for key, ErrNotFound if there is none,
// or ErrForeign if it belongs to another account.
func (s *Service) Applied(ctx context.Context, accountID, key string) (Request, error) {
	if !ValidKey(key) {
		return Request{}, ErrInvalid
	}
	r, err := s.Repo.Get(ctx, key)
	if err != nil {
		return r, err
	}
	if r.AccountID != accountID {
		return Request{}, ErrForeign
	}
	return r, nil
}

// Record stores a request that was applied and answered with a redirect to location.
func (s *Service) Record(ctx context.Context, accountID, key, route, location string) error {
	if !ValidKey(key) || accountID == "" || route == "" || location == "" || location[0] != '/' {
		return ErrInvalid
	}
	return s.Repo.Insert(ctx, Request{Key: key, AccountID: accountID, Route: route, Location: location, CreatedAt: s.now()})
}
