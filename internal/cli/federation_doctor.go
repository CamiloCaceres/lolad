package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/CamiloCaceres/lolad/internal/app"
	"github.com/CamiloCaceres/lolad/internal/core"
	"github.com/CamiloCaceres/lolad/internal/registry"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func federationDoctor(ctx context.Context, c registry.Config, out io.Writer) {
	if !c.Federation.Enabled {
		fmt.Fprintln(out, "INFO federation disabled")
		return
	}
	check := func(ok bool, label, fix string) {
		if ok {
			fmt.Fprintln(out, "OK   "+label)
		} else {
			fmt.Fprintln(out, "FIX  "+label+": "+fix)
		}
	}
	info, err := os.Stat(filepath.Join(c.StateDir, "federation", "provider.key"))
	check(err == nil && info.Mode().IsRegular(), "federation provider key", "start lolad to create its private provider identity")
	check(c.Federation.URL != "", "federation URL configured", "set federation.url to an HTTPS origin reachable by the other lolad")
	if c.Federation.URL != "" {
		client := http.Client{Timeout: 3 * time.Second}
		req, e := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(c.Federation.URL, "/")+"/healthz", nil)
		reachable := false
		if e == nil {
			resp, e := client.Do(req)
			if e == nil {
				reachable = resp.StatusCode == 200
				resp.Body.Close()
			}
		}
		check(reachable, "federation URL reachable from this machine", "check Tailscale Serve, certificates and listener; also verify reachability from the peer")
	}
	var b strings.Builder
	if err := request(ctx, app.URL(c.Listen), "GET", "/api/connections", nil, &b); err != nil {
		fmt.Fprintln(out, "FIX  federation peers: start lolad serve to inspect peers")
		return
	}
	var peers []core.Connection
	if json.Unmarshal([]byte(b.String()), &peers) != nil {
		fmt.Fprintln(out, "FIX  federation peers: invalid daemon response")
		return
	}
	for _, p := range peers {
		check(p.LastError == "", "federation peer "+p.Provider, p.LastError)
	}
}
