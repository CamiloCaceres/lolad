package web_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func fmtJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

// The user-channel spec's answer checks (3.6), as the dashboard applies them.
func TestRequestsSpecChecks(t *testing.T) {
	e := setup(t)
	form := askFrom(t, e, "ultron", "Shipping", "Where do I send the books?", map[string]any{"fields": []map[string]any{
		{"name": "city", "label": "City", "type": "text"},
		{"name": "copies", "label": "Copies", "type": "number"},
		{"name": "when", "label": "Ship on", "type": "date", "optional": true},
		{"name": "note", "label": "Note", "type": "longtext", "optional": true},
	}})
	pick := askFrom(t, e, "ultron", "Dinner", "Thai or pizza?", map[string]any{"options": []string{"Thai", "Pizza"}})
	for _, c := range []struct {
		name string
		form url.Values
		want string
	}{
		{"text is one line", url.Values{"do": {"save"}, "f.city": {"Bogotá\n"}, "f.copies": {"2"}}, "City takes one line"},
		{"no exponent", url.Values{"do": {"save"}, "f.city": {"Bogotá"}, "f.copies": {"1e3"}}, "Copies needs a number"},
		{"ASCII digits", url.Values{"do": {"save"}, "f.city": {"Bogotá"}, "f.copies": {"١٢"}}, "Copies needs a number"},
		{"real date", url.Values{"do": {"save"}, "f.city": {"Bogotá"}, "f.copies": {"2"}, "f.when": {"2026-02-29"}}, "Ship on needs a date"},
		{"year 0001 or later", url.Values{"do": {"save"}, "f.city": {"Bogotá"}, "f.copies": {"2"}, "f.when": {"0000-01-01"}}, "Ship on needs a date"},
		{"unknown field", url.Values{"do": {"save"}, "f.city": {"Bogotá"}, "f.copies": {"2"}, "f.colour": {"red"}}, "no field"},
	} {
		if code, body := stage(t, e, "ultron", form.ID, c.form); code != http.StatusUnprocessableEntity || !strings.Contains(body, c.want) {
			t.Errorf("%s: %d, want 422 with %q", c.name, code, c.want)
		}
	}
	if code, _ := stage(t, e, "ultron", form.ID, url.Values{"do": {"save"}, "f.city": {" Bogotá "}, "f.copies": {" 1,5 "}, "f.when": {" 2028-02-29 "}}); code != 200 {
		t.Fatalf("a valid form was refused: %d", code)
	}
	// The "Something else" box is always posted: with a choice picked, the
	// choice is the whole answer.
	if code, _ := stage(t, e, "ultron", pick.ID, url.Values{"option": {"Thai"}, "text": {"or pizza"}, "send": {"1"}}); code != 200 {
		t.Fatalf("a choice with leftover words: %d", code)
	}
	sent := e.svc.Sent()
	if b := userchat(t, sent[len(sent)-1]); strings.Contains(fmtJSON(b), "or pizza") {
		t.Errorf("leftover words must not ride along with a choice: %v", b)
	}
}
