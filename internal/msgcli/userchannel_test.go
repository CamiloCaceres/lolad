package msgcli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/CamiloCaceres/lolad/internal/core"
)

// The user-channel spec's fixtures (copied from its fixtures/ folder into
// testdata/userchannel): every valid tool call builds the spec's record,
// every invalid one is refused, and the get_user_messages forms match
// exactly.

func canon(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err = json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// recordOf is a built message as the spec's record (2.3, 3.5).
func recordOf(m userMessage) map[string]any {
	r := map[string]any{"kind": m.Chat.Kind, "blocks": m.blocks, "text": m.Body}
	if m.Chat.Title != "" {
		r["title"] = m.Chat.Title
	}
	if m.Chat.Kind == "ask" {
		switch {
		case m.Chat.Task:
			r["ask"] = map[string]any{"task": map[string]any{"done": m.Chat.Options[0], "failed": m.Chat.Options[1]}}
		case len(m.Chat.Options) > 0:
			r["ask"] = map[string]any{"options": m.Chat.Options}
		case len(m.Chat.Fields) > 0:
			r["ask"] = map[string]any{"fields": m.Chat.Fields}
		default:
			r["ask"] = map[string]any{}
		}
	}
	return r
}

func fixtureFiles(t *testing.T, dirs ...string) []string {
	t.Helper()
	var out []string
	for _, d := range dirs {
		files, err := filepath.Glob(filepath.Join("testdata", "userchannel", d, "*.json"))
		if err != nil || len(files) == 0 {
			t.Fatalf("no fixtures in %s: %v", d, err)
		}
		out = append(out, files...)
	}
	return out
}

func TestUserChannelToolFixtures(t *testing.T) {
	for _, path := range fixtureFiles(t, "valid", "invalid") {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var fx struct {
			Name, Tool   string
			Capabilities []string
			Input        json.RawMessage
			Valid        bool
			Expect       map[string]any
			Reason       string
		}
		if err = json.Unmarshal(b, &fx); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		t.Run(fx.Name, func(t *testing.T) {
			if fx.Capabilities != nil && !(slices.Contains(fx.Capabilities, "secret") && slices.Contains(fx.Capabilities, "file") && slices.Contains(fx.Capabilities, "attach")) {
				t.Skip("lolad offers every capability")
			}
			var m userMessage
			var err error
			if fx.Tool == "get_user_messages" {
				if !noArguments(fx.Input) {
					err = os.ErrInvalid
				}
			} else {
				var in toolInput
				if in, err = decodeToolInput(fx.Tool, fx.Input); err == nil {
					m, err = buildUserMessage(fx.Tool == "ask_user", in)
				}
			}
			if !fx.Valid {
				if err == nil {
					t.Fatalf("accepted; want refused: %s", fx.Reason)
				}
				return
			}
			if err != nil {
				t.Fatalf("refused: %v", err)
			}
			if got, want := canon(t, recordOf(m)), canon(t, fx.Expect); !reflect.DeepEqual(got, want) {
				gb, _ := json.Marshal(got)
				wb, _ := json.Marshal(want)
				t.Fatalf("record\n got: %s\nwant: %s", gb, wb)
			}
		})
	}
}

func TestUserChannelTextForm(t *testing.T) {
	// The fixtures carry the spec's POSIX hint for the secrets file.
	old := secretsGOOS
	secretsGOOS = "linux"
	t.Cleanup(func() { secretsGOOS = old })
	for _, path := range fixtureFiles(t, "get_user_messages") {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var fx struct {
			Name     string
			Secrets  string
			Open     int
			Requests []struct {
				ID, Title, Text string
				Ask             struct {
					Task   *toolTask
					Fields []core.AskField
				}
			}
			Items []struct {
				Type   string
				Record json.RawMessage
			}
			Expect struct {
				Structured any
				Text       string
			}
		}
		if err = json.Unmarshal(b, &fx); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		t.Run(fx.Name, func(t *testing.T) {
			reqs := map[string]umRequest{}
			for _, r := range fx.Requests {
				reqs[r.ID] = umRequest{Title: r.Title, Text: r.Text, Task: r.Ask.Task != nil, Fields: r.Ask.Fields}
			}
			var items []umItem
			for _, it := range fx.Items {
				if it.Type == "answer" {
					var a umAnswer
					if err := json.Unmarshal(it.Record, &a); err != nil {
						t.Fatal(err)
					}
					a.Question = question(reqs[a.Ask])
					items = append(items, umItem{answer: &a})
				} else {
					var m umMessage
					if err := json.Unmarshal(it.Record, &m); err != nil {
						t.Fatal(err)
					}
					items = append(items, umItem{message: &m})
				}
			}
			res := assemble(reqs, items, fx.Open, fx.Secrets)
			if res.Text != fx.Expect.Text {
				t.Fatalf("text\n got: %q\nwant: %q", res.Text, fx.Expect.Text)
			}
			if got, want := canon(t, res.Structured), canon(t, fx.Expect.Structured); !reflect.DeepEqual(got, want) {
				gb, _ := json.Marshal(got)
				wb, _ := json.Marshal(want)
				t.Fatalf("structured\n got: %s\nwant: %s", gb, wb)
			}
		})
	}
}

// TestGetUserMessages: the agent asks, the user answers and writes; one call
// returns both with the files saved; the next returns nothing new; two
// readers racing for a new message get it once between them.
func TestGetUserMessages(t *testing.T) {
	p, c, _ := mailbox(t)
	home := t.TempDir()
	t.Setenv("LOLABOT_HOME", home)
	t.Setenv("LOLAD_HARNESS", "claude")
	ctx := t.Context()
	send := func(ask bool, args string) string {
		t.Helper()
		tool := "message_user"
		if ask {
			tool = "ask_user"
		}
		in, err := decodeToolInput(tool, []byte(args))
		if err != nil {
			t.Fatal(err)
		}
		m, err := buildUserMessage(ask, in)
		if err != nil {
			t.Fatal(err)
		}
		id, err := c.sendToUser(m)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	dns := send(true, `{"title":"Add one DNS record","blocks":[{"type":"values","rows":[{"label":"Type","value":"A"}]}],"task":{"done":"Added"}}`)
	ship := send(true, `{"text":"Where do I ship the books?","fields":[{"label":"City"},{"label":"Screenshot","type":"file","optional":true},{"name":"SHIPPO_API_KEY","type":"secret"}]}`)
	send(true, `{"text":"Blue or green?","options":["Blue","Green"]}`)
	att, err := p.UploadAttachment(ctx, "milo", "shot.png", "image/png", strings.NewReader(pngBytes))
	if err != nil {
		t.Fatal(err)
	}
	answers := core.UserChat{Kind: "answers", Answers: []core.AskAnswer{
		{Ask: dns, Decision: "I couldn't do it", Text: "There is no A option."},
		{Ask: ship, Values: map[string]string{"city": "Bogotá", "screenshot": "shot.png"}, Secrets: []string{"SHIPPO_API_KEY"}, Files: map[string][]string{"screenshot": {att.ID}}},
	}}
	ctxMap := map[string]any{}
	raw, _ := json.Marshal(answers)
	var ucMap map[string]any
	_ = json.Unmarshal(raw, &ucMap)
	ctxMap["userchat"] = ucMap
	if _, err = p.Send(ctx, "milo", core.Outgoing{To: "a", Subject: "Answers to 2 requests", Body: "Answers", Type: "response", InReplyTo: ship, Context: ctxMap, Attachments: []string{att.ID}}); err != nil {
		t.Fatal(err)
	}
	if _, err = p.Send(ctx, "milo", core.Outgoing{To: "a", Subject: "Also", Body: "Also, use the blue logo."}); err != nil {
		t.Fatal(err)
	}
	res, err := c.getUserMessages()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"From the user (1 message, 2 answers; 1 request still open):",
		"Answer to \"Add one DNS record\" (request " + dns + "):\n   Result: I couldn't do it\n   There is no A option.",
		"Answer to \"Where do I ship the books?\" (request " + ship + "):\n   City: Bogotá\n   Screenshot: .user-channel/files/" + att.ID + "/shot.png\n   SHIPPO_API_KEY saved (in your environment)",
		"   Also, use the blue logo.",
	} {
		if !strings.Contains(res.Text, want) {
			t.Fatalf("text lacks %q:\n%s", want, res.Text)
		}
	}
	got := canon(t, res.Structured).(map[string]any)
	first := got["answers"].([]any)[0].(map[string]any)
	if first["outcome"] != "failed" || first["question"] != "Add one DNS record" || got["open"].(float64) != 1 {
		t.Fatalf("structured: %v", got)
	}
	second := got["answers"].([]any)[1].(map[string]any)
	if v := second["values"].(map[string]any); len(v) != 1 || v["city"] != "Bogotá" {
		t.Fatalf("values must hold only value fields: %v", second)
	}
	if b, err := os.ReadFile(filepath.Join(home, ".user-channel", "files", att.ID, "shot.png")); err != nil || string(b) != pngBytes {
		t.Fatalf("saved file: %q %v", b, err)
	}
	if res, err = c.getUserMessages(); err != nil || res.Text != "Nothing new from the user (1 request still open)." {
		t.Fatalf("second call: %q %v", res.Text, err)
	}
	// Two readers, one new message: exactly one of them returns it.
	if _, err = p.Send(ctx, "milo", core.Outgoing{To: "a", Subject: "Race", Body: "Only once."}); err != nil {
		t.Fatal(err)
	}
	other := *c
	var wg sync.WaitGroup
	results := make([]umResult, 2)
	for i, cl := range []*client{c, &other} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := cl.getUserMessages()
			if err != nil {
				t.Error(err)
			}
			results[i] = r
		}()
	}
	wg.Wait()
	if n := strings.Count(results[0].Text+results[1].Text, "Only once."); n != 1 {
		t.Fatalf("the message came back %d times:\n%s\n---\n%s", n, results[0].Text, results[1].Text)
	}
}

// pngBytes starts like a real PNG, so the upload scan passes it.
const pngBytes = "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR png bytes"

func TestSafeNameAndSecretsFile(t *testing.T) {
	for in, want := range map[string]string{"a/b.png": "a_b.png", ". .x": "x", "...": "file", "re:port?.pdf": "re_port_.pdf", "  name.txt.  ": "name.txt"} {
		if got := safeName(in); got != want {
			t.Errorf("safeName(%q) = %q, want %q", in, got, want)
		}
	}
	home := t.TempDir()
	// An agent session sets LOLAD_ENV_FILE to its own env file; the test
	// reads home/.env, so clear it.
	t.Setenv("LOLAD_ENV_FILE", "")
	if err := os.WriteFile(filepath.Join(home, ".env"), []byte("A=plain\nB=\"it's \\\"x\\\"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeSecretsFile(home, []string{"A", "B", "MISSING"}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(home, secretsFilePath))
	if err != nil || string(b) != "export A='plain'\nexport B='it'\\''s \"x\"'\n" {
		t.Fatalf("secrets file: %q %v", b, err)
	}
	if st, _ := os.Stat(filepath.Join(home, secretsFilePath)); st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode())
	}
}
