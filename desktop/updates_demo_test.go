package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/CamiloCaceres/lolad/desktop/updater"
)

// TestUpdatesDemo runs the real update flow against the fake update server
// behind a small control API, so Settings → Updates can be driven through
// every state for screenshots (only Verify/Apply are faked, as in the other
// flow tests). It only runs when LOLAD_UPDATES_DEMO_ADDR is set:
//
//	LOLAD_UPDATES_DEMO_ADDR=127.0.0.1:4791 go test -run TestUpdatesDemo -timeout 0 .
//
//	POST /reset?current=1.0.0   fresh flow (and server) for that version
//	POST /server?status=503 | ?corrupt=1 | ?chunked=1 | ?offline=1
//	POST /chunk                 release one quarter of a chunked download
//	POST /backend?verify=bad | ?apply=bad | ?onboarding=1
//	POST /action/<check|download|restart|retry|reopen>   (download runs async)
//	GET  /state                 the UpdateState JSON the page receives
func TestUpdatesDemo(t *testing.T) {
	addr := os.Getenv("LOLAD_UPDATES_DEMO_ADDR")
	if addr == "" {
		t.Skip("set LOLAD_UPDATES_DEMO_ADDR to run the demo")
	}
	var mu sync.Mutex
	var x *flowFixture
	gate := make(chan struct{}, 8)
	reset := func(cur string) {
		mu.Lock()
		defer mu.Unlock()
		x = newFlowFixture(t, cur)
		x.srv.publish(t, "1.1.0", "Settings now has an Updates section: check, download and restart from one place.\nFaster start when many assistants are running.")
		x.f.now = time.Now
	}
	reset("1.0.0")
	fx := func() *flowFixture { mu.Lock(); defer mu.Unlock(); return x }
	mux := http.NewServeMux()
	mux.HandleFunc("POST /reset", func(w http.ResponseWriter, r *http.Request) {
		cur := r.URL.Query().Get("current")
		if cur == "" {
			cur = "1.0.0"
		}
		reset(cur)
	})
	mux.HandleFunc("POST /server", func(w http.ResponseWriter, r *http.Request) {
		q, s := r.URL.Query(), fx().srv
		if q.Get("offline") != "" {
			s.Close()
			return
		}
		s.set(func(s *fakeUpdateServer) {
			if v := q.Get("status"); v != "" {
				s.status = atoiOr(v, 0)
			}
			s.corrupt = q.Get("corrupt") != ""
			if q.Get("chunked") != "" {
				s.chunk = gate
			}
		})
	})
	mux.HandleFunc("POST /chunk", func(http.ResponseWriter, *http.Request) { gate <- struct{}{} })
	mux.HandleFunc("POST /backend", func(w http.ResponseWriter, r *http.Request) {
		q, b := r.URL.Query(), fx().b
		b.verifyErr, b.applyErr = nil, nil
		if q.Get("verify") != "" {
			b.verifyErr = fmt.Errorf("%w: team id \"OTHER99\", running app has \"TEAM123\"", updater.ErrSignatureInvalid)
		}
		if q.Get("apply") != "" {
			b.applyErr = &os.PathError{Op: "rename", Path: "/Applications/Lolad.app", Err: os.ErrPermission}
		}
		b.onboarding = q.Get("onboarding") != ""
	})
	mux.HandleFunc("POST /action/{name}", func(w http.ResponseWriter, r *http.Request) {
		f := fx().f
		switch r.PathValue("name") {
		case "check":
			f.Check(bg, false)
		case "download":
			go f.Download(bg)
			time.Sleep(100 * time.Millisecond)
		case "restart":
			f.Restart()
		case "retry":
			f.Retry(bg)
		case "reopen":
			f.set(func(s *UpdateState) { s.Phase = phUpdated })
		}
	})
	mux.HandleFunc("GET /state", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(fx().f.State())
	})
	t.Logf("update demo on http://%s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		t.Fatal(err)
	}
}

func atoiOr(s string, d int) int {
	n := 0
	for _, c := range strings.TrimSpace(s) {
		if c < '0' || c > '9' {
			return d
		}
		n = n*10 + int(c-'0')
	}
	return n
}
