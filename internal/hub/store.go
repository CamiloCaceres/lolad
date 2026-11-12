package hub

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS hub_accounts(
  handle TEXT PRIMARY KEY,
  public_key TEXT NOT NULL UNIQUE,
  display TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  last_seen INTEGER NOT NULL DEFAULT 0,
  disabled INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS hub_invites(
  code_hash TEXT PRIMARY KEY,
  note TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL,
  used_by TEXT,
  used_at INTEGER);
`

// ErrNotFound: no such account.
var ErrNotFound = errors.New("not found")

// Account is one registered lolad.
type Account struct {
	Handle    string    `json:"handle"`
	PublicKey string    `json:"public_key"`
	Display   string    `json:"display"`
	CreatedAt time.Time `json:"created_at"`
	LastSeen  time.Time `json:"last_seen,omitzero"`
	Disabled  bool      `json:"disabled"`
}

// Store is the hub database: accounts and sign-up invites. The admin CLI and
// the server share it (SQLite WAL), so admin changes apply to a running hub.
type Store struct {
	db  *sql.DB
	now func() time.Time
}

// OpenStore opens (creating if needed) the hub database at path.
func OpenStore(path string) (*Store, error) {
	if e := os.MkdirAll(filepath.Dir(path), 0o700); e != nil {
		return nil, e
	}
	db, e := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)")
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(4)
	if _, e = db.Exec(schema); e != nil {
		db.Close()
		return nil, e
	}
	_ = os.Chmod(path, 0o600)
	return &Store{db: db, now: time.Now}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func hashCode(c string) string { h := sha256.Sum256([]byte(c)); return hex.EncodeToString(h[:]) }

// CreateInvite mints a single-use sign-up code. The code is returned once;
// only its hash is stored.
func (s *Store) CreateInvite(ctx context.Context, note string, ttl time.Duration) (string, time.Time, error) {
	if ttl <= 0 {
		ttl = 7 * 24 * time.Hour
	}
	b := make([]byte, 24)
	if _, e := rand.Read(b); e != nil {
		return "", time.Time{}, e
	}
	code := "hubinv_" + base64.RawURLEncoding.EncodeToString(b)
	now := s.now()
	exp := now.Add(ttl)
	_, e := s.db.ExecContext(ctx, `INSERT INTO hub_invites(code_hash,note,created_at,expires_at) VALUES(?,?,?,?)`, hashCode(code), note, now.Unix(), exp.Unix())
	return code, exp, e
}

func scanAccount(row interface{ Scan(...any) error }) (Account, error) {
	var a Account
	var created, seen int64
	var disabled int
	if e := row.Scan(&a.Handle, &a.PublicKey, &a.Display, &created, &seen, &disabled); e != nil {
		if errors.Is(e, sql.ErrNoRows) {
			return a, ErrNotFound
		}
		return a, e
	}
	a.CreatedAt = time.Unix(created, 0).UTC()
	if seen > 0 {
		a.LastSeen = time.Unix(seen, 0).UTC()
	}
	a.Disabled = disabled != 0
	return a, nil
}

const accountCols = `handle,public_key,display,created_at,last_seen,disabled`

// Account looks up one account by handle.
func (s *Store) Account(ctx context.Context, handle string) (Account, error) {
	return scanAccount(s.db.QueryRowContext(ctx, `SELECT `+accountCols+` FROM hub_accounts WHERE handle=?`, handle))
}

func (s *Store) accountByKey(ctx context.Context, key string) (Account, error) {
	return scanAccount(s.db.QueryRowContext(ctx, `SELECT `+accountCols+` FROM hub_accounts WHERE public_key=?`, key))
}

// Accounts lists all accounts by handle.
func (s *Store) Accounts(ctx context.Context) ([]Account, error) {
	rows, e := s.db.QueryContext(ctx, `SELECT `+accountCols+` FROM hub_accounts ORDER BY handle`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []Account
	for rows.Next() {
		a, e := scanAccount(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// SetDisabled disables or re-enables a handle.
func (s *Store) SetDisabled(ctx context.Context, handle string, disabled bool) error {
	v := 0
	if disabled {
		v = 1
	}
	return s.affect(s.db.ExecContext(ctx, `UPDATE hub_accounts SET disabled=? WHERE handle=?`, v, handle))
}

// Remove deletes an account, releasing its handle.
func (s *Store) Remove(ctx context.Context, handle string) error {
	return s.affect(s.db.ExecContext(ctx, `DELETE FROM hub_accounts WHERE handle=?`, handle))
}

func (s *Store) affect(r sql.Result, e error) error {
	if e != nil {
		return e
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) touch(ctx context.Context, handle string) {
	_, _ = s.db.ExecContext(ctx, `UPDATE hub_accounts SET last_seen=? WHERE handle=?`, s.now().Unix(), handle)
}

// registerError is a client-visible registration failure.
type registerError struct {
	status     int
	code, msg  string
	suggestion string
}

func (e *registerError) Error() string { return e.msg }

// register creates the account, consuming the invite, in one transaction.
// The same key with the same handle again succeeds without a code.
func (s *Store) register(ctx context.Context, code, handle, display, key string) (Account, error) {
	if a, e := s.accountByKey(ctx, key); e == nil {
		if a.Handle == handle {
			return a, nil
		}
		return Account{}, &registerError{status: 409, code: "key_registered", msg: "this lolad is already registered as " + a.Handle}
	} else if !errors.Is(e, ErrNotFound) {
		return Account{}, e
	}
	if _, e := s.Account(ctx, handle); e == nil {
		return Account{}, &registerError{status: 409, code: "name_taken", msg: "handle is taken", suggestion: s.suggest(ctx, handle)}
	} else if !errors.Is(e, ErrNotFound) {
		return Account{}, e
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return Account{}, e
	}
	defer tx.Rollback()
	now := s.now()
	res, e := tx.ExecContext(ctx, `UPDATE hub_invites SET used_by=?,used_at=? WHERE code_hash=? AND used_at IS NULL AND expires_at>?`, handle, now.Unix(), hashCode(code), now.Unix())
	if e != nil {
		return Account{}, e
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return Account{}, &registerError{status: 403, code: "invalid_invite", msg: "sign-up code is invalid, expired, or already used"}
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO hub_accounts(handle,public_key,display,created_at) VALUES(?,?,?,?)`, handle, key, display, now.Unix()); e != nil {
		if strings.Contains(e.Error(), "UNIQUE") {
			return Account{}, &registerError{status: 409, code: "name_taken", msg: "handle is taken", suggestion: s.suggest(ctx, handle)}
		}
		return Account{}, e
	}
	if e = tx.Commit(); e != nil {
		return Account{}, e
	}
	return s.Account(ctx, handle)
}

func (s *Store) suggest(ctx context.Context, handle string) string {
	base := handle
	if len(base) > 28 {
		base = base[:28]
	}
	for i := 2; i < 100; i++ {
		c := fmt.Sprintf("%s-%d", strings.TrimRight(base, "-"), i)
		if _, e := s.Account(ctx, c); errors.Is(e, ErrNotFound) && ValidHandle(c) {
			return c
		}
	}
	return ""
}
