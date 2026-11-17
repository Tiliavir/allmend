package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tiliavir/allmend/internal/db"
	"github.com/tiliavir/allmend/internal/env"
	"github.com/tiliavir/allmend/internal/migrations"
)

func TestBackup(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cfg := env.Config{DataDir: t.TempDir()}
	d, err := db.Open(ctx, cfg.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	if _, err := db.Migrate(ctx, d, migrations.All(), db.Options{BackupDir: cfg.BackupDir()}); err != nil {
		t.Fatal(err)
	}
	want, err := db.Counts(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	// Two older manual backups and the pre-migration one from above.
	for _, n := range []string{"allmend-20200101T000000Z-manual.db", "allmend-20210101T000000Z-manual.db"} {
		if err := os.WriteFile(filepath.Join(cfg.BackupDir(), n), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// The server's connection stays open, as in the running container.
	out, err := writeBackup(ctx, cfg, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "deleted old backup: "+filepath.Join(cfg.BackupDir(), "allmend-20200101T000000Z-manual.db")) ||
		strings.Contains(out, "20210101") {
		t.Errorf("unexpected report:\n%s", out)
	}
	files, _ := filepath.Glob(filepath.Join(cfg.BackupDir(), "*.db"))
	var manual, pre int
	var copyPath string
	for _, f := range files {
		switch {
		case strings.HasSuffix(f, "-manual.db"):
			manual++
			if !strings.Contains(f, "20210101") {
				copyPath = f
			}
		case strings.Contains(f, "-pre-v"):
			pre++
		}
	}
	if manual != 2 || pre != 1 {
		t.Fatalf("backup dir: %d manual, %d pre-migration: %v", manual, pre, files)
	}
	c, err := db.Open(ctx, copyPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	got, err := db.Counts(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("copy counts %v, want %v", got, want)
	}
}

func TestBackupRejects(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cfg := env.Config{DataDir: t.TempDir()}
	if _, err := writeBackup(ctx, cfg, 0); err == nil {
		t.Error("backup of a missing database succeeded")
	}
	if _, err := os.Stat(cfg.DBPath()); !os.IsNotExist(err) {
		t.Errorf("missing database was created: %v", err)
	}
	if _, err := writeBackup(ctx, cfg, -1); err == nil {
		t.Error("negative -keep accepted")
	}
}

func TestBackupCommandArgs(t *testing.T) {
	t.Parallel()
	if err := run([]string{"backup", "extra"}); err == nil {
		t.Error("extra argument accepted")
	}
}
