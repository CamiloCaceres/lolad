package app_test

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/CamiloCaceres/lolad/internal/core"
	"github.com/CamiloCaceres/lolad/internal/hub"
)

func TestJoinAndLeaveHub(t *testing.T) {
	st, e := hub.OpenStore(filepath.Join(t.TempDir(), "hub.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	var srv *hub.Server
	ts := httptest.NewServer(nil)
	defer ts.Close()
	if srv, e = hub.NewServer(hub.Options{Store: st, PublicURL: ts.URL}); e != nil {
		t.Fatal(e)
	}
	ts.Config.Handler = srv.Handler()
	code, _, _ := st.CreateInvite(t.Context(), "", time.Hour)

	a := remoteApp(t)
	direct := a.Info(t.Context()).URL
	if s, _ := a.Hub(t.Context()); s != nil {
		t.Fatal("joined before join")
	}
	if _, e = a.JoinHub(t.Context(), ts.URL, "milo", "hubinv_wrong"); e == nil {
		t.Fatal("bad code accepted")
	}
	s, e := a.JoinHub(t.Context(), ts.URL, "milo", code)
	if e != nil || s == nil || s.Handle != "milo" {
		t.Fatal(s, e)
	}
	info := a.Info(t.Context())
	if info.URL != ts.URL+"/p/milo" || info.Hub == nil {
		t.Fatal("federation URL not moved to the hub", info)
	}
	if _, e = a.JoinHub(t.Context(), ts.URL, "other", code); e == nil {
		t.Fatal("second hub membership accepted")
	}
	cfgDir := filepath.Dir(os.Getenv("LOLAD_STATE"))
	if fi, e := os.Stat(filepath.Join(cfgDir, "hub.yaml")); e != nil || fi.Mode().Perm() != 0o600 {
		t.Fatal("hub.yaml", e)
	}
	// Invites now carry the hub URL.
	if _, e = a.CreateInvite(t.Context()); e != nil {
		t.Fatal(e)
	}
	if e = a.LeaveHub(t.Context()); e != nil {
		t.Fatal(e)
	}
	if _, e = st.Account(t.Context(), "milo"); e == nil {
		t.Fatal("handle not released")
	}
	if got := a.Info(t.Context()); got.URL != direct || got.Hub != nil {
		t.Fatal("URL not restored", got)
	}
	if e = a.LeaveHub(t.Context()); e != core.ErrNotFound {
		t.Fatal(e)
	}
}
