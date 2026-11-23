package msgcli

import (
	"strings"
	"testing"

	"github.com/CamiloCaceres/lolad/internal/core"
)

func TestAskFields(t *testing.T) {
	got, err := askFields([]string{"Street address", "Notes?:longtext", "Size:choice:S|M| L ", "Copies:number", "Street address"}, []string{"OPENAI_API_KEY", "GH_TOKEN:GitHub token"})
	if err != nil {
		t.Fatal(err)
	}
	want := []core.AskField{
		{Name: "street_address", Label: "Street address", Type: "text"},
		{Name: "notes", Label: "Notes", Type: "longtext", Optional: true},
		{Name: "size", Label: "Size", Type: "choice", Options: []string{"S", "M", "L"}},
		{Name: "copies", Label: "Copies", Type: "number"},
		{Name: "street_address_2", Label: "Street address", Type: "text"},
		{Name: "OPENAI_API_KEY", Type: "secret"},
		{Name: "GH_TOKEN", Label: "GitHub token", Type: "secret"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d fields: %+v", len(got), got)
	}
	for i := range want {
		g, w := got[i], want[i]
		if g.Name != w.Name || g.Label != w.Label || g.Type != w.Type || g.Optional != w.Optional || strings.Join(g.Options, "|") != strings.Join(w.Options, "|") {
			t.Errorf("field %d = %+v, want %+v", i, g, w)
		}
	}
	for _, tc := range []struct {
		fields, secrets []string
		err             string
	}{
		{[]string{"Key:secret"}, nil, "--secret ENV_NAME"},
		{[]string{"Size:choice"}, nil, "list the options"},
		{[]string{"When:time"}, nil, "unknown type"},
		{[]string{":text"}, nil, "start with a label"},
		{[]string{"Name:text:a|b"}, nil, "only a choice takes options"},
		{nil, []string{"not a key"}, "env var name"},
		{nil, []string{"A", "A"}, "used twice"},
	} {
		if _, err := askFields(tc.fields, tc.secrets); err == nil || !strings.Contains(err.Error(), tc.err) {
			t.Errorf("askFields(%v, %v) = %v, want %q", tc.fields, tc.secrets, err, tc.err)
		}
	}
}
