package deliver

import (
	"strings"
	"testing"

	"github.com/CamiloCaceres/lolad/internal/core"
)

func TestUserNotice(t *testing.T) {
	m := core.Mail{ID: "msg_1700000000_abcdef12", FromName: "milo", Subject: "hi", Body: "hello"}
	got := UserNotice(m)
	if got != "[#abcdef12] The user sent you something. Call get_user_messages to read it." {
		t.Fatalf("notice: %q", got)
	}
	if !strings.HasPrefix(got, Ref(m.ID)) {
		t.Fatal("the notice must start with the ref classify looks for")
	}
	r := (&Router{notifiers: map[core.Harness]*Notifier{core.HarnessClaude: {}}}).WithUserTools(func(core.Agent, core.Mail) bool { return true })
	if r.notifiers[core.HarnessClaude].userTools == nil {
		t.Fatal("WithUserTools must reach every notifier")
	}
}
