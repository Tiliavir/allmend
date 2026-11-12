package ausgaben

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/tiliavir/allmend/internal/account"
	"github.com/tiliavir/allmend/internal/id"
)

// Repository stores groups, entries and recurring expenses. Every method
// takes the acting account (member) and only finds or changes data of groups
// that account belongs to (ErrNotFound otherwise).
type Repository interface {
	Groups(ctx context.Context, member string) ([]Group, error) // by name
	Group(ctx context.Context, member, groupID string) (Group, error)
	InsertGroup(ctx context.Context, g Group) error
	// UpdateGroup renames a group of member and appends add to its members.
	UpdateGroup(ctx context.Context, member, groupID, name string, add []string, at time.Time) error

	// Entries lists the group's entries that are not deleted, newest first.
	Entries(ctx context.Context, member, groupID string) ([]Entry, error)
	Entry(ctx context.Context, member, groupID, entryID string) (Entry, error)
	// InsertEntries stores all entries or none.
	InsertEntries(ctx context.Context, member string, es []Entry) error
	// ImportEntries stores es into a group without entries (ErrNotEmpty).
	ImportEntries(ctx context.Context, member, groupID string, es []Entry) error
	UpdateEntry(ctx context.Context, member string, e Entry, want int) error
	DeleteEntry(ctx context.Context, member, groupID, entryID string, want int, at time.Time) error

	Recurrings(ctx context.Context, member, groupID string) ([]Recurring, error) // by next date
	Recurring(ctx context.Context, member, groupID, id string) (Recurring, error)
	InsertRecurring(ctx context.Context, member string, r Recurring) error
	UpdateRecurring(ctx context.Context, member string, r Recurring, want int) error
	DeleteRecurring(ctx context.Context, member, groupID, id string, want int) error
	// Book stores es and r (its new Next and Version) if r is still at want.
	Book(ctx context.Context, member string, r Recurring, want int, es []Entry) error
}

