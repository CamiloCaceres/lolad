package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/CamiloCaceres/lolad/internal/amp"
	"github.com/CamiloCaceres/lolad/internal/core"
	"github.com/CamiloCaceres/lolad/internal/fed"
	"github.com/CamiloCaceres/lolad/internal/policy"
	"github.com/CamiloCaceres/lolad/internal/store"
)

// Renaming this lolad (its tenant) happens in two halves (SPEC "Rename").
//
// The request, in the running daemon, validates the name, tells connected
// peers we are leaving, writes <state>/rename.json and asks serve to exit.
// A service manager (launchd, systemd, the Windows watchdog, the desktop app)
// starts lolad again.
//
// At that start, before the database opens, applyPendingRename backs up what
// it will change, then runs the steps below. Each step is idempotent, so a
// crash anywhere is finished by the next start. org.yaml is written last and
// is the commit point: an error before it restores the backup and lolad
// starts under the old name; after it, a rename only rolls forward.

const (
	renamePlanFile = "rename.json"
	renameLastFile = "rename-last.json"
	renameBackups  = "rename-backup-"
	backupComplete = "complete"
	backupKeep     = 7 * 24 * time.Hour
)

// ErrRestart is returned by serve when the daemon exits to finish a rename;
// its non-zero exit makes the service manager start lolad again.
var ErrRestart = errors.New("restarting to finish renaming this lolad")

var renameSteps = []string{"backup", "database", "identities", "connections", "org", "finish"}

type renamePlan struct {
	From   string    `json:"from"`
	To     string    `json:"to"`
	At     time.Time `json:"at"`
	Anyway bool      `json:"anyway,omitempty"`
	Backup string    `json:"backup,omitempty"` // set once the backup is complete
}

type renamePaths struct{ state, config string }

func (p renamePaths) plan() string  { return filepath.Join(p.state, renamePlanFile) }
func (p renamePaths) last() string  { return filepath.Join(p.state, renameLastFile) }
func (p renamePaths) db() string    { return filepath.Join(p.state, "lolad.db") }
func (p renamePaths) org() string   { return filepath.Join(p.config, "org.yaml") }
func (p renamePaths) conns() string { return filepath.Join(p.config, "connections.yaml") }

// errCrash stands in for the process dying in tests: the step stops with no
// rollback, as a real crash would.
var errCrash = errors.New("simulated crash")

// applyPendingRename finishes a requested rename, if there is one. It returns
// the step that ran (Error set when it was rolled back) or nil. An error
// means lolad must not start: the rename could neither finish nor roll back.
// hook, for tests, runs before each step.
func applyPendingRename(ctx context.Context, p renamePaths, now time.Time, hook func(string) error) (*core.RenameStep, error) {
	pruneRenameBackups(p.state, now)
	var plan renamePlan
	b, e := os.ReadFile(p.plan())
	if errors.Is(e, os.ErrNotExist) {
		return nil, nil
	}
	if e == nil {
		e = json.Unmarshal(b, &plan)
	}
	if e != nil {
		return nil, fmt.Errorf("rename: read %s: %w", p.plan(), e)
	}
	org, e := policy.Load(p.org())
	if e != nil {
		return nil, fmt.Errorf("rename: %w", e)
	}
	step := &core.RenameStep{From: plan.From, To: plan.To, At: now.UTC()}
	switch org.Tenant {
	case plan.To: // committed: roll forward
		return step, finishRename(p, step, hook)
	case plan.From:
	default:
		step.Error = "org.yaml was changed before the rename ran"
		return step, endRename(p, step)
	}
	for _, s := range renameSteps[:5] {
		if hook != nil {
			if e = hook(s); e != nil {
				if errors.Is(e, errCrash) {
					return nil, e
				}
				break
			}
		}
		if e = runRenameStep(ctx, p, &plan, s); e != nil {
			break
		}
	}
	if e != nil {
		step.Error = e.Error()
		if plan.Backup != "" {
			if re := restoreRenameBackup(p, plan.Backup); re != nil {
				return nil, fmt.Errorf("rename to %q failed (%v) and could not be undone: %w; the backup is in %s", plan.To, e, re, plan.Backup)
			}
		}
		return step, endRename(p, step)
	}
	return step, finishRename(p, step, hook)
}

