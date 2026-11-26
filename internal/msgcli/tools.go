package msgcli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"golang.org/x/text/unicode/norm"

	"github.com/CamiloCaceres/lolad/internal/core"
)

// The user tools: message_user and ask_user (MCP, see mcp.go) and their
// command-line twin, tell-user <kind> --json. Both take toolInput and send the
// same mail: blocks in the userchat context, the same content as text in the
// body. The rules are the user-channel spec's (sections 2.5, 3.2-3.4): a call
// that breaks one is an error the model reads and fixes; nothing else is
// refused.

type toolInput struct {
	Kind    string       `json:"kind,omitempty"` // message_user: say | update | deliver
	Title   string       `json:"title,omitempty"`
	Text    string       `json:"text,omitempty"`
	Blocks  []core.Block `json:"blocks,omitempty"`
	Options []string     `json:"options,omitempty"`
	Fields  []toolField  `json:"fields,omitempty"`
	Task    *toolTask    `json:"task,omitempty"`
	Attach  []string     `json:"attach,omitempty"`
	// present names the keys the call gave: the key, not its value, selects
	// a request's mode (options: [] is an options request with no options).
	present map[string]bool
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
	// blocks are every block of the message as the spec's record has them;
	// Chat.Blocks leaves out a message that is only text.
	blocks []core.Block
}

const (
	maxOptions     = 6
	maxOptionRunes = 60
	maxTitleRunes  = 120
	maxAttach      = 10
)

var toolKeys = map[string][]string{
	"message_user": {"kind", "title", "text", "blocks", "attach"},
	"ask_user":     {"title", "text", "blocks", "attach", "options", "fields", "task"},
}

var blockKeys = map[string][]string{
	core.BlockText:    {"type", "text"},
	core.BlockCommand: {"type", "code", "where", "admin", "title"},
	core.BlockCode:    {"type", "code", "title"},
	core.BlockValues:  {"type", "rows", "title"},
	core.BlockLink:    {"type", "url", "label"},
	core.BlockSteps:   {"type", "items", "title"},
}

var fieldKeys = []string{"name", "label", "type", "options", "optional"}

// nullAt returns where v holds a JSON null ("" when it holds none).
func nullAt(v any, at string) string {
	switch x := v.(type) {
	case nil:
		return cmpOrStr(at, "arguments")
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			p := k
			if at != "" {
				p = at + "." + k
			}
			if s := nullAt(x[k], p); s != "" {
				return s
			}
		}
	case []any:
		for i, e := range x {
			if s := nullAt(e, fmt.Sprintf("%s[%d]", at, i)); s != "" {
				return s
			}
		}
	}
	return ""
}

func cmpOrStr(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

// checkShape enforces the rules on the raw arguments that a Go struct can't
// see: an object, no nulls, no keys a tool or block doesn't define.
func checkShape(tool string, raw []byte) (map[string]bool, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = []byte("{}")
	}
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("arguments: %v", err)
	}
	top, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("arguments must be a JSON object")
	}
	if at := nullAt(v, ""); at != "" {
		return nil, fmt.Errorf("%s: null is not allowed; leave the key out instead", at)
	}
	present := map[string]bool{}
	for k := range top {
		if !slices.Contains(toolKeys[tool], k) {
			switch {
			case tool == "message_user" && (k == "options" || k == "fields" || k == "task"):
				return nil, fmt.Errorf("options, fields and task are for ask_user: use it when you need an answer or need the user to do something")
			case tool == "ask_user" && k == "kind":
				return nil, fmt.Errorf("ask_user takes no kind (to tell the user something, use message_user)")
			}
			return nil, fmt.Errorf("unknown argument %q (%s takes %s)", k, tool, strings.Join(toolKeys[tool], ", "))
		}
		present[k] = true
	}
	if blocks, ok := top["blocks"].([]any); ok {
		for i, b := range blocks {
			m, ok := b.(map[string]any)
			if !ok {
				continue // the struct decode reports the type
			}
			t, _ := m["type"].(string)
			keys, known := blockKeys[t]
			if !known {
				continue // ValidateBlocks reports the type
			}
			for k := range m {
				if !slices.Contains(keys, k) {
					return nil, fmt.Errorf("block %d (%s): unknown key %q (a %s block takes %s)", i+1, t, k, t, strings.Join(keys[1:], ", "))
				}
			}
			rows, _ := m["rows"].([]any)
			for _, r := range rows {
				if rm, ok := r.(map[string]any); ok {
					for k := range rm {
						if k != "label" && k != "value" {
							return nil, fmt.Errorf("block %d (values): unknown key %q in a row (a row takes label, value)", i+1, k)
						}
					}
				}
			}
		}
	}
	if fields, ok := top["fields"].([]any); ok {
		for i, f := range fields {
			fm, ok := f.(map[string]any)
			if !ok {
				continue
			}
			for k := range fm {
				if !slices.Contains(fieldKeys, k) {
					return nil, fmt.Errorf("field %d: unknown key %q (a field takes %s)", i+1, k, strings.Join(fieldKeys, ", "))
				}
			}
			if t, ok := fm["type"]; ok {
				if s, _ := t.(string); s == "" {
					return nil, fmt.Errorf("field %d: type is one of %s (leave it out for text)", i+1, strings.Join(core.FieldTypes, ", "))
				}
			}
		}
	}
	if task, ok := top["task"].(map[string]any); ok {
		for k := range task {
			if k != "done" && k != "failed" {
				return nil, fmt.Errorf("task: unknown key %q (task takes done, failed)", k)
			}
		}
	}
	return present, nil
}

