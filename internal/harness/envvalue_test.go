package harness

import "testing"

func TestEnvValue(t *testing.T) {
	for in, want := range map[string]string{
		`plain`:          "plain",
		`"quoted value"`: "quoted value",
		`"esc\"aped"`:    `esc"aped`,
		`'single'`:       "single",
		`abc # comment`:  "abc",
		`abc#notcomment`: "abc#notcomment",
		`";kK:x^9'`:      `";kK:x^9'`, // a password that starts with a quote stays literal
		`'unterminated`:  "'unterminated",
		`"bad\qescape"`:  `"bad\qescape"`,
	} {
		if got := envValue(in); got != want {
			t.Errorf("envValue(%q) = %q, want %q", in, got, want)
		}
	}
}
