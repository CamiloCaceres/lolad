package app

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Backup uses SQLite's consistent online snapshot, including WAL contents.
func Backup(ctx context.Context, db *sql.DB, state string, now time.Time) error {
	dir := filepath.Join(state, "backups")
	if e := os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	path := filepath.Join(dir, "lolad-"+now.UTC().Format("20060102T150405.000000000Z")+".db")
	// The directory is private, and a unique filename is created only by SQLite.
	if _, e := db.ExecContext(ctx, `VACUUM INTO '`+strings.ReplaceAll(path, "'", "''")+`'`); e != nil {
		return fmt.Errorf("backup: %w", e)
	}
	if e := os.Chmod(path, 0600); e != nil {
		return e
	}
	files, e := filepath.Glob(filepath.Join(dir, "lolad-*.db"))
	if e != nil {
		return e
	}
	sort.Strings(files)
	for len(files) > 7 {
		if e := os.Remove(files[0]); e != nil {
			return e
		}
		files = files[1:]
	}
	return nil
}
func LatestBackup(state string) time.Time {
	files, _ := filepath.Glob(filepath.Join(state, "backups", "lolad-*.db"))
	var latest time.Time
	for _, p := range files {
		if f, e := os.Stat(p); e == nil && f.ModTime().After(latest) {
			latest = f.ModTime()
		}
	}
	return latest
}
func (a *App) Backups(ctx context.Context, logger *slog.Logger) {
	if logger == nil {
		logger = slog.Default()
	}
	check := func() {
		if time.Since(LatestBackup(a.Config.StateDir)) >= 24*time.Hour {
			if e := Backup(ctx, a.DB, a.Config.StateDir, time.Now()); e != nil && ctx.Err() == nil {
				logger.Error("database backup failed", "err", e)
			}
		}
	}
	check()
	tick := time.NewTicker(time.Hour)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			check()
		}
	}
}
