package registry

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	for _, ok := range []string{"listen: 127.0.0.1:4100\n", "version: 1\nlisten: 127.0.0.1:4100\n"} {
		os.WriteFile(path, []byte(ok), 0o600)
		if _, err := LoadConfig(path); err != nil {
			t.Fatalf("%q: %v", ok, err)
		}
	}
	// A newer file is refused before its (possibly changed) layout is decoded.
	os.WriteFile(path, []byte("version: 2\nlisten:\n  - a new shape\n"), 0o600)
	_, err := LoadConfig(path)
	if err == nil || !strings.Contains(err.Error(), "newer version of Lolad") || !strings.Contains(err.Error(), path) {
		t.Fatalf("got %v", err)
	}
	b, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(b), "version: 2") {
		t.Fatal("config was rewritten")
	}
}

func TestYAMLErrorsNameFileAndLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(path, []byte("listen: 127.0.0.1:4100\nagents_dirs:\n  - a\n bad: [\n"), 0o600)
	_, err := LoadConfig(path)
	var fe *FileError
	if !errors.As(err, &fe) || fe.Line == 0 || !strings.HasPrefix(err.Error(), path+", line ") || strings.Contains(err.Error(), "yaml:") {
		t.Fatalf("got %v", err)
	}
	os.WriteFile(path, []byte("listen: 127.0.0.1:4100\nagents:\n  nested: map\n"), 0o600)
	_, err = LoadConfig(path)
	if !errors.As(err, &fe) || fe.Line != 3 || !strings.Contains(err.Error(), path+", line 3: cannot unmarshal") {
		t.Fatalf("type error: %v", err)
	}
}

func TestSetListenKeepsTheRest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	src := "# my settings\nagents_dirs:\n  - /x # here\n\ntmux_socket: lolad\n"
	os.WriteFile(path, []byte(src), 0o640)
	if set, err := ListenSet(path); err != nil || set {
		t.Fatalf("%v %v", set, err)
	}
	if err := SetListen(path, "127.0.0.1:4101", "port 4100 was taken"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	got := string(b)
	if !strings.HasPrefix(got, src) || !strings.Contains(got, "# port 4100 was taken\n") {
		t.Fatalf("other lines changed:\n%s", got)
	}
	c, err := LoadConfig(path)
	if err != nil || c.Listen != "127.0.0.1:4101" || c.TmuxSocket != "lolad" || len(c.AgentsDirs) != 1 {
		t.Fatalf("%+v %v", c, err)
	}
	if set, _ := ListenSet(path); !set {
		t.Fatal("listen should now be set")
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o640 {
		t.Fatalf("mode %v", fi.Mode())
	}
	// An existing line is replaced in place.
	if err := SetListen(path, "127.0.0.1:4102", ""); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(path)
	if strings.Count(string(b), "listen:") != 1 || !strings.Contains(string(b), "4102") {
		t.Fatalf("%s", b)
	}
	// A missing file is created.
	fresh := filepath.Join(dir, "sub", "config.yaml")
	if err := SetListen(fresh, "127.0.0.1:4103", ""); err != nil {
		t.Fatal(err)
	}
	if c, err := LoadConfig(fresh); err != nil || c.Listen != "127.0.0.1:4103" {
		t.Fatalf("%+v %v", c, err)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".config-") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
}
