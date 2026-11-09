package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/tiliavir/allmend/internal/ausgaben"
)

// Ausgaben implements ausgaben.Repository. Every query is limited to groups
// the acting account is a member of.
type Ausgaben struct{ s *Store }

// Ausgaben returns the Ausgaben repository.
func (s *Store) Ausgaben() *Ausgaben { return &Ausgaben{s} }

// isMember is the membership condition; arguments: group id, account id.
const isMember = `EXISTS (SELECT 1 FROM expense_members m WHERE m.group_id = ? AND m.account_id = ?)`

type querier interface {
	QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row
	ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error)
}

func (r *Ausgaben) member(ctx context.Context, q querier, member, groupID string) error {
	var ok int
	if err := q.QueryRowContext(ctx, `SELECT `+isMember, groupID, member).Scan(&ok); err != nil {
		return fmt.Errorf("store.Ausgaben member: %w", err)
	}
	if ok == 0 {
		return ausgaben.ErrNotFound
	}
	return nil
}

// ── groups ────────────────────────────────────────────

const (
	groupsSQL = `SELECT g.id, g.name, g.created_by, g.created_at, g.updated_at FROM expense_groups g
JOIN expense_members me ON me.group_id = g.id AND me.account_id = ? ORDER BY g.name COLLATE NOCASE, g.id`
	groupSQL = `SELECT g.id, g.name, g.created_by, g.created_at, g.updated_at FROM expense_groups g
JOIN expense_members me ON me.group_id = g.id AND me.account_id = ? WHERE g.id = ?`
)

