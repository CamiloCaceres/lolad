package cli

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEmailCLISecretArgumentsRejected(t *testing.T) {
	for _, args := range [][]string{{"provider", "set-token", "do-not-print"}, {"iris", "rotate", "do-not-print"}, {"iris", "provision", "iris@example.test", "do-not-print"}} {
		var out bytes.Buffer
		handled, e := emailCommand(context.Background(), "http://127.0.0.1:1", args, strings.NewReader(""), &out)
		if !handled || e == nil || strings.Contains(e.Error()+out.String(), "do-not-print") {
			t.Fatal("secret argument accepted or echoed")
		}
	}
}
func TestEmailCLIRoutes(t *testing.T) {
	for _, tc := range []struct {
		args         []string
		method, path string
	}{{[]string{"ls"}, "GET", "/api/email"}, {[]string{"provider"}, "GET", "/api/email/provider"}, {[]string{"provider", "set-token"}, "PUT", "/api/email/provider"}, {[]string{"iris", "provision"}, "POST", "/api/agents/iris/email/provision"}, {[]string{"iris", "provision", "iris@example.test"}, "POST", "/api/agents/iris/email/provision"}, {[]string{"iris", "rotate"}, "POST", "/api/agents/iris/email/rotate"}, {[]string{"iris", "detach"}, "POST", "/api/agents/iris/email/detach"}} {
		t.Run(strings.Join(tc.args, "-"), func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != tc.method || r.URL.Path != tc.path {
					t.Error("wrong route")
				}
				b, _ := io.ReadAll(r.Body)
				if tc.method == "PUT" && !strings.Contains(string(b), "private-token") {
					t.Error("stdin token missing")
				}
				w.Write([]byte(`{"ok":true}`))
			}))
			defer s.Close()
			var out bytes.Buffer
			handled, e := emailCommand(context.Background(), s.URL, tc.args, strings.NewReader("private-token\n"), &out)
			if !handled || e != nil {
				t.Fatal(e)
			}
			if strings.Contains(out.String(), "private-token") {
				t.Fatal("token in output")
			}
		})
	}
}
