package web_test

import (
	"bytes"
	"github.com/CamiloCaceres/lolad/internal/web"
	"github.com/CamiloCaceres/lolad/internal/web/fake"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestEmailProvisionHandlers(t *testing.T) {
	svc := fake.New(fake.Options{})
	defer svc.Close()
	var logs bytes.Buffer
	h := web.New(svc, web.Options{Logger: slog.New(slog.NewTextHandler(&logs, nil))})
	request := func(method, path string, v url.Values) string {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(v.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("%s status %d", path, w.Code)
		}
		return w.Body.String()
	}
	page := request("GET", "/email", nil)
	for _, want := range []string{"Give an email", "iris@mail.example.com", "ultron@mail.example.com", "mail mod watches"} {
		if !strings.Contains(page, want) {
			t.Errorf("missing %s", want)
		}
	}
	page = request("POST", "/email/iris/provision", url.Values{})
	if !strings.Contains(page, "IMAP and SMTP logins passed") {
		t.Fatal("provision did not test")
	}
	page = request("GET", "/agents/iris/settings/email", nil)
	if !strings.Contains(page, "Rotate password") {
		t.Fatal("missing management controls")
	}
	page = request("POST", "/email/provider", url.Values{"provider": {"purelymail"}, "domain": {"example.test"}, "pattern": {"{slug}@{domain}"}, "watch": {"lolad"}, "operators": {"operator@example.test"}, "token": {"never-echo-this-token"}})
	if strings.Contains(page+logs.String(), "never-echo-this-token") {
		t.Fatal("token in response or logs")
	}
	page = request("POST", "/email/iris/detach", url.Values{"address": {"iris@mail.example.com"}, "delete_mailbox": {"yes"}, "confirm": {"wrong"}})
	if !strings.Contains(page, "full mailbox address") {
		t.Fatal("missing typed confirmation validation")
	}
}
