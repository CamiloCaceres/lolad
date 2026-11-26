package msgcli

import (
	"reflect"
	"strings"
	"testing"
)

// What models commonly send in place of the exact form is accepted and
// stored in the exact form; what the spec's fixtures refuse stays refused.
func TestToolInputLenientIn(t *testing.T) {
	cases := []struct {
		name, tool, input string
		check             func(t *testing.T, m userMessage)
	}{
		{"kind in any case", "message_user", `{"kind":" Deliver ","text":"Done"}`, func(t *testing.T, m userMessage) {
			if m.Chat.Kind != "deliver" {
				t.Fatalf("kind %q", m.Chat.Kind)
			}
		}},
		{"ask_user with kind ask", "ask_user", `{"kind":"Ask","text":"Proceed?","options":["Yes","No"]}`, func(t *testing.T, m userMessage) {
			if m.Chat.Kind != "ask" || !reflect.DeepEqual(m.Chat.Options, []string{"Yes", "No"}) {
				t.Fatalf("%+v", m.Chat)
			}
		}},
		{"field type in any case", "ask_user", `{"text":"Where?","fields":[{"label":"City","type":"Text"},{"label":"Notes","type":"LONGTEXT","optional":true}]}`, func(t *testing.T, m userMessage) {
			if m.Chat.Fields[0].Type != "text" || m.Chat.Fields[1].Type != "longtext" {
				t.Fatalf("%+v", m.Chat.Fields)
			}
		}},
		{"link title read as label", "message_user", `{"blocks":[{"type":"Link","title":"Open the page","url":"https://example.com"}]}`, func(t *testing.T, m userMessage) {
			b := m.blocks[0]
			if b.Type != "link" || b.Label != "Open the page" || b.Title != "" || !strings.Contains(m.Body, "Open the page: https://example.com") {
				t.Fatalf("%+v %q", b, m.Body)
			}
		}},
		{"link label wins over title", "message_user", `{"blocks":[{"type":"link","label":"Label","title":"Title","url":"https://example.com"}]}`, func(t *testing.T, m userMessage) {
			if m.blocks[0].Label != "Label" {
				t.Fatalf("%+v", m.blocks[0])
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in, err := decodeToolInput(c.tool, []byte(c.input))
			if err != nil {
				t.Fatal(err)
			}
			m, err := buildUserMessage(c.tool == "ask_user", in)
			if err != nil {
				t.Fatal(err)
			}
			c.check(t, m)
		})
	}
	for _, bad := range []struct{ tool, input string }{
		{"ask_user", `{"kind":"say","text":"Hi"}`},                                                 // wanted message_user
		{"message_user", `{"kind":"ask","text":"Proceed?"}`},                                       // spec: bad-kind
		{"message_user", `{"title":null,"text":"x"}`},                                              // spec: null-title
		{"message_user", `{"blocks":[{"type":"link","url":" https://x.com"}]}`},                    // spec: url-leading-space
		{"message_user", `{"blocks":[{"type":"values","rows":[{"label":"Type","value":"   "}]}]}`}, // spec: a row needs a value
	} {
		in, err := decodeToolInput(bad.tool, []byte(bad.input))
		if err == nil {
			_, err = buildUserMessage(bad.tool == "ask_user", in)
		}
		if err == nil {
			t.Errorf("%s %s: accepted, want refused", bad.tool, bad.input)
		}
	}
}
