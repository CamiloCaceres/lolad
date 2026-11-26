package web_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/CamiloCaceres/lolad/internal/web"
)

// TestFriendChatOnHome: a friend who started talking to an assistant is shown
// on Home, with a link to the messages, until the user says Got it.
func TestFriendChatOnHome(t *testing.T) {
	f, do := friendSetup(t, web.Options{})
	_, body, _ := do("GET", "/", nil)
	if strings.Contains(body, "started talking to") {
		t.Fatal("friend chat shown before any friend wrote")
	}
	f.AddFriendChat(web.FriendChat{Peer: "jugui.lolad.local", PeerDisplay: "Jugui", Remote: "jugui@jugui.lolad.local", RemoteDisplay: "Jugui", Kind: "human", Local: "ultron", Subject: "Trip to Lisbon?"})
	f.AddFriendChat(web.FriendChat{Peer: "jugui.lolad.local", PeerDisplay: "Jugui", Remote: "nova@jugui.lolad.local", RemoteDisplay: "Nova", Kind: "agent", Local: "ultron", Subject: "Shared notes"})
	_, body, _ = do("GET", "/", nil)
	for _, want := range []string{
		"Friends · 2",
		"Jugui from Jugui’s lolad started talking to Ultron",
		"Nova, an assistant from Jugui’s lolad started talking to Ultron",
		"“Trip to Lisbon?” · Ultron replies to them directly.",
		`href="/traffic?peer=ultron&amp;other=jugui%40jugui.lolad.local"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("home lacks %q", want)
		}
	}
	code, _, h := do("POST", "/friends/chats/dismiss", url.Values{"peer": {"jugui.lolad.local"}, "remote": {"jugui@jugui.lolad.local"}, "local": {"ultron"}})
	if code != http.StatusSeeOther || h.Get("Location") != "/" {
		t.Fatalf("dismiss: %d %s", code, h.Get("Location"))
	}
	_, body, _ = do("GET", "/", nil)
	if strings.Contains(body, "Trip to Lisbon?") || !strings.Contains(body, "Friends · 1") {
		t.Fatal("dismissed friend chat still shown")
	}
}
