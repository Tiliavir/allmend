package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Backup writes a consistent copy of the main database to dir using
// VACUUM INTO and returns its path. It never overwrites an existing file.
func Backup(ctx context.Context, d *sql.DB, dir, label string) (string, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("db.Backup mkdir: %w", err)
	}
	name := fmt.Sprintf("allmend-%s-%s.db", time.Now().UTC().Format("20060102T150405Z"), label)
	path := filepath.Join(dir, name)
	if _, err := os.Stat(path); err == nil {
		return "", fmt.Errorf("db.Backup: %s already exists", path)
	}
	if _, err := d.ExecContext(ctx, `VACUUM main INTO ?`, path); err != nil {
		return "", fmt.Errorf("db.Backup vacuum into: %w", err)
	}
	return path, nil
}

// Check runs SQLite's integrity check on the database file at path, opened
// read-only, and fails unless it reports "ok".
func Check(ctx context.Context, path string) error {
	d, err := sql.Open(DriverName, "file:"+path+"?mode=ro")
	if err != nil {
		return fmt.Errorf("db.Check open: %w", err)
	}
	defer func() { _ = d.Close() }()
	var res string
	if err := d.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&res); err != nil {
		return fmt.Errorf("db.Check %s: %w", path, err)
	}
	if res != "ok" {
		return fmt.Errorf("db.Check %s: %s", path, res)
	}
	return nil
}

// Prune deletes the oldest backups with the given label in dir so that keep
// of them remain, and returns the deleted paths. Backups with other labels
// (such as the pre-migration copies) are never touched.
func Prune(dir, label string, keep int) ([]string, error) {
	if keep < 1 {
		return nil, fmt.Errorf("db.Prune: keep must be at least 1, got %d", keep)
	}
	files, err := filepath.Glob(filepath.Join(dir, "allmend-*-"+label+".db"))
	if err != nil {
		return nil, fmt.Errorf("db.Prune: %w", err)
	}
	// The UTC timestamp in the name sorts chronologically.
	sort.Strings(files)
	if len(files) <= keep {
		return nil, nil
	}
	old := files[:len(files)-keep]
	for _, f := range old {
		if err := os.Remove(f); err != nil {
			return nil, fmt.Errorf("db.Prune: %w", err)
		}
	}
	return old, nil
}
