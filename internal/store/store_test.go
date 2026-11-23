package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func version(t *testing.T, db *sql.DB) int {
	t.Helper()
	var v int
	if err := db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func backups(t *testing.T, path string) []string {
	t.Helper()
	m, _ := filepath.Glob(path + ".v*-before-v*.bak")
	return m
}

// rawDB opens a file without any of Open's checks, as an older lolad would.
func rawDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func TestFreshDatabaseGetsCurrentVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lolad.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if v := version(t, db); v != SchemaVersion() || v < 1 {
		t.Fatalf("user_version %d, want %d", v, SchemaVersion())
	}
	if b := backups(t, path); len(b) != 0 {
		t.Fatalf("a new file needs no backup: %v", b)
	}
	db.Close()
	// Opening again is a no-op.
	db2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	db2.Close()
}

func TestUnversionedDatabaseUpgradesKeepingData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lolad.db")
	old := rawDB(t, path)
	if _, err := old.Exec("CREATE TABLE amp_messages (id TEXT PRIMARY KEY, body TEXT); INSERT INTO amp_messages VALUES ('m1', 'hello')"); err != nil {
		t.Fatal(err)
	}
	old.Close()

	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if v := version(t, db); v != SchemaVersion() {
		t.Fatalf("user_version %d", v)
	}
	var body string
	if err := db.QueryRow("SELECT body FROM amp_messages WHERE id='m1'").Scan(&body); err != nil || body != "hello" {
		t.Fatalf("data lost: %q %v", body, err)
	}
	b := backups(t, path)
	if len(b) != 1 {
		t.Fatalf("want one pre-upgrade copy, got %v", b)
	}
	cp := rawDB(t, b[0])
	defer cp.Close()
	if err := cp.QueryRow("SELECT body FROM amp_messages WHERE id='m1'").Scan(&body); err != nil || body != "hello" {
		t.Fatalf("backup incomplete: %q %v", body, err)
	}
	if v := version(t, cp); v != 0 {
		t.Fatalf("backup should be the old version, got %d", v)
	}
}

func TestMigrationsRunInOrderAndFailedStepRollsBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lolad.db")
	steps := []Migration{
		{Version: 1, Name: "baseline", Up: func(context.Context, Execer) error { return nil }},
		{Version: 2, Name: "notes", Up: func(ctx context.Context, tx Execer) error {
			_, err := tx.ExecContext(ctx, "CREATE TABLE notes (t TEXT); INSERT INTO notes VALUES ('kept')")
			return err
		}},
	}
	db, err := open(path, steps)
	if err != nil {
		t.Fatal(err)
	}
	if v := version(t, db); v != 2 {
		t.Fatalf("user_version %d", v)
	}
	db.Close()

	bad := append(steps, Migration{Version: 3, Name: "broken", Up: func(ctx context.Context, tx Execer) error {
		if _, err := tx.ExecContext(ctx, "INSERT INTO notes VALUES ('half')"); err != nil {
			return err
		}
		return errors.New("boom")
	}})
	if _, err := open(path, bad); err == nil || !strings.Contains(err.Error(), "boom") || !strings.Contains(err.Error(), "undone") {
		t.Fatalf("want failure, got %v", err)
	}
	db, err = open(path, steps)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	_ = db.QueryRow("SELECT count(*) FROM notes").Scan(&n)
	if v := version(t, db); v != 2 || n != 1 {
		t.Fatalf("failed step leaked: version %d, rows %d", v, n)
	}
	if b := backups(t, path); len(b) != 1 {
		t.Fatalf("want a copy before the failed upgrade: %v", b)
	}
}

func TestNewerDatabaseIsRefusedAndLeftAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lolad.db")
	old := rawDB(t, path)
	if _, err := old.Exec("PRAGMA journal_mode=WAL; CREATE TABLE x (a); PRAGMA user_version = 99"); err != nil {
		t.Fatal(err)
	}
	old.Close()
	_, err := Open(path)
	if !errors.Is(err, ErrNewerVersion) || !strings.Contains(err.Error(), "newer version of Lolad") || !strings.Contains(err.Error(), "Update Lolad") {
		t.Fatalf("got %v", err)
	}
	chk := rawDB(t, path)
	defer chk.Close()
	if v := version(t, chk); v != 99 {
		t.Fatalf("version changed to %d", v)
	}
	if b := backups(t, path); len(b) != 0 {
		t.Fatalf("refused file must not be touched: %v", b)
	}
}

func TestDamagedDatabaseNamesBackups(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lolad.db")
	if err := os.WriteFile(path, []byte(strings.Repeat("this is not sqlite ", 400)), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Open(path)
	if !errors.Is(err, ErrCorrupt) || !strings.Contains(err.Error(), filepath.Join(dir, "backups")) {
		t.Fatalf("got %v", err)
	}
}

func TestDamagedPagesAreDetected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lolad.db")
	old := rawDB(t, path)
	if _, err := old.Exec("PRAGMA page_size=4096; CREATE TABLE t (a TEXT); CREATE INDEX ta ON t(a)"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2000; i++ {
		if _, err := old.Exec("INSERT INTO t VALUES (?)", strings.Repeat("x", 100)); err != nil {
			t.Fatal(err)
		}
	}
	old.Close()
	b, err := os.ReadFile(path)
	if err != nil || len(b) < 4096*6 {
		t.Fatalf("setup: %d %v", len(b), err)
	}
	// Scribble over a few pages past the header page.
	for i := 4096 * 2; i < 4096*5; i++ {
		b[i] = 0xA5
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = Open(path)
	if !errors.Is(err, ErrCorrupt) || !strings.Contains(err.Error(), "backups") {
		t.Fatalf("got %v", err)
	}
}
