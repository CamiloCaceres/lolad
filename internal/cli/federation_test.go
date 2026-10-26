package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFederationCommands(t *testing.T) {
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		if r.Method == "GET" && r.URL.Path == "/api/connections" {
			fmt.Fprint(w, `[{"provider":"jugui.lolad.local","shares":[{"local":"ultron"}]}]`)
			return
		}
		if r.Method == "PATCH" {
			var b map[string]any
			if e := json.NewDecoder(r.Body).Decode(&b); e != nil {
				t.Error(e)
			}
			if shares, ok := b["shares"].([]any); ok {
				if len(calls) == 2 && len(shares) != 0 {
					t.Error("last share not removed")
				}
				if len(calls) == 4 && len(shares) != 1 {
					t.Error("share missing")
				}
			}
		}
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer server.Close()
	for _, args := range [][]string{{"connect", "unshare", "jugui.lolad.local", "ultron"}, {"connect", "share", "jugui.lolad.local", "ultron", "--with", "jugui@jugui.lolad.local", "--with", "ana@jugui.lolad.local"}, {"connect", "accept"}, {"connect", "pause", "jugui.lolad.local"}, {"approve", "jugui.lolad.local", "jugui@jugui.lolad.local", "ultron"}} {
		var out bytes.Buffer
		if e := federationCommand(t.Context(), server.URL, args, strings.NewReader("test-secret-code"), &out); e != nil {
			t.Fatal(e)
		}
		if strings.Contains(out.String(), "test-secret-code") {
			t.Fatal("invite leaked")
		}
	}
	before := len(calls)
	if e := federationCommand(t.Context(), server.URL, []string{"connect", "accept", "secret-must-not-appear"}, strings.NewReader(""), &bytes.Buffer{}); e == nil || strings.Contains(e.Error(), "secret-must-not-appear") {
		t.Fatal("argv invite not rejected safely")
	}
	if before != len(calls) {
		t.Fatal("argv invite sent request")
	}
}
