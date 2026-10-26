// Package auth owns operator device credentials. Plaintext tokens are never persisted.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

var ErrCredentials = errors.New("invalid or expired credentials")

const SessionLifetime = 30 * 24 * time.Hour

type Device struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Kind      string    `json:"kind"`
	Created   time.Time `json:"created"`
	LastSeen  time.Time `json:"last_seen"`
	LastIP    string    `json:"last_ip"`
	LastLogin string    `json:"last_login"`
	Revoked   bool      `json:"revoked"`
}
type Pairing struct {
	Code    string    `json:"code"`
	Expires time.Time `json:"expires"`
}
type Store struct {
	db       *sql.DB
	now      func() time.Time
	mu       sync.Mutex
	active   map[string]map[*int]context.CancelFunc
	failures map[string]failure
}
type failure struct {
	count int
	until time.Time
}

func New(db *sql.DB) (*Store, error) {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS auth_devices (
 id TEXT PRIMARY KEY, name TEXT NOT NULL, kind TEXT NOT NULL, token_hash TEXT UNIQUE NOT NULL,
 created INTEGER NOT NULL,last_seen INTEGER NOT NULL,last_ip TEXT NOT NULL,last_login TEXT NOT NULL,revoked INTEGER NOT NULL DEFAULT 0);
 CREATE TABLE IF NOT EXISTS auth_sessions (hash TEXT PRIMARY KEY,device_id TEXT NOT NULL REFERENCES auth_devices(id),expires INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS auth_pairings (hash TEXT PRIMARY KEY,name TEXT NOT NULL,expires INTEGER NOT NULL);`)
	if err != nil {
		return nil, err
	}
	return &Store{db: db, now: time.Now, active: map[string]map[*int]context.CancelFunc{}, failures: map[string]failure{}}, nil
}
func random(n int) string {
	b := make([]byte, n)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func hash(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func (s *Store) Pair(ctx context.Context, name string) (Pairing, error) {
	if len(name) > 120 {
		return Pairing{}, fmt.Errorf("device name is too long")
	}
	var b [5]byte
	if _, e := rand.Read(b[:]); e != nil {
		return Pairing{}, e
	}
	p := Pairing{base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b[:]), s.now().Add(10 * time.Minute).UTC()}
	_, e := s.db.ExecContext(ctx, `DELETE FROM auth_pairings WHERE expires<=?`, s.now().Unix())
	if e != nil {
		return p, e
	}
	_, e = s.db.ExecContext(ctx, `INSERT INTO auth_pairings(hash,name,expires) VALUES(?,?,?)`, hash(p.Code), strings.TrimSpace(name), p.Expires.Unix())
	return p, e
}
func validate(name, kind string) error {
	if strings.TrimSpace(name) == "" || len(name) > 120 {
		return fmt.Errorf("device name is required (maximum 120 bytes)")
	}
	if kind != "browser" && kind != "android" && kind != "cli" {
		return fmt.Errorf("kind must be browser, android or cli")
	}
	return nil
}
func (s *Store) Redeem(ctx context.Context, code, name, kind, ip, login string) (string, Device, error) {
	if e := validate(name, kind); e != nil {
		return "", Device{}, e
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return "", Device{}, e
	}
	defer tx.Rollback()
	var suggested string
	e = tx.QueryRowContext(ctx, `DELETE FROM auth_pairings WHERE hash=? AND expires>? RETURNING name`, hash(strings.ToUpper(strings.TrimSpace(code))), s.now().Unix()).Scan(&suggested)
	if errors.Is(e, sql.ErrNoRows) {
		return "", Device{}, ErrCredentials
	}
	if e != nil {
		return "", Device{}, e
	}
	if suggested != "" {
		name = suggested
	}
	token, d, e := s.issue(ctx, tx, name, kind, ip, login)
	if e != nil {
		return "", d, e
	}
	e = tx.Commit()
	return token, d, e
}

type execer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func (s *Store) issue(ctx context.Context, db execer, name, kind, ip, login string) (string, Device, error) {
	token := "lol_" + random(32)
	now := s.now().UTC()
	d := Device{ID: "dev_" + random(12), Name: name, Kind: kind, Created: now, LastSeen: now, LastIP: ip, LastLogin: login}
	_, e := db.ExecContext(ctx, `INSERT INTO auth_devices(id,name,kind,token_hash,created,last_seen,last_ip,last_login) VALUES(?,?,?,?,?,?,?,?)`, d.ID, d.Name, d.Kind, hash(token), now.Unix(), now.Unix(), ip, login)
	return token, d, e
}
func (s *Store) TailscaleDevice(ctx context.Context, login, ip string) (Device, error) {
	_, d, e := s.issue(ctx, s.db, "Browser · "+login, "browser", ip, login)
	return d, e
}
func scanDevice(row interface{ Scan(...any) error }) (Device, error) {
	var d Device
	var created, seen int64
	e := row.Scan(&d.ID, &d.Name, &d.Kind, &created, &seen, &d.LastIP, &d.LastLogin, &d.Revoked)
	d.Created = time.Unix(created, 0).UTC()
	d.LastSeen = time.Unix(seen, 0).UTC()
	if errors.Is(e, sql.ErrNoRows) {
		e = ErrCredentials
	}
	return d, e
}

const columns = `d.id,d.name,d.kind,d.created,d.last_seen,d.last_ip,d.last_login,d.revoked`

func (s *Store) Token(ctx context.Context, token, ip string) (Device, error) {
	if !strings.HasPrefix(token, "lol_") || len(token) != 68 {
		return Device{}, ErrCredentials
	}
	d, e := scanDevice(s.db.QueryRowContext(ctx, `SELECT `+columns+` FROM auth_devices d WHERE token_hash=? AND revoked=0`, hash(token)))
	if e == nil {
		e = s.touch(ctx, d.ID, ip)
	}
	return d, e
}
func (s *Store) touch(ctx context.Context, id, ip string) error {
	_, e := s.db.ExecContext(ctx, `UPDATE auth_devices SET last_seen=?,last_ip=? WHERE id=?`, s.now().Unix(), ip, id)
	return e
}
func (s *Store) Session(ctx context.Context, cookie, ip string) (Device, error) {
	d, e := scanDevice(s.db.QueryRowContext(ctx, `SELECT `+columns+` FROM auth_sessions s JOIN auth_devices d ON d.id=s.device_id WHERE s.hash=? AND s.expires>? AND d.revoked=0`, hash(cookie), s.now().Unix()))
	if e == nil {
		_, e = s.db.ExecContext(ctx, `UPDATE auth_sessions SET expires=? WHERE hash=?`, s.now().Add(SessionLifetime).Unix(), hash(cookie))
		if e == nil {
			e = s.touch(ctx, d.ID, ip)
		}
	}
	return d, e
}
func (s *Store) NewSession(ctx context.Context, id string) (string, error) {
	value := random(32)
	_, e := s.db.ExecContext(ctx, `DELETE FROM auth_sessions WHERE expires<=?`, s.now().Unix())
	if e != nil {
		return "", e
	}
	res, e := s.db.ExecContext(ctx, `INSERT INTO auth_sessions(hash,device_id,expires) SELECT ?,id,? FROM auth_devices WHERE id=? AND revoked=0`, hash(value), s.now().Add(SessionLifetime).Unix(), id)
	if e == nil {
		n, _ := res.RowsAffected()
		if n != 1 {
			e = ErrCredentials
		}
	}
	return value, e
}
func (s *Store) Logout(ctx context.Context, value string) error {
	_, e := s.db.ExecContext(ctx, `DELETE FROM auth_sessions WHERE hash=?`, hash(value))
	return e
}
func (s *Store) Devices(ctx context.Context) ([]Device, error) {
	rows, e := s.db.QueryContext(ctx, `SELECT `+columns+` FROM auth_devices d ORDER BY created DESC,id`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Device{}
	for rows.Next() {
		d, e := scanDevice(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
func (s *Store) Revoke(ctx context.Context, id string) error {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	res, e := tx.ExecContext(ctx, `UPDATE auth_devices SET revoked=1 WHERE id=?`, id)
	if e != nil {
		return e
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrCredentials
	}
	if _, e = tx.ExecContext(ctx, `DELETE FROM auth_sessions WHERE device_id=?`, id); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, cancel := range s.active[id] {
		cancel()
	}
	delete(s.active, id)
	return nil
}
func (s *Store) track(ctx context.Context, id string) (context.Context, func()) {
	ctx, cancel := context.WithCancel(ctx)
	key := new(int)
	s.mu.Lock()
	if s.active[id] == nil {
		s.active[id] = map[*int]context.CancelFunc{}
	}
	s.active[id][key] = cancel
	s.mu.Unlock()
	return ctx, func() {
		cancel()
		s.mu.Lock()
		delete(s.active[id], key)
		if len(s.active[id]) == 0 {
			delete(s.active, id)
		}
		s.mu.Unlock()
	}
}

// Failures are bounded per IP and per supplied Tailscale login, never by secret.
func (s *Store) blocked(keys ...string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, k := range keys {
		f := s.failures[k]
		if s.now().Before(f.until) && f.count >= 10 {
			return true
		}
	}
	return false
}
func (s *Store) failed(keys ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for k, f := range s.failures {
		if !now.Before(f.until) {
			delete(s.failures, k)
		}
	}
	for _, k := range keys {
		if len(s.failures) >= 10000 {
			return
		}
		f := s.failures[k]
		if !now.Before(f.until) {
			f = failure{until: now.Add(10 * time.Minute)}
		}
		f.count++
		s.failures[k] = f
	}
}
