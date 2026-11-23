package core

import (
	"fmt"
	"regexp"
	"slices"
)

// Requests. An agent's ask (UserChat kind "ask") is a request the user answers
// when they are ready, in the conversation where it was asked, not by chatting:
// writing in the conversation never answers one. A request is a question with
// Options (buttons), with Fields (a small form), or with neither (the user
// answers in their own words). The user can also skip it.
//
// Answers go back together: one mail of kind "answers" with an AskAnswer per
// request, so the agent wakes once for a batch. Secret fields are saved to the
// agent's env file; the mail names the keys, never the values.

// Field types an ask can request.
const (
	FieldText     = "text"     // one line
	FieldLongText = "longtext" // a paragraph
	FieldNumber   = "number"
	FieldDate     = "date"   // YYYY-MM-DD
	FieldChoice   = "choice" // one of Options
	FieldSecret   = "secret" // saved to the agent's env file under Name
	FieldLogs     = "logs"   // pasted output or logs, shown monospace
	FieldFile     = "file"   // files the user attaches (a screenshot, a log file)
)

// FieldTypes lists the valid AskField types.
var FieldTypes = []string{FieldText, FieldLongText, FieldNumber, FieldDate, FieldChoice, FieldSecret, FieldLogs, FieldFile}

// MaxAskFields caps how many inputs one request may ask for.
const MaxAskFields = 12

// AskField is one input of a request.
type AskField struct {
	Name     string   `json:"name"` // key in AskAnswer.Values; for a secret, the env var name
	Label    string   `json:"label,omitempty"`
	Type     string   `json:"type"`
	Options  []string `json:"options,omitempty"` // choice
	Optional bool     `json:"optional,omitempty"`
}

// AskAnswer is the user's reply to one request.
type AskAnswer struct {
	Ask      string            `json:"ask"` // the ask's mail ID
	Skipped  bool              `json:"skipped,omitempty"`
	Decision string            `json:"decision,omitempty"` // the chosen option
	Text     string            `json:"text,omitempty"`     // an answer in the user's own words
	Values   map[string]string `json:"values,omitempty"`   // non-secret fields by name
	Secrets  []string          `json:"secrets,omitempty"`  // env keys saved for the agent; never values
	// Files are the attachment IDs a file field received, by field name; the
	// files travel with the answers mail and Values names them.
	Files map[string][]string `json:"files,omitempty"`
}

var envKeyRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

// ValidEnvKey reports whether k can name an env var in an agent's env file.
func ValidEnvKey(k string) bool { return envKeyRE.MatchString(k) }

// ValidateAskFields checks a request's fields: known types, unique names,
// choices with options, secrets named like env vars.
func ValidateAskFields(fields []AskField) error {
	if len(fields) > MaxAskFields {
		return fmt.Errorf("%w: at most %d fields", ErrInvalid, MaxAskFields)
	}
	seen := map[string]bool{}
	for _, f := range fields {
		switch {
		case f.Name == "":
			return fmt.Errorf("%w: a field needs a name", ErrInvalid)
		case seen[f.Name]:
			return fmt.Errorf("%w: two fields are named %q", ErrInvalid, f.Name)
		case !slices.Contains(FieldTypes, f.Type):
			return fmt.Errorf("%w: field %q: unknown type %q (use %v)", ErrInvalid, f.Name, f.Type, FieldTypes)
		case f.Type == FieldChoice && len(f.Options) < 2:
			return fmt.Errorf("%w: field %q: a choice needs at least two options", ErrInvalid, f.Name)
		case f.Type == FieldSecret && !ValidEnvKey(f.Name):
			return fmt.Errorf("%w: secret %q is not a valid env var name (letters, digits, _)", ErrInvalid, f.Name)
		}
		seen[f.Name] = true
	}
	return nil
}
