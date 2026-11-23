package fed

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/CamiloCaceres/lolad/internal/core"
	"gopkg.in/yaml.v3"
)

// Renaming this lolad ends every connection: peers know us by provider name
// (X-AMP-Provider) and hold our full addresses in their grants. v1 redoes
// connections; the steps below are what the rename does on our side.

// RenameNotSent is the delivery detail of remote mail that was still queued
// when this lolad was renamed.
const RenameNotSent = "not sent: this lolad was renamed"

// OutboxPending counts outbound remote mail still waiting to be delivered.
func (f *Engine) OutboxPending(ctx context.Context) (int, error) {
	var n int
	e := f.o.DB.QueryRowContext(ctx, `SELECT count(*) FROM fed_outbox WHERE kind='deliver'`).Scan(&n)
	return n, e
}

// PeersForRename lists the connections a rename will end.
func (f *Engine) PeersForRename() []core.Connection {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []core.Connection
	for _, p := range f.doc.Peers {
		if !p.Removed {
			out = append(out, cloneConnection(p.Connection))
		}
	}
	return out
}

// NotifyRename tells each connected peer, best effort and in parallel, that
// we are leaving, so their card says "removed by them" instead of failing
// quietly once our name changes. Nothing changes on our side here.
func (f *Engine) NotifyRename(ctx context.Context) {
	f.mu.Lock()
	var peers []peer
	for _, p := range f.doc.Peers {
		if !p.Removed {
			peers = append(peers, p)
		}
	}
	f.mu.Unlock()
	var wg sync.WaitGroup
	for _, p := range peers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			_, _, _ = f.request(c, p, "POST", "unpair", map[string]any{})
		}()
	}
	wg.Wait()
}

// RenameTx is the database part of a rename, inside tx: queued remote mail
// is marked not sent (its wire was signed under the old name) and leaves the
// outbox, and links and codes not used yet are cancelled (they carry the old
// name). Idempotent.
func RenameTx(ctx context.Context, tx *sql.Tx) error {
	if _, e := tx.ExecContext(ctx, `UPDATE amp_messages SET delivery=?, delivery_error=? WHERE id IN (SELECT id FROM fed_outbox WHERE kind='deliver')`, core.DeliveryFailed, RenameNotSent); e != nil && !noTable(e) {
		return e
	}
	for _, q := range []string{`DELETE FROM fed_outbox`, `DELETE FROM fed_invites WHERE used_at IS NULL`} {
		if _, e := tx.ExecContext(ctx, q); e != nil && !noTable(e) {
			return e
		}
	}
	return nil
}

// RenameConnections empties connections.yaml in configDir (peers and grants);
// a missing file stays missing. Idempotent.
func RenameConnections(configDir string) error {
	path := filepath.Join(configDir, "connections.yaml")
	if _, e := os.Stat(path); errors.Is(e, os.ErrNotExist) {
		return nil
	}
	b, e := yaml.Marshal(map[string]any{"peers": []any{}, "grants": []any{}})
	if e != nil {
		return e
	}
	return atomicWrite(path, b)
}

func noTable(e error) bool {
	return e != nil && (strings.Contains(e.Error(), "no such table") || strings.Contains(e.Error(), "no such column"))
}

// OpenInvites counts invite links and codes not used and not expired yet; a
// rename cancels them.
func (f *Engine) OpenInvites(ctx context.Context) (int, error) {
	var n int
	e := f.o.DB.QueryRowContext(ctx, `SELECT count(*) FROM fed_invites WHERE used_at IS NULL AND expires>?`, f.o.Clock().Unix()).Scan(&n)
	return n, e
}
