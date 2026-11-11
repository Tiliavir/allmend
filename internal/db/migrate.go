package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"
)

// Migration is one schema step. Versions are global and strictly increasing,
// so steps of different modules apply in a defined order.
type Migration struct {
	Version int
	Name    string
	// Up runs inside the migration transaction.
	Up func(ctx context.Context, tx Tx, env MigrationEnv) error
}

// Tx is the part of *sql.Tx migrations use.
type Tx interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// MigrationEnv carries the inputs migrations may need besides the database.
type MigrationEnv struct {
	Now time.Time
}

// SQL returns an Up func that executes the given statements.
func SQL(stmts string) func(context.Context, Tx, MigrationEnv) error {
	return func(ctx context.Context, tx Tx, _ MigrationEnv) error {
		_, err := tx.ExecContext(ctx, stmts)
		return err
	}
}

// Options control a migration run.
type Options struct {
	BackupDir string // where the pre-migration copy goes; required when migrations are pending
	Env       MigrationEnv
}

// Result reports what a run did.
type Result struct {
	From, To   int
	Applied    []string
	BackupPath string
}

const versionTable = `CREATE TABLE IF NOT EXISTS schema_migrations (
	version    INTEGER PRIMARY KEY,
	name       TEXT    NOT NULL,
	applied_at TEXT    NOT NULL
) STRICT`

// Migrate applies all pending migrations in one transaction. Before that it
// writes a consistent copy of the database to BackupDir. Any error rolls the
// transaction back; the database is then unchanged.
func Migrate(ctx context.Context, d *sql.DB, ms []Migration, opt Options) (Result, error) {
	if err := validate(ms); err != nil {
		return Result{}, err
	}
	if _, err := d.ExecContext(ctx, versionTable); err != nil {
		return Result{}, fmt.Errorf("db.Migrate version table: %w", err)
	}
	current, err := CurrentVersion(ctx, d)
	if err != nil {
		return Result{}, err
	}
	res := Result{From: current, To: current}
	last := ms[len(ms)-1].Version
	if current > last {
		return res, fmt.Errorf("db.Migrate: database is at version %d, this binary knows only up to %d — refusing to run an older binary", current, last)
	}
	var pending []Migration
	for _, m := range ms {
		if m.Version > current {
			pending = append(pending, m)
		}
	}
	if len(pending) == 0 {
		return res, nil
	}

	if opt.BackupDir == "" {
		return res, errors.New("db.Migrate: BackupDir is required")
	}
	res.BackupPath, err = Backup(ctx, d, opt.BackupDir, fmt.Sprintf("pre-v%d", last))
	if err != nil {
		return res, err
	}

	env := opt.Env
	if env.Now.IsZero() {
		env.Now = time.Now().UTC()
	}
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return res, fmt.Errorf("db.Migrate begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, m := range pending {
		if err := m.Up(ctx, tx, env); err != nil {
			return res, fmt.Errorf("db.Migrate %04d_%s: %w", m.Version, m.Name, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, name, applied_at) VALUES (?, ?, ?)`,
			m.Version, m.Name, env.Now.Format(time.RFC3339)); err != nil {
			return res, fmt.Errorf("db.Migrate record %04d: %w", m.Version, err)
		}
		res.Applied = append(res.Applied, fmt.Sprintf("%04d_%s", m.Version, m.Name))
	}
	if err := checkForeignKeys(ctx, tx); err != nil {
		return res, err
	}
	if err := tx.Commit(); err != nil {
		return res, fmt.Errorf("db.Migrate commit: %w", err)
	}
	res.To = last
	return res, nil
}

// CurrentVersion returns the highest applied migration version (0 if none).
func CurrentVersion(ctx context.Context, d *sql.DB) (int, error) {
	var v sql.NullInt64
	if err := d.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&v); err != nil {
		return 0, fmt.Errorf("db.CurrentVersion: %w", err)
	}
	return int(v.Int64), nil
}

func validate(ms []Migration) error {
	if len(ms) == 0 {
		return errors.New("db: no migrations")
	}
	if !sort.SliceIsSorted(ms, func(i, j int) bool { return ms[i].Version < ms[j].Version }) {
		return errors.New("db: migrations not sorted")
	}
	for i, m := range ms {
		if m.Version != i+1 || m.Up == nil || m.Name == "" {
			return fmt.Errorf("db: migration #%d must have version %d, a name and Up", i, i+1)
		}
	}
	return nil
}

func checkForeignKeys(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return fmt.Errorf("db.Migrate foreign_key_check: %w", err)
	}
	defer func() { _ = rows.Close() }()
	if rows.Next() {
		var table string
		var rowid sql.NullInt64
		var parent string
		var fkid int
		_ = rows.Scan(&table, &rowid, &parent, &fkid)
		return fmt.Errorf("db.Migrate: foreign key violation in %s → %s", table, parent)
	}
	return rows.Err()
}