// Service implements the use cases for the acting account. Module access
// is checked by the caller; group membership here and in the repository.
type Service struct {
	Repo     Repository
	Accounts interface {
		Get(ctx context.Context, id string) (account.Account, error)
	}
	Now func() time.Time
	Loc *time.Location // for "today"
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// Today is the local date (YYYY-MM-DD).
func (s *Service) Today() string {
	loc := s.Loc
	if loc == nil {
		loc = time.UTC
	}
	return s.now().In(loc).Format(time.DateOnly)
}

// CreateGroup creates a group of a and others. Everyone must be active and
// have access to the module.
func (s *Service) CreateGroup(ctx context.Context, a account.Account, name string, members []string) (Group, error) {
	var p Problems
	checkGroupName(&p, name)
	all := []string{a.ID}
	for _, m := range members {
		if !slices.Contains(all, m) {
			all = append(all, m)
		}
	}
	p.add(len(all) < MinMembers, "Mindestens eine weitere Person wählen")
	p.add(len(all) > MaxMembers, "Zu viele Personen")
	if err := s.checkCandidates(ctx, &p, all[1:]); err != nil {
		return Group{}, err
	}
	if err := p.Err(); err != nil {
		return Group{}, err
	}
	now := s.now()
	g := Group{ID: id.New(), Name: name, Members: all, CreatedBy: a.ID, CreatedAt: now, UpdatedAt: now}
	return g, s.Repo.InsertGroup(ctx, g)
}

// EditGroup renames g and adds members; nobody is ever removed. New
// members must be active and have access to the module.
func (s *Service) EditGroup(ctx context.Context, a account.Account, groupID, name string, add []string) (Group, error) {
	g, err := s.Repo.Group(ctx, a.ID, groupID)
	if err != nil {
		return g, err
	}
	var p Problems
	checkGroupName(&p, name)
	var fresh []string
	for _, m := range add {
		p.add(g.IsMember(m), "Diese Person ist schon dabei")
		p.add(slices.Contains(fresh, m), "Eine Person zweimal gewählt")
		fresh = append(fresh, m)
	}
	p.add(len(g.Members)+len(fresh) > MaxMembers, "Zu viele Personen")
	if err := s.checkCandidates(ctx, &p, fresh); err != nil {
		return g, err
	}
	if err := p.Err(); err != nil {
		return g, err
	}
	if err := s.Repo.UpdateGroup(ctx, a.ID, g.ID, name, fresh, s.now()); err != nil {
		return g, err
	}
	g.Name, g.Members = name, append(g.Members, fresh...)
	return g, nil
}

// checkCandidates checks that ids are active accounts with module access.
func (s *Service) checkCandidates(ctx context.Context, p *Problems, ids []string) error {
	for _, m := range ids {
		o, err := s.Accounts.Get(ctx, m)
		if errors.Is(err, account.ErrNotFound) {
			p.add(true, "Unbekannte Person")
			continue
		}
		if err != nil {
			return err
		}
		p.add(o.Status != account.Active || !o.Can(account.Ausgaben), o.DisplayName()+" hat keinen Zugriff auf Ausgaben")
	}
	return nil
}

// View is one group with everything the group page shows.
type View struct {
	Group      Group
	Entries    []Entry
	Recurrings []Recurring
	Balances   []Balance
	Transfers  []Transfer
}

// Open books due recurring expenses and loads the group of a.
func (s *Service) Open(ctx context.Context, a account.Account, groupID string) (View, error) {
	g, err := s.Repo.Group(ctx, a.ID, groupID)
	if err != nil {
		return View{}, err
	}
	if err := s.bookDue(ctx, a, g); err != nil {
		return View{}, err
	}
	v := View{Group: g}
	if v.Entries, err = s.Repo.Entries(ctx, a.ID, g.ID); err != nil {
		return v, err
	}
	if v.Recurrings, err = s.Repo.Recurrings(ctx, a.ID, g.ID); err != nil {
		return v, err
	}
	v.Balances = Balances(g, v.Entries)
	for _, b := range v.Balances {
		v.Transfers = append(v.Transfers, Settle(b)...)
	}
	return v, nil
}

// bookDue creates the entries of every recurring expense due up to today.
// A concurrent booking of the same expense wins; this one is skipped.
func (s *Service) bookDue(ctx context.Context, a account.Account, g Group) error {
	rs, err := s.Repo.Recurrings(ctx, a.ID, g.ID)
	if err != nil {
		return err
	}
	today := s.Today()
	for _, r := range rs {
		dates, next := Due(r, today)
		if len(dates) == 0 {
			continue
		}
		now := s.now()
		es := make([]Entry, 0, len(dates))
		for _, d := range dates {
			es = append(es, Entry{
				ID: id.New(), GroupID: g.ID, Kind: Expense, Date: d, Description: r.Description, Category: r.Category,
				Amount: r.Amount, Currency: r.Currency, PaidBy: r.PaidBy, Shares: slices.Clone(r.Shares), RecurringID: r.ID,
				Version: 1, CreatedBy: r.CreatedBy, UpdatedBy: r.CreatedBy, CreatedAt: now, UpdatedAt: now,
			})
		}
		want := r.Version
		r.Next, r.Version, r.UpdatedAt = next, want+1, now
		if err := s.Repo.Book(ctx, a.ID, r, want, es); err != nil && !errors.Is(err, ErrConflict) {
			return fmt.Errorf("ausgaben: book %s: %w", r.ID, err)
		}
	}
	return nil
}

// SaveEntry creates (ID empty) or updates an entry in one of a's groups.
func (s *Service) SaveEntry(ctx context.Context, a account.Account, e Entry) (Entry, error) {
	g, err := s.Repo.Group(ctx, a.ID, e.GroupID)
	if err != nil {
		return e, err
	}
	if e.PaidBy == "" {
		return e, (&Problems{"Wer hat bezahlt? fehlt"}).Err()
	}
	if err := CheckEntry(g, e); err != nil {
		return e, err
	}
	e.UpdatedBy, e.UpdatedAt = a.ID, s.now()
	if e.ID == "" {
		e.ID, e.Version, e.CreatedBy, e.CreatedAt, e.RecurringID = id.New(), 1, a.ID, e.UpdatedAt, ""
		return e, s.Repo.InsertEntries(ctx, a.ID, []Entry{e})
	}
	old, err := s.Repo.Entry(ctx, a.ID, g.ID, e.ID)
	if err != nil {
		return e, err
	}
	want := e.Version
	e.CreatedBy, e.CreatedAt, e.RecurringID, e.Version = old.CreatedBy, old.CreatedAt, old.RecurringID, want+1
	return e, s.Repo.UpdateEntry(ctx, a.ID, e, want)
}

// DeleteEntry marks an entry as deleted (it stays in the database).
func (s *Service) DeleteEntry(ctx context.Context, a account.Account, groupID, entryID string, version int) error {
	return s.Repo.DeleteEntry(ctx, a.ID, groupID, entryID, version, s.now())
}

// SaveRecurring creates (ID empty) or updates a recurring expense. The first
// booking date must lie within a year from today.
func (s *Service) SaveRecurring(ctx context.Context, a account.Account, r Recurring) (Recurring, error) {
	g, err := s.Repo.Group(ctx, a.ID, r.GroupID)
	if err != nil {
		return r, err
	}
	if err := CheckRecurring(g, r); err != nil {
		return r, err
	}
	today, _ := time.Parse(time.DateOnly, s.Today())
	if r.Next < today.AddDate(-1, 0, 0).Format(time.DateOnly) || r.Next > today.AddDate(1, 0, 0).Format(time.DateOnly) {
		return r, (&Problems{"Nächstes Datum muss innerhalb eines Jahres von heute liegen"}).Err()
	}
	r.UpdatedAt = s.now()
	if r.ID == "" {
		r.ID, r.Version, r.CreatedBy, r.CreatedAt = id.New(), 1, a.ID, r.UpdatedAt
		return r, s.Repo.InsertRecurring(ctx, a.ID, r)
	}
	old, err := s.Repo.Recurring(ctx, a.ID, g.ID, r.ID)
	if err != nil {
		return r, err
	}
	want := r.Version
	r.CreatedBy, r.CreatedAt, r.Version = old.CreatedBy, old.CreatedAt, want+1
	return r, s.Repo.UpdateRecurring(ctx, a.ID, r, want)
}

// DeleteRecurring ends a recurring expense; booked entries stay.
func (s *Service) DeleteRecurring(ctx context.Context, a account.Account, groupID, id string, version int) error {
	return s.Repo.DeleteRecurring(ctx, a.ID, groupID, id, version)
}

// Import reads a Splitwise export into an empty group of a and returns the
// number of entries. names maps the file's person columns to members.
func (s *Service) Import(ctx context.Context, a account.Account, groupID string, r io.Reader, names map[string]string) (int, error) {
	g, err := s.Repo.Group(ctx, a.ID, groupID)
	if err != nil {
		return 0, err
	}
	es, err := ParseSplitwise(r, g, names)
	if err != nil {
		return 0, err
	}
	now := s.now()
	for i := range es {
		e := &es[i]
		if err := CheckEntry(g, *e); err != nil {
			return 0, fmt.Errorf("ausgaben: import produced an invalid entry (%s %s): %w", e.Date, e.Description, err)
		}
		e.ID, e.Version, e.CreatedBy, e.UpdatedBy, e.CreatedAt, e.UpdatedAt = id.New(), 1, a.ID, a.ID, now, now
	}
	return len(es), s.Repo.ImportEntries(ctx, a.ID, g.ID, es)
}
