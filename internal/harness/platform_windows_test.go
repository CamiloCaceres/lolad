package harness

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWindowsLaunchEnvironment(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	env := map[string]string{"Path": "C:\\tools;C:\\Windows", "USERPROFILE": home}
	normalizeLaunchEnv(env)
	if env["PATH"] != "C:\\tools;C:\\Windows" || env["HOME"] != home {
		t.Fatal("Windows PATH/HOME were not normalized")
	}
	if _, ok := env["Path"]; ok {
		t.Fatal("duplicate case-insensitive Path key")
	}
	env["HOME"] = "custom-home"
	normalizeLaunchEnv(env)
	if env["HOME"] != "custom-home" {
		t.Fatal("explicit HOME replaced")
	}
}
func TestWindowsJSONLockSerializesWriters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	first, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := os.OpenFile(path, os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if err := lockFile(first); err != nil {
		t.Fatal(err)
	}
	defer unlockFile(first)
	acquired := make(chan error, 1)
	go func() { acquired <- lockFile(second) }()
	select {
	case err := <-acquired:
		if err == nil {
			unlockFile(second)
		}
		t.Fatalf("second writer did not wait: %v", err)
	case <-time.After(75 * time.Millisecond):
	}
	unlockFile(first)
	select {
	case err := <-acquired:
		if err != nil {
			t.Fatal(err)
		}
		unlockFile(second)
	case <-time.After(3 * time.Second):
		t.Fatal("writer did not acquire released lock")
	}
}
