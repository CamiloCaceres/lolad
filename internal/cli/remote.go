package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/CamiloCaceres/lolad/internal/app"
	"github.com/CamiloCaceres/lolad/internal/auth"
	"github.com/CamiloCaceres/lolad/internal/registry"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func remoteCommand(ctx context.Context, base string, c registry.Config, args []string, out io.Writer) error {
	switch args[0] {
	case "pair":
		name := ""
		if len(args) == 3 && args[1] == "--name" {
			name = args[2]
		} else if len(args) != 1 {
			return fmt.Errorf("usage: lolad pair [--name device]")
		}
		var b strings.Builder
		if e := request(ctx, base, "POST", "/api/devices/pair", map[string]string{"name": name}, &b); e != nil {
			return e
		}
		var p auth.Pairing
		if e := json.Unmarshal([]byte(b.String()), &p); e != nil {
			return e
		}
		fmt.Fprintf(out, "Pairing code: %s\nExpires: %s (single use)\n", p.Code, p.Expires.Format(time.RFC3339))
		if c.Remote.PublicURL != "" {
			fmt.Fprintln(out, "Open:", c.Remote.PublicURL+"/login")
			fmt.Fprintln(out, "Phone:", auth.PairURL(c.Remote.PublicURL, p.Code))
		}
		return nil
	case "devices":
		if len(args) == 1 || len(args) == 2 && args[1] == "ls" {
			return request(ctx, base, "GET", "/api/devices", nil, out)
		}
		if len(args) == 3 && args[1] == "rm" {
			return request(ctx, base, "DELETE", "/api/devices/"+args[2], nil, out)
		}
		return fmt.Errorf("usage: lolad devices [ls|rm <id>]")
	case "doctor":
		return doctor(ctx, c, out)
	}
	return fmt.Errorf("unknown command")
}
func doctor(ctx context.Context, c registry.Config, out io.Writer) error {
	federationDoctor(ctx, c, out)
	check := func(ok bool, label, fix string) {
		if ok {
			fmt.Fprintln(out, "OK   "+label)
		} else {
			fmt.Fprintln(out, "FIX  "+label+": "+fix)
		}
	}
	e := app.ValidateListeners(c.Listen, c.Remote.Listen, c.Remote.PublicURL)
	check(e == nil, "listener config", "use distinct numeric loopback addresses and an HTTPS public_url")
	for _, d := range append(append([]string{}, c.AgentsDirs...), c.Agents...) {
		info, e := os.Stat(d)
		check(e == nil && info.IsDir(), "agent directory "+d, "create it or correct agents_dirs/agents in config")
	}
	doctorTmux(ctx, c, out)
	for _, bin := range []string{"claude", "codewhale"} {
		if bin == "claude" && c.ClaudeBin != "" {
			bin = c.ClaudeBin
		}
		_, e := exec.LookPath(bin)
		check(e == nil, "binary "+bin, "install it or fix PATH/claude_bin")
	}
	for _, addr := range []string{c.Listen, c.Remote.Listen} {
		if addr == "" {
			continue
		}
		conn, e := net.DialTimeout("tcp", addr, time.Second)
		check(e == nil, "listener "+addr, "start/restart lolad serve and check port conflicts")
		if conn != nil {
			conn.Close()
		}
	}
	if c.Remote.Listen == "" {
		fmt.Fprintln(out, "INFO remote access disabled (remote.listen is empty)")
	} else {
		path, e := exec.LookPath("tailscale")
		if e != nil {
			fmt.Fprintln(out, "FIX  Tailscale CLI: install Tailscale and enable HTTPS certificates")
		} else {
			tsctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			b, e := exec.CommandContext(tsctx, path, "serve", "status", "--json").Output()
			// Inspect the exact public host and root proxy without echoing tailnet data.
			mapped := e == nil && serveMapped(b, c.Remote.PublicURL, c.Remote.Listen)
			check(mapped, "Tailscale Serve mapping", "run tailscale serve --bg --https=<public URL port> http://"+c.Remote.Listen)
		}
	}
	latest := app.LatestBackup(c.StateDir)
	check(!latest.IsZero() && time.Since(latest) < 48*time.Hour, "daily database backup", "keep lolad running; inspect "+filepath.Join(c.StateDir, "backups")+" and disk space")
	// This probe is read-only and only uses the configured trusted listener.
	client := http.Client{Timeout: 2 * time.Second}
	req, _ := http.NewRequestWithContext(ctx, "GET", app.URL(c.Listen)+"/healthz", nil)
	resp, e := client.Do(req)
	check(e == nil && resp.StatusCode == 200, "daemon health", "start lolad serve")
	if resp != nil {
		resp.Body.Close()
	}
	return nil
}

func serveMapped(data []byte, public, backend string) bool {
	var config struct {
		Web map[string]struct {
			Handlers map[string]struct{ Proxy string }
		}
	}
	if json.Unmarshal(data, &config) != nil {
		return false
	}
	origin, e := url.Parse(public)
	if e != nil || origin.Host == "" {
		return false
	}
	host := origin.Host
	if origin.Port() == "" {
		host = origin.Hostname() + ":443"
	}
	web, ok := config.Web[host]
	if !ok {
		return false
	}
	return web.Handlers["/"].Proxy == "http://"+backend
}
