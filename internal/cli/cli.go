// Package cli implements the HTTP client and daemon entry points.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/CamiloCaceres/lolad/internal/api"
	"github.com/CamiloCaceres/lolad/internal/app"
	"github.com/CamiloCaceres/lolad/internal/core"
	"github.com/CamiloCaceres/lolad/internal/hooks"
	"github.com/CamiloCaceres/lolad/internal/msgcli"
	"github.com/CamiloCaceres/lolad/internal/registry"
	"github.com/CamiloCaceres/lolad/internal/web"
)

const Version = "0.1.0"
const help = `lolad — agent daemon and mail
  serve                         run the loopback daemon
  ls | status <agent>           inspect the fleet
  start|stop|interrupt <agent>  control an agent
  restart [--fresh] <agent>     restart/resume or start fresh
  attach [--read-only] <agent>  attach to the private tmux session
  capture <agent> [--lines N]   inspect terminal output
  logs [agent] [--limit N]      lifecycle events
  send <agent> <text>           send mail as the user
  new <name> --harness claude|codewhale [--model M] [--dir D]
      [--slug S] [--role R] [--on-wake TEXT] [--mission TEXT]
      [--team T] [--head] [--start] [--permissions bypass|default]
  adopt <dir> --harness claude|codewhale [--slug S] [--model M]
  org show | org check <from> <to> | org put (JSON on stdin)
  reload | version
  msg ...                      agent-side signed AMP client
  hook <event>                 harness hook (JSON on stdin)
Environment: LOLAD_URL, LOLAD_CONFIG, LOLAD_STATE
`