// decodeToolInput reads a tool's arguments strictly, so a misspelt key is an
// error the agent sees instead of something silently dropped.
func decodeToolInput(tool string, raw []byte) (toolInput, error) {
	var in toolInput
	present, err := checkShape(tool, raw)
	if err != nil {
		return in, err
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = []byte("{}")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return in, fmt.Errorf("arguments: %v", err)
	}
	in.present = present
	return in, nil
}

func oneLineRunes(s string, max int) bool {
	return core.OneLine(s) && utf8.RuneCountInString(strings.TrimSpace(s)) <= max
}

// buildUserMessage checks a tool call and lays out its mail. ask selects
// ask_user; otherwise in.Kind (say by default) is the message kind.
func buildUserMessage(ask bool, in toolInput) (userMessage, error) {
	var m userMessage
	kind := "ask"
	if !ask {
		kind = in.Kind
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
	} else if in.Kind != "" {
		return m, fmt.Errorf("ask_user takes no kind (to tell the user something, use message_user)")
	}
	if !oneLineRunes(in.Title, maxTitleRunes) {
		return m, fmt.Errorf("title: one line of at most %d characters", maxTitleRunes)
	}
	title := strings.TrimSpace(in.Title)
	var blocks []core.Block
	if !core.Blank(in.Text) {
		blocks = append(blocks, core.Block{Type: core.BlockText, Text: in.Text})
	}
	blocks = append(blocks, in.Blocks...)
	if len(blocks) == 0 {
		return m, fmt.Errorf("give text or blocks")
	}
	if len(blocks) > core.MaxBlocks {
		return m, fmt.Errorf("at most %d blocks, counting the one made from text", core.MaxBlocks)
	}
	if err := core.ValidateBlocks(blocks); err != nil {
		return m, err
	}
	if in.present["attach"] && (len(in.Attach) == 0 || len(in.Attach) > maxAttach) {
		return m, fmt.Errorf("attach: give 1 to %d file paths", maxAttach)
	}
	m.Chat = core.UserChat{Kind: kind, Title: title}
	if ask {
		n := 0
		for _, k := range []string{"options", "fields", "task"} {
			if in.present[k] || (k == "task" && in.Task != nil) || (k == "options" && len(in.Options) > 0) || (k == "fields" && len(in.Fields) > 0) {
				n++
			}
		}
		if n > 1 {
			return m, fmt.Errorf("use one of options, fields or task (for a choice inside a form, use a field of type choice)")
		}
		switch {
		case in.Task != nil:
			done, failed := "Done", "I couldn't do it"
			for _, l := range []struct {
				v   string
				def *string
			}{{in.Task.Done, &done}, {in.Task.Failed, &failed}} {
				if !oneLineRunes(l.v, maxOptionRunes) {
					return m, fmt.Errorf("task: done and failed are one line of at most %d characters", maxOptionRunes)
				}
				if t := strings.TrimSpace(l.v); t != "" {
					*l.def = t
				}
			}
			if done == failed {
				return m, fmt.Errorf("task: done and failed need different labels")
			}
			m.Chat.Options, m.Chat.Task = []string{done, failed}, true
		case in.present["options"] || len(in.Options) > 0:
			if len(in.Options) < 2 || len(in.Options) > maxOptions {
				return m, fmt.Errorf("options: give 2 to %d buttons (for one thing to confirm, use task)", maxOptions)
			}
			seen := map[string]bool{}
			for _, o := range in.Options {
				t := strings.TrimSpace(o)
				if t == "" || !oneLineRunes(o, maxOptionRunes) || seen[t] {
					return m, fmt.Errorf("options: each a different, non-blank label of one line and at most %d characters, consequence included", maxOptionRunes)
				}
				seen[t] = true
				m.Chat.Options = append(m.Chat.Options, t)
			}
		case in.present["fields"] || len(in.Fields) > 0:
			if len(in.Fields) == 0 {
				return m, fmt.Errorf("fields: give 1 to %d fields", core.MaxAskFields)
			}
			fields, err := toolFields(in.Fields)
			if err != nil {
				return m, err
			}
			m.Chat.Fields = fields
		}
	}
	m.blocks = blocks
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

// toolFields checks a request's form (spec 3.3) and names every field.
func toolFields(in []toolField) ([]core.AskField, error) {
	if len(in) > core.MaxAskFields {
		return nil, fmt.Errorf("fields: at most %d", core.MaxAskFields)
	}
	out := make([]core.AskField, len(in))
	explicit := map[string]bool{}
	for i, tf := range in {
		f := core.AskField{Type: tf.Type, Optional: tf.Optional}
		if f.Type == "" {
			f.Type = core.FieldText
		}
		if !slices.Contains(core.FieldTypes, f.Type) {
			return nil, fmt.Errorf("field %d: unknown type %q (use %s)", i+1, f.Type, strings.Join(core.FieldTypes, ", "))
		}
		if !oneLineRunes(tf.Label, core.MaxFieldLabel) {
			return nil, fmt.Errorf("field %d: a label is one line of at most %d characters", i+1, core.MaxFieldLabel)
		}
		f.Label = strings.TrimSpace(tf.Label)
		name := strings.TrimSpace(tf.Name)
		switch {
		case f.Type == core.FieldSecret:
			if !core.ValidEnvKey(name) {
				return nil, fmt.Errorf("field %d: a secret needs name set to the environment variable it is saved as, e.g. OPENAI_API_KEY (letters, digits, _; at most 128)", i+1)
			}
		case name == "" && f.Label == "":
			return nil, fmt.Errorf("field %d needs a name or a label", i+1)
		case name != "" && (utf8.RuneCountInString(name) > core.MaxFieldName || strings.ContainsFunc(name, unicode.IsSpace)):
			return nil, fmt.Errorf("field %d: a name is at most %d characters, without spaces", i+1, core.MaxFieldName)
		}
		if name != "" {
			if explicit[name] {
				return nil, fmt.Errorf("field %d: two fields are named %q", i+1, name)
			}
			explicit[name] = true
		}
		f.Name = name
		if f.Type != core.FieldChoice && len(tf.Options) > 0 {
			return nil, fmt.Errorf("field %d: only a choice takes options", i+1)
		}
		if f.Type == core.FieldChoice {
			if len(tf.Options) < 2 || len(tf.Options) > core.MaxChoiceItems {
				return nil, fmt.Errorf("field %d: a choice needs 2 to %d options", i+1, core.MaxChoiceItems)
			}
			seen := map[string]bool{}
			for _, o := range tf.Options {
				t := strings.TrimSpace(o)
				if t == "" || !oneLineRunes(o, core.MaxFieldLabel) || seen[t] {
					return nil, fmt.Errorf("field %d: each option a different, non-blank line of at most %d characters", i+1, core.MaxFieldLabel)
				}
				seen[t] = true
				f.Options = append(f.Options, t)
			}
		}
		out[i] = f
	}
	taken := explicit
	for i := range out {
		if out[i].Name == "" {
			out[i].Name = deriveName(out[i].Label, taken)
			taken[out[i].Name] = true
		}
	}
	return out, core.ValidateAskFields(out)
}

var lowerAll = cases.Lower(language.Und)

// deriveName makes a field name from its label (spec 3.3): NFC, full
// lowercasing, runs of anything but letters and digits as one _, at most 56
// characters, "field" when nothing is left, _2, _3… when taken.
// "Street address" → street_address.
func deriveName(label string, taken map[string]bool) string {
	var b strings.Builder
	under := false
	for _, r := range lowerAll.String(norm.NFC.String(label)) {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			if under && b.Len() > 0 {
				b.WriteByte('_')
			}
			b.WriteRune(r)
			under = false
			continue
		}
		under = true
	}
	name := b.String()
	if r := []rune(name); len(r) > 56 {
		name = strings.TrimRight(string(r[:56]), "_")
	}
	if name == "" {
		name = "field"
	}
	if !taken[name] {
		return name
	}
	for k := 2; ; k++ {
		if s := fmt.Sprintf("%s_%d", name, k); !taken[s] {
			return s
		}
	}
}

