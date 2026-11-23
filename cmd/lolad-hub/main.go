// Command lolad-hub is the lolad hub: a relay that gives lolads behind NAT a
// public federation URL. See SPEC.md "Hub (v5)" and deploy/hub/.
//
//	lolad-hub serve --public-url https://hub.example.com [--listen 127.0.0.1:8095] [--db PATH] [--trusted-proxies CIDR,...]
//	lolad-hub invite [--note TEXT] [--ttl 168h] [--db PATH]
//	lolad-hub accounts [--db PATH]
//	lolad-hub disable|enable|rm|block <handle> [--db PATH]
//	lolad-hub signup open|invite [--db PATH]
//
// The database defaults to $LOLAD_HUB_DB, else /var/lib/lolad-hub/hub.db.
// serve also reads $LOLAD_HUB_PUBLIC_URL, $LOLAD_HUB_LISTEN and
// $LOLAD_HUB_TRUSTED_PROXIES (see deploy/hub/Dockerfile), and
// $LOLAD_HUB_ADMIN_TOKEN: when set (24+ characters), the operator console is
// served at /console and the admin API at /v1/hub/admin/. It is read from the
// environment only, never a flag, so it stays out of process listings.
//
// Open sign-up (docs/friend-connect-design.md §1): $LOLAD_HUB_SIGNUP is
// "open" (default) or "invite"; `lolad-hub signup` or the console override it.
// Limits, 0 = off: $LOLAD_HUB_SIGNUPS_PER_IP (3/24 h), $LOLAD_HUB_SIGNUPS_PER_NET
// (10/24 h per /24 or /56), $LOLAD_HUB_SIGNUPS_HOURLY (60), $LOLAD_HUB_SIGNUPS_DAILY
// (300), $LOLAD_HUB_MAX_ACCOUNTS (2000), $LOLAD_HUB_REAP_UNUSED (72h) and
// $LOLAD_HUB_BYTES_PER_DAY (0).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/CamiloCaceres/lolad/internal/hub"
)

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "lolad-hub:", err)
		os.Exit(1)
	}
}

const usage = `usage:
  lolad-hub serve --public-url https://hub.example.com [--listen 127.0.0.1:8095] [--db PATH] [--trusted-proxies CIDR,...]
  lolad-hub invite [--note TEXT] [--ttl 168h] [--db PATH]
  lolad-hub accounts [--db PATH]
  lolad-hub disable|enable|rm|block <handle> [--db PATH]
  lolad-hub signup open|invite [--db PATH]
  lolad-hub version`

