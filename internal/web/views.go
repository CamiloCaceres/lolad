package web

import (
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/CamiloCaceres/lolad/internal/core"
)

func funcs(s *server) template.FuncMap {
	return template.FuncMap{
		"asset":      s.asset,
		"pageStyles": s.pageStyles,
		"when":       when,
		"ago":        ago,
		"stamp":      func(t time.Time) string { return t.Format(time.RFC3339) },
		"long":       func(t time.Time) string { return t.Local().Format("Mon 2 Jan 2006, 15:04") },
		"clock":      func(t time.Time) string { return t.Local().Format("15:04") },
		"since":      func(t time.Time) string { return span(time.Since(t)) },
		"until":      func(t time.Time) string { return span(time.Until(t)) },
		"future":     func(t time.Time) bool { return time.Until(t) > time.Second },
		"initials":   initials,
		"rich":       rich,
		"excerpt":    excerpt,
		"short":      shortID,
		"attention":  attention,
		"running":    running,
		"plural":     plural,
		"has":        slices.Contains[[]string],
		"add":        func(a, b int) int { return a + b },
		"dict":       dict,
		"chat":       userChat,
		"subject":    subject,
		"json":       func(v any) (string, error) { b, err := json.Marshal(v); return string(b), err },
		"harness":    harnessLabel,
		"list":       func(v ...string) []string { return v },
		"hasPrefix":  strings.HasPrefix,
		"indent":     func(depth int) string { return strings.Repeat("\u00a0\u00a0\u00a0", max(depth-1, 0)) },
		"evtime":     evtime,
		"stampNano":  func(t time.Time) string { return t.Format(time.RFC3339Nano) },
		"sentence":   sentence,
		"baseOf":     path.Base,
		"dirOf": func(p string) string {
			if d := path.Dir(p); d != "." {
				return d
			}
			return ""
		},
		"bytes": bytesLabel,
		"stateOf": func(a *core.AgentStatus) core.State {
			if a == nil {
				return ""
			}
			return a.State
		},
	}
}

func dict(kv ...any) (map[string]any, error) {
	if len(kv)%2 != 0 {
		return nil, fmt.Errorf("dict: odd number of arguments")
	}
	m := make(map[string]any, len(kv)/2)
	for i := 0; i < len(kv); i += 2 {
		k, ok := kv[i].(string)
		if !ok {
			return nil, fmt.Errorf("dict: key %v is not a string", kv[i])
		}
		m[k] = kv[i+1]
	}
	return m, nil
}

// when is a messaging client's timestamp: a clock time today, "Yesterday",
// a weekday within the week, a date after that.
func when(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	t, now := t.Local(), time.Now()
	y1, m1, d1 := t.Date()
	y2, m2, d2 := now.Date()
	today := time.Date(y2, m2, d2, 0, 0, 0, 0, time.Local)
	day := time.Date(y1, m1, d1, 0, 0, 0, 0, time.Local)
	switch days := int(today.Sub(day).Hours() / 24); {
	case days <= 0:
		return t.Format("15:04")
	case days == 1:
		return "Yesterday"
	case days < 7:
		return t.Format("Monday")
	case y1 == y2:
		return t.Format("2 Jan")
	}
	return t.Format("2 Jan 2006")
}

// evtime is a log timestamp: the clock today, with the day before that.
func evtime(t time.Time) string {
	switch w := when(t); {
	case strings.Contains(w, ":"):
		return t.Local().Format("15:04:05")
	case w == "Yesterday":
		return "Yesterday " + t.Local().Format("15:04")
	}
	return t.Local().Format("2 Jan 15:04")
}

// ago is a short relative time: "now", "4m", "3h", "2d".
func ago(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := time.Since(t)
	switch {
	case d < 45*time.Second:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()+0.5))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 60*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
	return t.Local().Format("2 Jan 2006")
}

// span is a duration the way a person says it: "45s", "12m", "3h 20m", "2d 4h".
func span(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd %dh", int(d.Hours()/24), int(d.Hours())%24)
}

