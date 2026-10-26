package api

import (
	"github.com/CamiloCaceres/lolad/internal/web/fake"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEmailAPIWriteOnlyAndProvision(t *testing.T) {
	svc := fake.New(fake.Options{})
	defer svc.Close()
	m := http.NewServeMux()
	emailRoutes(m, svc)
	for _, tc := range []struct{ method, path, body, want string }{{"PUT", "/api/email/provider", `{"token":"api-token-never-return"}`, `"token_set":true`}, {"GET", "/api/email/provider", "", `"token_set":true`}, {"GET", "/api/email", "", "iris"}, {"POST", "/api/agents/iris/email/provision", `{}`, `"ok":true`}, {"POST", "/api/agents/iris/email/rotate", `{}`, "Password rotated"}, {"POST", "/api/agents/iris/email/detach", `{}`, "Mailbox detached"}} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		m.ServeHTTP(w, r)
		if w.Code != 200 || !strings.Contains(w.Body.String(), tc.want) {
			t.Fatalf("%s: %d %s", tc.path, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "api-token-never-return") {
			t.Fatal("token returned")
		}
	}
}