func (r *Ausgaben) groups(ctx context.Context, query string, args ...any) ([]ausgaben.Group, error) {
	rows, err := r.s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store.Ausgaben groups: %w", err)
	}
	var out []ausgaben.Group
	for rows.Next() {
		var g ausgaben.Group
		var c, u string
		if err := rows.Scan(&g.ID, &g.Name, &g.CreatedBy, &c, &u); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if g.CreatedAt, err = parseTime(c); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if g.UpdatedAt, err = parseTime(u); err != nil {
			_ = rows.Close()
			return nil, err
		}
		out = append(out, g)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].Members, err = r.members(ctx, out[i].ID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (r *Ausgaben) members(ctx context.Context, groupID string) ([]string, error) {
	rows, err := r.s.db.QueryContext(ctx, `SELECT account_id FROM expense_members WHERE group_id = ? ORDER BY position`, groupID)
	if err != nil {
		return nil, fmt.Errorf("store.Ausgaben members: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// Groups lists member's groups by name.
func (r *Ausgaben) Groups(ctx context.Context, member string) ([]ausgaben.Group, error) {
	return r.groups(ctx, groupsSQL, member)
}

// Group loads one of member's groups.
func (r *Ausgaben) Group(ctx context.Context, member, groupID string) (ausgaben.Group, error) {
	gs, err := r.groups(ctx, groupSQL, member, groupID)
	if err != nil {
		return ausgaben.Group{}, err
	}
	if len(gs) == 0 {
		return ausgaben.Group{}, ausgaben.ErrNotFound
	}
	return gs[0], nil
}

// InsertGroup stores a new group with its members.
func (r *Ausgaben) InsertGroup(ctx context.Context, g ausgaben.Group) error {
	tx, err := r.s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO expense_groups (id, name, created_by, created_at, updated_at) VALUES (?,?,?,?,?)`,
		g.ID, g.Name, g.CreatedBy, fmtTime(g.CreatedAt), fmtTime(g.UpdatedAt)); err != nil {
		return fmt.Errorf("store.Ausgaben.InsertGroup: %w", err)
	}
	for i, m := range g.Members {
		if _, err := tx.ExecContext(ctx, `INSERT INTO expense_members (group_id, account_id, position) VALUES (?,?,?)`, g.ID, m, i); err != nil {
			return fmt.Errorf("store.Ausgaben.InsertGroup member: %w", err)
		}
	}
	return tx.Commit()
}

// ── entries ───────────────────────────────────────────

const (
	entriesSQL = `SELECT e.id, e.group_id, e.kind, e.date, e.description, e.category, e.amount, e.currency, e.paid_by, e.recurring_id, e.version, e.created_by, e.created_at, e.updated_by, e.updated_at
FROM expense_entries e WHERE e.group_id = ? AND e.deleted_at IS NULL AND ` + isMember + ` AND (? = '' OR e.id = ?)
ORDER BY e.date DESC, e.created_at DESC, e.id`
)

// entries loads the group's entries, or only entryID if not empty.
func (r *Ausgaben) entries(ctx context.Context, member, groupID, entryID string) ([]ausgaben.Entry, error) {
	if err := r.member(ctx, r.s.db, member, groupID); err != nil {
		return nil, err
	}
	rows, err := r.s.db.QueryContext(ctx, entriesSQL, groupID, groupID, member, entryID, entryID)
	if err != nil {
		return nil, fmt.Errorf("store.Ausgaben entries: %w", err)
	}
	var out []ausgaben.Entry
	idx := map[string]int{}
	for rows.Next() {
		var e ausgaben.Entry
		var paid, rec sql.NullString
		var c, u string
		if err := rows.Scan(&e.ID, &e.GroupID, &e.Kind, &e.Date, &e.Description, &e.Category, &e.Amount, &e.Currency, &paid, &rec, &e.Version, &e.CreatedBy, &c, &e.UpdatedBy, &u); err != nil {
			_ = rows.Close()
			return nil, err
		}
		e.PaidBy, e.RecurringID = paid.String, rec.String
		if e.CreatedAt, err = parseTime(c); err == nil {
			e.UpdatedAt, err = parseTime(u)
		}
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		idx[e.ID] = len(out)
		out = append(out, e)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return out, nil
	}
	sh, err := r.s.db.QueryContext(ctx, `SELECT s.entry_id, s.account_id, s.amount FROM expense_shares s
JOIN expense_entries e ON e.id = s.entry_id AND e.deleted_at IS NULL
JOIN expense_members m ON m.group_id = s.group_id AND m.account_id = s.account_id
WHERE s.group_id = ? ORDER BY m.position`, groupID)
	if err != nil {
		return nil, fmt.Errorf("store.Ausgaben shares: %w", err)
	}
	defer func() { _ = sh.Close() }()
	for sh.Next() {
		var eid string
		var s ausgaben.Share
		if err := sh.Scan(&eid, &s.AccountID, &s.Amount); err != nil {
			return nil, err
		}
		if i, ok := idx[eid]; ok {
			out[i].Shares = append(out[i].Shares, s)
		}
	}
	return out, sh.Err()
}

// Entries lists a group's entries that are not deleted, newest first.
func (r *Ausgaben) Entries(ctx context.Context, member, groupID string) ([]ausgaben.Entry, error) {
	return r.entries(ctx, member, groupID, "")
}

// Entry loads one entry that is not deleted.
func (r *Ausgaben) Entry(ctx context.Context, member, groupID, entryID string) (ausgaben.Entry, error) {
	es, err := r.entries(ctx, member, groupID, entryID)
	if err != nil {
		return ausgaben.Entry{}, err
	}
	if len(es) == 0 {
		return ausgaben.Entry{}, ausgaben.ErrNotFound
	}
	return es[0], nil
}

func nullS(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func insertEntry(ctx context.Context, tx *sql.Tx, e ausgaben.Entry) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO expense_entries (id, group_id, kind, date, description, category, amount, currency, paid_by, recurring_id, version, created_by, created_at, updated_by, updated_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		e.ID, e.GroupID, string(e.Kind), e.Date, e.Description, e.Category, e.Amount, e.Currency, nullS(e.PaidBy), nullS(e.RecurringID),
		e.Version, e.CreatedBy, fmtTime(e.CreatedAt), e.UpdatedBy, fmtTime(e.UpdatedAt)); err != nil {
		return fmt.Errorf("store.Ausgaben insert entry: %w", err)
	}
	return writeShares(ctx, tx, entryShares, e.ID, e.GroupID, e.Shares)
}

// shareSQL are the statements for one share table: delete all, insert one.
type shareSQL struct{ del, ins string }

var (
	entryShares = shareSQL{
		`DELETE FROM expense_shares WHERE entry_id = ?`,
		`INSERT INTO expense_shares (entry_id, group_id, account_id, amount) VALUES (?,?,?,?)`,
	}
	recurringShares = shareSQL{
		`DELETE FROM expense_recurring_shares WHERE recurring_id = ?`,
		`INSERT INTO expense_recurring_shares (recurring_id, group_id, account_id, amount) VALUES (?,?,?,?)`,
	}
)

func writeShares(ctx context.Context, tx *sql.Tx, q shareSQL, id, groupID string, shares []ausgaben.Share) error {
	if _, err := tx.ExecContext(ctx, q.del, id); err != nil {
		return fmt.Errorf("store.Ausgaben shares: %w", err)
	}
	for _, s := range shares {
		if _, err := tx.ExecContext(ctx, q.ins, id, groupID, s.AccountID, s.Amount); err != nil {
			return fmt.Errorf("store.Ausgaben shares: %w", err)
		}
	}
	return nil
}

func (r *Ausgaben) insertAll(ctx context.Context, tx *sql.Tx, member string, es []ausgaben.Entry) error {
	checked := map[string]bool{}
	for _, e := range es {
		if !checked[e.GroupID] {
			if err := r.member(ctx, tx, member, e.GroupID); err != nil {
				return err
			}
			checked[e.GroupID] = true
		}
		if err := insertEntry(ctx, tx, e); err != nil {
			return err
		}
	}
	return nil
}

// InsertEntries stores all entries or none.
func (r *Ausgaben) InsertEntries(ctx context.Context, member string, es []ausgaben.Entry) error {
	tx, err := r.s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := r.insertAll(ctx, tx, member, es); err != nil {
		return err
	}
	return tx.Commit()
}

// ImportEntries stores es into a group that has no entries yet.
func (r *Ausgaben) ImportEntries(ctx context.Context, member, groupID string, es []ausgaben.Entry) error {
	tx, err := r.s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := r.member(ctx, tx, member, groupID); err != nil {
		return err
	}
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM expense_entries WHERE group_id = ? AND deleted_at IS NULL`, groupID).Scan(&n); err != nil {
		return fmt.Errorf("store.Ausgaben.ImportEntries: %w", err)
	}
	if n > 0 {
		return ausgaben.ErrNotEmpty
	}
	for _, e := range es {
		if e.GroupID != groupID {
			return fmt.Errorf("store.Ausgaben.ImportEntries: entry of another group")
		}
	}
	if err := r.insertAll(ctx, tx, member, es); err != nil {
		return err
	}
	return tx.Commit()
}

// changed maps "no row updated" to ErrNotFound or ErrConflict.
func changed(ctx context.Context, tx *sql.Tx, res sql.Result, table, id string) error {
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}
	var x int
	q := `SELECT 1 FROM expense_recurring WHERE id = ?`
	if table == "expense_entries" {
		q = `SELECT 1 FROM expense_entries WHERE id = ? AND deleted_at IS NULL`
	}
	err := tx.QueryRowContext(ctx, q, id).Scan(&x)
	if errors.Is(err, sql.ErrNoRows) {
		return ausgaben.ErrNotFound
	}
	if err != nil {
		return err
	}
	return ausgaben.ErrConflict
}

// UpdateEntry saves an entry if the stored version is want.
func (r *Ausgaben) UpdateEntry(ctx context.Context, member string, e ausgaben.Entry, want int) error {
	tx, err := r.s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := r.member(ctx, tx, member, e.GroupID); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE expense_entries SET kind=?, date=?, description=?, category=?, amount=?, currency=?, paid_by=?, version=?, updated_by=?, updated_at=?
WHERE id=? AND group_id=? AND version=? AND deleted_at IS NULL`,
		string(e.Kind), e.Date, e.Description, e.Category, e.Amount, e.Currency, nullS(e.PaidBy), e.Version, e.UpdatedBy, fmtTime(e.UpdatedAt), e.ID, e.GroupID, want)
	if err != nil {
		return fmt.Errorf("store.Ausgaben.UpdateEntry: %w", err)
	}
	if err := changed(ctx, tx, res, "expense_entries", e.ID); err != nil {
		return err
	}
	if err := writeShares(ctx, tx, entryShares, e.ID, e.GroupID, e.Shares); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteEntry marks an entry as deleted if the stored version is want.
func (r *Ausgaben) DeleteEntry(ctx context.Context, member, groupID, entryID string, want int, at time.Time) error {
	tx, err := r.s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := r.member(ctx, tx, member, groupID); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE expense_entries SET deleted_at=?, deleted_by=?, version=version+1 WHERE id=? AND group_id=? AND version=? AND deleted_at IS NULL`,
		fmtTime(at), member, entryID, groupID, want)
	if err != nil {
		return fmt.Errorf("store.Ausgaben.DeleteEntry: %w", err)
	}
	if err := changed(ctx, tx, res, "expense_entries", entryID); err != nil {
		return err
	}
	return tx.Commit()
}

// ── recurring ─────────────────────────────────────────

// recurrings loads the group's recurring expenses, or only id if not empty.
func (r *Ausgaben) recurrings(ctx context.Context, member, groupID, id string) ([]ausgaben.Recurring, error) {
	if err := r.member(ctx, r.s.db, member, groupID); err != nil {
		return nil, err
	}
	rows, err := r.s.db.QueryContext(ctx, `SELECT id, group_id, description, category, amount, currency, paid_by, day, next_date, version, created_by, created_at, updated_at
FROM expense_recurring WHERE group_id = ? AND (? = '' OR id = ?) ORDER BY next_date, description, id`, groupID, id, id)
	if err != nil {
		return nil, fmt.Errorf("store.Ausgaben recurrings: %w", err)
	}
	var out []ausgaben.Recurring
	for rows.Next() {
		var x ausgaben.Recurring
		var c, u string
		if err := rows.Scan(&x.ID, &x.GroupID, &x.Description, &x.Category, &x.Amount, &x.Currency, &x.PaidBy, &x.Day, &x.Next, &x.Version, &x.CreatedBy, &c, &u); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if x.CreatedAt, err = parseTime(c); err == nil {
			x.UpdatedAt, err = parseTime(u)
		}
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		out = append(out, x)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range out {
		sh, err := r.s.db.QueryContext(ctx, `SELECT s.account_id, s.amount FROM expense_recurring_shares s
JOIN expense_members m ON m.group_id = s.group_id AND m.account_id = s.account_id WHERE s.recurring_id = ? ORDER BY m.position`, out[i].ID)
		if err != nil {
			return nil, fmt.Errorf("store.Ausgaben recurring shares: %w", err)
		}
		for sh.Next() {
			var s ausgaben.Share
			if err := sh.Scan(&s.AccountID, &s.Amount); err != nil {
				_ = sh.Close()
				return nil, err
			}
			out[i].Shares = append(out[i].Shares, s)
		}
		if err := sh.Close(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Recurrings lists a group's recurring expenses by next date.
func (r *Ausgaben) Recurrings(ctx context.Context, member, groupID string) ([]ausgaben.Recurring, error) {
	return r.recurrings(ctx, member, groupID, "")
}

// Recurring loads one recurring expense.
func (r *Ausgaben) Recurring(ctx context.Context, member, groupID, id string) (ausgaben.Recurring, error) {
	rs, err := r.recurrings(ctx, member, groupID, id)
	if err != nil {
		return ausgaben.Recurring{}, err
	}
	if len(rs) == 0 {
		return ausgaben.Recurring{}, ausgaben.ErrNotFound
	}
	return rs[0], nil
}

// InsertRecurring stores a new recurring expense.
func (r *Ausgaben) InsertRecurring(ctx context.Context, member string, x ausgaben.Recurring) error {
	tx, err := r.s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := r.member(ctx, tx, member, x.GroupID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO expense_recurring (id, group_id, description, category, amount, currency, paid_by, day, next_date, version, created_by, created_at, updated_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`, x.ID, x.GroupID, x.Description, x.Category, x.Amount, x.Currency, x.PaidBy, x.Day, x.Next, x.Version, x.CreatedBy, fmtTime(x.CreatedAt), fmtTime(x.UpdatedAt)); err != nil {
		return fmt.Errorf("store.Ausgaben.InsertRecurring: %w", err)
	}
	if err := writeShares(ctx, tx, recurringShares, x.ID, x.GroupID, x.Shares); err != nil {
		return err
	}
	return tx.Commit()
}

func updateRecurring(ctx context.Context, tx *sql.Tx, x ausgaben.Recurring, want int) error {
	res, err := tx.ExecContext(ctx, `UPDATE expense_recurring SET description=?, category=?, amount=?, currency=?, paid_by=?, day=?, next_date=?, version=?, updated_at=?
WHERE id=? AND group_id=? AND version=?`, x.Description, x.Category, x.Amount, x.Currency, x.PaidBy, x.Day, x.Next, x.Version, fmtTime(x.UpdatedAt), x.ID, x.GroupID, want)
	if err != nil {
		return fmt.Errorf("store.Ausgaben.UpdateRecurring: %w", err)
	}
	return changed(ctx, tx, res, "expense_recurring", x.ID)
}

// UpdateRecurring saves a recurring expense if the stored version is want.
func (r *Ausgaben) UpdateRecurring(ctx context.Context, member string, x ausgaben.Recurring, want int) error {
	tx, err := r.s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := r.member(ctx, tx, member, x.GroupID); err != nil {
		return err
	}
	if err := updateRecurring(ctx, tx, x, want); err != nil {
		return err
	}
	if err := writeShares(ctx, tx, recurringShares, x.ID, x.GroupID, x.Shares); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteRecurring removes a recurring expense; its booked entries stay.
func (r *Ausgaben) DeleteRecurring(ctx context.Context, member, groupID, id string, want int) error {
	tx, err := r.s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := r.member(ctx, tx, member, groupID); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM expense_recurring WHERE id=? AND group_id=? AND version=?`, id, groupID, want)
	if err != nil {
		return fmt.Errorf("store.Ausgaben.DeleteRecurring: %w", err)
	}
	if err := changed(ctx, tx, res, "expense_recurring", id); err != nil {
		return err
	}
	return tx.Commit()
}

// Book stores the entries of a recurring expense and its new state.
func (r *Ausgaben) Book(ctx context.Context, member string, x ausgaben.Recurring, want int, es []ausgaben.Entry) error {
	tx, err := r.s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := r.member(ctx, tx, member, x.GroupID); err != nil {
		return err
	}
	if err := updateRecurring(ctx, tx, x, want); err != nil {
		return err
	}
	if err := r.insertAll(ctx, tx, member, es); err != nil {
		return err
	}
	return tx.Commit()
}
