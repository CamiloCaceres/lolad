package app_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CamiloCaceres/lolad/internal/app"
	"github.com/CamiloCaceres/lolad/internal/core"
	"github.com/CamiloCaceres/lolad/internal/hub"
)

// friendApp is an app with no direct federation URL, whose default hub is
// hubURL ($LOLAD_HUB_URL).
func friendApp(t *testing.T, hubURL string) *app.App {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("LOLAD_STATE", filepath.Join(dir, "state"))
	t.Setenv("LOLAD_HUB_URL", hubURL)
	config := filepath.Join(dir, "config.yaml")
	agent := filepath.Join(dir, "agent")
	os.MkdirAll(agent, 0700)
	os.WriteFile(filepath.Join(agent, "lolabot.yaml"), []byte("agent:\n  name: Test\nruntime:\n  harness: claude\n  autostart: false\n"), 0600)
	os.WriteFile(config, []byte("listen: 127.0.0.1:4192\nagents: ["+agent+"]\ntmux_socket: lolad-test-friend-"+filepath.Base(dir)+"\n"), 0600)
	a, e := app.New(t.Context(), config)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { a.Close() })
	return a
}

func testHubServer(t *testing.T, mode string) (*hub.Store, string) {
	t.Helper()
	st, e := hub.OpenStore(filepath.Join(t.TempDir(), "hub.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { st.Close() })
	ts := httptest.NewServer(nil)
	t.Cleanup(ts.Close)
	srv, e := hub.NewServer(hub.Options{Store: st, PublicURL: ts.URL, Signup: mode})
	if e != nil {
		t.Fatal(e)
	}
	ts.Config.Handler = srv.Handler()
	return st, ts.URL
}

// Making a friend link joins the default hub on its own, picking a free
// handle from the tenant.
func TestFriendInviteAutoJoinsHub(t *testing.T) {
	st, url := testHubServer(t, hub.SignupOpen)
	a := friendApp(t, url)
	tenant := strings.TrimSuffix(a.Info(t.Context()).Provider, ".lolad.local")
	// Someone already holds the handle the tenant would give.
	code, _, _ := st.CreateInvite(t.Context(), "", 0)
	if _, e := hub.Register(t.Context(), nil, url, tenant, "", code, newHubSigner(t)); e != nil {
		t.Fatal(e)
	}
	var offer []string
	for _, v := range a.Registry.List() {
		offer = append(offer, v.Name)
	}
	inv, e := a.CreateFriendInvite(t.Context(), core.FriendInviteRequest{Agents: offer})
	if e != nil {
		t.Fatal(e)
	}
	s, _ := a.Hub(t.Context())
	if s == nil || s.Handle != tenant+"-2" || s.URL != url {
		t.Fatalf("%+v", s)
	}
	if got := a.Info(t.Context()).URL; got != url+"/p/"+tenant+"-2" {
		t.Fatal("federation URL", got)
	}
	if !strings.HasPrefix(inv.Link, "https://lolad.caceresdev.com/join#2.") {
		t.Fatal(inv.Link)
	}
	// A second link reuses the membership.
	if _, e = a.CreateFriendInvite(t.Context(), core.FriendInviteRequest{}); e != nil {
		t.Fatal(e)
	}
	if acc, _ := st.Accounts(t.Context()); len(acc) != 2 {
		t.Fatal("joined twice", acc)
	}
}

func TestFriendInviteInviteOnlyHub(t *testing.T) {
	_, url := testHubServer(t, hub.SignupInvite)
	a := friendApp(t, url)
	_, e := a.CreateFriendInvite(t.Context(), core.FriendInviteRequest{})
	var um interface{ UserMessage() string }
	if !errors.As(e, &um) || !strings.Contains(um.UserMessage(), "invite-only") {
		t.Fatal(e)
	}
	if s, _ := a.Hub(t.Context()); s != nil {
		t.Fatal("joined anyway")
	}
}

func TestFriendInviteNoHub(t *testing.T) {
	a := friendApp(t, "") // auto-join off, no direct URL
	_, e := a.CreateFriendInvite(t.Context(), core.FriendInviteRequest{})
	var um interface{ UserMessage() string }
	if !errors.As(e, &um) || !strings.Contains(um.UserMessage(), "turned off") {
		t.Fatal(e)
	}
}

func newHubSigner(t *testing.T) hub.Signer {
	t.Helper()
	pub, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	der, _ := x509.MarshalPKIXPublicKey(pub)
	return hub.Signer{PublicKey: string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})), Sign: func(m []byte) []byte { return ed25519.Sign(key, m) }}
}
