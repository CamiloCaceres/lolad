// Command webdev serves the dashboard against the in-memory fake, so the UI
// can be built and judged without the daemon:
//
//	go run ./internal/web/cmd/webdev            # http://127.0.0.1:4199
//	go run ./internal/web/cmd/webdev -live      # re-read templates and assets from disk
//	go run ./internal/web/cmd/webdev -empty     # first-run state
//	go run ./internal/web/cmd/webdev -welcome-done  # onboarding finished: /welcome shows the last screen
//	go run ./internal/web/cmd/webdev -rename manual # renaming waits for a manual restart (or: failed)
package main

import (
	"flag"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"

	"github.com/CamiloCaceres/lolad/internal/setup"
	"github.com/CamiloCaceres/lolad/internal/web"
	"github.com/CamiloCaceres/lolad/internal/web/fake"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:4199", "listen address")
	live := flag.Bool("live", false, "serve templates and static files from the source tree, re-read on every request")
	quiet := flag.Bool("still", false, "don't simulate activity (no state changes or incoming mail)")
	empty := flag.Bool("empty", false, "start like a fresh install: no agents, no mail")
	done := flag.Bool("welcome-done", false, "onboarding already finished, so /welcome shows its last screen")
	rename := flag.String("rename", "", "manual: renaming waits for a restart; failed: the last rename rolled back")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	svc := fake.New(fake.Options{Simulate: !*quiet, Empty: *empty})
	defer svc.Close()
	if !*empty { // a friend on Jugui's lolad started talking to Ultron
		svc.AddFriendChat(web.FriendChat{Peer: "jugui.lolad.local", PeerDisplay: "Jugui", Remote: "jugui@jugui.lolad.local", RemoteDisplay: "Jugui", Kind: "human", Local: "ultron", Subject: "Can you help me plan a trip to Lisbon?"})
	}
	switch *rename {
	case "manual":
		svc.SetRenameRestart("manual")
	case "failed":
		svc.FailLastRename("milo-pc", "lolad couldn’t save the new name, so it put the old one back")
	}

	demo := setup.NewDemo()
	if *done {
		demo.Set(func(st *setup.Status) {
			st.ServiceDone, st.FolderSet, st.NameSet, st.SkippedAgent = true, true, true, true
			st.Harnesses[0].Installed, st.Harnesses[0].LoggedIn = true, true
		})
	}
	opts := web.Options{Logger: log, Setup: demo} // /welcome with canned detection
	if *live {
		_, file, _, _ := runtime.Caller(0)
		opts.Dir = filepath.Join(filepath.Dir(file), "..", "..")
	}
	log.Info("lolad dashboard (fake service)", "url", "http://"+*addr, "live", *live, "simulate", !*quiet)
	if err := http.ListenAndServe(*addr, web.New(svc, opts)); err != nil {
		log.Error("serve", "err", err)
		os.Exit(1)
	}
}
