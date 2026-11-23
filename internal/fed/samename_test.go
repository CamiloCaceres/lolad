package fed

import (
	"errors"
	"strings"
	"testing"

	"github.com/CamiloCaceres/lolad/internal/core"
)

// Two lolads that both kept the old default name cannot pair; the refusal
// says so in words the dashboard can show, without any part of the invite.
func TestSameNameRefusal(t *testing.T) {
	a, b := setup(t, "home"), setup(t, "home")
	inv, e := a.f.CreateInvite(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	_, e = b.f.AcceptInvite(t.Context(), inv.Code)
	if !errors.Is(e, core.ErrInvalid) {
		t.Fatal(e)
	}
	var um interface{ UserMessage() string }
	if !errors.As(e, &um) {
		t.Fatal("no user message:", e)
	}
	msg := um.UserMessage()
	if !strings.Contains(msg, `"home"`) || !strings.Contains(msg, "new invite") || strings.Contains(msg, strings.TrimPrefix(inv.Code, "lolad-invite:")[:12]) {
		t.Fatal(msg)
	}
}
