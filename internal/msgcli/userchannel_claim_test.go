package msgcli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CamiloCaceres/lolad/internal/core"
)

// One call stops fetching once its time budget is spent: what it prepared
// comes back, the rest stays unread for the next call (nothing is claimed
// and then dropped).
func TestGetUserMessagesBudgetLeavesRestUnread(t *testing.T) {
	p, c, _ := mailbox(t)
	t.Setenv("LOLABOT_HOME", t.TempDir())
	t.Setenv("LOLAD_HARNESS", "claude")
	old := prepareBudget
	prepareBudget = 0
	t.Cleanup(func() { prepareBudget = old })
	for _, body := range []string{"First.", "Second."} {
		if _, err := p.Send(t.Context(), "milo", core.Outgoing{To: "a", Subject: body, Body: body}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond)
	}
	res, err := c.getUserMessages()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "First.") || strings.Contains(res.Text, "Second.") ||
		!strings.HasSuffix(res.Text, "More from the user is waiting: call get_user_messages again.") || res.Structured["more"] != true {
		t.Fatalf("first call:\n%s", res.Text)
	}
	if res, err = c.getUserMessages(); err != nil || !strings.Contains(res.Text, "Second.") || strings.Contains(res.Text, "First.") {
		t.Fatalf("second call: %q %v", res.Text, err)
	}
}

// Codewhale: if the secrets file can't be written, the answer still comes
// back (it is claimed by then), with a note instead of an error.
func TestGetUserMessagesSecretsFileFailureIsSoft(t *testing.T) {
	p, c, _ := mailbox(t)
	home := t.TempDir()
	t.Setenv("LOLABOT_HOME", home)
	t.Setenv("LOLAD_HARNESS", string(core.HarnessCodewhale))
	if err := os.WriteFile(filepath.Join(home, ".env"), []byte("OPENAI_API_KEY=sk-test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// .user-channel is a file, so the secrets folder can't be made.
	if err := os.WriteFile(filepath.Join(home, ".user-channel"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	in, err := decodeToolInput("ask_user", []byte(`{"text":"I need your OpenAI key.","fields":[{"name":"OPENAI_API_KEY","type":"secret"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	m, err := buildUserMessage(true, in)
	if err != nil {
		t.Fatal(err)
	}
	ask, err := c.sendToUser(m)
	if err != nil {
		t.Fatal(err)
	}
	var uc map[string]any
	raw, _ := json.Marshal(core.UserChat{Kind: "answers", Answers: []core.AskAnswer{{Ask: ask, Secrets: []string{"OPENAI_API_KEY"}}}})
	_ = json.Unmarshal(raw, &uc)
	if _, err = p.Send(t.Context(), "milo", core.Outgoing{To: "a", Subject: "Answers", Body: "Answers", Type: "response", InReplyTo: ask, Context: map[string]any{"userchat": uc}}); err != nil {
		t.Fatal(err)
	}
	res, err := c.getUserMessages()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "OPENAI_API_KEY saved") || !strings.Contains(res.Text, "The secrets could not be copied to .user-channel/secrets.env") {
		t.Fatalf("text:\n%s", res.Text)
	}
	if strings.Contains(res.Text, "sk-test") {
		t.Fatal("the secret's value leaked into the result")
	}
	if res, err = c.getUserMessages(); err != nil || !strings.HasPrefix(res.Text, "Nothing new from the user") {
		t.Fatalf("second call: %q %v", res.Text, err)
	}
}
