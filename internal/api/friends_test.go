package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/CamiloCaceres/lolad/internal/core"
)

type friendsFake struct {
	federationFake
	link   string
	agents []string
	err    error
}

type peopleError struct{}

func (peopleError) Error() string       { return "invalid request: expired" }
func (peopleError) Unwrap() error       { return core.ErrInvalid }
func (peopleError) UserMessage() string { return "This invite has expired. Ask Ana for a new link." }

func (f *friendsFake) CreateFriendInvite(_ context.Context, r core.FriendInviteRequest) (core.FriendInvite, error) {
	f.agents = r.Agents
	return core.FriendInvite{ID: "abc", Link: "https://x/join#2.secret"}, f.err
}
func (f *friendsFake) FriendInvites(context.Context) ([]core.FriendInviteInfo, error) {
	return []core.FriendInviteInfo{{ID: "abc", For: "Ben", Agents: []string{}}}, nil
}
func (f *friendsFake) CancelFriendInvite(_ context.Context, id string) error {
	if id != "abc" {
		return core.ErrNotFound
	}
	return nil
}
func (f *friendsFake) PreviewFriendInvite(_ context.Context, link string) (core.InvitePreview, error) {
	f.link = link
	return core.InvitePreview{FromDisplay: "Ana", Agents: []core.AgentOffer{{Slug: "maple", Display: "Maple"}}}, f.err
}
func (f *friendsFake) AcceptFriendInvite(_ context.Context, link string, r core.AcceptRequest) (core.AcceptResult, error) {
	f.link, f.agents = link, r.Agents
	return core.AcceptResult{State: core.FriendWaiting, Message: "Waiting for Ana's Lolad"}, f.err
}

func TestFriendRoutes(t *testing.T) {
	f := &friendsFake{}
	h := New(f, Options{})
	do := func(method, path, body string) (int, string, string) {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
		return w.Code, w.Body.String(), w.Header().Get("Cache-Control")
	}
	if st, body, cc := do("POST", "/api/friends/invites", `{"agents":["maple"],"for":"Ben"}`); st != 200 || !strings.Contains(body, `"link":"https://x/join#2.secret"`) || cc != "no-store" || f.agents[0] != "maple" {
		t.Fatal(st, body, cc)
	}
	if st, body, _ := do("GET", "/api/friends/invites", ""); st != 200 || !strings.Contains(body, `"for":"Ben"`) || strings.Contains(body, "secret") {
		t.Fatal(st, body)
	}
	if st, _, _ := do("DELETE", "/api/friends/invites/abc", ""); st != 200 {
		t.Fatal(st)
	}
	if st, _, _ := do("DELETE", "/api/friends/invites/nope", ""); st != 404 {
		t.Fatal(st)
	}
	if st, body, _ := do("POST", "/api/friends/preview", `{"link":"L1"}`); st != 200 || f.link != "L1" || !strings.Contains(body, `"from_display":"Ana"`) || !strings.Contains(body, `"slug":"maple"`) {
		t.Fatal(st, body)
	}
	st, body, _ := do("POST", "/api/friends/accept", `{"link":"L2","agents":["bee"]}`)
	var res core.AcceptResult
	if st != 200 || json.Unmarshal([]byte(body), &res) != nil || res.State != "waiting" || f.link != "L2" || f.agents[0] != "bee" {
		t.Fatal(st, body)
	}
	// A refusal written for people is passed through as the message.
	f.err = peopleError{}
	if st, body, _ := do("POST", "/api/friends/accept", `{"link":"L3"}`); st != 422 || !strings.Contains(body, "This invite has expired. Ask Ana for a new link.") {
		t.Fatal(st, body)
	}
	// Other errors never quote the request (the link is a secret).
	f.err = errors.Join(core.ErrInvalid, errors.New("details about L4-secret"))
	if st, body, _ := do("POST", "/api/friends/accept", `{"link":"L4-secret"}`); st != 422 || strings.Contains(body, "L4-secret") {
		t.Fatal(st, body)
	}
	if st, body, _ := do("POST", "/api/friends/accept", `{"link":"L5-secret", "x":`); st != 422 || strings.Contains(body, "L5-secret") {
		t.Fatal(st, body)
	}
	// Links never travel in a GET.
	if st, _, _ := do("GET", "/api/friends/accept?link=x", ""); st != 405 {
		t.Fatal(st)
	}
}

// Without a FriendInviter the routes are not mounted.
func TestFriendRoutesOptional(t *testing.T) {
	w := httptest.NewRecorder()
	New(&federationFake{}, Options{}).ServeHTTP(w, httptest.NewRequest("POST", "/api/friends/accept", strings.NewReader(`{}`)))
	if w.Code == 200 {
		t.Fatal(w.Code)
	}
}
