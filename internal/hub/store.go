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
	// JoinedVia is how the account signed up: "open" (no code) or "invite".
	JoinedVia string `json:"joined_via"`
	// FirstTunnel is when it first connected; zero = never (open sign-ups
	// that never connect are reaped).
	FirstTunnel time.Time `json:"first_tunnel,omitzero"`
	// Note is the note of the invite this account joined with (operator
	// only; empty for accounts made before notes were linked).
	Note string `json:"note,omitempty"`
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
	if e = migrate(db); e != nil {
		db.Close()
		return nil, fmt.Errorf("hub: migrate %s: %w", path, e)
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
	var created, seen, first int64
	var disabled int
	if e := row.Scan(&a.Handle, &a.PublicKey, &a.Display, &created, &seen, &disabled, &a.JoinedVia, &first, &a.Note); e != nil {
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
	if first > 0 {
		a.FirstTunnel = time.Unix(first, 0).UTC()
	}
	return a, nil
}

// accountCols ends with the note of the latest invite used for the handle
// since the account was created.
const accountCols = `handle,public_key,display,created_at,last_seen,disabled,joined_via,COALESCE(first_tunnel,0),
  COALESCE((SELECT note FROM hub_invites i WHERE i.used_by=hub_accounts.handle AND i.used_at>=hub_accounts.created_at ORDER BY i.used_at DESC LIMIT 1),'')`

// Account looks up one account by handle.
func (s *Store) Account(ctx context.Context, handle string) (Account, error) {
	return scanAccount(s.db.QueryRowContext(ctx, `SELECT `+accountCols+` FROM hub_accounts WHERE handle=?`, handle))
}

func (s *Store) accountByKey(ctx context.Context, key string) (Account, error) {
	return scanAccount(s.db.QueryRowContext(ctx, `SELECT `+accountCols+` FROM hub_accounts WHERE public_key=?`, key))
}

// Accounts lists all accounts by handle.
func (s *Store) Accounts(ctx context.Context) ([]Account, error) {
	rows, e := s.db.QueryContext(ctx, `SELECT `+accountCols+` FROM hub_accounts ORDER BY created_at DESC, handle`)
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

// touch records a tunnel opening: last_seen, and first_tunnel the first time.
func (s *Store) touch(ctx context.Context, handle string) {
	now := s.now().Unix()
	_, _ = s.db.ExecContext(ctx, `UPDATE hub_accounts SET last_seen=?, first_tunnel=COALESCE(first_tunnel,?) WHERE handle=?`, now, now, handle)
}

// registerError is a client-visible registration failure.
type registerError struct {
	status     int
	code, msg  string
	suggestion string
	handle     string // the key's existing handle, for key_registered
}

func (e *registerError) Error() string { return e.msg }

// register creates the account in one transaction. With a code it consumes
// that invite (via "invite"); with none it is an open sign-up (via "open"),
// which the server allows only in open mode. The same key with the same
// handle again succeeds without a code.
func (s *Store) register(ctx context.Context, code, handle, display, key string) (Account, error) {
	if a, e := s.accountByKey(ctx, key); e == nil {
		if a.Handle == handle {
			return a, nil
		}
		return Account{}, &registerError{status: 409, code: "key_registered", msg: "this lolad is already registered as " + a.Handle, handle: a.Handle}
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
	via := "open"
	if code != "" {
		via = "invite"
		res, e := tx.ExecContext(ctx, `UPDATE hub_invites SET used_by=?,used_at=? WHERE code_hash=? AND used_at IS NULL AND expires_at>?`, handle, now.Unix(), hashCode(code), now.Unix())
		if e != nil {
			return Account{}, e
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return Account{}, &registerError{status: 403, code: "invalid_invite", msg: "sign-up code is invalid, expired, or already used"}
		}
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO hub_accounts(handle,public_key,display,created_at,joined_via) VALUES(?,?,?,?,?)`, handle, key, display, now.Unix(), via); e != nil {
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

// Invite is a sign-up code as the operator sees it: never the code itself,
// which exists only at creation.
type Invite struct {
	ID        int64     `json:"id"`
	Note      string    `json:"note"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
	UsedBy    string    `json:"used_by,omitempty"`
	UsedAt    time.Time `json:"used_at,omitzero"`
}

// Invites lists codes still waiting to be used (newest first) and codes
// already used (most recent first, at most 50). Expired unused codes are left
// out.
func (s *Store) Invites(ctx context.Context) (pending, used []Invite, err error) {
	now := s.now().Unix()
	rows, err := s.db.QueryContext(ctx, `SELECT rowid,note,created_at,expires_at,COALESCE(used_by,''),COALESCE(used_at,0) FROM hub_invites
	  WHERE used_at IS NOT NULL OR expires_at>? ORDER BY COALESCE(used_at,0) DESC, created_at DESC, rowid DESC`, now)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	pending, used = []Invite{}, []Invite{}
	for rows.Next() {
		var v Invite
		var created, exp, usedAt int64
		if err = rows.Scan(&v.ID, &v.Note, &created, &exp, &v.UsedBy, &usedAt); err != nil {
			return nil, nil, err
		}
		v.CreatedAt, v.ExpiresAt = time.Unix(created, 0).UTC(), time.Unix(exp, 0).UTC()
		if usedAt > 0 {
			v.UsedAt = time.Unix(usedAt, 0).UTC()
			if len(used) < 50 {
				used = append(used, v)
			}
			continue
		}
		pending = append(pending, v)
	}
	return pending, used, rows.Err()
}

// CreateInviteID is CreateInvite that also returns the invite's id.
func (s *Store) CreateInviteID(ctx context.Context, note string, ttl time.Duration) (Invite, string, error) {
	code, exp, err := s.CreateInvite(ctx, note, ttl)
	if err != nil {
		return Invite{}, "", err
	}
	v := Invite{Note: note, ExpiresAt: time.Unix(exp.Unix(), 0).UTC()}
	var created int64
	err = s.db.QueryRowContext(ctx, `SELECT rowid,created_at FROM hub_invites WHERE code_hash=?`, hashCode(code)).Scan(&v.ID, &created)
	v.CreatedAt = time.Unix(created, 0).UTC()
	return v, code, err
}

// CancelInvite deletes a code that has not been used yet.
func (s *Store) CancelInvite(ctx context.Context, id int64) error {
	return s.affect(s.db.ExecContext(ctx, `DELETE FROM hub_invites WHERE rowid=? AND used_at IS NULL`, id))
}

// migrate brings a database made by an older hub up to date. Each step is
// idempotent; old binaries ignore what it adds, so a rollback is safe.
func migrate(db *sql.DB) error {
	if _, e := db.Exec(`CREATE TABLE IF NOT EXISTS hub_settings(key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS hub_banned_keys(public_key TEXT PRIMARY KEY, handle TEXT NOT NULL DEFAULT '', banned_at INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS hub_accounts_created ON hub_accounts(created_at);`); e != nil {
		return e
	}
	cols := map[string]bool{}
	rows, e := db.Query(`SELECT name FROM pragma_table_info('hub_accounts')`)
	if e != nil {
		return e
	}
	for rows.Next() {
		var n string
		if e = rows.Scan(&n); e != nil {
			rows.Close()
			return e
		}
		cols[n] = true
	}
	rows.Close()
	if !cols["joined_via"] {
		// Every account before open sign-up came in with an operator code.
		if _, e = db.Exec(`ALTER TABLE hub_accounts ADD COLUMN joined_via TEXT NOT NULL DEFAULT 'invite'`); e != nil {
			return e
		}
	}
	if !cols["first_tunnel"] {
		if _, e = db.Exec(`ALTER TABLE hub_accounts ADD COLUMN first_tunnel INTEGER`); e != nil {
			return e
		}
		if _, e = db.Exec(`UPDATE hub_accounts SET first_tunnel=last_seen WHERE last_seen>0`); e != nil {
			return e
		}
	}
	return nil
}

// Setting reads a hub setting; "" when unset.
func (s *Store) Setting(ctx context.Context, key string) (string, error) {
	var v string
	e := s.db.QueryRowContext(ctx, `SELECT value FROM hub_settings WHERE key=?`, key).Scan(&v)
	if errors.Is(e, sql.ErrNoRows) {
		return "", nil
	}
	return v, e
}

// SetSetting stores a hub setting.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, e := s.db.ExecContext(ctx, `INSERT INTO hub_settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return e
}

// Block removes an account and bans its key from signing up again.
func (s *Store) Block(ctx context.Context, handle string) error {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var key string
	if e = tx.QueryRowContext(ctx, `SELECT public_key FROM hub_accounts WHERE handle=?`, handle).Scan(&key); errors.Is(e, sql.ErrNoRows) {
		return ErrNotFound
	} else if e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `INSERT OR REPLACE INTO hub_banned_keys(public_key,handle,banned_at) VALUES(?,?,?)`, key, handle, s.now().Unix()); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `DELETE FROM hub_accounts WHERE handle=?`, handle); e != nil {
		return e
	}
	return tx.Commit()
}

func (s *Store) banned(ctx context.Context, key string) (bool, error) {
	var n int
	e := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM hub_banned_keys WHERE public_key=?`, key).Scan(&n)
	return n > 0, e
}

// SignupCounts reports accounts in total and open sign-ups since each time.
func (s *Store) signupCounts(ctx context.Context, sinceHour, sinceDay time.Time) (total, hour, day int, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT COUNT(*),
  COALESCE(SUM(CASE WHEN created_at>=? AND joined_via='open' THEN 1 ELSE 0 END),0),
  COALESCE(SUM(CASE WHEN created_at>=? AND joined_via='open' THEN 1 ELSE 0 END),0) FROM hub_accounts`, sinceHour.Unix(), sinceDay.Unix()).Scan(&total, &hour, &day)
	return
}

// reap removes open sign-ups that never connected and are older than age,
// returning their handles.
func (s *Store) reap(ctx context.Context, age time.Duration) ([]string, error) {
	cut := s.now().Add(-age).Unix()
	rows, e := s.db.QueryContext(ctx, `SELECT handle FROM hub_accounts WHERE joined_via='open' AND first_tunnel IS NULL AND created_at<?`, cut)
	if e != nil {
		return nil, e
	}
	var out []string
	for rows.Next() {
		var h string
		if e = rows.Scan(&h); e != nil {
			rows.Close()
			return nil, e
		}
		out = append(out, h)
	}
	rows.Close()
	for _, h := range out {
		if _, e = s.db.ExecContext(ctx, `DELETE FROM hub_accounts WHERE handle=? AND first_tunnel IS NULL`, h); e != nil {
			return nil, e
		}
	}
	return out, nil
}