func initials(name string) string {
	var out []rune
	for _, f := range strings.FieldsFunc(name, func(r rune) bool { return r == ' ' || r == '-' || r == '_' }) {
		r, _ := utf8.DecodeRuneInString(f)
		out = append(out, unicode.ToUpper(r))
		if len(out) == 2 {
			break
		}
	}
	if len(out) == 0 {
		return "?"
	}
	return string(out)
}

func excerpt(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)[:n]
	return strings.TrimRight(string(r), " ,.;:") + "…"
}

func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[len(id)-8:]
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// attention is true for states the operator should look at.
func attention(st core.State) bool {
	return st == core.StateWaiting || st == core.StateCrashed || st == core.StateFailed
}

func running(st core.State) bool {
	switch st {
	case core.StateStopped, core.StateFailed:
		return false
	}
	return true
}

// placeholderSubjects are what senders put in the subject when they have no
// real one: `lolad msg tell-user` uses the message kind, `lolad send` uses
// "Message".
var placeholderSubjects = map[string]bool{"message": true, "say": true, "update": true, "deliver": true, "ask": true, "reply": true, "decision": true, "(no subject)": true}

// subject is the message's subject when it says something, "" when it is a
// placeholder.
func subject(m core.Mail) string {
	s := baseSubject(m.Subject)
	if placeholderSubjects[strings.ToLower(s)] || strings.EqualFold(s, userChat(m).Kind) {
		return ""
	}
	return s
}

// userChat reads payload.context.userchat whatever shape it arrived in (a
// map from JSON, or core.UserChat from an in-process sender).
func userChat(m core.Mail) core.UserChat {
	var uc core.UserChat
	v, ok := m.Context["userchat"]
	if !ok {
		return uc
	}
	if c, ok := v.(core.UserChat); ok {
		return c
	}
	b, err := json.Marshal(v)
	if err == nil {
		_ = json.Unmarshal(b, &uc)
	}
	return uc
}

var (
	fenceRE  = regexp.MustCompile("(?s)```[a-zA-Z0-9_+-]*\n?(.*?)```")
	inlineRE = regexp.MustCompile("`([^`\n]+)`|\\*\\*([^*\n]+)\\*\\*|(https?://[^\\s<]+[^\\s<.,;:!?)\\]'\"])")
)

// rich renders a message body: text with newlines kept, ```fenced``` code,
// `inline code`, **bold** and bare links. Everything is escaped first.
func rich(body string) template.HTML {
	var b strings.Builder
	last := 0
	for _, m := range fenceRE.FindAllStringSubmatchIndex(body, -1) {
		b.WriteString(inline(body[last:m[0]]))
		b.WriteString(`<pre class="code"><code>`)
		b.WriteString(html.EscapeString(strings.TrimRight(body[m[2]:m[3]], "\n")))
		b.WriteString(`</code></pre>`)
		last = m[1]
	}
	b.WriteString(inline(body[last:]))
	return template.HTML(strings.TrimSpace(b.String()))
}

func inline(s string) string {
	var b strings.Builder
	last := 0
	for _, m := range inlineRE.FindAllStringSubmatchIndex(s, -1) {
		b.WriteString(html.EscapeString(s[last:m[0]]))
		switch {
		case m[2] >= 0:
			b.WriteString("<code>" + html.EscapeString(s[m[2]:m[3]]) + "</code>")
		case m[4] >= 0:
			b.WriteString("<strong>" + html.EscapeString(s[m[4]:m[5]]) + "</strong>")
		default:
			u := html.EscapeString(s[m[6]:m[7]])
			b.WriteString(`<a href="` + u + `" target="_blank" rel="noopener noreferrer">` + u + `</a>`)
		}
		last = m[1]
	}
	b.WriteString(html.EscapeString(s[last:]))
	return b.String()
}

// sentence capitalises the first letter: "model changed, .env changed" →
// "Model changed, .env changed".
func sentence(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	if n == 0 {
		return s
	}
	return string(unicode.ToUpper(r)) + s[n:]
}

// bytesLabel is a file size the way a file manager shows it.
func bytesLabel(n int64) string {
	switch {
	case n < 1000:
		return fmt.Sprintf("%d B", n)
	case n < 1000*1000:
		return fmt.Sprintf("%.1f KB", float64(n)/1000)
	}
	return fmt.Sprintf("%.1f MB", float64(n)/1e6)
}
