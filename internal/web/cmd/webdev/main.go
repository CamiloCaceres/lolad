// Command webdev serves the dashboard against the in-memory fake, so the UI
// can be built and judged without the daemon:
//
//	go run ./internal/web/cmd/webdev            # http://127.0.0.1:4199
//	go run ./internal/web/cmd/webdev -live      # re-read templates and assets from disk
package main

import (
	"flag"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"

	"github.com/CamiloCaceres/lolad/internal/web"
	"github.com/CamiloCaceres/lolad/internal/web/fake"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:4199", "listen address")
	live := flag.Bool("live", false, "serve templates and static files from the source tree, re-read on every request")
	quiet := flag.Bool("still", false, "don't simulate activity (no state changes or incoming mail)")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	svc := fake.New(fake.Options{Simulate: !*quiet})
	defer svc.Close()

	opts := web.Options{Logger: log}
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