func finishRename(p renamePaths, step *core.RenameStep, hook func(string) error) error {
	if hook != nil {
		if e := hook("finish"); errors.Is(e, errCrash) {
			return e
		}
	}
	return endRename(p, step)
}

// endRename records the outcome and removes the plan.
func endRename(p renamePaths, step *core.RenameStep) error {
	b, _ := json.MarshalIndent(step, "", "  ")
	if e := writeFileAtomic(p.last(), b); e != nil {
		return e
	}
	if e := os.Remove(p.plan()); e != nil && !errors.Is(e, os.ErrNotExist) {
		return e
	}
	return nil
}

func runRenameStep(ctx context.Context, p renamePaths, plan *renamePlan, s string) error {
	switch s {
	case "backup":
		if plan.Backup != "" {
			if _, e := os.Stat(filepath.Join(plan.Backup, backupComplete)); e == nil {
				return nil
			}
		}
		dir, e := backupForRename(ctx, p)
		if e != nil {
			return fmt.Errorf("backup: %w", e)
		}
		plan.Backup = dir
		b, _ := json.MarshalIndent(plan, "", "  ")
		return writeFileAtomic(p.plan(), b)
	case "database":
		return withDB(p.db(), func(db *sql.DB) error {
			tx, e := db.BeginTx(ctx, nil)
			if e != nil {
				return e
			}
			defer tx.Rollback()
			if e = amp.RenameAddresses(ctx, tx, plan.From, plan.To); e != nil {
				return e
			}
			if e = fed.RenameTx(ctx, tx); e != nil {
				return e
			}
			return tx.Commit()
		})
	case "identities":
		return amp.RenameIdentityFiles(p.state, plan.From, plan.To)
	case "connections":
		return fed.RenameConnections(p.config)
	case "org":
		return policy.SetTenant(p.org(), plan.To)
	}
	return fmt.Errorf("unknown rename step %q", s)
}

// withDB opens the database, runs fn, and empties the WAL before closing so
// the main file alone holds everything (a restore can then replace it).
func withDB(path string, fn func(*sql.DB) error) error {
	db, e := store.Open(path)
	if e != nil {
		return e
	}
	e = fn(db)
	if _, ce := db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); e == nil {
		e = ce
	}
	if ce := db.Close(); e == nil {
		e = ce
	}
	return e
}

// backupForRename copies the database (VACUUM INTO), org.yaml,
// connections.yaml and every identity.json into a fresh directory, and marks
// it complete last. Files that do not exist are recorded as absent.
func backupForRename(ctx context.Context, p renamePaths) (string, error) {
	dir, e := os.MkdirTemp(p.state, renameBackups+time.Now().UTC().Format("20060102T150405")+"-")
	if e != nil {
		return "", e
	}
	if _, e = os.Stat(p.db()); e == nil {
		e = withDB(p.db(), func(db *sql.DB) error {
			_, e := db.ExecContext(ctx, `VACUUM INTO ?`, filepath.Join(dir, "lolad.db"))
			return e
		})
	} else if errors.Is(e, os.ErrNotExist) {
		e = nil
	}
	if e != nil {
		return "", e
	}
	files := map[string]string{"org.yaml": p.org(), "connections.yaml": p.conns()}
	ids, _ := filepath.Glob(filepath.Join(p.state, "identities", "*", "identity.json"))
	for _, f := range ids {
		rel, _ := filepath.Rel(p.state, f)
		files[rel] = f
	}
	for rel, src := range files {
		if e = copyFile(src, filepath.Join(dir, rel)); errors.Is(e, os.ErrNotExist) {
			e = writeFileAtomic(filepath.Join(dir, rel+".absent"), nil)
		}
		if e != nil {
			return "", e
		}
	}
	return dir, writeFileAtomic(filepath.Join(dir, backupComplete), []byte(time.Now().UTC().Format(time.RFC3339)))
}

