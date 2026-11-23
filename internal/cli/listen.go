package cli

import (
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/CamiloCaceres/lolad/internal/registry"
)

// fallbackPorts are tried, in order, when the default dashboard port is held
// by another program (docs/desktop.md §6.1).
func fallbackPorts() []int {
	ports := make([]int, 0, 99)
	for p := 4101; p <= 4199; p++ {
		ports = append(ports, p)
	}
	return ports
}

// listenLocal opens the dashboard listener on addr. When the port is taken:
//   - by another lolad: the error is returned, as before (one daemon per port);
//   - by another program, and config.yaml does not set listen: itself: a free
//     loopback port from fallback is used and saved as listen: in cfgPath, so
//     the app, the CLI and the agents all find it;
//   - by another program, and listen: is set by the user: a plain error naming
//     the port, since moving a port the user chose would surprise them.
func listenLocal(cfgPath, addr string, log io.Writer, fallback []int, isLolad func(string) bool) (net.Listener, error) {
	l, err := net.Listen("tcp", addr)
	if err == nil || !addrInUse(err) {
		return l, err
	}
	host, port, _ := net.SplitHostPort(addr)
	if isLolad(addr) {
		return nil, fmt.Errorf("another lolad is already running on port %s (%s): %w", port, addr, err)
	}
	explicit, cerr := registry.ListenSet(cfgPath)
	if cerr != nil {
		return nil, cerr
	}
	if explicit {
		return nil, fmt.Errorf("port %s is being used by another program, so lolad cannot start. "+
			"Quit that program, or change \"listen:\" in %s to a free port (for example 127.0.0.1:%d)", port, cfgPath, suggest(host, fallback))
	}
	if host == "" || host == "0.0.0.0" || host == "::" || host == "localhost" {
		host = "127.0.0.1"
	}
	for _, p := range fallback {
		next := net.JoinHostPort(host, strconv.Itoa(p))
		l, lerr := net.Listen("tcp", next)
		if lerr != nil {
			continue
		}
		note := fmt.Sprintf("Chosen by lolad on %s: port %s was being used by another program.", time.Now().Format("2006-01-02"), port)
		if werr := registry.SetListen(cfgPath, next, note); werr != nil {
			l.Close()
			return nil, fmt.Errorf("port %s is being used by another program and lolad could not save the free port %d in %s: %w", port, p, cfgPath, werr)
		}
		fmt.Fprintf(log, "lolad: port %s is being used by another program; using %s instead (saved as listen: in %s)\n", port, next, cfgPath)
		return l, nil
	}
	return nil, fmt.Errorf("port %s is being used by another program and no free port was found between %d and %d. "+
		"Quit that program, or set \"listen:\" in %s to a free port", port, first(fallback), last(fallback), cfgPath)
}

// suggest returns the first fallback port that is free right now, for the
// error message.
func suggest(host string, fallback []int) int {
	if host == "" || host == "0.0.0.0" || host == "::" || host == "localhost" {
		host = "127.0.0.1"
	}
	for _, p := range fallback {
		if l, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(p))); err == nil {
			l.Close()
			return p
		}
	}
	return 4101
}

func first(p []int) int {
	if len(p) == 0 {
		return 0
	}
	return p[0]
}

func last(p []int) int {
	if len(p) == 0 {
		return 0
	}
	return p[len(p)-1]
}

// addrInUse recognizes "address already in use" on every platform (Windows
// reports WSAEADDRINUSE, which is not syscall.EADDRINUSE there).
func addrInUse(err error) bool {
	if errors.Is(err, syscall.EADDRINUSE) {
		return true
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "address already in use") || strings.Contains(s, "only one usage of each socket address")
}

// loladOnPort reports whether the program answering on addr is a lolad. A slow
// answer is asked twice before it counts as "not lolad".
func loladOnPort(addr string) bool {
	for i := 0; i < 2; i++ {
		if _, ok := probeHealth(addr); ok {
			return true
		}
	}
	return false
}
