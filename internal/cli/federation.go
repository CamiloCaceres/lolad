package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/CamiloCaceres/lolad/internal/core"
	"io"
	"net/url"
	"strings"
)

func federationCommand(ctx context.Context, base string, args []string, stdin io.Reader, out io.Writer) error {
	switch args[0] {
	case "connections", "approvals":
		if len(args) != 1 {
			return fmt.Errorf("usage: lolad %s", args[0])
		}
		path := "/api/connections"
		if args[0] == "approvals" {
			path = "/api/grants"
		}
		return request(ctx, base, "GET", path, nil, out)
	case "approve", "block":
		if len(args) != 4 {
			return fmt.Errorf("usage: lolad %s <peer> <remote> <local>", args[0])
		}
		return request(ctx, base, "POST", "/api/grants/decide", map[string]any{"peer": args[1], "remote": args[2], "local": args[3], "allow": args[0] == "approve"}, out)
	}
	if len(args) < 2 {
		return fmt.Errorf("usage: lolad connect invite|accept|share|unshare|pause|resume|rm")
	}
	switch args[1] {
	case "invite":
		if len(args) != 2 {
			return fmt.Errorf("usage: lolad connect invite")
		}
		return request(ctx, base, "POST", "/api/connections/invite", nil, out)
	case "accept":
		if len(args) != 2 {
			return fmt.Errorf("usage: lolad connect accept < invite-file (code must never be passed as an argument)")
		}
		// Read only from stdin: avoids shell history and process-list exposure.
		data, e := io.ReadAll(io.LimitReader(stdin, 65537))
		if e != nil {
			return fmt.Errorf("reading invite from stdin failed")
		}
		code := strings.TrimSpace(string(data))
		if len(data) > 65536 || code == "" {
			return fmt.Errorf("provide the invite code on stdin (at most 64 KiB)")
		}
		var response strings.Builder
		if e = request(ctx, base, "POST", "/api/connections", map[string]string{"code": code}, &response); e != nil {
			return fmt.Errorf("invite was not accepted; check connectivity and use an unexpired invite")
		}
		_, e = io.WriteString(out, response.String())
		return e
	case "pause", "resume", "rm":
		if len(args) != 3 {
			return fmt.Errorf("usage: lolad connect %s <provider>", args[1])
		}
		path := "/api/connections/" + url.PathEscape(args[2])
		if args[1] == "rm" {
			return request(ctx, base, "DELETE", path, nil, out)
		}
		return request(ctx, base, "PATCH", path, map[string]bool{"paused": args[1] == "pause"}, out)
	case "share", "unshare":
		if len(args) < 4 {
			return fmt.Errorf("usage: lolad connect %s <provider> <agent> [--with <address>]...", args[1])
		}
		remote := []string{}
		for i := 4; i < len(args); i++ {
			if args[1] != "share" || args[i] != "--with" || i+1 >= len(args) {
				return fmt.Errorf("expected --with <address>")
			}
			i++
			remote = append(remote, args[i])
		}
		var b strings.Builder
		if e := request(ctx, base, "GET", "/api/connections", nil, &b); e != nil {
			return e
		}
		var peers []core.Connection
		if e := json.Unmarshal([]byte(b.String()), &peers); e != nil {
			return e
		}
		for _, peer := range peers {
			if peer.Provider == args[2] {
				shares := []core.Share{}
				for _, s := range peer.Shares {
					if s.Local != args[3] {
						shares = append(shares, s)
					}
				}
				if args[1] == "share" {
					shares = append(shares, core.Share{Local: args[3], Remote: remote})
				}
				return request(ctx, base, "PATCH", "/api/connections/"+url.PathEscape(peer.Provider), map[string]any{"shares": shares}, out)
			}
		}
		return fmt.Errorf("connection not found")
	}
	return fmt.Errorf("unknown connect command")
}
