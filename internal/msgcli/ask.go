package msgcli

import (
	"fmt"
	"slices"
	"strings"

	"github.com/CamiloCaceres/lolad/internal/core"
)

// askFields turns tell-user ask's --field and --secret flags into the
// request's form.
//
//	--field "Label"                 one line of text
//	--field "Label:longtext"        also number, date
//	--field "Size:choice:S|M|L"     one of the options
//	--field "Notes?:longtext"       a trailing ? makes the field optional
//	--secret OPENAI_API_KEY         saved to the agent's env file, never in mail
//	--secret "OPENAI_API_KEY:OpenAI API key"
func askFields(fieldSpecs, secretSpecs []string) ([]core.AskField, error) {
	var out []core.AskField
	names := map[string]bool{}
	for _, spec := range fieldSpecs {
		f, err := parseField(spec)
		if err != nil {
			return nil, err
		}
		base, name := f.Name, f.Name
		for i := 2; names[name]; i++ {
			name = fmt.Sprintf("%s_%d", base, i)
		}
		f.Name, names[name] = name, true
		out = append(out, f)
	}
	for _, spec := range secretSpecs {
		key, label, _ := strings.Cut(strings.TrimSpace(spec), ":")
		key, label = strings.TrimSpace(key), strings.TrimSpace(label)
		if !core.ValidEnvKey(key) {
			return nil, fmt.Errorf("--secret %q: use an env var name such as OPENAI_API_KEY (letters, digits, _), optionally followed by :Label", spec)
		}
		if names[key] {
			return nil, fmt.Errorf("--secret %q: the name is used twice", key)
		}
		names[key] = true
		out = append(out, core.AskField{Name: key, Label: label, Type: core.FieldSecret})
	}
	return out, core.ValidateAskFields(out)
}

// parseField reads "Label[?][:type[:a|b|c]]".
func parseField(spec string) (core.AskField, error) {
	parts := strings.SplitN(spec, ":", 3)
	label := strings.TrimSpace(parts[0])
	f := core.AskField{Type: core.FieldText}
	if l, ok := strings.CutSuffix(label, "?"); ok {
		label, f.Optional = strings.TrimSpace(l), true
	}
	if label == "" {
		return f, fmt.Errorf("--field %q: start with a label, e.g. --field \"City\" or --field \"Size:choice:S|M|L\"", spec)
	}
	f.Label, f.Name = label, fieldName(label)
	if len(parts) > 1 {
		f.Type = strings.ToLower(strings.TrimSpace(parts[1]))
	}
	if f.Type == core.FieldSecret {
		return f, fmt.Errorf("--field %q: ask for secrets with --secret ENV_NAME so the value never travels in mail", spec)
	}
	if !slices.Contains(core.FieldTypes, f.Type) {
		return f, fmt.Errorf("--field %q: unknown type %q (text, longtext, number, date, logs, file, choice)", spec, f.Type)
	}
	if len(parts) == 3 {
		if f.Type != core.FieldChoice {
			return f, fmt.Errorf("--field %q: only a choice takes options", spec)
		}
		for _, o := range strings.Split(parts[2], "|") {
			if o = strings.TrimSpace(o); o != "" {
				f.Options = append(f.Options, o)
			}
		}
	}
	if f.Type == core.FieldChoice && len(f.Options) < 2 {
		return f, fmt.Errorf("--field %q: list the options, e.g. \"Size:choice:S|M|L\"", spec)
	}
	return f, nil
}
