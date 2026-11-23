package cli

import (
	"bytes"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/CamiloCaceres/lolad/internal/registry"
)

// freePort returns a loopback port that was free a moment ago.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// occupy holds a loopback port, as another program would.
func occupy(t *testing.T) (string, string) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	_, port, _ := net.SplitHostPort(l.Addr().String())
	return l.Addr().String(), port
}

func never(string) bool  { return false }
func always(string) bool { return true }

func TestListenLocalMovesOffABusyPort(t *testing.T) {
	addr, _ := occupy(t)
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	src := "# mine\ntmux_socket: lolad\nagents_dirs: [/x]\n"
	os.WriteFile(cfg, []byte(src), 0o600)
	free := freePort(t)
	var log bytes.Buffer
	l, err := listenLocal(cfg, addr, &log, []int{free}, never)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	want := "127.0.0.1:" + strconv.Itoa(free)
	if l.Addr().String() != want {
		t.Fatalf("listening on %s, want %s", l.Addr(), want)
	}
	c, err := registry.LoadConfig(cfg)
	if err != nil || c.Listen != want || c.TmuxSocket != "lolad" || len(c.AgentsDirs) != 1 {
		t.Fatalf("config %+v %v", c, err)
	}
	b, _ := os.ReadFile(cfg)
	if !strings.HasPrefix(string(b), src) {
		t.Fatalf("other settings changed:\n%s", b)
	}
	if !strings.Contains(log.String(), "using "+want) {
		t.Fatalf("log: %q", log.String())
	}
}

func TestListenLocalMissingConfigKeepsDefaultAgentsDir(t *testing.T) {
	addr, _ := occupy(t)
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	l, err := listenLocal(cfg, addr, &bytes.Buffer{}, []int{freePort(t)}, never)
	if err != nil {
		t.Fatal(err)
	}
	l.Close()
	c, err := registry.LoadConfig(cfg)
	def := registry.DefaultConfig()
	if err != nil || len(c.AgentsDirs) != 1 || c.AgentsDirs[0] != def.AgentsDirs[0] {
		t.Fatalf("%+v %v", c.AgentsDirs, err)
	}
}

func TestListenLocalExplicitPortIsNotMoved(t *testing.T) {
	addr, port := occupy(t)
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	src := "listen: " + addr + "\n"
	os.WriteFile(cfg, []byte(src), 0o600)
	_, err := listenLocal(cfg, addr, &bytes.Buffer{}, []int{freePort(t)}, never)
	if err == nil || !strings.Contains(err.Error(), "port "+port+" is being used by another program") || !strings.Contains(err.Error(), cfg) {
		t.Fatalf("got %v", err)
	}
	if b, _ := os.ReadFile(cfg); string(b) != src {
		t.Fatalf("config changed: %s", b)
	}
}

func TestListenLocalAnotherLoladKeepsSingleInstance(t *testing.T) {
	addr, _ := occupy(t)
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	_, err := listenLocal(cfg, addr, &bytes.Buffer{}, []int{freePort(t)}, always)
	if err == nil || !strings.Contains(err.Error(), "another lolad") {
		t.Fatalf("got %v", err)
	}
	if _, e := os.Stat(cfg); !os.IsNotExist(e) {
		t.Fatal("config must not be written when a lolad holds the port")
	}
}

func TestListenLocalNoFreePort(t *testing.T) {
	addr, _ := occupy(t)
	other, _ := occupy(t)
	_, p, _ := net.SplitHostPort(other)
	n, _ := strconv.Atoi(p)
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	_, err := listenLocal(cfg, addr, &bytes.Buffer{}, []int{n}, never)
	if err == nil || !strings.Contains(err.Error(), "no free port") {
		t.Fatalf("got %v", err)
	}
}

func TestLoladOnPortReadsHealth(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("hello")) }))
	defer other.Close()
	if loladOnPort(strings.TrimPrefix(other.URL, "http://")) {
		t.Fatal("another web server is not lolad")
	}
	lolad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/health" {
			w.Write([]byte(`{"product":"lolad","version":"1"}`))
		}
	}))
	defer lolad.Close()
	if !loladOnPort(strings.TrimPrefix(lolad.URL, "http://")) {
		t.Fatal("lolad not recognized")
	}
}
