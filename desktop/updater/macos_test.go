package updater

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeCodesign answers codesign/launchctl calls. team maps a bundle path
// (by suffix of the base dir) to its team id; "" = ad hoc.
type fakeCodesign struct {
	calls     []string
	teams     map[string]string // path substring -> team ("" adhoc, "!" unsigned)
	verifyErr bool
}

func (f *fakeCodesign) run(name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	if strings.HasSuffix(name, "codesign") {
		path := args[len(args)-1]
		team := ""
		for sub, t := range f.teams {
			if strings.Contains(path, sub) {
				team = t
			}
		}
		if args[0] == "--verify" {
			if f.verifyErr {
				return []byte("a sealed resource is missing or invalid"), errors.New("exit 1")
			}
			return nil, nil
		}
		switch team {
		case "!":
			return []byte("code object is not signed at all"), errors.New("exit 1")
		case "":
			return []byte("Identifier=dev.caceres.lolad\nSignature=adhoc\nTeamIdentifier=not set\n"), nil
		}
		return []byte("Identifier=dev.caceres.lolad\nAuthority=Developer ID Application: X\nTeamIdentifier=" + team + "\n"), nil
	}
	return nil, nil
}

func (f *fakeCodesign) called(sub string) bool {
	for _, c := range f.calls {
		if strings.Contains(c, sub) {
			return true
		}
	}
	return false
}

func setupMac(t *testing.T) (zipPath, target string) {
	dir := t.TempDir()
	target = filepath.Join(dir, "apps", "Lolad.app")
	os.MkdirAll(filepath.Join(target, "Contents/MacOS"), 0o755)
	os.WriteFile(filepath.Join(target, "Contents/MacOS/lolad"), []byte("OLD"), 0o755)
	dl := filepath.Join(dir, "dl")
	os.Mkdir(dl, 0o755)
	zipPath = writeTmp(t, dl, "Lolad.app.zip", appZip(t, "NEW"))
	return
}

