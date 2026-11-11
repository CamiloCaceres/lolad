package updater

import (
	"archive/zip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func testKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	b, err := os.ReadFile("testdata/test-key.pem")
	if err != nil {
		t.Fatal(err)
	}
	k, err := ParsePrivateKeyPEM(b)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

type zent struct {
	name, body string
	mode       fs.FileMode
}

// buildZip makes a zip; symlinks use mode fs.ModeSymlink and body = target.
func buildZip(t *testing.T, ents []zent) []byte {
	t.Helper()
	var sb strings.Builder
	zw := zip.NewWriter(&sb)
	for _, e := range ents {
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		h.SetMode(e.mode)
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(e.name, "/") {
			w.Write([]byte(e.body))
		}
	}
	zw.Close()
	return []byte(sb.String())
}

func writeTmp(t *testing.T, dir, name string, b []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// fakeApp zip with Lolad.app/Contents/MacOS/lolad containing marker.
func appZip(t *testing.T, marker string) []byte {
	return buildZip(t, []zent{
		{"Lolad.app/", "", fs.ModeDir | 0o755},
		{"Lolad.app/Contents/", "", fs.ModeDir | 0o755},
		{"Lolad.app/Contents/MacOS/lolad", marker, 0o755},
		{"Lolad.app/Contents/Frameworks/A", "a", 0o644},
		{"Lolad.app/Contents/Frameworks/Current", "A", fs.ModeSymlink | 0o755},
	})
}

type server struct {
	*httptest.Server
	mu       sync.Mutex
	manifest []byte
	files    map[string][]byte
}

func newServer(t *testing.T) *server {
	s := &server{files: map[string][]byte{}}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if r.URL.Path == "/update.json" {
			w.Write(s.manifest)
			return
		}
		if b, ok := s.files[r.URL.Path]; ok {
			w.Write(b)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *server) publish(t *testing.T, key ed25519.PrivateKey, m Manifest) {
	t.Helper()
	raw, _ := json.Marshal(m)
	signed, err := SignManifest(raw, key)
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.manifest = signed
	s.mu.Unlock()
}

// fixture: a server with artifact art.zip for the platform "darwin-arm64".
type fixture struct {
	srv  *server
	key  ed25519.PrivateKey
	dir  string
	body []byte
}

func newFixture(t *testing.T, version string) *fixture {
	f := &fixture{srv: newServer(t), key: testKey(t), dir: t.TempDir(), body: appZip(t, "NEW")}
	f.srv.files["/art.zip"] = f.body
	f.publishManifest(t, version, "mux-2")
	return f
}

func (f *fixture) publishManifest(t *testing.T, version, mux string) {
	f.srv.publish(t, f.key, Manifest{
		Version: version, NotesURL: "https://example.com/notes", MultiplexerVersion: mux,
		Platforms: map[string]Artifact{
			"darwin-arm64": {URL: f.srv.URL + "/art.zip", SHA256: sum(f.body), Size: int64(len(f.body))},
		},
	})
}

func (f *fixture) updater(t *testing.T, mut func(*Config)) *Updater {
	cfg := Config{
		CurrentVersion: "1.0.0", ManifestURL: f.srv.URL + "/update.json",
		PublicKey: f.key.Public().(ed25519.PublicKey), Platform: "darwin-arm64",
		TempDir: f.dir, OnboardingFlag: filepath.Join(f.dir, "flag"),
		MultiplexerVersionFile: filepath.Join(f.dir, "multiplexer.version"),
	}
	if mut != nil {
		mut(&cfg)
	}
	u, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return u
}
