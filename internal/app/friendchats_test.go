package app

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/CamiloCaceres/lolad/internal/core"
)

// TestFriendChatRecordedOnce: the first message a friend sends one of our
// agents is recorded, pushed and listed for Home; later mail from the same
// pair, mail to the user and local mail are not. Got it hides it.
func TestFriendChatRecordedOnce(t *testing.T) {
	pushes := make(chan string, 4)
	push := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		pushes <- string(b)
	}))
	defer push.Close()
	dir := t.TempDir()
	t.Setenv("LOLAD_STATE", filepath.Join(dir, "state"))
	agent := filepath.Join(dir, "ultron")
	if err := os.Mkdir(agent, 0700); err != nil {
		t.Fatal(err)
	}
	for path, data := range map[string]string{
		filepath.Join(agent, "lolabot.yaml"): "agent:\n  name: Ultron\nruntime:\n  harness: claude\n  autostart: false\n  wake_on_message: false\n",
		filepath.Join(dir, "config.yaml"):    "agents_dirs: []\nagents: [" + agent + "]\ntmux_socket: lolad-test-friendchat\n",
		filepath.Join(dir, "org.yaml"):       "tenant: milo\nuser:\n  name: milo\n  display: Milo\n  ntfy: " + push.URL + "\n",
	} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	a, err := New(t.Context(), filepath.Join(dir, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	events := a.Subscribe(t.Context())

	m := core.Mail{ID: "msg_1_aaaa", FromName: "jugui@jugui.lolad.local", ToName: "ultron", Remote: "jugui.lolad.local", Subject: "Trip\nto Lisbon?", CreatedAt: time.Now()}
	a.friendChat(m)
	select {
	case got := <-pushes:
		if got != "jugui@jugui.lolad.local from jugui.lolad.local’s lolad started talking to Ultron: Trip to Lisbon?" {
			t.Fatalf("push: %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no push for a new friend chat")
	}
	select {
	case ev := <-events:
		if ev.Kind != "federation" || ev.Agent != "ultron" {
			t.Fatalf("event: %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("no event for a new friend chat")
	}
	m.ID = "msg_2_bbbb"
	a.friendChat(m)                                                                                                        // same pair again
	a.friendChat(core.Mail{ID: "msg_3", FromName: "jugui@jugui.lolad.local", ToName: "milo", Remote: "jugui.lolad.local"}) // to the user
	a.friendChat(core.Mail{ID: "msg_4", FromName: "milo", ToName: "ultron"})                                               // local
	select {
	case got := <-pushes:
		t.Fatalf("unexpected push %q", got)
	case <-time.After(200 * time.Millisecond):
	}
	list, err := a.FriendChats(t.Context())
	if err != nil || len(list) != 1 || list[0].Local != "ultron" || list[0].Remote != "jugui@jugui.lolad.local" || list[0].Subject != "Trip to Lisbon?" {
		t.Fatalf("friend chats: %+v %v", list, err)
	}
	if err = a.DismissFriendChat(t.Context(), "jugui.lolad.local", "jugui@jugui.lolad.local", "ultron"); err != nil {
		t.Fatal(err)
	}
	if list, _ = a.FriendChats(t.Context()); len(list) != 0 {
		t.Fatalf("dismissed chat still listed: %+v", list)
	}
	a.friendChat(m) // dismissed pairs stay quiet
	select {
	case got := <-pushes:
		t.Fatalf("push after dismiss %q", got)
	case <-time.After(200 * time.Millisecond):
	}
}
