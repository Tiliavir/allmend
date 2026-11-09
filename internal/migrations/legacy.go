package migrations

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/tiliavir/allmend/internal/account"
	"github.com/tiliavir/allmend/internal/db"
	"github.com/tiliavir/allmend/internal/id"
)

// adminID returns the id of the configured first admin, creating the account
// if needed: imported legacy data needs an owner before the server's own
// EnsureAdmin runs.
func adminID(ctx context.Context, tx db.Tx, env db.MigrationEnv) (string, error) {
	if env.AdminEmail == "" {
		return "", errors.New("legacy import needs ALLMEND_ADMIN_EMAIL")
	}
	var aid string
	err := tx.QueryRowContext(ctx, `SELECT id FROM accounts WHERE email = ?`, env.AdminEmail).Scan(&aid)
	if err == nil {
		return aid, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	aid = id.New()
	now := env.Now.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
	if _, err := tx.ExecContext(ctx, `INSERT INTO accounts (id, email, is_admin, status, created_at, updated_at) VALUES (?, ?, 1, 'active', ?, ?)`,
		aid, env.AdminEmail, now, now); err != nil {
		return "", err
	}
	for _, m := range account.Modules {
		if _, err := tx.ExecContext(ctx, `INSERT INTO account_modules (account_id, module) VALUES (?, ?)`, aid, string(m.ID)); err != nil {
			return "", err
		}
	}
	return aid, nil
}

// legacyTable reports whether the attached legacy database has table t.
func legacyTable(ctx context.Context, tx db.Tx, env db.MigrationEnv, t string) (bool, error) {
	if !env.LegacyAttached {
		return false, nil
	}
	var n int
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM legacy.sqlite_master WHERE type = 'table' AND name = ?`, t).Scan(&n)
	return n == 1, err
}

// sameCount fails the migration if source and target row counts differ.
func sameCount(ctx context.Context, tx db.Tx, what, source, target string, args ...any) error {
	var a, b int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+source).Scan(&a); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+target, args...).Scan(&b); err != nil {
		return err
	}
	if a != b {
		return fmt.Errorf("legacy import %s: %d rows in old database, %d imported", what, a, b)
	}
	return nil
}
