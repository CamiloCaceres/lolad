package cli

import (
	"context"
	"errors"
	"testing"

	"github.com/CamiloCaceres/lolad/internal/api"
	"github.com/CamiloCaceres/lolad/internal/registry"
)

func byID(checks []DoctorCheck, id string) *DoctorCheck {
	for i := range checks {
		if checks[i].ID == id {
			return &checks[i]
		}
	}
	return nil
}

func fakeDeps() doctorDeps {
	return doctorDeps{
		GOOS: "darwin", Home: "/Users/a",
		Find: func(n, _ string) (string, error) {
			if n == "tmux" || n == "claude" {
				return "/bin/" + n, nil
			}
			return "", errors.New("missing")
		},
		Version: func(context.Context, string) string { return "1.2.3" },
		Health:  func(string) (api.Health, bool) { return api.Health{}, false },
		Listen:  func(string) error { return nil },
		Service: func() ServiceStatus { return ServiceStatus{Mechanism: "launchd"} },
	}
}

func TestDoctorChecks(t *testing.T) {
	c := registry.DefaultConfig()
	c.Listen = "127.0.0.1:4100"
	c.AgentsDirs = []string{"/Users/a/Documents/agents", "/Users/a/agents"}
	checks := doctorChecks(context.Background(), c, nil, fakeDeps())
	for id, want := range map[string]string{"config": "ok", "tmux": "ok", "claude": "ok", "codex": "warn", "codewhale": "warn", "port": "ok", "service": "warn", "agent-dir:/Users/a/Documents/agents": "warn"} {
		got := byID(checks, id)
		if got == nil || got.Status != want {
			t.Errorf("%s: %+v want %s", id, got, want)
		}
	}
	if byID(checks, "claude").Message != "/bin/claude (1.2.3)" {
		t.Fatal(byID(checks, "claude").Message)
	}
	if w := byID(checks, "codex"); w.Fix == "" {
		t.Fatal("warn needs a fix")
	}
}

func TestDoctorFailures(t *testing.T) {
	d := fakeDeps()
	d.Find = func(string, string) (string, error) { return "", errors.New("missing") }
	d.Listen = func(string) error { return errors.New("in use") }
	checks := doctorChecks(context.Background(), registry.DefaultConfig(), errors.New("yaml: bad"), d)
	for id, want := range map[string]string{"config": "fail", "tmux": "fail", "claude": "fail", "codex": "warn", "port": "fail"} {
		if got := byID(checks, id); got == nil || got.Status != want {
			t.Errorf("%s: %+v want %s", id, got, want)
		}
	}
}

func TestDoctorPortOwnedByLolad(t *testing.T) {
	d := fakeDeps()
	d.Health = func(string) (api.Health, bool) { return api.Health{Product: "lolad", Version: "1", PID: 9}, true }
	d.Listen = func(string) error { return errors.New("in use") }
	if p := byID(doctorChecks(context.Background(), registry.DefaultConfig(), nil, d), "port"); p.Status != "ok" {
		t.Fatalf("%+v", p)
	}
}

func TestProtectedFolder(t *testing.T) {
	for _, c := range []struct{ goos, dir, want string }{
		{"darwin", "/Users/a/Desktop/x", "~/Desktop"},
		{"darwin", "/Users/a/Downloads", "~/Downloads"},
		{"darwin", "/Users/a/Library/Mobile Documents/com~apple~CloudDocs/a", "iCloud/cloud-synced folder ~/Library/Mobile Documents"},
		{"darwin", "/Users/a/agents", ""},
		{"darwin", "/Users/a/DocumentsX", ""},
		{"linux", "/Users/a/Desktop", ""},
	} {
		if got := protectedFolder(c.goos, "/Users/a", c.dir); got != c.want {
			t.Errorf("%s: %q want %q", c.dir, got, c.want)
		}
	}
}
