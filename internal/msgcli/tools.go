package msgcli

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/CamiloCaceres/lolad/internal/core"
)

// The user tools: message_user and ask_user (MCP, see mcp.go) and their
// command-line twin, tell-user <kind> --json. Both take toolInput and send the
// same mail: blocks in the userchat context, the same content as text in the
// body.

type toolInput struct {
	Kind    string       `json:"kind,omitempty"` // message_user: say | update | deliver
	Title   string       `json:"title,omitempty"`
	Text    string       `json:"text,omitempty"`
	Blocks  []core.Block `json:"blocks,omitempty"`
	Options []string     `json:"options,omitempty"`
	Fields  []toolField  `json:"fields,omitempty"`
	Task    *toolTask    `json:"task,omitempty"`
	Attach  []string     `json:"attach,omitempty"`
}

type toolField struct {
	Name     string   `json:"name,omitempty"`
	Label    string   `json:"label,omitempty"`
	Type     string   `json:"type,omitempty"`
	Options  []string `json:"options,omitempty"`
	Optional bool     `json:"optional,omitempty"`
}

type toolTask struct {
	Done   string `json:"done,omitempty"`
	Failed string `json:"failed,omitempty"`
}

// userMessage is a tool call turned into mail for the user.
type userMessage struct {
	Subject, Body, Type string
	Chat                core.UserChat
	Attach              []string
}

const (
	maxOptions     = 6
	maxOptionRunes = 60
	maxTitleRunes  = 120
)

// decodeToolInput reads a tool's arguments strictly, so a misspelt key is an
// error the agent sees instead of something silently dropped.
func decodeToolInput(raw []byte) (toolInput, error) {
	var in toolInput
	if len(strings.TrimSpace(string(raw))) == 0 {
		return in, fmt.Errorf("arguments required")
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return in, fmt.Errorf("arguments: %v", err)
	}
	return in, nil
}

// buildUserMessage checks a tool call and lays out its mail. ask selects
// ask_user; otherwise in.Kind (say by default) is the message kind.
func buildUserMessage(ask bool, in toolInput) (userMessage, error) {
	var m userMessage
	kind := "ask"
	if !ask {
		kind = strings.ToLower(strings.TrimSpace(in.Kind))
		if kind == "" {
			kind = "say"
		}
		switch kind {
		case "say", "update", "deliver":
		default:
			return m, fmt.Errorf("kind must be say, update or deliver (to ask something, use ask_user)")
		}
		if len(in.Options) > 0 || len(in.Fields) > 0 || in.Task != nil {
			return m, fmt.Errorf("options, fields and task are for ask_user: use it when you need an answer or need the user to do something")
		}
	} else if in.Kind != "" && in.Kind != "ask" {
		return m, fmt.Errorf("ask_user takes no kind")
	}
	blocks := in.Blocks
	if t := strings.TrimSpace(in.Text); t != "" {
		blocks = append([]core.Block{{Type: core.BlockText, Text: t}}, blocks...)
	}
	if len(blocks) == 0 {
		return m, fmt.Errorf("give text or blocks")
	}
	if err := core.ValidateBlocks(blocks); err != nil {
		return m, err
	}
	title := strings.TrimSpace(in.Title)
	if utf8.RuneCountInString(title) > maxTitleRunes {
		return m, fmt.Errorf("title: at most %d characters", maxTitleRunes)
	}
	m.Chat = core.UserChat{Kind: kind}
	if ask {
		n := 0
		for _, set := range []bool{len(in.Options) > 0, len(in.Fields) > 0, in.Task != nil} {
			if set {
				n++
			}
		}
		if n > 1 {
			return m, fmt.Errorf("use one of options, fields or task (for a choice inside a form, use a field of type choice)")
		}
		switch {
		case in.Task != nil:
			done := strings.TrimSpace(in.Task.Done)
			if done == "" {
				done = "Done"
			}
			failed := strings.TrimSpace(in.Task.Failed)
			if failed == "" {
				failed = "I couldn't do it"
			}
			if done == failed {
				return m, fmt.Errorf("task: done and failed need different labels")
			}
			m.Chat.Options, m.Chat.Task = []string{done, failed}, true
		case len(in.Options) > 0:
			if len(in.Options) < 2 || len(in.Options) > maxOptions {
				return m, fmt.Errorf("options: give 2 to %d buttons (for one thing to confirm, use task)", maxOptions)
			}
			seen := map[string]bool{}
			for _, o := range in.Options {
				o = strings.TrimSpace(o)
				if o == "" || utf8.RuneCountInString(o) > maxOptionRunes || seen[o] {
					return m, fmt.Errorf("options: each a short, different label (at most %d characters)", maxOptionRunes)
				}
				seen[o] = true
				m.Chat.Options = append(m.Chat.Options, o)
			}
		case len(in.Fields) > 0:
			fields, err := toolFields(in.Fields)
			if err != nil {
				return m, err
			}
			m.Chat.Fields = fields
		}
	}
	// A message that is only text stays plain: the body says it all.
	if len(blocks) > 1 || blocks[0].Type != core.BlockText {
		m.Chat.Blocks = blocks
	}
	m.Body = core.BlocksText(blocks)
	m.Subject = title
	for _, b := range blocks {
		if m.Subject == "" {
			m.Subject = subjectFromText(b.Text) // the first text, else the first heading
		}
		if m.Subject == "" && b.Type != core.BlockLink {
			m.Subject = subjectFromText(b.Title)
		}
	}
	if m.Subject == "" {
		m.Subject = subjectFromText(m.Body)
	}
	m.Type = map[string]string{"ask": "request", "update": "update"}[kind]
	if m.Type == "" {
		m.Type = "notification"
	}
	m.Attach = in.Attach
	return m, nil
}

func toolFields(in []toolField) ([]core.AskField, error) {
	var out []core.AskField
	names := map[string]bool{}
	for i, tf := range in {
		f := core.AskField{Label: strings.TrimSpace(tf.Label), Type: strings.ToLower(strings.TrimSpace(tf.Type)), Optional: tf.Optional}
		if f.Type == "" {
			f.Type = core.FieldText
		}
		name := strings.TrimSpace(tf.Name)
		if f.Type == core.FieldSecret {
			if !core.ValidEnvKey(name) {
				return nil, fmt.Errorf("field %d: a secret needs name set to the env var it is saved as, e.g. OPENAI_API_KEY", i+1)
			}
		} else {
			if f.Label == "" && name == "" {
				return nil, fmt.Errorf("field %d needs a label", i+1)
			}
			if name == "" {
				name = fieldName(f.Label)
			}
			for base, k := name, 2; names[name]; k++ {
				name = fmt.Sprintf("%s_%d", base, k)
			}
		}
		if names[name] {
			return nil, fmt.Errorf("field %d: the name %q is used twice", i+1, name)
		}
		names[name], f.Name = true, name
		for _, o := range tf.Options {
			if o = strings.TrimSpace(o); o != "" {
				f.Options = append(f.Options, o)
			}
		}
		if len(f.Options) > 0 && f.Type != core.FieldChoice {
			return nil, fmt.Errorf("field %d: only a choice takes options", i+1)
		}
		out = append(out, f)
	}
	return out, core.ValidateAskFields(out)
}
