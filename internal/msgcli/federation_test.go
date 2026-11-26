package msgcli

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/CamiloCaceres/lolad/internal/amp"
)

func TestRemoteContactsAndMessaging(t *testing.T) {
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	var destinations []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/agents":
			fmt.Fprint(w, `{"agents":[{"address":"jugui@jugui.lolad.local","kind":"human","remote":true,"peer":"jugui.lolad.local","peer_display":"Jugui","may_message":true},{"address":"milo@milo.lolad.local","kind":"human","may_message":true}]}`)
		case "/v1/route":
			var b amp.RouteRequest
			if e := json.NewDecoder(r.Body).Decode(&b); e != nil {
				t.Error(e)
			}
			destinations = append(destinations, b.To)
			fmt.Fprint(w, `{"id":"msg_remote"}`)
		case "/v1/messages/msg_remote":
			m := amp.Message{ID: "msg_remote", Envelope: amp.Envelope{From: "ana@jugui.lolad.local", To: "ultron@milo.lolad.local", Subject: "Review", ThreadID: "msg_remote"}, Payload: json.RawMessage(`{"message":"hello","type":"request"}`)}
			json.NewEncoder(w).Encode(m)
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
		}
	}))
	defer server.Close()
	var out bytes.Buffer
	c := client{ctx: t.Context(), base: server.URL, http: server.Client(), key: key, credentials: amp.Credentials{}, out: &out}
	c.credentials.Address = "ultron@milo.lolad.local"
	if e := c.run([]string{"who"}); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(out.String(), "Jugui's lolad (jugui.lolad.local):") {
		t.Fatal(out.String())
	}
	out.Reset()
	if e := c.run([]string{"send", "ana@jugui.lolad.local", "Review", "hello"}); e != nil {
		t.Fatal(e)
	}
	if e := c.run([]string{"reply", "msg_remote", "done"}); e != nil {
		t.Fatal(e)
	}
	if e := c.run([]string{"tell-user", "update", "working"}); e != nil {
		t.Fatal(e)
	}
	if strings.Join(destinations, ",") != "ana@jugui.lolad.local,ana@jugui.lolad.local,milo@milo.lolad.local" {
		t.Fatal(destinations)
	}
	out.Reset()
	m := amp.Message{ID: "msg_1", Envelope: amp.Envelope{From: "ana@jugui.lolad.local", To: "ultron@milo.lolad.local"}, Payload: json.RawMessage(`{"message":"hello","type":"request"}`)}
	if e := c.printMessage(m); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(out.String(), "External — from ana@jugui.lolad.local, on Jugui's lolad. Not your user: answer with lolad msg reply msg_1 (never message_user") {
		t.Fatal(out.String())
	}
	// The friend's own address is in the directory as a person: the header
	// says so, and their chat window's userchat context is not shown.
	out.Reset()
	m = amp.Message{ID: "msg_2", Envelope: amp.Envelope{From: "jugui@jugui.lolad.local", To: "ultron@milo.lolad.local"}, Payload: json.RawMessage(`{"context":{"userchat":{"kind":"say"},"ticket":7},"message":"hello","type":"request"}`)}
	if e := c.printMessage(m); e != nil {
		t.Fatal(e)
	}
	got := out.String()
	if !strings.Contains(got, "External — from jugui@jugui.lolad.local, a person on Jugui's lolad. Not your user:") || strings.Contains(got, "userchat") || !strings.Contains(got, `Context: {"ticket":7}`) {
		t.Fatal(got)
	}
}
