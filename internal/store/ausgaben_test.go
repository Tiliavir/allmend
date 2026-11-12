package store_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tiliavir/allmend/internal/account"
	"github.com/tiliavir/allmend/internal/ausgaben"
	"github.com/tiliavir/allmend/internal/store"
)

func ausgabenSetup(t *testing.T) (*store.Store, *ausgaben.Service, map[string]account.Account) {
	t.Helper()
	s, _ := open(t)
	ctx := context.Background()
	now := time.Date(2026, 3, 15, 10, 0, 0, 0, time.UTC)
	acc := map[string]account.Account{}
	for _, n := range []string{"anna", "ben", "kim", "zoe"} {
		a := account.Account{ID: n, Email: n + "@example.org", Name: n, IsAdmin: n == "anna", Status: account.Active, Theme: account.ThemeAuto, CreatedAt: now, UpdatedAt: now}
		if n != "zoe" {
			a.Modules = []account.Module{account.Ausgaben}
		}
		if err := s.Accounts().Insert(ctx, a); err != nil {
			t.Fatal(err)
		}
		acc[n] = a
	}
	svc := &ausgaben.Service{Repo: s.Ausgaben(), Accounts: s.Accounts(), Now: func() time.Time { return now }, Loc: time.UTC}
	return s, svc, acc
}

func TestAusgabenGroupsAndEntries(t *testing.T) {
	s, svc, acc := ausgabenSetup(t)
	ctx := context.Background()
	anna, ben, kim := acc["anna"], acc["ben"], acc["kim"]
	var inv *ausgaben.InvalidError
	if _, err := svc.CreateGroup(ctx, anna, "Haushalt", []string{"zoe"}); !errors.As(err, &inv) {
		t.Fatalf("member without module access: %v", err)
	}
	if _, err := svc.CreateGroup(ctx, anna, "Haushalt", nil); !errors.As(err, &inv) {
		t.Fatalf("group alone: %v", err)
	}
	g, err := svc.CreateGroup(ctx, anna, "Haushalt", []string{"ben", "anna"})
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Members) != 2 || g.Members[0] != "anna" {
		t.Fatalf("members %v", g.Members)
	}
	e, err := svc.SaveEntry(ctx, ben, ausgaben.Entry{GroupID: g.ID, Kind: ausgaben.Expense, Date: "2026-03-01", Description: "Markt", Category: "groceries",
		Amount: 1839, Currency: "EUR", PaidBy: "ben", Shares: ausgaben.Equal(g, 1839, "ben", g.Members)})
	if err != nil {
		t.Fatal(err)
	}
	// Kim is no member: sees nothing, changes nothing.
	if gs, _ := svc.Repo.Groups(ctx, kim.ID); len(gs) != 0 {
		t.Error("kim sees the group")
	}
	if _, err := svc.Open(ctx, kim, g.ID); !errors.Is(err, ausgaben.ErrNotFound) {
		t.Errorf("kim opens: %v", err)
	}
	e2 := e
	e2.Description = "Kim war hier"
	if _, err := svc.SaveEntry(ctx, kim, e2); !errors.Is(err, ausgaben.ErrNotFound) {
		t.Errorf("kim edits: %v", err)
	}
	if err := s.Ausgaben().UpdateEntry(ctx, kim.ID, e2, e.Version); !errors.Is(err, ausgaben.ErrNotFound) {
		t.Errorf("kim edits via repo: %v", err)
	}
	if err := svc.DeleteEntry(ctx, kim, g.ID, e.ID, e.Version); !errors.Is(err, ausgaben.ErrNotFound) {
		t.Errorf("kim deletes: %v", err)
	}
	// Anna edits; a stale version conflicts.
	e2.Description = "Wochenmarkt"
	saved, err := svc.SaveEntry(ctx, anna, e2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SaveEntry(ctx, ben, e2); !errors.Is(err, ausgaben.ErrConflict) {
		t.Errorf("stale save: %v", err)
	}
	v, err := svc.Open(ctx, anna, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Entries) != 1 || v.Entries[0].Description != "Wochenmarkt" || v.Entries[0].ShareOf("anna") != 920 || v.Entries[0].UpdatedBy != "anna" {
		t.Fatalf("entries %+v", v.Entries)
	}
	if len(v.Transfers) != 1 || v.Transfers[0] != (ausgaben.Transfer{From: "anna", To: "ben", Amount: 920, Currency: "EUR"}) {
		t.Fatalf("transfers %+v", v.Transfers)
	}
	// The import refuses a group with entries.
	if _, err := svc.Import(ctx, anna, g.ID, strings.NewReader(swExport), map[string]string{"A": "anna", "B": "ben"}); !errors.Is(err, ausgaben.ErrNotEmpty) {
		t.Errorf("import into used group: %v", err)
	}
	// Deleting keeps the row but hides it.
	if err := svc.DeleteEntry(ctx, ben, g.ID, saved.ID, saved.Version); err != nil {
		t.Fatal(err)
	}
	if v, _ := svc.Open(ctx, anna, g.ID); len(v.Entries) != 0 || len(v.Balances) != 0 {
		t.Fatalf("deleted entry still shown: %+v", v.Entries)
	}
	var by string
	if err := s.DB().QueryRowContext(ctx, `SELECT deleted_by FROM expense_entries WHERE id = ?`, saved.ID).Scan(&by); err != nil || by != "ben" {
		t.Fatalf("soft delete: %q %v", by, err)
	}
	// Now empty again: the import works.
	n, err := svc.Import(ctx, anna, g.ID, strings.NewReader(swExport), map[string]string{"A": "anna", "B": "ben"})
	if err != nil || n != 2 {
		t.Fatalf("import: %d %v", n, err)
	}
	// The database refuses a share for someone outside the group.
	bad := ausgaben.Entry{ID: "x", GroupID: g.ID, Kind: ausgaben.Expense, Date: "2026-03-02", Description: "x", Amount: 100, Currency: "EUR", PaidBy: "anna",
		Shares: []ausgaben.Share{{AccountID: "kim", Amount: 100}}, Version: 1, CreatedBy: "anna", UpdatedBy: "anna"}
	if err := s.Ausgaben().InsertEntries(ctx, anna.ID, []ausgaben.Entry{bad}); err == nil {
		t.Error("share outside the group stored")
	}
}

