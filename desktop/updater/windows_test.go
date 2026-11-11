package updater

import (
	"errors"
	"strings"
	"testing"
)

// psFake returns "<status>|<subject>" per file path substring.
func psFake(m map[string]string) runner {
	return func(name string, args ...string) ([]byte, error) {
		file := args[len(args)-1]
		for sub, v := range m {
			if strings.Contains(file, sub) {
				return []byte("noise\r\n" + v + "\r\n"), nil
			}
		}
		return []byte("NotSigned|\r\n"), nil
	}
}

func winUpdater(t *testing.T, r runner, allow bool, quit *bool) *Updater {
	return newFixture(t, "1.1.0").updater(t, func(c *Config) { c.run = r; c.AllowUnsigned = allow; c.Quit = func() { *quit = true } })
}

func TestApplyWindows(t *testing.T) {
	ok := map[string]string{"Lolad.exe": "Valid|CN=Caceres", "Setup.exe": "Valid|CN=Caceres"}
	var gotPath string
	var gotArgs []string
	start := func(p string, a ...string) error { gotPath, gotArgs = p, a; return nil }

	quit := false
	u := winUpdater(t, psFake(ok), false, &quit)
	if err := u.applyWin(`C:\t\Setup.exe`, `C:\app\Lolad.exe`, start); err != nil {
		t.Fatal(err)
	}
	if gotPath != `C:\t\Setup.exe` || len(gotArgs) != 1 || gotArgs[0] != "/S" || !quit {
		t.Fatalf("installer not run: %s %v %v", gotPath, gotArgs, quit)
	}

	refuse := func(name string, m map[string]string, allow bool, want error) {
		quit, gotPath = false, ""
		u := winUpdater(t, psFake(m), allow, &quit)
		if err := u.applyWin(`C:\t\Setup.exe`, `C:\app\Lolad.exe`, start); !errors.Is(err, want) {
			t.Errorf("%s: %v", name, err)
		}
		if gotPath != "" || quit {
			t.Errorf("%s: installer ran", name)
		}
	}
	refuse("different signer", map[string]string{"Lolad.exe": "Valid|CN=Caceres", "Setup.exe": "Valid|CN=Mallory"}, false, ErrSignatureInvalid)
	refuse("invalid hash", map[string]string{"Lolad.exe": "Valid|CN=Caceres", "Setup.exe": "HashMismatch|CN=Caceres"}, false, ErrSignatureInvalid)
	refuse("unsigned installer", map[string]string{"Lolad.exe": "Valid|CN=Caceres"}, true, ErrSignatureInvalid)
	refuse("running unsigned", map[string]string{"Setup.exe": "Valid|CN=Caceres"}, false, ErrRunningUnsigned)

	// dev: running exe unsigned + AllowUnsigned skips the check
	quit = false
	u = winUpdater(t, psFake(nil), true, &quit)
	if err := u.applyWin(`C:\t\Setup.exe`, `C:\app\Lolad.exe`, start); err != nil || !quit {
		t.Fatalf("dev skip: %v", err)
	}
}
