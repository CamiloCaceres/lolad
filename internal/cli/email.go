package cli

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"
)

// Secrets are accepted only on stdin. Arity is checked before reading it.
func emailCommand(ctx context.Context, base string, args []string, stdin io.Reader, out io.Writer) (bool, error) {
	if len(args) == 1 && args[0] == "ls" {
		return true, request(ctx, base, "GET", "/api/email", nil, out)
	}
	if len(args) > 0 && args[0] == "provider" {
		if len(args) == 1 {
			return true, request(ctx, base, "GET", "/api/email/provider", nil, out)
		}
		if len(args) != 2 || args[1] != "set-token" {
			return true, fmt.Errorf("usage: lolad email provider [set-token]; token must come from stdin, never arguments")
		}
		b, e := io.ReadAll(io.LimitReader(stdin, 65537))
		if e != nil {
			return true, fmt.Errorf("cannot read token from stdin")
		}
		token := strings.TrimSuffix(strings.TrimSuffix(string(b), "\n"), "\r")
		if len(token) == 0 || len(token) > 65536 || strings.ContainsAny(token, "\r\n") {
			return true, fmt.Errorf("provide one token on stdin (maximum 64 KiB)")
		}
		return true, request(ctx, base, "PUT", "/api/email/provider", map[string]string{"token": token}, out)
	}
	if len(args) >= 2 && (args[1] == "provision" || args[1] == "rotate" || args[1] == "detach") {
		if len(args) > 3 || (args[1] != "provision" && len(args) != 2) {
			return true, fmt.Errorf("usage: lolad email <agent> provision [address] | rotate | detach; secrets never belong in arguments")
		}
		address := ""
		if len(args) == 3 {
			address = args[2]
		}
		return true, request(ctx, base, "POST", "/api/agents/"+url.PathEscape(args[0])+"/email/"+args[1], map[string]string{"address": address}, out)
	}
	return false, nil
}
