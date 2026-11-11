package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/tiliavir/allmend/internal/account"
)

// Accounts implements account.Repository.
type Accounts struct{ s *Store }

// Accounts returns the account repository.
func (s *Store) Accounts() *Accounts { return &Accounts{s} }

const accountCols = `id, email, name, is_admin, status, COALESCE(google_sub, ''), COALESCE(password_hash, ''), password_must_change, theme, created_at, updated_at`

func scanAccount(row interface{ Scan(...any) error }) (account.Account, error) {
	var a account.Account
	var admin, must int
	var status, theme, created, updated string
	if err := row.Scan(&a.ID, &a.Email, &a.Name, &admin, &status, &a.GoogleSub, &a.PasswordHash, &must, &theme, &created, &updated); err != nil {
		return a, err
	}
	a.IsAdmin, a.PasswordMustChange, a.Status, a.Theme = admin == 1, must == 1, account.Status(status), account.Theme(theme)
	var err error
	if a.CreatedAt, err = parseTime(created); err != nil {
		return a, err
	}
	a.UpdatedAt, err = parseTime(updated)
	return a, err
}

func (r *Accounts) one(ctx context.Context, where string, arg any) (account.Account, error) {
	a, err := scanAccount(r.s.db.QueryRowContext(ctx, `SELECT `+accountCols+` FROM accounts WHERE `+where, arg))
	if errors.Is(err, sql.ErrNoRows) {
		return a, account.ErrNotFound
	}
	if err != nil {
		return a, fmt.Errorf("store.Accounts: %w", err)
	}
	a.Modules, err = r.modules(ctx, a.ID)
	return a, err
}

// Get loads an account by id.
func (r *Accounts) Get(ctx context.Context, id string) (account.Account, error) {
	return r.one(ctx, `id = ?`, id)
}

// ByEmail loads an account by e-mail (case-insensitive).
func (r *Accounts) ByEmail(ctx context.Context, email string) (account.Account, error) {
	return r.one(ctx, `email = ?`, email)
}

// ByGoogleSub loads an account by its Google subject.
func (r *Accounts) ByGoogleSub(ctx context.Context, sub string) (account.Account, error) {
	return r.one(ctx, `google_sub = ?`, sub)
}

// List returns all accounts ordered by name and e-mail.
func (r *Accounts) List(ctx context.Context) ([]account.Account, error) {
	rows, err := r.s.db.QueryContext(ctx, `SELECT `+accountCols+` FROM accounts ORDER BY name COLLATE NOCASE, email`)
	if err != nil {
		return nil, fmt.Errorf("store.Accounts.List: %w", err)
	}
	var out []account.Account
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("store.Accounts.List scan: %w", err)
		}
		out = append(out, a)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, fmt.Errorf("store.Accounts.List: %w", err)
	}
	for i := range out {
		if out[i].Modules, err = r.modules(ctx, out[i].ID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (r *Accounts) modules(ctx context.Context, id string) ([]account.Module, error) {
	rows, err := r.s.db.QueryContext(ctx, `SELECT module FROM account_modules WHERE account_id = ? ORDER BY module`, id)
	if err != nil {
		return nil, fmt.Errorf("store.Accounts modules: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var ms []account.Module
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err != nil {
			return nil, err
		}
		ms = append(ms, account.Module(m))
	}
	return ms, rows.Err()
}

// Insert creates an account with its modules.
func (r *Accounts) Insert(ctx context.Context, a account.Account) error {
	return r.write(ctx, a, true)
}

// Update replaces all fields and modules of an existing account.
func (r *Accounts) Update(ctx context.Context, a account.Account) error {
	return r.write(ctx, a, false)
}

func (r *Accounts) write(ctx context.Context, a account.Account, insert bool) error {
	for _, m := range a.Modules {
		if !account.KnownModule(m) {
			return fmt.Errorf("%w: %q", account.ErrUnknownMod, m)
		}
	}
	if !slices.Contains(account.Themes, a.Theme) {
		return fmt.Errorf("%w: %q", account.ErrUnknownTheme, a.Theme)
	}
	tx, err := r.s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store.Accounts begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	args := []any{a.Email, a.Name, b2i(a.IsAdmin), string(a.Status), nullStr(a.GoogleSub), nullStr(a.PasswordHash), b2i(a.PasswordMustChange), string(a.Theme), fmtTime(a.UpdatedAt)}
	if insert {
		_, err = tx.ExecContext(ctx, `INSERT INTO accounts (email, name, is_admin, status, google_sub, password_hash, password_must_change, theme, updated_at, id, created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
			append(args, a.ID, fmtTime(a.CreatedAt))...)
	} else {
		var res sql.Result
		res, err = tx.ExecContext(ctx, `UPDATE accounts SET email=?, name=?, is_admin=?, status=?, google_sub=?, password_hash=?, password_must_change=?, theme=?, updated_at=? WHERE id=?`,
			append(args, a.ID)...)
		if err == nil {
			if n, _ := res.RowsAffected(); n == 0 {
				return account.ErrNotFound
			}
		}
	}
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed: accounts.email") {
			return account.ErrEmailTaken
		}
		return fmt.Errorf("store.Accounts write: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM account_modules WHERE account_id = ?`, a.ID); err != nil {
		return fmt.Errorf("store.Accounts modules delete: %w", err)
	}
	ms := slices.Clone(a.Modules)
	slices.Sort(ms)
	for _, m := range slices.Compact(ms) {
		if _, err := tx.ExecContext(ctx, `INSERT INTO account_modules (account_id, module) VALUES (?, ?)`, a.ID, string(m)); err != nil {
			return fmt.Errorf("store.Accounts modules insert: %w", err)
		}
	}
	if a.Status == account.Disabled {
		if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE account_id = ?`, a.ID); err != nil {
			return fmt.Errorf("store.Accounts end sessions: %w", err)
		}
	}
	if err := keepAdmin(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

// keepAdmin fails with ErrLastAdmin when active accounts remain but no
// active admin.
func keepAdmin(ctx context.Context, tx *sql.Tx) error {
	var admins int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM accounts WHERE is_admin = 1 AND status = 'active'`).Scan(&admins); err != nil {
		return fmt.Errorf("store.Accounts count admins: %w", err)
	}
	var total int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM accounts WHERE status = 'active'`).Scan(&total); err != nil {
		return fmt.Errorf("store.Accounts count: %w", err)
	}
	if total > 0 && admins == 0 {
		return account.ErrLastAdmin
	}
	return nil
}

// Delete removes an account. Sessions, module grants and page grants go
// with it (ON DELETE CASCADE). Every other reference to the account (owned
// rows, pages written, games, expense groups) is a foreign key without
// cascade, so SQLite refuses the delete: that becomes ErrHasData and the
// transaction is rolled back. No data is deleted implicitly.
func (r *Accounts) Delete(ctx context.Context, id string) error {
	tx, err := r.s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store.Accounts begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `DELETE FROM accounts WHERE id = ?`, id)
	if err != nil {
		if strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
			return account.ErrHasData
		}
		return fmt.Errorf("store.Accounts delete: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return account.ErrNotFound
	}
	if err := keepAdmin(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}
