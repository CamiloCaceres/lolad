package web_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/CamiloCaceres/lolad/internal/core"
	lsetup "github.com/CamiloCaceres/lolad/internal/setup"
	"github.com/CamiloCaceres/lolad/internal/web"
	"github.com/CamiloCaceres/lolad/internal/web/fake"
)

// friendSetup serves the dashboard on the fake itself, so the friend-link
// methods (web.FriendInviter) are visible to it.
func friendSetup(t *testing.T, opts web.Options) (*fake.Service, func(string, string, url.Values) (int, string, http.Header)) {
	t.Helper()
	f := fake.New(fake.Options{})
	t.Cleanup(f.Close)
	server := httptest.NewServer(web.New(f, opts))
	t.Cleanup(server.Close)
	return f, func(method, path string, form url.Values) (int, string, http.Header) {
		t.Helper()
		req, err := http.NewRequest(method, server.URL+path, strings.NewReader(form.Encode()))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := noRedirect.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b), resp.Header
	}
}

var tokenRE = regexp.MustCompile(`name="token" value="([0-9a-f]+)"`)

func previewToken(t *testing.T, body string) string {
	t.Helper()
	m := tokenRE.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no preview token in page")
	}
	return m[1]
}

func TestFriendInviteLinkShownOnce(t *testing.T) {
	_, do := friendSetup(t, web.Options{})
	status, body, _ := do("GET", "/connections", nil)
	if status != 200 || !strings.Contains(body, "Invite a friend") || !strings.Contains(body, "Got an invite?") {
		t.Fatalf("friend flow missing: %d", status)
	}
	for _, word := range []string{"hub", "handle", "tenant", "provider"} {
		main, _, _ := strings.Cut(body, `class="connection-advanced"`)
		main = regexp.MustCompile(`<[^>]*>`).ReplaceAllString(main, " ") // visible text only
		main = strings.ReplaceAll(main, "Their Lolad’s identity", "")
		if regexp.MustCompile(`(?i)\b` + word + `\b`).MatchString(main) {
			t.Errorf("%q appears in the main flow", word)
		}
	}

	status, body, _ = do("POST", "/connections/friends/invite", url.Values{"for": {"Ben"}, "agent": {"lola", "not-an-agent"}})
	if status != 200 {
		t.Fatalf("create: %d", status)
	}
	if !strings.Contains(body, "Your link for Ben is ready") || !strings.Contains(body, "It works once, for 7 days.") {
		t.Fatal("created link screen missing its words")
	}
	m := regexp.MustCompile(`value="(https://lolad\.caceresdev\.com/join#2\.[^"]+)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("link absent from creation response")
	}
	link := m[1]
	_, secret, _ := strings.Cut(link, "#")
	for _, p := range []string{"/connections", "/", "/agents/lola", "/traffic"} {
		_, b, _ := do("GET", p, nil)
		if strings.Contains(b, secret) {
			t.Fatalf("link leaked on %s", p)
		}
	}
	_, body, _ = do("GET", "/connections", nil)
	if !strings.Contains(body, "For <strong>Ben</strong> · Lola") {
		t.Fatal("open link not listed with its note and agents")
	}
}

func TestFriendPreviewAndConnect(t *testing.T) {
	f, do := friendSetup(t, web.Options{})
	link := fake.FriendLink("Ana", "ana", [][2]string{{"nova", "Nova"}, {"pip", "Pip"}}, time.Now().Add(time.Hour), false)
	_, secret, _ := strings.Cut(link, "#")

	status, body, _ := do("POST", "/connections/friends/preview", url.Values{"link": {"  " + link + "\n"}})
	if status != 200 {
		t.Fatalf("preview: %d", status)
	}
	for _, want := range []string{"Ana invited you", "Ana is sharing", "Nova", "Pip", "Connect with Ana", "Which of your assistants can Ana talk to?"} {
		if !strings.Contains(body, want) {
			t.Errorf("preview missing %q", want)
		}
	}
	if strings.Contains(body, secret) {
		t.Fatal("pasted link echoed in the preview")
	}
	tok := previewToken(t, body)

	status, _, hdr := do("POST", "/connections/friends/accept", url.Values{"token": {tok}, "agent": {"lola"}})
	if status != http.StatusSeeOther || hdr.Get("Location") != "/connections?connected=ana.lolad.local" {
		t.Fatalf("accept: %d %q", status, hdr.Get("Location"))
	}
	_, body, _ = do("GET", "/connections?connected=ana.lolad.local", nil)
	for _, want := range []string{"Connected with Ana. You can message Ana now.", "Send Ana a message", `href="/inbox/ana@ana.lolad.local"`, "Ana can reach"} {
		if !strings.Contains(body, want) {
			t.Errorf("connected page missing %q", want)
		}
	}
	cs, _ := f.Connections(t.Context())
	var ana core.Connection
	for _, c := range cs {
		if c.Provider == "ana.lolad.local" {
			ana = c
		}
	}
	if !ana.UserReachable || len(ana.Shares) != 1 || ana.Shares[0].Local != "lola" {
		t.Fatalf("connection not applied: %+v", ana)
	}

	// The token is single use.
	status, body, _ = do("POST", "/connections/friends/accept", url.Values{"token": {tok}})
	if status != 422 || !strings.Contains(body, "Paste it again") {
		t.Fatalf("reused token: %d", status)
	}
	// The same link again: the service's refusal, in its own words.
	_, body, _ = do("POST", "/connections/friends/preview", url.Values{"link": {link}})
	if !strings.Contains(body, "You’re already connected with Ana") {
		t.Fatal("already connected not shown")
	}
}

func TestFriendRefusalsShowUserMessage(t *testing.T) {
	_, do := friendSetup(t, web.Options{})
	status, body, _ := do("POST", "/connections/friends/preview", url.Values{"link": {"hello, not a link"}})
	if status != 422 || !strings.Contains(body, "That doesn’t look like a Lolad invite link.") {
		t.Fatalf("bad link: %d", status)
	}
	if strings.Contains(body, "hello, not a link") {
		t.Fatal("input echoed")
	}
	expired := fake.FriendLink("Ben", "ben", nil, time.Now().Add(-time.Hour), false)
	_, body, _ = do("POST", "/connections/friends/preview", url.Values{"link": {expired}})
	if !strings.Contains(body, "This invite has expired. Ask Ben for a new link.") || strings.Contains(body, "Connect with Ben") {
		t.Fatal("expired preview")
	}
	_, body, _ = do("POST", "/connections/friends/preview", url.Values{"link": {""}})
	if !strings.Contains(body, "Paste the link your friend sent you first.") {
		t.Fatal("empty paste")
	}
}

func TestFriendWaiting(t *testing.T) {
	f, do := friendSetup(t, web.Options{})
	link := fake.FriendLink("Cara", "cara", nil, time.Now().Add(time.Hour), true)
	_, body, _ := do("POST", "/connections/friends/preview", url.Values{"link": {link}})
	status, body, _ := do("POST", "/connections/friends/accept", url.Values{"token": {previewToken(t, body)}})
	if status != 200 || !strings.Contains(body, "Waiting for Cara’s Lolad to come online.") || !strings.Contains(body, "This finishes by itself") {
		t.Fatalf("waiting screen: %d", status)
	}
	_, body, _ = do("GET", "/connections", nil)
	if !strings.Contains(body, ">Waiting<") {
		t.Fatal("waiting card missing")
	}
	f.FinishWaiting("cara.lolad.local")
	_, body, _ = do("GET", "/connections", nil)
	if strings.Contains(body, ">Waiting<") || !strings.Contains(body, "Send Cara a message") {
		t.Fatal("not connected after the friend came online")
	}
}

func TestFriendStopSharing(t *testing.T) {
	f, do := friendSetup(t, web.Options{})
	status, _, _ := do("POST", "/connections/jugui.lolad.local", url.Values{"action": {"stopsharing"}})
	if status != http.StatusSeeOther {
		t.Fatalf("stop sharing: %d", status)
	}
	cs, _ := f.Connections(t.Context())
	for _, c := range cs {
		if c.Provider == "jugui.lolad.local" && (c.UserReachable || len(c.Shares) != 0) {
			t.Fatalf("still sharing: %+v", c)
		}
	}
}

func TestFriendCancelLink(t *testing.T) {
	f, do := friendSetup(t, web.Options{})
	do("POST", "/connections/friends/invite", url.Values{"for": {"Ben"}})
	links, _ := f.FriendInvites(t.Context())
	if len(links) != 1 {
		t.Fatal("no link")
	}
	status, _, _ := do("POST", "/connections/friends/"+links[0].ID+"/cancel", nil)
	if status != http.StatusSeeOther {
		t.Fatalf("cancel: %d", status)
	}
	if links, _ = f.FriendInvites(t.Context()); len(links) != 0 {
		t.Fatal("link not cancelled")
	}
}

// Without friend links (an older service), Sharing keeps pairing by code.
func TestSharingWithoutFriendLinks(t *testing.T) {
	_, do := fedSetup(t)
	_, body := do("GET", "/connections", nil)
	if strings.Contains(body, "Invite a friend") || !strings.Contains(body, "Make an invite code") {
		t.Fatal("code pairing expected")
	}
	if status, _ := do("POST", "/connections/friends/preview", url.Values{"link": {"x"}}); status != 404 {
		t.Fatalf("friend routes must 404 without the service: %d", status)
	}
}

func TestWelcomeFriendStep(t *testing.T) {
	demo := lsetup.NewDemo()
	demo.Set(func(st *lsetup.Status) {
		st.ServiceDone, st.FolderSet, st.NameSet, st.SkippedAgent = true, true, true, true
		st.Harnesses[0].Installed, st.Harnesses[0].LoggedIn = true, true
	})
	_, do := friendSetup(t, web.Options{Setup: demo})
	_, body, _ := do("GET", "/welcome", nil)
	if !strings.Contains(body, "Got an invite from a friend?") {
		t.Fatal("done screen lacks the friend step")
	}
	link := fake.FriendLink("Dana", "dana", [][2]string{{"nova", "Nova"}}, time.Now().Add(time.Hour), false)
	status, body, _ := do("POST", "/welcome/friend", url.Values{"link": {link}})
	if status != 200 || !strings.Contains(body, "Dana invited you") || !strings.Contains(body, "Connect with Dana") {
		t.Fatalf("welcome preview: %d", status)
	}
	status, body, _ = do("POST", "/welcome/friend/accept", url.Values{"token": {previewToken(t, body)}})
	if status != 200 || !strings.Contains(body, "You’re connected with Dana") || !strings.Contains(body, `href="/inbox/dana@dana.lolad.local"`) {
		t.Fatalf("welcome connected: %d", status)
	}
	_, body, _ = do("POST", "/welcome/friend", url.Values{"link": {"nope"}})
	if !strings.Contains(body, "That doesn’t look like a Lolad invite link.") {
		t.Fatal("welcome refusal")
	}
}
