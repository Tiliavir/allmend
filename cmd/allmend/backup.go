package main

import (
	"context"
	"fmt"
	"os"

	"github.com/tiliavir/allmend/internal/db"
	"github.com/tiliavir/allmend/internal/env"
)

// backupLabel marks backups written by `allmend backup`; -keep prunes only these.
const backupLabel = "manual"

func backup(keep int) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	out, err := writeBackup(context.Background(), cfg, keep)
	// Print what was done even when pruning failed after the backup.
	if _, werr := os.Stdout.WriteString(out); err == nil {
		err = werr
	}
	return err
}

// writeBackup copies the database consistently into the backup directory
// (safe while the server runs), checks the copy and, with keep > 0, deletes
// the oldest manual backups beyond keep. It returns the report to print.
func writeBackup(ctx context.Context, cfg env.Config, keep int) (string, error) {
	if keep < 0 {
		return "", fmt.Errorf("-keep must not be negative, got %d", keep)
	}
	// Opening a missing path would create an empty database and back that up.
	if _, err := os.Stat(cfg.DBPath()); err != nil {
		return "", fmt.Errorf("backup: %w", err)
	}
	d, err := db.Open(ctx, cfg.DBPath())
	if err != nil {
		return "", err
	}
	path, err := db.Backup(ctx, d, cfg.BackupDir(), backupLabel)
	_ = d.Close()
	if err != nil {
		return "", err
	}
	if err := db.Check(ctx, path); err != nil {
		return "", err
	}
	out := "backup written: " + path + "\n"
	if keep > 0 {
		removed, err := db.Prune(cfg.BackupDir(), backupLabel, keep)
		if err != nil {
			return out, err
		}
		for _, r := range removed {
			out += "deleted old backup: " + r + "\n"
		}
	}
	return out, nil
}
