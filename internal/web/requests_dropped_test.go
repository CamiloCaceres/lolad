package web_test

import (
	"os"
	"strings"
	"testing"
)

// A choice card says beforehand that words typed under "Something else"
// don't go with a choice (user-channel 3.6), instead of dropping them
// silently; the note shows only when a choice is picked over typed words.
func TestChoiceCardSaysTypedWordsAreNotSent(t *testing.T) {
	e := setup(t)
	askFrom(t, e, "ultron", "Logo colour", "Blue or green?", map[string]any{"options": []string{"Blue", "Green"}})
	_, page := e.get("/inbox/ultron")
	if !strings.Contains(page, `class="req-note ask-dropped"`) || !strings.Contains(page, "won’t be sent") {
		t.Fatalf("no note on the choice card:\n%s", page)
	}
	css, err := os.ReadFile("static/pages/inbox.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range []string{
		".ask-dropped { display: none; }",
		".ask-opts:has(input[data-ask-pick]:not([data-ask-other]):checked):has(.ask-other textarea:not(:placeholder-shown)) .ask-dropped { display: flex; }",
	} {
		if !strings.Contains(string(css), rule) {
			t.Errorf("inbox.css lacks %q", rule)
		}
	}
}
