package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const defaultListen = "127.0.0.1:4100"

// configDir mirrors the daemon: LOLAD_CONFIG overrides the file, otherwise
// ~/.config/lolad/config.yaml.
func configPath() string {
	if p := os.Getenv("LOLAD_CONFIG"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "lolad", "config.yaml")
}

func stateDir() string {
	if p := os.Getenv("LOLAD_STATE"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "lolad")
}

// listenAddr reads only the top-level `listen:` key. The app never parses the
// rest of the config; the daemon owns it.
func listenAddr() string {
	if v := os.Getenv("LOLAD_URL"); v != "" {
		return strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(v, "http://"), "https://"), "/")
	}
	f, err := os.Open(configPath())
	if err != nil {
		return defaultListen
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "listen:") {
			continue
		}
		v := strings.TrimSpace(strings.TrimPrefix(line, "listen:"))
		if i := strings.Index(v, " #"); i >= 0 {
			v = v[:i]
		}
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		if v != "" {
			return v
		}
	}
	return defaultListen
}

// dashboardURL turns a listen address into a loopback URL. A wildcard bind
// still reaches the daemon through loopback.
func dashboardURL(listen string) string {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "http://" + listen + "/"
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port) + "/"
}

var probeClient = &http.Client{
	Timeout:       2 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// alive reports whether something answers the dashboard root. There is no
// /api/health yet (docs/desktop.md §8); once it exists this should use it.
func alive(url string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	resp, err := probeClient.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode < 500
}

func findLolad() (string, error) {
	if p := os.Getenv("LOLAD_BIN"); p != "" {
		return p, nil
	}
	exe, _ := os.Executable()
	return findLoladFrom(exe, runtime.GOOS)
}

// findLoladFrom looks for the CLI next to the app: Contents/Helpers inside a
// macOS bundle, bin\ beside Lolad.exe on Windows, then the usual install dirs.
//
// Both platforms have case-insensitive filesystems, so "lolad" beside "Lolad"
// is the app itself. Candidates that are the running executable are skipped.
func findLoladFrom(exe, goos string) (string, error) {
	name := "lolad"
	if goos == "windows" {
		name = "lolad.exe"
	}
	self, _ := os.Stat(exe)
	dir := filepath.Dir(exe)
	home, _ := os.UserHomeDir()
	cands := []string{
		filepath.Join(dir, "..", "Helpers", name),
		filepath.Join(dir, "bin", name),
		filepath.Join(dir, name),
		filepath.Join(home, ".local", "bin", name),
		"/opt/homebrew/bin/" + name,
		"/usr/local/bin/" + name,
	}
	for _, c := range cands {
		st, err := os.Stat(c)
		if err != nil || st.IsDir() || (self != nil && os.SameFile(self, st)) {
			continue
		}
		return c, nil
	}
	p, err := exec.LookPath(name)
	if err != nil {
		return "", err
	}
	if st, e := os.Stat(p); e == nil && self != nil && os.SameFile(self, st) {
		return "", errors.New("lolad executable not found")
	}
	return p, nil
}

// startDaemon launches `lolad serve` detached with a minimal environment.
// Agents inherit the daemon's environment, so this must never forward the
// app's own variables (they can carry tokens from whatever launched us).
func startDaemon() error {
	bin, err := findLolad()
	if err != nil {
		return fmt.Errorf("lolad executable not found: %w", err)
	}
	logDir := filepath.Join(stateDir(), "logs")
	_ = os.MkdirAll(logDir, 0o700)
	logf, err := os.OpenFile(filepath.Join(logDir, "daemon.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	cmd := exec.Command(bin, "serve")
	cmd.Env = cleanEnv()
	cmd.Stdout, cmd.Stderr = logf, logf
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func cleanEnv() []string {
	keep := []string{"HOME", "USER", "LOGNAME", "LANG", "TMPDIR", "LOLAD_CONFIG", "LOLAD_STATE",
		"USERPROFILE", "APPDATA", "LOCALAPPDATA", "SystemRoot", "TEMP", "TMP"}
	var env []string
	for _, k := range keep {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	// A GUI app gets launchd's minimal PATH; add the usual harness locations
	// until onboarding stores the real one (docs/desktop.md §7.2).
	home, _ := os.UserHomeDir()
	path := strings.Join([]string{
		filepath.Join(home, ".local", "bin"), "/opt/homebrew/bin", "/usr/local/bin", "/usr/bin", "/bin", "/usr/sbin", "/sbin",
	}, string(os.PathListSeparator))
	if runtime.GOOS == "windows" {
		path = os.Getenv("PATH")
	}
	return append(env, "PATH="+path)
}
