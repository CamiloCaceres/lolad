package supervisor

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/CamiloCaceres/lolad/internal/core"
	"github.com/robfig/cron/v3"
)

// MailWatch is the sole process boundary owned by this package. Implementations
// must honor cancellation; the default gives email.sh a 90-second deadline.
type MailWatch interface {
	Watch(context.Context, string, string) ([]byte, error)
}
type ExecMailWatch struct{}

func (ExecMailWatch) Watch(ctx context.Context, dir, account string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, filepath.Join(dir, "tools/email.sh"), "watch", account)
	cmd.Dir = dir
	return cmd.Output()
}

type triggerState struct {
	Agent    core.Agent
	Trigger  core.Trigger
	Next     time.Time
	Schedule cron.Schedule
	Running  bool
}

func (s *Supervisor) configureTriggers() error {
	next := map[string]*triggerState{}
	now := s.opts.Clock.Now()
	for _, r := range s.agents {
		for i, t := range r.Status.Runtime.Triggers {
			n := 0
			for _, v := range []string{t.Cron, t.Mail, t.Webhook} {
				if v != "" {
					n++
				}
			}
			if n != 1 {
				return fmt.Errorf("trigger needs exactly one kind")
			}
			key := fmt.Sprintf("%s/%d", r.Status.Name, i)
			st := &triggerState{Agent: r.Status.Agent, Trigger: t, Next: now}
			if t.Cron != "" {
				schedule, e := cron.ParseStandard(t.Cron)
				if e != nil {
					return e
				}
				st.Schedule = schedule
				st.Next = schedule.Next(now)
			}
			if t.Mail != "" && t.Every != "" {
				d, e := time.ParseDuration(t.Every)
				if e != nil || d <= 0 {
					return fmt.Errorf("invalid mail interval %q", t.Every)
				}
			}
			if old := s.triggers[key]; old != nil && old.Trigger == t && old.Agent.Dir == st.Agent.Dir {
				st.Next = old.Next
				next[key] = old
				continue
			}
			next[key] = st
			if t.Webhook != "" {
				if s.opts.Launch.StateDir == "" {
					return fmt.Errorf("StateDir required for webhook tokens")
				}
				dir := filepath.Join(s.opts.Launch.StateDir, "agents", r.Status.Name)
				if e := os.MkdirAll(dir, 0700); e != nil {
					return e
				}
				path := filepath.Join(dir, "webhook.token")
				f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
				if e == nil {
					var token [32]byte
					_, e = rand.Read(token[:])
					if e == nil {
						_, e = f.WriteString(hex.EncodeToString(token[:]) + "\n")
					}
					ce := f.Close()
					if e != nil {
						os.Remove(path)
						return e
					}
					if ce != nil {
						return ce
					}
				} else if !os.IsExist(e) {
					return e
				}
			}
		}
	}
	s.triggers = next
	return nil
}
func (s *Supervisor) runTriggers(ctx context.Context, now time.Time) {
	s.mu.Lock()
	var due []*triggerState
	for _, st := range s.triggers {
		if st.Trigger.Webhook != "" || st.Running || now.Before(st.Next) {
			continue
		}
		st.Running = true
		if st.Schedule != nil {
			st.Next = st.Schedule.Next(now)
		} else {
			d := 2 * time.Minute
			if st.Trigger.Every != "" {
				d, _ = time.ParseDuration(st.Trigger.Every)
			}
			st.Next = now.Add(d)
		}
		due = append(due, st)
	}
	s.mu.Unlock()
	for _, st := range due {
		if st.Trigger.Cron != "" {
			s.sendTrigger(ctx, st.Agent.Name, "cron", st.Trigger.Prompt)
			s.mu.Lock()
			st.Running = false
			s.mu.Unlock()
		} else {
			// A slow network mailbox must never stall lifecycle reconciliation.
			s.triggerWG.Add(1)
			go func(st *triggerState) {
				defer s.triggerWG.Done()
				s.pollMail(ctx, st)
				s.mu.Lock()
				st.Running = false
				s.mu.Unlock()
			}(st)
		}
	}
}
func (s *Supervisor) sendTrigger(ctx context.Context, name, subject, body string) error {
	if s.opts.Mailer == nil {
		return fmt.Errorf("mailer required")
	}
	_, e := s.opts.Mailer.Send(ctx, "lolad", core.Outgoing{To: name, Subject: subject, Body: body, Type: "notification"})
	s.mu.Lock()
	defer s.mu.Unlock()
	if e != nil {
		s.event(name, "error", "trigger: "+e.Error())
	} else {
		s.event(name, "trigger", subject)
	}
	return e
}
func (s *Supervisor) pollMail(ctx context.Context, st *triggerState) {
	b, e := s.opts.MailWatch.Watch(ctx, st.Agent.Dir, st.Trigger.Mail)
	if e != nil {
		s.mu.Lock()
		s.event(st.Agent.Name, "error", "mail watch: "+e.Error())
		s.mu.Unlock()
		return
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	var result struct {
		New *[]struct {
			ID      string `json:"id"`
			From    string `json:"from"`
			Subject string `json:"subject"`
			Trust   string `json:"trust"`
		} `json:"new"`
	}
	if e = json.Unmarshal([]byte(lines[len(lines)-1]), &result); e != nil || result.New == nil {
		s.mu.Lock()
		s.event(st.Agent.Name, "error", "mail watch: invalid last JSON line")
		s.mu.Unlock()
		return
	}
	for _, m := range *result.New {
		if m.Trust != "operator" {
			s.mu.Lock()
			s.event(st.Agent.Name, "external_mail", m.From+": "+m.Subject)
			s.mu.Unlock()
			continue
		}
		body := fmt.Sprintf("%s\n[EMAIL] New email from %s: %s\nRead with tools/email.sh read %s %s and check its Trust and Auth lines.", st.Trigger.Prompt, m.From, m.Subject, st.Trigger.Mail, m.ID)
		_ = s.sendTrigger(ctx, st.Agent.Name, "Email: "+m.Subject, body)
	}
}

// Trigger is called only after the API verifies the agent's webhook token.
func (s *Supervisor) Trigger(ctx context.Context, agent, name, body string) error {
	s.mu.Lock()
	r := s.agents[agent]
	if r == nil {
		s.mu.Unlock()
		return core.ErrNotFound
	}
	prefix := ""
	found := false
	for _, t := range r.Status.Runtime.Triggers {
		if t.Webhook == name {
			prefix = t.Prompt
			found = true
			break
		}
	}
	s.mu.Unlock()
	if !found {
		return core.ErrNotFound
	}
	if prefix != "" {
		body = prefix + "\n" + body
	}
	return s.sendTrigger(ctx, agent, "Webhook: "+name, body)
}