func defaultDB() string {
	if p := os.Getenv("LOLAD_HUB_DB"); p != "" {
		return p
	}
	return "/var/lib/lolad-hub/hub.db"
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func run(ctx context.Context, args []string, out, errOut io.Writer) error {
	if len(args) == 0 {
		return errors.New(usage)
	}
	cmd, rest := args[0], args[1:]
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(errOut)
	db := fs.String("db", defaultDB(), "database path")
	switch cmd {
	case "version":
		fmt.Fprintln(out, "lolad-hub protocol", hub.Version)
		return nil
	case "serve":
		listen := fs.String("listen", envOr("LOLAD_HUB_LISTEN", "127.0.0.1:8095"), "listen address (put a TLS proxy in front)")
		public := fs.String("public-url", os.Getenv("LOLAD_HUB_PUBLIC_URL"), "public base URL, e.g. https://hub.example.com")
		proxies := fs.String("trusted-proxies", os.Getenv("LOLAD_HUB_TRUSTED_PROXIES"), "CIDRs of reverse proxies whose X-Forwarded-For is trusted (loopback always is)")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		trusted, err := hub.ParsePrefixes(*proxies)
		if err != nil {
			return fmt.Errorf("--trusted-proxies: %w", err)
		}
		lim, err := limitsFromEnv()
		if err != nil {
			return err
		}
		token := os.Getenv("LOLAD_HUB_ADMIN_TOKEN")
		os.Unsetenv("LOLAD_HUB_ADMIN_TOKEN")
		return serve(ctx, *db, *listen, *public, trusted, token, os.Getenv("LOLAD_HUB_SIGNUP"), lim)
	case "invite":
		note := fs.String("note", "", "who it is for (shown in accounts)")
		ttl := fs.Duration("ttl", 7*24*time.Hour, "how long the code is valid")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		st, err := hub.OpenStore(*db)
		if err != nil {
			return err
		}
		defer st.Close()
		code, exp, err := st.CreateInvite(ctx, *note, *ttl)
		if err != nil {
			return err
		}
		fmt.Fprintln(out, code)
		fmt.Fprintf(errOut, "single use, valid until %s\n", exp.Local().Format(time.RFC1123))
		return nil
	case "accounts":
		if err := fs.Parse(rest); err != nil {
			return err
		}
		st, err := hub.OpenStore(*db)
		if err != nil {
			return err
		}
		defer st.Close()
		list, err := st.Accounts(ctx)
		if err != nil {
			return err
		}
		w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "HANDLE\tDISPLAY\tCREATED\tLAST SEEN\tSTATE")
		for _, a := range list {
			seen, state := "never", "active"
			if !a.LastSeen.IsZero() {
				seen = a.LastSeen.Local().Format("2006-01-02 15:04")
			}
			if a.Disabled {
				state = "disabled"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", a.Handle, a.Display, a.CreatedAt.Local().Format("2006-01-02"), seen, state)
		}
		return w.Flush()
	case "signup":
		if err := fs.Parse(rest); err != nil {
			return err
		}
		if fs.NArg() != 1 || fs.Arg(0) != hub.SignupOpen && fs.Arg(0) != hub.SignupInvite {
			return errors.New("usage: lolad-hub signup open|invite")
		}
		st, err := hub.OpenStore(*db)
		if err != nil {
			return err
		}
		defer st.Close()
		if err = st.SetSetting(ctx, "signup_mode", fs.Arg(0)); err == nil {
			fmt.Fprintf(errOut, "sign-up is now %s (a running hub applies it at once)\n", fs.Arg(0))
		}
		return err
	case "disable", "enable", "rm", "block":
		if err := fs.Parse(rest); err != nil {
			return err
		}
		// Flags may also follow the handle (disable bob --db PATH).
		if fs.NArg() > 1 {
			h := fs.Arg(0)
			if err := fs.Parse(fs.Args()[1:]); err != nil {
				return err
			}
			rest = append([]string{h}, fs.Args()...)
			_ = fs.Parse(rest)
		}
		if fs.NArg() != 1 {
			return fmt.Errorf("usage: lolad-hub %s <handle>", cmd)
		}
		st, err := hub.OpenStore(*db)
		if err != nil {
			return err
		}
		defer st.Close()
		h := fs.Arg(0)
		switch cmd {
		case "rm":
			err = st.Remove(ctx, h)
		case "block":
			err = st.Block(ctx, h)
		default:
			err = st.SetDisabled(ctx, h, cmd == "disable")
		}
		if errors.Is(err, hub.ErrNotFound) {
			return fmt.Errorf("no account %q", h)
		}
		if err == nil {
			fmt.Fprintf(errOut, "%s: done (a running hub applies it within 30 s)\n", h)
		}
		return err
	}
	return errors.New(usage)
}

// limitsFromEnv reads the sign-up and traffic limits; unset means default.
func limitsFromEnv() (hub.Limits, error) {
	l := hub.DefaultLimits()
	ints := map[string]*int{"LOLAD_HUB_SIGNUPS_PER_IP": &l.SignupsPerIP, "LOLAD_HUB_SIGNUPS_PER_NET": &l.SignupsPerNet,
		"LOLAD_HUB_SIGNUPS_HOURLY": &l.SignupsHourly, "LOLAD_HUB_SIGNUPS_DAILY": &l.SignupsDaily, "LOLAD_HUB_MAX_ACCOUNTS": &l.MaxAccounts}
	for k, p := range ints {
		if v := os.Getenv(k); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return l, fmt.Errorf("%s: want a whole number, 0 to turn it off", k)
			}
			*p = n
		}
	}
	if v := os.Getenv("LOLAD_HUB_REAP_UNUSED"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < 0 {
			return l, fmt.Errorf("LOLAD_HUB_REAP_UNUSED: want a duration like 72h, 0 to turn it off")
		}
		l.ReapUnused = d
	}
	if v := os.Getenv("LOLAD_HUB_BYTES_PER_DAY"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			return l, fmt.Errorf("LOLAD_HUB_BYTES_PER_DAY: want a whole number of bytes, 0 for no cap")
		}
		l.BytesPerDay = n
	}
	return l, nil
}

func serve(ctx context.Context, dbPath, listen, public string, trusted []netip.Prefix, adminToken, signup string, lim hub.Limits) error {
	if public == "" {
		return errors.New("--public-url is required")
	}
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	st, err := hub.OpenStore(dbPath)
	if err != nil {
		return err
	}
	defer st.Close()
	srv, err := hub.NewServer(hub.Options{Store: st, PublicURL: public, Logger: log, TrustedProxies: trusted, AdminToken: adminToken, Signup: signup, Limits: &lim})
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	// No WriteTimeout: tunnels are long-lived WebSockets.
	hs := &http.Server{Addr: listen, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute, MaxHeaderBytes: 64 << 10}
	done := make(chan struct{})
	go func() { defer close(done); srv.Run(ctx) }()
	errc := make(chan error, 1)
	go func() { errc <- hs.ListenAndServe() }()
	log.Info("lolad-hub listening", "addr", listen, "public_url", public, "db", dbPath, "trusted_proxies", len(trusted), "console", adminToken != "", "signup", srv.SignupMode(ctx))
	select {
	case err = <-errc:
		stop()
	case <-ctx.Done():
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = hs.Shutdown(sctx)
		cancel()
		err = nil
	}
	<-done
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	return err
}