func marker(t *testing.T, app string) string {
	b, err := os.ReadFile(filepath.Join(app, "Contents/MacOS/lolad"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func macUpdater(t *testing.T, fc *fakeCodesign, allowUnsigned bool, quit *bool) *Updater {
	f := newFixture(t, "1.1.0")
	return f.updater(t, func(c *Config) {
		c.run = fc.run
		c.AllowUnsigned = allowUnsigned
		c.Quit = func() { *quit = true }
	})
}

func TestApplyMacSameTeam(t *testing.T) {
	zipPath, target := setupMac(t)
	fc := &fakeCodesign{teams: map[string]string{"apps/Lolad.app": "TEAM123", ".lolad-update-": "TEAM123"}}
	quit := false
	u := macUpdater(t, fc, false, &quit)
	var relaunched string
	if err := u.applyMac(zipPath, target, 501, func(a string) error { relaunched = a; return nil }); err != nil {
		t.Fatal(err)
	}
	if marker(t, target) != "NEW" || relaunched != target || !quit {
		t.Fatalf("not swapped/relaunched: %s %s %v", marker(t, target), relaunched, quit)
	}
	if !fc.called("--verify --deep --strict") || !fc.called("launchctl kickstart -k gui/501/dev.caceres.lolad") {
		t.Fatalf("calls: %v", fc.calls)
	}
	if _, err := os.Stat(zipPath); err == nil {
		t.Error("downloaded zip not cleaned")
	}
	ents, _ := os.ReadDir(filepath.Dir(target))
	if len(ents) != 1 {
		t.Errorf("leftovers beside the app: %v", ents)
	}
}

func TestApplyMacRefusals(t *testing.T) {
	run := func(name string, fc *fakeCodesign, allow bool, wantErr error) {
		t.Run(name, func(t *testing.T) {
			zipPath, target := setupMac(t)
			quit := false
			u := macUpdater(t, fc, allow, &quit)
			err := u.applyMac(zipPath, target, 501, func(string) error { t.Error("relaunched"); return nil })
			if !errors.Is(err, wantErr) {
				t.Fatalf("err=%v want %v", err, wantErr)
			}
			if marker(t, target) != "OLD" || quit {
				t.Fatal("old app must be untouched")
			}
			if fc.called("kickstart") {
				t.Fatal("daemon restarted on a refused update")
			}
			if ents, _ := os.ReadDir(filepath.Dir(target)); len(ents) != 1 {
				t.Errorf("staging left behind: %v", ents)
			}
		})
	}
	run("team mismatch", &fakeCodesign{teams: map[string]string{"apps/Lolad.app": "TEAM123", ".lolad-update-": "OTHER99"}}, false, ErrSignatureInvalid)
	run("new bundle ad hoc", &fakeCodesign{teams: map[string]string{"apps/Lolad.app": "TEAM123", ".lolad-update-": ""}}, false, ErrSignatureInvalid)
	run("deep verify fails", &fakeCodesign{verifyErr: true, teams: map[string]string{"apps/Lolad.app": "TEAM123", ".lolad-update-": "TEAM123"}}, false, ErrSignatureInvalid)
	run("running app ad hoc", &fakeCodesign{teams: map[string]string{}}, false, ErrRunningUnsigned)
	run("running app unsigned", &fakeCodesign{teams: map[string]string{"apps/Lolad.app": "!"}}, false, ErrRunningUnsigned)
}

func TestApplyMacAllowUnsignedSkipsBothChecks(t *testing.T) {
	zipPath, target := setupMac(t)
	fc := &fakeCodesign{teams: map[string]string{}} // everything ad hoc
	quit := false
	u := macUpdater(t, fc, true, &quit)
	if err := u.applyMac(zipPath, target, 501, func(string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if fc.called("--verify") || marker(t, target) != "NEW" {
		t.Fatalf("checks not skipped: %v", fc.calls)
	}
	// AllowUnsigned must NOT skip checks when the running app is properly signed.
	zipPath, target = setupMac(t)
	fc = &fakeCodesign{teams: map[string]string{"apps/Lolad.app": "TEAM123", ".lolad-update-": "OTHER"}}
	u = macUpdater(t, fc, true, &quit)
	if err := u.applyMac(zipPath, target, 501, func(string) error { return nil }); !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("signed running app + allow flag must still verify: %v", err)
	}
}

func TestApplyMacBadZipLeavesOldApp(t *testing.T) {
	_, target := setupMac(t)
	dl := t.TempDir()
	zipPath := writeTmp(t, dl, "x.zip", buildZip(t, []zent{{"../evil", "x", 0o644}}))
	quit := false
	u := macUpdater(t, &fakeCodesign{}, true, &quit)
	if err := u.applyMac(zipPath, target, 1, func(string) error { return nil }); err == nil {
		t.Fatal("traversal zip accepted")
	}
	zipPath = writeTmp(t, dl, "y.zip", buildZip(t, []zent{{"NotAnApp/x", "x", 0o644}}))
	if err := u.applyMac(zipPath, target, 1, func(string) error { return nil }); err == nil {
		t.Fatal("zip without .app accepted")
	}
	if marker(t, target) != "OLD" {
		t.Fatal("old app changed")
	}
}

func TestSwapBundleRollsBack(t *testing.T) {
	_, target := setupMac(t)
	missing := filepath.Join(filepath.Dir(target), "does-not-exist.app")
	if err := swapBundle(missing, target); err == nil {
		t.Fatal("expected failure")
	}
	if marker(t, target) != "OLD" {
		t.Fatal("old bundle not restored")
	}
	if ents, _ := os.ReadDir(filepath.Dir(target)); len(ents) != 1 {
		t.Fatalf("aside copy left: %v", ents)
	}
	// target missing entirely: fails cleanly
	if err := swapBundle(missing, filepath.Join(filepath.Dir(target), "Nope.app")); err == nil {
		t.Fatal("expected failure")
	}
}

func TestApplyMacRelaunchFailureReported(t *testing.T) {
	zipPath, target := setupMac(t)
	quit := false
	u := macUpdater(t, &fakeCodesign{}, true, &quit)
	err := u.applyMac(zipPath, target, 1, func(string) error { return fmt.Errorf("boom") })
	if err == nil || !strings.Contains(err.Error(), "relaunch") || marker(t, target) != "NEW" || quit {
		t.Fatalf("err=%v", err)
	}
}
