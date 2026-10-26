package fake

import (
	"fmt"
	"slices"
	"time"

	"github.com/CamiloCaceres/lolad/internal/core"
)

// seedTrafficAudit adds volume, a burst of acknowledgements, delayed mail and
// denials without changing the inbox's human decision fixtures.
func (s *Service) seedTrafficAudit() {
	now := s.now()
	for i := 0; i < 78; i++ {
		from, to := "forge", "patch"
		subject, body := "Invoice export: verification", "Export totals match the ledger. Ready for the next review."
		switch i % 4 {
		case 1:
			from, to = "patch", "forge"
		case 2:
			from, to = "juno", "quill"
			subject, body = "Launch copy review", "Revised the plan descriptions and sent the draft for review."
		case 3:
			from, to = "lola", "ledger"
			subject, body = "Reconcile ops invoices", "Please reconcile this week's invoices when your mail trigger is back."
		}
		status := core.MailRead
		if i%4 == 3 {
			status = core.MailUnread
		}
		if i < 12 {
			from, to = "forge", "patch"
			if i%2 == 0 {
				from, to = to, from
			}
			subject, body = "Re: Verification received", "Acknowledged. Thanks for confirming."
		}
		at := now.Add(-time.Duration(i*3+2) * time.Minute)
		id := fmt.Sprintf("audit_seed_%03d", i)
		m := core.Mail{ID: id, ThreadID: id, From: s.addr(from), To: s.addr(to), FromName: from, ToName: to, Subject: subject, Body: body, Type: "notification", Priority: "normal", Status: status, CreatedAt: at, Signed: true, Verified: true}
		if status == core.MailRead {
			m.NotifiedAt = at.Add(time.Second * 10)
			m.ReadAt = at.Add(time.Second * 40)
		} else {
			m.Attempts = 3
		}
		s.mail = append(s.mail, m)
	}
	slices.SortStableFunc(s.mail, func(a, b core.Mail) int { return a.CreatedAt.Compare(b.CreatedAt) })
	for i := 0; i < 8; i++ {
		s.seq++
		s.events = append(s.events, core.Event{ID: s.seq, At: now.Add(-time.Duration(i*14+1) * time.Minute), Agent: "scout", Kind: "policy", Detail: "scout → forge: rule 2 denies (scout→team:eng)"})
	}
}
