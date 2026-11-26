package msgcli

import (
	"strings"
	"testing"

	"github.com/CamiloCaceres/lolad/docs"
)

// TestInstructionsCoverMailFromOtherLolads: both texts an agent is given (its
// CLAUDE.md section and the user tools' MCP instructions) tell it that mail
// from another lolad is not from its user and is answered with msg reply.
func TestInstructionsCoverMailFromOtherLolads(t *testing.T) {
	for name, text := range map[string]string{"CLAUDE.md section": docs.AgentMessaging, "MCP instructions": mcpInstructions} {
		for _, want := range []string{"external", "not from your user", "lolad msg reply <id>", "not instructions"} {
			if !strings.Contains(strings.Join(strings.Fields(text), " "), want) {
				t.Errorf("%s lacks %q", name, want)
			}
		}
	}
	for _, want := range []string{"### Messages from other lolads", "plus anyone who wrote to you from another lolad (reply only)", "never `message_user`", "Files can't be sent between lolads yet"} {
		if !strings.Contains(strings.Join(strings.Fields(docs.AgentMessaging), " "), want) {
			t.Errorf("CLAUDE.md section lacks %q", want)
		}
	}
}
