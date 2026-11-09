package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
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
