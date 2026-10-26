package supervisor

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/CamiloCaceres/lolad/internal/core"
)

func (s *Supervisor) enqueue(m core.Mail) *pending {
	name := m.ToName
	if name == "" {
		name = strings.Split(m.To, "@")[0]
	}
	if s.agents[name] == nil {
		return nil
	}
	q := s.queues[name]
	if p := q[m.ID]; p != nil {
		p.Mail = m
		return p
	}
	p := &pending{Mail: m}
	if s.opts.DB != nil {
		var data string
		if e := s.opts.DB.QueryRow(`SELECT data FROM sup_mail WHERE id=? AND agent=?`, m.ID, name).Scan(&data); e == nil {
			_ = json.Unmarshal([]byte(data), p)
			p.Mail = m
		}
	}
	q[m.ID] = p
	return p
}
func (s *Supervisor) OnMail(m core.Mail) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.enqueue(m)
	if p == nil {
		return
	}
	name := m.ToName
	if name == "" {
		name = strings.Split(m.To, "@")[0]
	}
	r := s.agents[name]
	s.event(name, "mail", m.ID)
	if r.Status.Desired == "down" && r.Status.Runtime.WakeOnMessageEnabled() {
		r.Status.Desired = "up"
		r.Status.NextRestart = time.Time{}
		r.Crashes = nil
		if r.Status.State == core.StateFailed {
			r.Status.State = core.StateStopped
		}
		_ = s.persist(r)
	}
}
func (s *Supervisor) sweep(ctx context.Context) {
	if s.opts.Mailer == nil {
		return
	}
	s.mu.Lock()
	names := make([]string, 0, len(s.agents))
	for name := range s.agents {
		names = append(names, name)
	}
	s.mu.Unlock()
	for _, name := range names {
		mail, e := s.opts.Mailer.Unread(ctx, name)
		if e != nil {
			continue
		}
		s.mu.Lock()
		q := s.queues[name]
		if q == nil {
			s.mu.Unlock()
			continue
		}
		unread := map[string]bool{}
		now := s.opts.Clock.Now()
		for _, m := range mail {
			unread[m.ID] = true
			if now.Sub(m.CreatedAt) < 90*time.Second {
				continue
			}
			p := s.enqueue(m)
			if p != nil && p.Confirmations > 0 && p.Confirmations < 4 && now.Sub(p.LastNotice) >= 90*time.Second && p.Attempts >= 4 {
				p.Attempts = 0
				p.Next = now
				s.savePending(name, p)
			}
		}
		for id := range q {
			if !unread[id] {
				delete(q, id)
				if s.opts.DB != nil {
					_, _ = s.opts.DB.Exec(`DELETE FROM sup_mail WHERE id=?`, id)
				}
			}
		}
		s.mu.Unlock()
	}
}

var retryDelays = []time.Duration{0, 30 * time.Second, 2 * time.Minute, 10 * time.Minute}

func (s *Supervisor) deliver(ctx context.Context, r *record, now time.Time) {
	if s.opts.Notifier == nil || s.opts.Mailer == nil || r.Status.State != core.StateIdle || now.Sub(r.Status.LastActivity) > 15*time.Minute {
		return
	}
	queue := s.queues[r.Status.Name]
	ids := make([]string, 0, len(queue))
	for id := range queue {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := queue[ids[i]].Mail, queue[ids[j]].Mail
		if a.CreatedAt.Equal(b.CreatedAt) {
			return a.ID < b.ID
		}
		return a.CreatedAt.Before(b.CreatedAt)
	})
	for _, id := range ids {
		p := queue[id]
		if p.Attempts >= len(retryDelays) || p.Confirmations >= 4 || now.Before(p.Next) || (!p.LastBlock.IsZero() && now.Sub(p.LastBlock) < 2*time.Minute) {
			continue
		}
		current, e := s.opts.Mailer.Get(ctx, id)
		if e != nil {
			continue
		}
		if current.Status == core.MailRead {
			delete(s.queues[r.Status.Name], id)
			continue
		}
		result, e := s.opts.Notifier.Notify(ctx, r.Status.Agent, p.Mail)
		if result == core.NotifyBusy {
			continue
		}
		p.Attempts++
		if p.Attempts < len(retryDelays) {
			p.Next = now.Add(retryDelays[p.Attempts] - retryDelays[p.Attempts-1])
		}
		if e == nil && result == core.NotifyConfirmed {
			if s.opts.Mailer.MarkNotified(ctx, id) == nil {
				p.Confirmations++
				p.LastNotice = now
				p.Attempts = len(retryDelays)
				s.event(r.Status.Name, "notified", id)
			}
		} else if e != nil {
			s.event(r.Status.Name, "delivery", e.Error())
		}
		s.savePending(r.Status.Name, p)
		// One notice starts a new turn. A fresh idle hook is needed before typing another.
		if result == core.NotifyConfirmed {
			s.setState(r, core.StateBusy, "mail notification")
			break
		}
	}
}

// Notification counters survive daemon restarts; an unread message must not
// receive a fresh retry budget simply because lolad was restarted.
func (s *Supervisor) savePending(name string, p *pending) {
	if s.opts.DB == nil {
		return
	}
	b, e := json.Marshal(p)
	if e != nil {
		return
	}
	if _, e = s.opts.DB.Exec(`INSERT INTO sup_mail(id,agent,data) VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET data=excluded.data`, p.Mail.ID, name, string(b)); e != nil {
		s.event(name, "error", "persist mail scheduling: "+e.Error())
	}
}
