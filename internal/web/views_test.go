package web

import (
	"testing"
	"time"

	"github.com/CamiloCaceres/lolad/internal/core"
)

func TestRich(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"plain <b>text</b>", "plain &lt;b&gt;text&lt;/b&gt;"},
		{"run `go test` now", "run <code>go test</code> now"},
		{"**ship** it", "<strong>ship</strong> it"},
		{"see https://lumio.co/pricing.", `see <a href="https://lumio.co/pricing" target="_blank" rel="noopener noreferrer">https://lumio.co/pricing</a>.`},
		{"```go\nfmt.Println(\"<x>\")\n```", `<pre class="code"><code>fmt.Println(&#34;&lt;x&gt;&#34;)</code></pre>`},
		{`javascript:alert(1) "quoted"`, `javascript:alert(1) &#34;quoted&#34;`},
	} {
		if got := string(rich(tc.in)); got != tc.want {
			t.Errorf("rich(%q)\n got %s\nwant %s", tc.in, got, tc.want)
		}
	}
}

func TestUserChatShapes(t *testing.T) {
	want := core.UserChat{Kind: "ask", Options: []string{"a", "b"}}
	for _, ctx := range []map[string]any{
		{"userchat": want},
		{"userchat": map[string]any{"kind": "ask", "options": []any{"a", "b"}}},
		{"userchat": map[string]any{"kind": "ask", "options": []string{"a", "b"}}},
	} {
		got := userChat(core.Mail{Context: ctx})
		if got.Kind != want.Kind || len(got.Options) != 2 || got.Options[1] != "b" {
			t.Errorf("userChat(%v) = %+v", ctx, got)
		}
	}
	if uc := userChat(core.Mail{}); uc.Kind != "" {
		t.Errorf("no context: %+v", uc)
	}
}

func TestShapeAsks(t *testing.T) {
	at := time.Date(2026, 10, 25, 9, 0, 0, 0, time.Local)
	ask := func(id string, min int) msgView {
		return msgView{Mail: core.Mail{ID: id, FromName: "lola", ToName: "milo", Subject: "Q", CreatedAt: at.Add(time.Duration(min) * time.Minute),
			Context: map[string]any{"userchat": map[string]any{"kind": "ask", "options": []string{"yes", "no"}}}}}
	}
	mine := func(id, reply, kind, decision string, min int) msgView {
		return msgView{Mail: core.Mail{ID: id, FromName: "milo", ToName: "lola", InReplyTo: reply, CreatedAt: at.Add(time.Duration(min) * time.Minute), Status: core.MailRead,
			Context: map[string]any{"userchat": map[string]any{"kind": kind, "decision": decision}}}}
	}
	msgs := []msgView{
		ask("a1", 0), mine("m1", "a1", "decision", "yes", 1), // decided
		ask("a2", 2), mine("m2", "a2", "reply", "", 3), // answered in words; buttons stay usable
		ask("a3", 4), // open
	}
	shape(msgs, "milo")
	if !msgs[0].Answered || msgs[0].Answer != "yes" {
		t.Errorf("a1 = answered %v answer %q", msgs[0].Answered, msgs[0].Answer)
	}
	if !msgs[2].Answered || msgs[2].Answer != "" {
		t.Errorf("a2 = answered %v answer %q", msgs[2].Answered, msgs[2].Answer)
	}
	if msgs[4].Answered {
		t.Error("a3 should still be open")
	}
	if msgs[3].Receipt != "Read" || msgs[1].Receipt != "" {
		t.Errorf("receipt only on the user's last message: %q %q", msgs[3].Receipt, msgs[1].Receipt)
	}
	if msgs[0].Day == "" || msgs[1].Day != "" {
		t.Error("day separator only on the first message of a day")
	}
}
