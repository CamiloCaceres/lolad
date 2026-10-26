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

func TestMarkdown(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"# Title", "<h1>Title</h1>\n"},
		{"one\ntwo\n\nthree", "<p>one two</p>\n<p>three</p>\n"},
		{"- a\n- **b**", "<ul>\n<li>a</li>\n<li><strong>b</strong></li>\n</ul>\n"},
		{"1. x\n2. y", "<ol>\n<li>x</li>\n<li>y</li>\n</ol>\n"},
		{"```\n<b>\n```", "<pre class=\"code\"><code>&lt;b&gt;</code></pre>\n"},
		{"> quoted", "<blockquote><p>quoted</p>\n</blockquote>\n"},
		{"| a | b |\n|---|---|\n| 1 | `2` |", "<div class=\"md-table\"><table><thead><tr><th>a</th><th>b</th></tr></thead><tbody>\n<tr><td>1</td><td><code>2</code></td></tr>\n</tbody></table></div>\n"},
		{"<img src=x onerror=alert(1)>", "<p>&lt;img src=x onerror=alert(1)&gt;</p>\n"},
		{"[ok](https://lumio.co) [bad](javascript:alert(1))", `<p><a href="https://lumio.co" target="_blank" rel="noopener noreferrer">ok</a> bad <span class="md-ref">(javascript:alert(1)</span>)</p>` + "\n"},
		{"- [x] done", "<ul>\n<li class=\"task\">☑ done</li>\n</ul>\n"},
	} {
		if got := string(markdown(tc.in)); got != tc.want {
			t.Errorf("markdown(%q)\n got %q\nwant %q", tc.in, got, tc.want)
		}
	}
}
