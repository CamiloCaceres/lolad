// Package store opens lolad's SQLite database. Each package owns its own
// tables (prefixed with its name) and creates them from its own schema.
//
// On top of that, the database file carries a schema version (PRAGMA
// user_version) that only moves forward. Open runs the migrations a file has
// not seen yet, each in its own transaction, after copying the file aside, and
// refuses a file written by a newer lolad instead of guessing.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Migration is one forward-only step. Up runs inside a write transaction
// together with the user_version bump, so a step is applied fully or not at
// all. Steps must never be edited or reordered once released: append new ones.
type Migration struct {
	Version int
	Name    string
	Up      func(ctx context.Context, tx Execer) error
}

// Execer is what a migration may use: a connection inside the step's
// transaction.
type Execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// migrations is the ordered history of the database file.
//
// Version 1 is the baseline: the tables each package creates itself with
// CREATE TABLE IF NOT EXISTS (and the additive column checks they already do).
// It changes nothing, so a file from before versioning (user_version 0) moves
// to 1 without touching its data. Later steps that need ordering across
// packages, or that cannot be expressed idempotently, are appended here.
var migrations = []Migration{
	{Version: 1, Name: "baseline", Up: func(context.Context, Execer) error { return nil }},
}

// SchemaVersion is the newest database version this build understands.
func SchemaVersion() int { return len(migrations) }

// ErrNewerVersion: the file was written by a newer lolad. It is left as is.
var ErrNewerVersion = errors.New("written by a newer version of Lolad")

// ErrCorrupt: the file failed SQLite's integrity check. It is left as is.
var ErrCorrupt = errors.New("database file is damaged")

// Open opens (creating if needed) the database at path with WAL, a busy
// timeout and foreign keys on, checks it, and brings its schema version up to
// date.
func Open(path string) (*sql.DB, error) { return open(path, migrations) }

func open(path string, steps []Migration) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	if err := prepare(context.Background(), db, path, steps); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func prepare(ctx context.Context, db *sql.DB, path string, steps []Migration) error {
	if err := db.PingContext(ctx); err != nil {
		return classify(path, err)
	}
	if err := integrity(ctx, db, path); err != nil {
		return err
	}
	var cur int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&cur); err != nil {
		return classify(path, err)
	}
	target := len(steps)
	if cur > target {
		return fmt.Errorf("%s was %w (data version %d; this version of Lolad understands up to %d). Update Lolad to open it; the file was not changed", path, ErrNewerVersion, cur, target)
	}
	if cur == target {
		return nil
	}
	var tables int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type='table'").Scan(&tables); err != nil {
		return classify(path, err)
	}
	if tables > 0 {
		// An existing install: keep a copy of the file as it was before any
		// step runs. VACUUM INTO is a consistent snapshot including the WAL.
		if _, err := backupBefore(ctx, db, path, cur, target, time.Now()); err != nil {
			return fmt.Errorf("could not copy %s before upgrading it, so nothing was changed: %w", path, err)
		}
	}
	for _, m := range steps[cur:] {
		if err := apply(ctx, db, m); err != nil {
			return fmt.Errorf("upgrading %s to data version %d (%s) failed, and that step was undone: %w", path, m.Version, m.Name, err)
		}
	}
	return nil
}

// apply runs one step and bumps user_version in the same transaction. It
// re-reads the version inside the transaction, so two processes opening the
// file at once do not both apply a step.
func apply(ctx context.Context, db *sql.DB, m Migration) (err error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	// IMMEDIATE takes the write lock up front, so the version read below is
	// not stale.
	if _, err = conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()
	var cur int
	if err = conn.QueryRowContext(ctx, "PRAGMA user_version").Scan(&cur); err != nil {
		return err
	}
	if cur >= m.Version {
		_, err = conn.ExecContext(ctx, "COMMIT") // someone else got there first
		return err
	}
	if m.Up != nil {
		if err = m.Up(ctx, conn); err != nil {
			return err
		}
	}
	if _, err = conn.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", m.Version)); err != nil {
		return err
	}
	_, err = conn.ExecContext(ctx, "COMMIT")
	return err
}

// backupBefore copies the database next to itself, as
// <name>.v<cur>-before-v<target>-<time>.bak, and returns the copy's path.
func backupBefore(ctx context.Context, db *sql.DB, path string, cur, target int, now time.Time) (string, error) {
	dst := fmt.Sprintf("%s.v%d-before-v%d-%s.bak", path, cur, target, now.UTC().Format("20060102T150405Z"))
	if _, err := os.Stat(dst); err == nil {
		dst = fmt.Sprintf("%s.v%d-before-v%d-%d.bak", path, cur, target, now.UnixNano())
	}
	if _, err := db.ExecContext(ctx, "VACUUM INTO '"+strings.ReplaceAll(dst, "'", "''")+"'"); err != nil {
		return "", err
	}
	_ = os.Chmod(dst, 0o600)
	return dst, nil
}

// integrity runs PRAGMA quick_check (fast; skips the index cross-check).
func integrity(ctx context.Context, db *sql.DB, path string) error {
	rows, err := db.QueryContext(ctx, "PRAGMA quick_check")
	if err != nil {
		return classify(path, err)
	}
	defer rows.Close()
	var problems []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return classify(path, err)
		}
		if s != "ok" {
			problems = append(problems, s)
		}
	}
	if err := rows.Err(); err != nil {
		return classify(path, err)
	}
	if len(problems) > 0 {
		if len(problems) > 3 {
			problems = append(problems[:3], fmt.Sprintf("and %d more", len(problems)-3))
		}
		return corrupt(path, strings.Join(problems, "; "))
	}
	return nil
}

// classify turns SQLite's "not a database" and "malformed" errors into the
// plain damaged-file message; anything else is returned as is.
func classify(path string, err error) error {
	s := strings.ToLower(err.Error())
	for _, k := range []string{"not a database", "malformed", "corrupt", "sqlite_notadb"} {
		if strings.Contains(s, k) {
			return corrupt(path, err.Error())
		}
	}
	return err
}

func corrupt(path, detail string) error {
	backups := filepath.Join(filepath.Dir(path), "backups")
	return fmt.Errorf("Lolad's %w: %s (%s). Lolad did not change it. Daily backups are kept in %s: quit Lolad, move the damaged file somewhere else, copy the newest backup to %s, then start Lolad again", ErrCorrupt, path, detail, backups, path)
}

// Exec runs a multi-statement schema.
func Exec(db *sql.DB, schema string) error {
	_, err := db.Exec(schema)
	return err
}
