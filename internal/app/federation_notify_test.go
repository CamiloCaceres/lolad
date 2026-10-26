package app

import (
	"context"
	"github.com/CamiloCaceres/lolad/internal/core"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFederationApprovalEventAndPush(t *testing.T) {
	received := make(chan [2]string, 1)
	push := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		title := r.Header.Get("Title")
		b, _ := io.ReadAll(r.Body)
		received <- [2]string{title, string(b)}
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
		filepath.Join(dir, "config.yaml"):    "agents_dirs: []\nagents: [" + agent + "]\ntmux_socket: lolad-v4-wiring-notify\n",
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
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	events := a.Subscribe(ctx)
	a.federationApproval(core.Grant{Peer: "jugui.lolad.local", Remote: "jugui@jugui.lolad.local", RemoteDisplay: "Jugui", Local: "ultron", FirstSubject: "Review"})
	pushText := <-received
	if pushText[0] != "Jugui wants to message Ultron: Review" || pushText[1] != pushText[0] {
		t.Fatal("approval push text mismatch")
	}
	select {
	case event := <-events:
		if event.Kind != "federation" || event.Agent != "ultron" {
			t.Fatal(event)
		}
	case <-time.After(time.Second):
		t.Fatal("approval not broadcast to SSE subscribers")
	}
}
