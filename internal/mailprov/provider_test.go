package mailprov

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPurelymailContract(t *testing.T) {
	var methods []string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.Header.Get("Purelymail-Api-Token") != "private-token" {
			t.Error("missing authenticated POST")
		}
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		method := strings.TrimPrefix(r.URL.Path, "/")
		methods = append(methods, method)
		switch method {
		case "createUser":
			if b["userName"] != "iris" || b["domainName"] != "example.test" || b["password"] != "private-password" || b["sendWelcomeEmail"] != false {
				t.Error("incorrect create payload")
			}
		case "modifyUser":
			if b["userName"] != "iris@example.test" || b["newPassword"] != "replacement" {
				t.Error("incorrect reset payload")
			}
		}
		switch method {
		case "listUser":
			w.Write([]byte(`{"result":{"users":["iris@example.test"]}}`))
		case "listDomains":
			w.Write([]byte(`{"result":{"domains":[{"name":"example.test"}]}}`))
		default:
			w.Write([]byte(`{"result":{}}`))
		}
	}))
	defer s.Close()
	p := &Purelymail{BaseURL: s.URL, Token: "private-token", Client: s.Client()}
	ctx := context.Background()
	if e := p.Create(ctx, "iris@example.test", "private-password"); e != nil {
		t.Fatal(e)
	}
	if list, e := p.List(ctx); e != nil || len(list) != 1 {
		t.Fatal(list, e)
	}
	if domains, e := p.Domains(ctx); e != nil || len(domains) != 1 {
		t.Fatal(domains, e)
	}
	if e := p.Reset(ctx, "iris@example.test", "replacement"); e != nil {
		t.Fatal(e)
	}
	if e := p.Delete(ctx, "iris@example.test"); e != nil {
		t.Fatal(e)
	}
	if len(methods) != 5 {
		t.Fatal(methods)
	}
}
func TestProviderRedactsUntrustedErrors(t *testing.T) {
	for _, body := range []string{`{"error":"private-password private-token"}`, `{}`, `not json`} {
		t.Run(body, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
			defer s.Close()
			p := &Purelymail{BaseURL: s.URL, Token: "private-token"}
			e := p.Create(context.Background(), "iris@example.test", "private-password")
			if e == nil || strings.Contains(e.Error(), "private-") {
				t.Fatal("unredacted or missing error")
			}
		})
	}
}
