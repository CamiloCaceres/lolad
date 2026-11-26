package web_test

import (
	"strings"
	"testing"
)

// A link stored before URLs were checked may carry spaces around it: it is
// still drawn as a link, with the spaces gone.
func TestStoredLinkWithSpacesRenders(t *testing.T) {
	e := setup(t)
	sayFrom(t, e, "ultron", "fallback text", map[string]any{"blocks": []map[string]any{
		{"type": "link", "label": "Open the page", "url": "  https://example.com/page \n"},
	}})
	_, page := e.get("/inbox/ultron")
	if !strings.Contains(page, `<a class="blk-link" href="https://example.com/page"`) || !strings.Contains(page, "Open the page") {
		t.Fatalf("link not drawn:\n%s", page)
	}
}