// fieldName is deriveName with nothing taken (tell-user --field).
func fieldName(label string) string { return deriveName(label, nil) }

// resolveAttach resolves the files a send attaches, the same way for every
// path (MCP tools, tell-user, send, reply): a relative path is taken from
// base (the session's working directory), an absolute path is used as is,
// and each must be an existing regular file. There is no folder limit: the
// agent may already read and send any file it can open, and the recipient is
// its user or a contact policy allows.
func resolveAttach(base string, paths []string) ([]string, error) {
	out := make([]string, len(paths))
	for i, p := range paths {
		if core.Blank(p) {
			return nil, fmt.Errorf("attach: path %d is empty", i+1)
		}
		if !filepath.IsAbs(p) {
			p = filepath.Join(base, p)
		}
		st, err := os.Stat(p)
		switch {
		case err != nil:
			return nil, fmt.Errorf("attach: %s doesn't exist (relative paths start at %s)", paths[i], base)
		case st.IsDir():
			return nil, fmt.Errorf("attach: %s is a folder; zip it first", paths[i])
		case !st.Mode().IsRegular():
			return nil, fmt.Errorf("attach: %s is not a regular file", paths[i])
		}
		out[i] = filepath.Clean(p)
	}
	return out, nil
}

// attachBase is where relative attach paths start: the working directory,
// else the agent's folder.
func attachBase() string {
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return agentHome()
}
