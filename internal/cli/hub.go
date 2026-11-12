package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
)

// hubCommand: lolad hub join <url> --handle <h> | status | leave.
// The sign-up code is read from stdin (one line), never from argv.
func hubCommand(ctx context.Context, base string, args []string, stdin io.Reader, out io.Writer) error {
	const usage = "usage: lolad hub join <hub-url> --handle <name> < code | lolad hub status | lolad hub leave"
	if len(args) < 2 {
		return fmt.Errorf(usage)
	}
	switch args[1] {
	case "status":
		if len(args) != 2 {
			return fmt.Errorf(usage)
		}
		return request(ctx, base, "GET", "/api/hub", nil, out)
	case "leave":
		if len(args) != 2 {
			return fmt.Errorf(usage)
		}
		return request(ctx, base, "DELETE", "/api/hub", nil, out)
	case "join":
		var url, handle string
		for i := 2; i < len(args); i++ {
			switch {
			case args[i] == "--handle" && i+1 < len(args):
				i++
				handle = args[i]
			case strings.HasPrefix(args[i], "--handle="):
				handle = strings.TrimPrefix(args[i], "--handle=")
			case url == "" && !strings.HasPrefix(args[i], "-"):
				url = args[i]
			default:
				return fmt.Errorf(usage)
			}
		}
		if url == "" || handle == "" {
			return fmt.Errorf(usage)
		}
		sc := bufio.NewScanner(io.LimitReader(stdin, 4096))
		code := ""
		for sc.Scan() {
			if code = strings.TrimSpace(sc.Text()); code != "" {
				break
			}
		}
		if code == "" {
			return fmt.Errorf("paste the hub sign-up code on stdin (it must never be passed as an argument)")
		}
		return request(ctx, base, "POST", "/api/hub/join", map[string]string{"url": url, "handle": handle, "code": code}, out)
	}
	return fmt.Errorf(usage)
}