const swExport = "Date,Description,Category,Cost,Currency,A,B\n\n" +
	"2026-01-02,Brot,Groceries,4.00,EUR,2.00,-2.00\n" +
	"2026-01-03,Abo,Music,4.99,EUR,0.00,0.00\n\n" +
	"2026-03-01,Total balance, , ,EUR,2.00,-2.00\n"

func TestAusgabenRecurringBooking(t *testing.T) {
	_, svc, acc := ausgabenSetup(t)
	ctx := context.Background()
	anna := acc["anna"]
	g, err := svc.CreateGroup(ctx, anna, "Fixkosten", []string{"ben"})
	if err != nil {
		t.Fatal(err)
	}
	r := ausgaben.Recurring{GroupID: g.ID, Description: "Strom", Category: "electricity", Amount: 9000, Currency: "EUR", PaidBy: "anna",
		Shares: ausgaben.Equal(g, 9000, "anna", g.Members), Day: 31, Next: "2026-01-31"}
	if r, err = svc.SaveRecurring(ctx, anna, r); err != nil {
		t.Fatal(err)
	}
	stale := r
	// Today is 2026-03-15: January and February are due.
	v, err := svc.Open(ctx, acc["ben"], g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Entries) != 2 || v.Entries[0].Date != "2026-02-28" || v.Entries[1].Date != "2026-01-31" || v.Entries[0].RecurringID != r.ID {
		t.Fatalf("booked %+v", v.Entries)
	}
	if len(v.Recurrings) != 1 || v.Recurrings[0].Next != "2026-03-31" {
		t.Fatalf("next %+v", v.Recurrings)
	}
	// Opening again books nothing twice.
	if v, _ = svc.Open(ctx, anna, g.ID); len(v.Entries) != 2 {
		t.Fatalf("double booking: %d", len(v.Entries))
	}
	// Editing with the version from before the booking conflicts.
	stale.Amount, stale.Shares = 100, ausgaben.Equal(g, 100, "anna", g.Members)
	if _, err := svc.SaveRecurring(ctx, anna, stale); !errors.Is(err, ausgaben.ErrConflict) {
		t.Errorf("stale recurring: %v", err)
	}
	cur := v.Recurrings[0]
	if err := svc.DeleteRecurring(ctx, anna, g.ID, cur.ID, cur.Version); err != nil {
		t.Fatal(err)
	}
	v, _ = svc.Open(ctx, anna, g.ID)
	if len(v.Recurrings) != 0 || len(v.Entries) != 2 || v.Entries[0].RecurringID != "" {
		t.Fatalf("after end: %+v %+v", v.Recurrings, v.Entries)
	}
	// Start dates far away are refused.
	r.ID, r.Next = "", "2024-01-31"
	var inv *ausgaben.InvalidError
	if _, err := svc.SaveRecurring(ctx, anna, r); !errors.As(err, &inv) {
		t.Errorf("old start: %v", err)
	}
}