// restoreRenameBackup puts back everything backupForRename saved.
func restoreRenameBackup(p renamePaths, dir string) error {
	if _, e := os.Stat(filepath.Join(dir, backupComplete)); e != nil {
		return fmt.Errorf("backup incomplete: %w", e)
	}
	if _, e := os.Stat(filepath.Join(dir, "lolad.db")); e == nil {
		// The WAL was emptied by every step that wrote; drop it before the
		// main file is replaced so stale frames never apply to the copy.
		for _, s := range []string{"-wal", "-shm"} {
			if e := os.Remove(p.db() + s); e != nil && !errors.Is(e, os.ErrNotExist) {
				return e
			}
		}
		if e := copyFile(filepath.Join(dir, "lolad.db"), p.db()); e != nil {
			return e
		}
	}
	return filepath.WalkDir(dir, func(path string, d os.DirEntry, e error) error {
		if e != nil || d.IsDir() {
			return e
		}
		rel, _ := filepath.Rel(dir, path)
		if rel == "lolad.db" || rel == backupComplete {
			return nil
		}
		var dst string
		switch {
		case rel == "org.yaml", rel == "connections.yaml":
			dst = filepath.Join(p.config, rel)
		case rel == "org.yaml.absent", rel == "connections.yaml.absent":
			if e := os.Remove(filepath.Join(p.config, strings.TrimSuffix(rel, ".absent"))); e != nil && !errors.Is(e, os.ErrNotExist) {
				return e
			}
			return nil
		case strings.HasPrefix(rel, "identities"+string(filepath.Separator)):
			dst = filepath.Join(p.state, rel)
		default:
			return nil
		}
		return copyFile(path, dst)
	})
}

// pruneRenameBackups removes backups older than backupKeep.
func pruneRenameBackups(state string, now time.Time) {
	list, _ := filepath.Glob(filepath.Join(state, renameBackups+"*"))
	for _, d := range list {
		if st, e := os.Stat(d); e == nil && st.IsDir() && now.Sub(st.ModTime()) > backupKeep {
			_ = os.RemoveAll(d)
		}
	}
}

// copyFile copies src to dst atomically with mode 0600.
func copyFile(src, dst string) error {
	in, e := os.Open(src)
	if e != nil {
		return e
	}
	defer in.Close()
	if e = os.MkdirAll(filepath.Dir(dst), 0o700); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(dst), ".rename-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if e = f.Chmod(0o600); e == nil {
		_, e = io.Copy(f, in)
	}
	if e == nil {
		e = f.Sync()
	}
	if ce := f.Close(); e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	return os.Rename(f.Name(), dst)
}

func writeFileAtomic(path string, b []byte) error {
	if e := os.MkdirAll(filepath.Dir(path), 0o700); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".rename-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if e = f.Chmod(0o600); e == nil {
		_, e = f.Write(b)
	}
	if e == nil {
		e = f.Sync()
	}
	if ce := f.Close(); e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	return os.Rename(f.Name(), path)
}

func readRenameStep(path string) *core.RenameStep {
	b, e := os.ReadFile(path)
	if e != nil {
		return nil
	}
	var s core.RenameStep
	if json.Unmarshal(b, &s) != nil {
		return nil
	}
	return &s
}

// ---------------------------------------------------------------- the request

// restarter lets the API ask serve to exit after its answer is sent.
type restarter struct {
	once sync.Once
	ch   chan struct{}
}

// RestartRequested is closed when the daemon should exit with ErrRestart.
func (a *App) RestartRequested() <-chan struct{} { return a.restart.ch }

func (a *App) requestRestart() {
	go func() {
		time.Sleep(300 * time.Millisecond) // let the 202 reach the browser
		a.restart.once.Do(func() { close(a.restart.ch) })
	}()
}

func (a *App) renamePaths() renamePaths {
	return renamePaths{state: a.Config.StateDir, config: filepath.Dir(a.configPath)}
}

var _ core.LoladRenamer = (*App)(nil)

