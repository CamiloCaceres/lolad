package fed

import (
	"context"
	"encoding/json"
	"time"

	"github.com/CamiloCaceres/lolad/internal/core"
)

type queued struct {
	ID, Peer, Kind string
	Body           []byte
	Attempts       int
	Created        int64
}

// Run drains the durable outbox until cancellation. A short local ticker closes
// the tiny wake-before-commit race; network backoff is persisted independently.
// Run must be started once per engine.
func (f *Engine) Run(ctx context.Context) {
	if !f.Enabled() {
		return
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	f.recoverApprovals(ctx)
	for {
		f.drain(ctx)
		select {
		case <-ctx.Done():
			return
		case <-f.wake:
		case <-ticker.C:
		}
	}
}

// Approval decisions survive process crashes between the YAML write and release.
func (f *Engine) recoverApprovals(ctx context.Context) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, g := range f.doc.Grants {
		if g.State != core.GrantPending {
			_ = f.resolveGrant(ctx, g)
		}
	}
}
func (f *Engine) drain(ctx context.Context) {
	rows, e := f.o.DB.QueryContext(ctx, `SELECT id,peer,kind,body,attempts,created_at FROM fed_outbox WHERE next_at<=? ORDER BY next_at LIMIT 100`, f.o.Clock().UnixMilli())
	if e != nil {
		return
	}
	var items []queued
	for rows.Next() {
		var q queued
		if e = rows.Scan(&q.ID, &q.Peer, &q.Kind, &q.Body, &q.Attempts, &q.Created); e != nil {
			break
		}
		items = append(items, q)
	}
	rows.Close()
	if e != nil {
		return
	}
	for _, q := range items {
		if ctx.Err() != nil {
			return
		}
		f.process(ctx, q)
	}
}
func (f *Engine) process(ctx context.Context, q queued) {
	f.mu.Lock()
	p := f.find(q.Peer)
	if p == nil || p.Removed {
		f.mu.Unlock()
		_ = f.failPeer(ctx, q.Peer, "connection removed")
		return
	}
	copy := *p
	f.mu.Unlock()
	if copy.Paused {
		return
	}
	if f.o.Clock().Sub(time.UnixMilli(q.Created)) >= 7*24*time.Hour {
		f.finish(ctx, q, core.DeliveryFailed, "delivery gave up after 7 days")
		f.event("Delivery gave up: " + q.Peer + " (" + q.ID + ")")
		return
	}
	var body any
	path := "deliver"
	if q.Kind == "receipt" {
		path = "receipt"
		body = json.RawMessage(q.Body)
	} else {
		wire, e := f.o.Provider.Wire(ctx, q.ID)
		if e != nil {
			f.finish(ctx, q, core.DeliveryFailed, "outbound mail missing")
			return
		}
		mail, e := f.o.Provider.Get(ctx, q.ID)
		if e != nil {
			return
		}
		if mail.Delivery == core.DeliveryDelivered || mail.Delivery == core.DeliveryBlocked || mail.Delivery == core.DeliveryFailed {
			_, _ = f.o.DB.ExecContext(ctx, `DELETE FROM fed_outbox WHERE id=?`, q.ID)
			return
		}
		pub, e := f.o.Provider.PublicKey(ctx, mail.FromName)
		if e != nil {
			f.finish(ctx, q, core.DeliveryFailed, "sender public key unavailable")
			return
		}
		body = deliverBody{Envelope: wire.Envelope, Payload: wire.Payload, SenderPublicKey: pub}
	}
	status, b, e := f.request(ctx, copy, "POST", path, body)
	if e == nil && status >= 200 && status < 300 {
		if q.Kind == "receipt" {
			f.finish(ctx, q, "", "")
			return
		}
		var response struct{ Status, ID string }
		if json.Unmarshal(b, &response) == nil && response.ID == q.ID && (response.Status == core.DeliveryHeld || response.Status == core.DeliveryDelivered) {
			f.finish(ctx, q, response.Status, "")
			return
		}
	}
	detail := "peer unreachable"
	if e == nil {
		detail = "peer returned an invalid delivery response"
		if status >= 400 {
			detail = wireError(status, b).Error()
		}
	}
	if e == nil && status >= 400 && status < 500 && status != 429 {
		state := core.DeliveryFailed
		if status == 403 {
			var v struct{ Error string }
			_ = json.Unmarshal(b, &v)
			if v.Error == "forbidden" {
				state = core.DeliveryBlocked
			}
		}
		f.finish(ctx, q, state, detail)
		return
	}
	attempt := q.Attempts
	idx := attempt
	if idx >= len(f.o.Backoff) {
		idx = len(f.o.Backoff) - 1
	}
	next := f.o.Clock().Add(f.o.Backoff[idx]).UnixMilli()
	_, _ = f.o.DB.ExecContext(ctx, `UPDATE fed_outbox SET attempts=attempts+1,next_at=?,last_error=? WHERE id=?`, next, detail, q.ID)
	f.mu.Lock()
	if p = f.find(q.Peer); p != nil {
		p.LastError = detail
	}
	f.mu.Unlock()
	if q.Kind == "deliver" {
		f.change(ctx, q.ID, core.DeliveryQueued, detail)
	}
}
func (f *Engine) finish(ctx context.Context, q queued, state, detail string) {
	if q.Kind == "deliver" {
		f.change(ctx, q.ID, state, detail)
	}
	_, _ = f.o.DB.ExecContext(ctx, `DELETE FROM fed_outbox WHERE id=?`, q.ID)
}