// options accepts flags before or after positional arguments.
func options(args []string) ([]string, map[string]string, error) {
	p := []string{}
	o := map[string]string{}
	bools := map[string]bool{"fresh": true, "start": true, "head": true, "read-only": true}
	allowed := map[string]bool{}
	for _, k := range strings.Fields("fresh start head read-only harness model dir slug role user on-wake mission team permissions lines limit subject priority type in-reply-to decision") {
		allowed[k] = true
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "--") {
			p = append(p, a)
			continue
		}
		k, v, has := strings.Cut(strings.TrimPrefix(a, "--"), "=")
		if !allowed[k] {
			return nil, nil, fmt.Errorf("unknown option --%s", k)
		}
		if bools[k] && !has {
			v = "true"
		} else if !has {
			if i+1 >= len(args) {
				return nil, nil, fmt.Errorf("--%s needs a value", k)
			}
			i++
			v = args[i]
		}
		o[k] = v
	}
	return p, o, nil
}
func request(ctx context.Context, base, method, path string, body any, out io.Writer) error {
	var b io.Reader
	if body != nil {
		data, e := json.Marshal(body)
		if e != nil {
			return e
		}
		b = bytes.NewReader(data)
	}
	r, e := http.NewRequestWithContext(ctx, method, base+path, b)
	if e != nil {
		return e
	}
	r.Header.Set("Content-Type", "application/json")
	resp, e := (&http.Client{Timeout: 5 * time.Minute}).Do(r)
	if e != nil {
		return fmt.Errorf("cannot reach lolad at %s: %w; start it with 'lolad serve'", base, e)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	_, e = io.Copy(out, resp.Body)
	return e
}
func Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		fmt.Fprint(stdout, help)
		return 0
	}
	if args[0] == "msg" {
		return msgcli.Run(ctx, args[1:], stdout, stderr)
	}
	if args[0] == "hook" {
		if len(args) != 2 {
			fmt.Fprintln(stderr, "usage: lolad hook <event>")
			return 1
		}
		return hooks.Run(ctx, args[1], stdin, stdout, os.Getenv)
	}
	if args[0] == "version" {
		fmt.Fprintln(stdout, Version)
		return 0
	}
	var e error
	if args[0] == "serve" {
		e = serve(ctx, stderr)
	} else {
		e = command(ctx, args, stdin, stdout)
	}
	if e != nil {
		fmt.Fprintln(stderr, "lolad:", e)
		return 1
	}
	return 0
}
func command(ctx context.Context, args []string, stdin io.Reader, out io.Writer) error {
	c, e := registry.LoadConfig("")
	if e != nil {
		return e
	}
	base := strings.TrimRight(os.Getenv("LOLAD_URL"), "/")
	if base == "" {
		base = app.URL(c.Listen)
	}
	p, o, e := options(args[1:])
	if e != nil {
		return e
	}
	need := func(n int) error {
		if len(p) != n {
			return fmt.Errorf("%s expects %d argument(s); run lolad help", args[0], n)
		}
		return nil
	}
	agent := func() string { return url.PathEscape(p[0]) }
	method, path := "GET", ""
	var body any
	switch args[0] {
	case "ls":
		if e = need(0); e != nil {
			return e
		}
		path = "/api/agents"
	case "status":
		if e = need(1); e != nil {
			return e
		}
		path = "/api/agents/" + agent()
	case "start", "stop", "restart", "interrupt":
		if e = need(1); e != nil {
			return e
		}
		method = "POST"
		path = "/api/agents/" + agent() + "/" + args[0]
		if o["fresh"] == "true" {
			path += "?fresh=true"
		}
	case "logs":
		if len(p) > 1 {
			return fmt.Errorf("logs expects at most one agent")
		}
		path = "/api/events?limit=" + url.QueryEscape(o["limit"])
		if len(p) == 1 {
			path += "&agent=" + url.QueryEscape(p[0])
		}
	case "capture":
		if e = need(1); e != nil {
			return e
		}
		path = "/api/agents/" + agent() + "/capture?lines=" + url.QueryEscape(o["lines"])
	case "send":
		if e = need(2); e != nil {
			return e
		}
		method = "POST"
		path = "/api/mail/send"
		context := map[string]any{"userchat": core.UserChat{Kind: "say"}}
		if o["decision"] != "" {
			context["userchat"] = core.UserChat{Kind: "decision", Decision: o["decision"]}
		}
		subject := o["subject"]
		if subject == "" {
			subject = subjectFromText(p[1])
		}
		body = core.Outgoing{To: p[0], Body: p[1], Subject: subject, Priority: o["priority"], Type: o["type"], InReplyTo: o["in-reply-to"], Context: context}
	case "new":
		if e = need(1); e != nil {
			return e
		}
		method = "POST"
		path = "/api/create"
		body = core.CreateRequest{Name: p[0], Slug: o["slug"], Dir: o["dir"], Role: o["role"], User: o["user"], Harness: core.Harness(o["harness"]), Model: o["model"], Permissions: core.Permissions(o["permissions"]), OnWake: o["on-wake"], Mission: o["mission"], Team: o["team"], Head: o["head"] == "true", Start: o["start"] == "true"}
	case "adopt":
		if e = need(1); e != nil {
			return e
		}
		method = "POST"
		path = "/api/adopt"
		autostart := o["start"] == "true"
		body = core.AdoptRequest{Dir: p[0], Slug: o["slug"], Runtime: core.Runtime{Autostart: &autostart, Harness: core.Harness(o["harness"]), Model: o["model"], Permissions: core.Permissions(o["permissions"]), OnWake: o["on-wake"]}}
	case "reload":
		if e = need(0); e != nil {
			return e
		}
		method = "POST"
		path = "/api/reload"
	case "org":
		if len(p) == 1 && p[0] == "show" {
			path = "/api/org"
		} else if len(p) == 3 && p[0] == "check" {
			path = "/api/org/check?from=" + url.QueryEscape(p[1]) + "&to=" + url.QueryEscape(p[2])
		} else if len(p) == 1 && p[0] == "put" {
			method = "PUT"
			path = "/api/org"
			var org core.Org
			if e = json.NewDecoder(stdin).Decode(&org); e != nil {
				return e
			}
			body = org
		} else {
			return fmt.Errorf("usage: lolad org show|check <from> <to>|put")
		}
	case "attach":
		if e = need(1); e != nil {
			return e
		}
		if e = request(ctx, base, "GET", "/api/agents/"+agent(), nil, io.Discard); e != nil {
			return e
		}
		bin, e := exec.LookPath("tmux")
		if e != nil {
			return e
		}
		socket := c.TmuxSocket
		if socket == "" {
			socket = "lolad"
		}
		argv := []string{"tmux", "-L", socket, "attach", "-t", "=" + p[0]}
		if o["read-only"] == "true" {
			argv = append(argv, "-r")
		}
		return syscall.Exec(bin, argv, os.Environ())
	default:
		return fmt.Errorf("unknown command %q; run lolad help", args[0])
	}
	return request(ctx, base, method, path, body, out)
}
func serve(parent context.Context, log io.Writer) error {
	ctx, cancel := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer cancel()
	a, e := app.New(ctx, "")
	if e != nil {
		return e
	}
	defer a.Close()
	host, port, e := net.SplitHostPort(a.Config.Listen)
	if e != nil {
		return e
	}
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return fmt.Errorf("listen must be a loopback address")
	}
	if _, e = strconv.Atoi(port); e != nil {
		return e
	}
	listener, e := net.Listen("tcp", a.Config.Listen)
	if e != nil {
		return e
	}
	defer listener.Close()
	mux := http.NewServeMux()
	rest := api.New(a, api.Options{HookSink: a.Tracker, StateDir: a.Config.StateDir, Trigger: func(r *http.Request, ag, n, b string) error { return a.Trigger(r.Context(), ag, n, b) }})
	mux.Handle("/api/", rest)
	mux.Handle("/hook/", rest)
	mux.Handle("/v1/", a.Provider)
	mux.Handle("/", web.New(a, web.Options{}))
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 90 * time.Second}
	runDone := make(chan error, 1)
	go func() { runDone <- a.Run(ctx) }()
	serveDone := make(chan error, 1)
	go func() { serveDone <- srv.Serve(listener) }()
	fmt.Fprintln(log, "lolad listening on", a.Config.Listen)
	select {
	case <-ctx.Done():
	case e = <-serveDone:
		cancel()
	}
	shutdown, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	se := srv.Shutdown(shutdown)
	if se != nil {
		_ = srv.Close()
	}
	<-runDone
	if e != nil && !errors.Is(e, http.ErrServerClosed) {
		return e
	}
	return se
}

// subjectFromText uses a readable first line without splitting Unicode characters.
func subjectFromText(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	runes := []rune(strings.TrimSpace(line))
	if len(runes) > 80 {
		runes = runes[:80]
	}
	return string(runes)
}
