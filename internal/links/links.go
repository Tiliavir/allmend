// Package links manages the links to other home-network services shown on
// the start page.
package links

import (
	"context"
	"errors"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/tiliavir/allmend/internal/id"
)

// Link points to another application.
type Link struct {
	ID          string
	Name        string
	URL         string
	Description string
	Icon        string
	Public      bool // shown without login
	Position    int
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Icons offered for links.
var Icons = []string{"link", "check", "cal", "pdf", "book", "server", "home", "page", "chart"}

// Errors.
var (
	ErrNotFound = errors.New("link not found")
	ErrInvalid  = errors.New("link invalid")
)

// Repository stores links.
type Repository interface {
	List(ctx context.Context) ([]Link, error) // ordered by Position
	Get(ctx context.Context, id string) (Link, error)
	Insert(ctx context.Context, l Link) error
	Update(ctx context.Context, l Link) error
	Delete(ctx context.Context, id string) error
	// Reorder sets Position = index for the given ids.
	Reorder(ctx context.Context, ids []string) error
}

// Service implements link use cases. Admin checks are done by the caller.
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

// Visible returns the links for the start page.
func (s *Service) Visible(ctx context.Context, loggedIn bool) ([]Link, error) {
	all, err := s.Repo.List(ctx)
	if err != nil || loggedIn {
		return all, err
	}
	return slices.DeleteFunc(all, func(l Link) bool { return !l.Public }), nil
}

// Validate checks and normalises user input.
func Validate(l Link) (Link, error) {
	l.Name, l.Description = strings.TrimSpace(l.Name), strings.TrimSpace(l.Description)
	u, err := url.Parse(strings.TrimSpace(l.URL))
	var errs []string
	if l.Name == "" || len([]rune(l.Name)) > 60 {
		errs = append(errs, "Name fehlt oder ist zu lang")
	}
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
		errs = append(errs, "URL muss mit http:// oder https:// beginnen")
	} else {
		l.URL = u.String()
	}
	if len([]rune(l.Description)) > 120 {
		errs = append(errs, "Beschreibung ist zu lang")
	}
	if !slices.Contains(Icons, l.Icon) {
		errs = append(errs, "Unbekanntes Icon")
	}
	if len(errs) > 0 {
		return l, &InvalidError{Problems: errs}
	}
	return l, nil
}

// InvalidError lists what is wrong with a link.
type InvalidError struct{ Problems []string }

func (e *InvalidError) Error() string { return "link invalid: " + strings.Join(e.Problems, "; ") }

// Unwrap makes errors.Is(err, ErrInvalid) work.
func (e *InvalidError) Unwrap() error { return ErrInvalid }

// Create adds a link at the end.
func (s *Service) Create(ctx context.Context, l Link) (Link, error) {
	l, err := Validate(l)
	if err != nil {
		return l, err
	}
	all, err := s.Repo.List(ctx)
	if err != nil {
		return l, err
	}
	now := s.now()
	l.ID, l.Position, l.CreatedAt, l.UpdatedAt = id.New(), len(all), now, now
	return l, s.Repo.Insert(ctx, l)
}

// Save updates a link's fields (not its position).
func (s *Service) Save(ctx context.Context, l Link) (Link, error) {
	old, err := s.Repo.Get(ctx, l.ID)
	if err != nil {
		return l, err
	}
	l, err = Validate(l)
	if err != nil {
		return l, err
	}
	l.Position, l.CreatedAt, l.UpdatedAt = old.Position, old.CreatedAt, s.now()
	return l, s.Repo.Update(ctx, l)
}

// Delete removes a link.
func (s *Service) Delete(ctx context.Context, linkID string) error { return s.Repo.Delete(ctx, linkID) }

// Move shifts a link one place up (delta -1) or down (+1).
func (s *Service) Move(ctx context.Context, linkID string, delta int) error {
	all, err := s.Repo.List(ctx)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(all, func(l Link) bool { return l.ID == linkID })
	if i < 0 {
		return ErrNotFound
	}
	j := i + delta
	if j < 0 || j >= len(all) {
		return nil
	}
	all[i], all[j] = all[j], all[i]
	ids := make([]string, len(all))
	for k, l := range all {
		ids[k] = l.ID
	}
	return s.Repo.Reorder(ctx, ids)
}
