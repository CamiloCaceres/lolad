package updater

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Short notes travel in the signed manifest and come back from Check; a
// forged notes field fails the signature like any other field.
func TestCheckCarriesSignedNotes(t *testing.T) {
	f := newFixture(t, "1.1.0")
	m := Manifest{
		Version: "1.1.0", NotesURL: "https://example.com/notes", MultiplexerVersion: "mux-2",
		Notes: "  Settings has an Updates section.\r\nFaster start.  ",
		Platforms: map[string]Artifact{
			"darwin-arm64": {URL: f.srv.URL + "/art.zip", SHA256: sum(f.body), Size: int64(len(f.body))},
		},
	}
	f.srv.publish(t, f.key, m)
	u := f.updater(t, nil)
	res, err := u.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Notes != "Settings has an Updates section.\nFaster start." {
		t.Fatalf("notes = %q", res.Notes)
	}

	f.srv.mu.Lock()
	f.srv.manifest = []byte(strings.Replace(string(f.srv.manifest), "Faster start.", "Install this other thing.", 1))
	f.srv.mu.Unlock()
	if _, err := u.Check(context.Background()); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("edited notes: err = %v, want ErrBadSignature", err)
	}
}

func TestClipNotes(t *testing.T) {
	long := strings.Repeat("é", 700)
	got := clipNotes(long)
	if r := []rune(got); len(r) != 600 || !strings.HasSuffix(got, "…") {
		t.Fatalf("len %d, suffix %q", len(r), string(r[len(r)-1]))
	}
}

// verifyMacZip runs the same checks as applyMac but leaves the app alone and
// cleans up its scratch dir.
func TestVerifyMacZip(t *testing.T) {
	cases := []struct {
		name string
		fc   *fakeCodesign
		want error
	}{
		{"same team", &fakeCodesign{teams: map[string]string{"apps/Lolad.app": "TEAM123", ".lolad-update-": "TEAM123"}}, nil},
		{"unsigned beta, intact seal", &fakeCodesign{teams: map[string]string{}}, nil},
		{"team mismatch", &fakeCodesign{teams: map[string]string{"apps/Lolad.app": "TEAM123", ".lolad-update-": "OTHER99"}}, ErrSignatureInvalid},
		{"broken seal", &fakeCodesign{verifyErr: true, teams: map[string]string{}}, ErrSignatureInvalid},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			zipPath, target := setupMac(t)
			quit := false
			u := macUpdater(t, c.fc, false, &quit)
			err := u.verifyMacZip(zipPath, target)
			if c.want == nil && err != nil || c.want != nil && !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
			if marker(t, target) != "OLD" || quit || c.fc.called("kickstart") {
				t.Fatal("verify must not install, quit or restart anything")
			}
			if _, err := os.Stat(zipPath); err != nil {
				t.Fatal("verify removed the download; Apply still needs it")
			}
			ents, _ := os.ReadDir(filepath.Dir(zipPath))
			if len(ents) != 1 {
				t.Errorf("scratch left beside the download: %v", ents)
			}
		})
	}
}

func TestVerifyMacZipBadArchive(t *testing.T) {
	zipPath, target := setupMac(t)
	os.WriteFile(zipPath, []byte("not a zip"), 0o644)
	quit := false
	u := macUpdater(t, &fakeCodesign{}, false, &quit)
	if err := u.verifyMacZip(zipPath, target); err == nil || !strings.Contains(err.Error(), "unpack") {
		t.Fatalf("err = %v", err)
	}
}

// A daemon launchd does not manage (kickstart fails) is stopped so the
// relaunched app starts the new one; a launchd job is only kickstarted.
func TestApplyMacStopsUnmanagedDaemon(t *testing.T) {
	for _, managed := range []bool{true, false} {
		zipPath, target := setupMac(t)
		fc := &fakeCodesign{teams: map[string]string{}}
		run := fc.run
		if !managed {
			run = func(name string, args ...string) ([]byte, error) {
				out, err := fc.run(name, args...)
				if strings.HasSuffix(name, "launchctl") {
					return []byte("Could not find service"), errors.New("exit 113")
				}
				return out, err
			}
		}
		stopped := false
		f := newFixture(t, "1.1.0")
		u := f.updater(t, func(c *Config) {
			c.run = run
			c.Quit = func() {}
			c.StopDaemon = func() error { stopped = true; return nil }
		})
		if err := u.applyMac(zipPath, target, 501, func(string) error { return nil }); err != nil {
			t.Fatal(err)
		}
		if stopped == managed {
			t.Errorf("managed=%v: StopDaemon called = %v", managed, stopped)
		}
	}
}