func (a *App) LoladName(ctx context.Context) (core.LoladName, error) {
	t := a.Policy.Org().Tenant
	v := core.LoladName{Name: t, Provider: amp.ProviderName(t), Restart: a.RestartMode, Connections: []core.Connection{}}
	if v.Restart == "" {
		v.Restart = "auto"
	}
	if a.Engine != nil {
		if c := a.Engine.PeersForRename(); c != nil {
			v.Connections = c
		}
		var e error
		if v.OpenLinks, e = a.Engine.OpenInvites(ctx); e != nil {
			return v, e
		}
		if v.Outbox, e = a.Engine.OutboxPending(ctx); e != nil {
			return v, e
		}
	}
	p := a.renamePaths()
	if b, e := os.ReadFile(p.plan()); e == nil {
		var plan renamePlan
		if json.Unmarshal(b, &plan) == nil {
			v.Pending = &core.RenameStep{From: plan.From, To: plan.To, At: plan.At}
		}
	}
	v.Last = readRenameStep(p.last())
	return v, nil
}

func (a *App) CheckLoladName(_ context.Context, input string) (core.NameCheck, error) {
	name, e := policy.CheckRename(a.Policy.Org().Tenant, input)
	if e != nil {
		return core.NameCheck{Message: userText(e)}, nil
	}
	return core.NameCheck{OK: true, Name: name, Message: "Addresses will look like " + a.Me().Name + "@" + amp.ProviderName(name) + "."}, nil
}

// OutboxError refuses a rename while remote mail is still on the way.
type OutboxError struct{ N int }

func (e OutboxError) Error() string { return core.ErrConflict.Error() + ": " + e.UserMessage() }
func (e OutboxError) Unwrap() error { return core.ErrConflict }
func (e OutboxError) Outbox() int   { return e.N }
func (e OutboxError) UserMessage() string {
	n := "1 message"
	if e.N != 1 {
		n = fmt.Sprintf("%d messages", e.N)
	}
	return n + " to other lolads " + map[bool]string{true: "is", false: "are"}[e.N == 1] + " still on the way. Wait until they arrive, or rename anyway and they won't be sent."
}

type pendingError struct{ to string }

func (e pendingError) Error() string { return core.ErrConflict.Error() + ": " + e.UserMessage() }
func (e pendingError) Unwrap() error { return core.ErrConflict }
func (e pendingError) UserMessage() string {
	return fmt.Sprintf("A rename to %q is waiting for lolad to restart.", e.to)
}

func (a *App) RenameLolad(ctx context.Context, r core.RenameRequest) (core.RenameAccepted, error) {
	a.mutate.Lock()
	defer a.mutate.Unlock()
	p := a.renamePaths()
	if b, e := os.ReadFile(p.plan()); e == nil {
		var plan renamePlan
		_ = json.Unmarshal(b, &plan)
		return core.RenameAccepted{}, pendingError{plan.To}
	}
	from := a.Policy.Org().Tenant
	name, e := policy.CheckRename(from, r.Name)
	if e != nil {
		return core.RenameAccepted{}, e
	}
	if a.Engine != nil {
		n, e := a.Engine.OutboxPending(ctx)
		if e != nil {
			return core.RenameAccepted{}, e
		}
		if n > 0 && !r.Anyway {
			return core.RenameAccepted{}, OutboxError{n}
		}
	}
	b, _ := json.MarshalIndent(renamePlan{From: from, To: name, At: time.Now().UTC(), Anyway: r.Anyway}, "", "  ")
	if e = writeFileAtomic(p.plan(), b); e != nil {
		return core.RenameAccepted{}, e
	}
	if a.Engine != nil && a.Engine.Enabled() {
		a.Engine.NotifyRename(ctx)
	}
	a.RecordEvent(core.Event{Kind: "rename", Detail: "Renaming this lolad from " + from + " to " + name + "; restarting"})
	a.requestRestart()
	return core.RenameAccepted{Name: name, Restarting: a.RestartMode != "manual"}, nil
}

func userText(e error) string {
	var um interface{ UserMessage() string }
	if errors.As(e, &um) {
		return um.UserMessage()
	}
	return e.Error()
}
