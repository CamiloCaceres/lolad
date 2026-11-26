package web_test

import (
	"io"
	"log/slog"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/CamiloCaceres/lolad/internal/web"
	"github.com/CamiloCaceres/lolad/internal/web/fake"
)

// TestUIDemoServer serves the dashboard over the fake service for manual
// looks and screenshots (e.g. Settings → Updates). It only runs when
// LOLAD_UI_DEMO_ADDR is set:
//
//	LOLAD_UI_DEMO_ADDR=127.0.0.1:4790 go test -run TestUIDemoServer -timeout 0 ./internal/web/
func TestUIDemoServer(t *testing.T) {
	addr := os.Getenv("LOLAD_UI_DEMO_ADDR")
	if addr == "" {
		t.Skip("set LOLAD_UI_DEMO_ADDR to run the demo server")
	}
	f := fake.New(fake.Options{})
	defer f.Close()
	srv := &http.Server{Addr: addr, Handler: web.New(f, web.Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}), ReadHeaderTimeout: 10 * time.Second}
	t.Logf("dashboard on http://%s", addr)
	if err := srv.ListenAndServe(); err != nil {
		t.Fatal(err)
	}
}
