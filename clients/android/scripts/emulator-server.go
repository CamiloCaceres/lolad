//go:build ignore

// An isolated real lolad API with synthetic agents/mail, for emulator verification.
// No harness is launched. Trusted listener: 4193; authenticated listener: 4183.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/CamiloCaceres/lolad/internal/app"
	"github.com/CamiloCaceres/lolad/internal/core"
)

func main() {
	localListener, err := net.Listen("tcp", "127.0.0.1:4193")
	if err != nil {
		log.Fatal(err)
	}
	defer localListener.Close()
	remoteListener, err := net.Listen("tcp", "127.0.0.1:4183")
	if err != nil {
		localListener.Close()
		log.Fatal(err)
	}
	defer remoteListener.Close()
	dir, err := os.MkdirTemp("", "lolad-android-")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(dir)
	os.Setenv("LOLAD_STATE", filepath.Join(dir, "state"))
	os.Setenv("LOLAD_CONFIG", filepath.Join(dir, "config.yaml"))
	for _, name := range []string{"iris", "ultron"} {
		p := filepath.Join(dir, "agents", name)
		os.MkdirAll(p, 0700)
		os.WriteFile(filepath.Join(p, "lolabot.yaml"), []byte(fmt.Sprintf("agent:\n  name: %s\n  role: Personal assistant\nruntime:\n  harness: claude\n  model: claude-opus-4-6\n  autostart: false\n  wake_on_message: false\n", name)), 0600)
	}
	os.WriteFile(os.Getenv("LOLAD_CONFIG"), []byte(fmt.Sprintf("listen: 127.0.0.1:4193\ntmux_socket: lolad-v3-android\nagents_dirs: [%s]\nremote:\n  listen: 127.0.0.1:4183\n", filepath.Join(dir, "agents"))), 0600)
	os.WriteFile(filepath.Join(dir, "org.yaml"), []byte("tenant: android-test\nuser:\n  name: milo\n  display: Milo\ndefaults:\n  user_to_agents: all\n  agents_to_user: all\n  independents: user-only\n"), 0600)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	a, err := app.New(ctx, os.Getenv("LOLAD_CONFIG"))
	if err != nil {
		log.Fatal(err)
	}
	defer a.Close()
	defer exec.Command("tmux", "-L", "lolad-v3-android", "kill-server").Run()
	exec.Command("tmux", "-L", "lolad-v3-android", "-f", "/dev/null", "new-session", "-d", "-s", "iris", "/bin/sh", "-c", "printf 'Iris · isolated Android test\nReady for your message.\n'; sleep 7200").Run()
	send := func(peer, kind, subject, body string, options ...string) {
		_, e := a.Provider.Send(ctx, peer, core.Outgoing{To: a.Me().Name, Subject: subject, Body: body, Context: map[string]any{"userchat": core.UserChat{Kind: kind, Options: options}}})
		if e != nil {
			log.Fatal(e)
		}
	}
	send("ultron", "say", "All quiet", "The demo environment is ready. No live services were contacted.")
	send("iris", "update", "Reviewing", "I’m checking the draft and collecting the final details.")
	send("iris", "deliver", "Your morning brief", "## Ready for review\n\n- The Android connection is isolated.\n- **No live agents** are involved.\n\nNext: review the plan and choose how to proceed.")
	send("iris", "ask", "Which direction?", "The draft is ready. Would you like me to proceed with the plan?", "Proceed", "Revise first")
	trusted := &http.Server{Addr: "127.0.0.1:4193", Handler: a.Handler(false, "android-test", slog.Default()), BaseContext: func(net.Listener) context.Context { return ctx }}
	remote := &http.Server{Addr: "127.0.0.1:4183", Handler: a.Handler(true, "android-test", slog.Default()), BaseContext: func(net.Listener) context.Context { return ctx }}
	go trusted.Serve(localListener)
	go remote.Serve(remoteListener)
	json.NewEncoder(os.Stdout).Encode(map[string]string{"trusted": "http://127.0.0.1:4193", "remote": "http://127.0.0.1:4183", "state": "temporary; removed on exit"})
	<-ctx.Done()
	shutdown, done := context.WithTimeout(context.Background(), 2*time.Second)
	defer done()
	trusted.Shutdown(shutdown)
	remote.Shutdown(shutdown)
}
