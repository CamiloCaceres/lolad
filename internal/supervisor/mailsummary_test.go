package supervisor

import (
	"strings"
	"testing"

	"github.com/CamiloCaceres/lolad/internal/core"
)

// An agent with the user tools is told to call get_user_messages for the
// user's mail (msg read would mark it read and hide it from the tool);
// other mail still lists msg read.
func TestStopHookSendsUserMailToGetUserMessages(t *testing.T) {
	f := setup(t, func(o *Options) {
		o.UserTools = func(a core.Agent, m core.Mail) bool { return a.Name == "agent" && m.FromName == "milo" }
	})
	f.tick(t)
	f.m.mail["u1"] = core.Mail{ID: "u1", FromName: "milo", ToName: "agent", Subject: "Logo", Status: core.MailUnread}
	f.m.mail["o1"] = core.Mail{ID: "o1", FromName: "iris", ToName: "agent", Subject: "Draft ready", Status: core.MailUnread}
	reply := f.s.HandleHook(ctx, core.HookEvent{Agent: "agent", Event: "stop", Seq: 1})
	if !reply.Block {
		t.Fatal("stop hook did not block")
	}
	for _, want := range []string{
		"The user sent you something. Call get_user_messages to read it (not lolad msg read).",
		"Read it with lolad msg read <id>:\n- o1 from iris: Draft ready",
	} {
		if !strings.Contains(reply.Reason, want) {
			t.Fatalf("reason lacks %q:\n%s", want, reply.Reason)
		}
	}
	if strings.Contains(reply.Reason, "u1") {
		t.Fatalf("user mail listed for msg read:\n%s", reply.Reason)
	}
}

func TestMailSummaryForms(t *testing.T) {
	two := []core.Mail{{ID: "a"}, {ID: "b"}}
	if got := mailSummary(two, nil); got != "The user sent you 2 messages. Call get_user_messages to read them (not lolad msg read).\n" {
		t.Fatalf("%q", got)
	}
	if mailSummary(nil, nil) != "" {
		t.Fatal("empty summary")
	}
}
