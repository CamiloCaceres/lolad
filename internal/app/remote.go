package app

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/CamiloCaceres/lolad/internal/api"
	"github.com/CamiloCaceres/lolad/internal/auth"
	"github.com/CamiloCaceres/lolad/internal/web"
)

// ValidateListeners enforces that neither trusted HTTP nor the Serve backend is
// directly exposed on the LAN/tailnet. The only trusted header issuer is Serve.
func ValidateListeners(local, remote, public string) error {
	for index, addr := range []string{local, remote} {
		if addr == "" {
			continue
		}
		h, _, e := net.SplitHostPort(addr)
		if e != nil {
			return e
		}
		ip := net.ParseIP(h)
		if index == 0 && h == "localhost" {
			continue
		} // preserve trusted listener compatibility
		if ip == nil || !ip.IsLoopback() {
			return fmt.Errorf("listener %q must use a numeric loopback address", addr)
		}
	}
	if remote != "" && remote == local {
		return fmt.Errorf("local and remote listeners must differ")
	}
	if public != "" {
		u, e := url.Parse(public)
		if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" {
			return fmt.Errorf("remote.public_url must be an HTTPS origin without path, credentials or query")
		}
	}
	return nil
}

// Handler constructs independent routing trees; remote mounts only signed federation from AMP, never local AMP or hooks
// from harnesses. Webhook triggers keep their own per-agent authentication.
func (a *App) Handler(remote bool, version string, logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	public := ""
	if remote {
		public = strings.TrimRight(a.Config.Remote.PublicURL, "/")
	}
	rest := api.New(a, api.Options{Auth: a.Auth, Remote: remote, Version: version, PublicURL: public, HookSink: a.Tracker, StateDir: a.Config.StateDir, Trigger: func(r *http.Request, ag, n, b string) error { return a.Trigger(r.Context(), ag, n, b) }})
	mux.Handle("/api/", rest)
	mux.Handle("/hook/", rest)
	if !remote {
		mux.Handle("/v1/", a.Provider)
	}
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "version": version})
	})
	mux.HandleFunc("GET /download/{file}", a.download)
	mux.Handle("/", web.New(a, web.Options{Auth: a.Auth, PublicURL: a.Config.Remote.PublicURL, TrustedOrigin: public, Version: version}))
	protection := http.NewCrossOriginProtection()
	if public != "" {
		_ = protection.AddTrustedOrigin(public)
	}
	var h http.Handler = protection.Handler(mux)
	if remote {
		h = a.Auth.Remote(h, a.Config.Remote.TailscaleLogins)
	}
	// Signed server-to-server traffic has its own authentication and must not
	// require a browser device cookie or a same-origin browser request.
	browser := h
	h = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/federation/") {
			a.Engine.Handler().ServeHTTP(w, r)
			return
		}
		browser.ServeHTTP(w, r)
	})
	return harden(h, remote, logger)
}
func (a *App) download(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("file")
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00\r\n") {
		http.NotFound(w, r)
		return
	}
	// os.Root prevents symlinks from escaping the downloads directory too.
	root, e := os.OpenRoot(filepath.Join(a.Config.StateDir, "downloads"))
	if e != nil {
		http.NotFound(w, r)
		return
	}
	defer root.Close()
	f, e := root.Open(name)
	if e != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, name, info.ModTime(), f)
}

type responseLog struct {
	http.ResponseWriter
	status int
}

func (w *responseLog) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *responseLog) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
		w.ResponseWriter.WriteHeader(code)
	}
}
func (w *responseLog) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	return w.ResponseWriter.Write(b)
}
func (w *responseLog) Flush() {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}
func (w *responseLog) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	c, b, e := http.NewResponseController(w.ResponseWriter).Hijack()
	if e == nil {
		w.status = 101
	}
	return c, b, e
}
func harden(next http.Handler, remote bool, logger *slog.Logger) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseLog{ResponseWriter: w}
		r = auth.WithRequestIdentity(r)
		defer func() {
			if recover() != nil {
				logger.Error("request panic", "method", r.Method, "path", r.URL.Path)
				if rw.status == 0 {
					http.Error(rw, "internal server error", 500)
				}
			}
			status := rw.status
			if status == 0 {
				status = 200
			}
			id := auth.DeviceID(r.Context())
			if id == "" {
				if remote {
					id = "remote"
				} else {
					id = "local"
				}
			}
			logger.Info("request", "method", r.Method, "path", r.URL.Path, "status", status, "duration", time.Since(start), "device", id)
		}()
		rw.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(rw, r)
	})
}
