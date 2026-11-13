package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tiliavir/allmend/internal/account"
	"github.com/tiliavir/allmend/internal/offline"
)

func TestOfflineRequests(t *testing.T) {
	s, _ := open(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 13, 6, 0, 0, 0, time.UTC)
	for _, id := range []string{"admin", "a1", "a2"} {
		if err := s.Accounts().Insert(ctx, account.Account{ID: id, Email: id + "@example.org", IsAdmin: id == "admin", Status: account.Active, Theme: account.ThemeAuto, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	svc := offline.Service{Repo: s.Offline(), Now: func() time.Time { return now }}
	const key = "0b8c3f9e-1d2a-4c5b-8e7f-123456789abc"
	if _, err := svc.Applied(ctx, "a1", key); !errors.Is(err, offline.ErrNotFound) {
		t.Fatalf("unknown key: %v", err)
	}
	if err := svc.Record(ctx, "a1", key, "POST /velo/touren", "/velo/touren?jahr=2026"); err != nil {
		t.Fatal(err)
	}
	got, err := svc.Applied(ctx, "a1", key)
	if err != nil || got.Location != "/velo/touren?jahr=2026" || got.Route != "POST /velo/touren" || !got.CreatedAt.Equal(now) {
		t.Fatalf("applied: %+v %v", got, err)
	}
	if _, err := svc.Applied(ctx, "a2", key); !errors.Is(err, offline.ErrForeign) {
		t.Fatalf("other account: %v", err)
	}
	if err := svc.Record(ctx, "a1", key, "POST /velo/touren", "/velo"); !errors.Is(err, offline.ErrInvalid) {
		t.Fatalf("duplicate key: %v", err)
	}
	for _, bad := range []struct{ key, loc string }{
		{"not-a-uuid", "/velo"},
		{"0B8C3F9E-1D2A-4C5B-8E7F-123456789ABC", "/velo"}, // upper case
		{"0b8c3f9e-1d2a-1c5b-8e7f-123456789abc", "/velo"}, // not v4
		{"0b8c3f9e-1d2a-4c5b-8e7f-123456789abd", "https://example.org/"},
	} {
		if err := svc.Record(ctx, "a1", bad.key, "POST /velo/touren", bad.loc); !errors.Is(err, offline.ErrInvalid) {
			t.Errorf("Record(%q, %q): %v", bad.key, bad.loc, err)
		}
	}
	// The rows go with the account (technical data, like sessions).
	if err := s.Accounts().Delete(ctx, "a1"); err != nil {
		t.Fatalf("delete account: %v", err)
	}
	if _, err := svc.Applied(ctx, "a1", key); !errors.Is(err, offline.ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
}
