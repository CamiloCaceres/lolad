// Package web is lolad's dashboard: server-rendered html/template pages,
// enhanced with htmx, kept live over SSE, with an xterm.js terminal over a
// websocket. It talks to core.Service only. Every asset is embedded.
//
// It claims "/" and everything under it except /api, /v1 and /hook, which the
// integrator mounts beside it.
package web

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path"
	"slices"
	"strings"
	"sync"

	"github.com/CamiloCaceres/lolad/internal/auth"
	"github.com/CamiloCaceres/lolad/internal/core"
)

//go:embed templates static
var embedded embed.FS

// Options configure the dashboard.
type Options struct {
	Auth          *auth.Store
	PublicURL     string
	TrustedOrigin string
	Version       string
	Logger        *slog.Logger // default slog.Default()
	// Dir, when set, is this package's source directory: templates and static
	// files are read from it and templates re-parsed on every request, so the
	// UI can be edited without restarting. Leave empty in production.
	Dir string
	// MaxUpload is the per-file cap for files sent from the inbox; default
	// DefaultMaxUpload (the provider's local limit).
	MaxUpload int64
}

type server struct {
	auth          *auth.Store
	publicURL     string
	trustedOrigin string
	version       string
	svc           core.Service
	log           *slog.Logger
	files         fs.FS // holds templates/ and static/
	reload        bool
	maxUpload     int64
	uploads       pending // files uploaded in a conversation, not sent yet

	mu     sync.Mutex
	base   *template.Template
	pages  map[string]*template.Template
	hashes map[string]string // static path -> content hash, for cache busting
	build  string            // hash of every static file: which assets a page loaded
}

// New returns the dashboard handler, to be mounted at "/".
func New(svc core.Service, opts Options) http.Handler {
	s := &server{svc: svc, log: opts.Logger, files: embedded, auth: opts.Auth, publicURL: opts.PublicURL, trustedOrigin: opts.TrustedOrigin, version: opts.Version, maxUpload: opts.MaxUpload}
	if s.log == nil {
		s.log = slog.Default()
	}
	if opts.Dir != "" {
		s.files, s.reload = os.DirFS(opts.Dir), true
	}
	if err := s.load(); err != nil {
		panic(err) // embedded templates are part of the build; a parse error is a bug
	}

	mux := http.NewServeMux()
	s.remoteRoutes(mux)
	mux.HandleFunc("GET /connections", s.connectionsPage)
	mux.HandleFunc("POST /connections/invite", s.connectionInvite)
	mux.HandleFunc("POST /connections/accept", s.connectionAccept)
	mux.HandleFunc("POST /connections/{provider}", s.connectionUpdate)
	mux.HandleFunc("POST /connections/{provider}/remove", s.connectionRemove)
	mux.HandleFunc("POST /grants/decide", s.grantDecide)
	mux.HandleFunc("GET /{$}", s.inbox)
	mux.HandleFunc("GET /inbox/{peer}", s.thread)
	mux.HandleFunc("POST /inbox/{peer}", s.send)
	mux.HandleFunc("POST /inbox/{peer}/decide", s.decide)
	mux.HandleFunc("POST /inbox/{peer}/attachments", s.upload)
	mux.HandleFunc("GET /inbox/{peer}/attachments/{id}", s.download)
	mux.HandleFunc("GET /inbox/{peer}/messages", s.messages)
	mux.HandleFunc("GET /inbox/{peer}/head", s.threadHead)
	mux.HandleFunc("GET /fragments/conversations", s.conversationsFragment)
	mux.HandleFunc("GET /fragments/nav", s.navFragment)

	mux.HandleFunc("GET /agents", s.fleet)
	mux.HandleFunc("GET /fragments/fleet", s.fleetFragment)
	mux.HandleFunc("GET /agents/{name}", s.agent)
	mux.HandleFunc("GET /agents/{name}/part/{part}", s.agentPart)
	mux.HandleFunc("POST /agents/{name}/{action}", s.control)
	mux.HandleFunc("GET /agents/{name}/terminal", s.terminal)
	mux.HandleFunc("GET /agents/{name}/settings", s.settings)
	mux.HandleFunc("GET /agents/{name}/settings/{tab}", s.settings)
	mux.HandleFunc("POST /agents/{name}/settings/runtime", s.saveRuntime)
	mux.HandleFunc("POST /agents/{name}/settings/skills/mode", s.saveSkillsMode)
	mux.HandleFunc("POST /agents/{name}/settings/skills/add", s.addSkill)
	mux.HandleFunc("POST /agents/{name}/settings/skills/remove", s.removeSkill)
	mux.HandleFunc("POST /agents/{name}/settings/secrets", s.setSecret)
	mux.HandleFunc("POST /agents/{name}/settings/secrets/delete", s.deleteSecret)
	mux.HandleFunc("POST /agents/{name}/settings/files", s.saveFile)
	mux.HandleFunc("POST /agents/{name}/settings/files/preview", s.previewFile)
	mux.HandleFunc("POST /agents/{name}/settings/email/account", s.saveAccount)
	mux.HandleFunc("POST /agents/{name}/settings/email/account/delete", s.deleteAccount)
	mux.HandleFunc("POST /agents/{name}/settings/email", s.saveEmailGeneral)
	mux.HandleFunc("POST /agents/{name}/settings/email/test", s.testEmail)

	mux.HandleFunc("GET /org", s.orgPage)
	mux.HandleFunc("POST /org/teams", s.orgTeamAdd)
	mux.HandleFunc("POST /org/teams/{id}", s.orgTeamSave)
	mux.HandleFunc("POST /org/teams/{id}/delete", s.orgTeamDelete)
	mux.HandleFunc("POST /org/rules", s.orgRuleAdd)
	mux.HandleFunc("POST /org/rules/{i}/{op}", s.orgRuleOp)
	mux.HandleFunc("POST /org/defaults", s.orgDefaults)
	mux.HandleFunc("POST /org/check", s.orgCheck)

	mux.HandleFunc("GET /new", s.newPage)
	mux.HandleFunc("POST /new", s.create)
	mux.HandleFunc("POST /adopt", s.adopt)

	// Email provisioning routes (optional mailprov.Service).
	mux.HandleFunc("GET /email", s.emailPage)
	mux.HandleFunc("POST /email/provider", s.emailProviderSave)
	mux.HandleFunc("POST /email/{name}/{op}", s.emailAction)
	mux.HandleFunc("GET /traffic", s.traffic)
	mux.HandleFunc("GET /stream", s.stream)
	// Remote clients using the historical browser event path get the same stream.
	mux.HandleFunc("GET /events", s.stream)
	mux.HandleFunc("GET /static/", s.static)
	mux.HandleFunc("/", s.notFound)

	// Loopback-only, but a page on any other origin could still POST here
	// from the user's browser; refuse cross-origin writes.
	protection := http.NewCrossOriginProtection()
	if opts.TrustedOrigin != "" {
		_ = protection.AddTrustedOrigin(opts.TrustedOrigin)
	}
	return headers(protection.Handler(s.fresh(mux)))
}

func headers(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hd := w.Header()
		hd.Set("X-Content-Type-Options", "nosniff")
		hd.Set("Referrer-Policy", "same-origin")
		hd.Set("X-Frame-Options", "DENY")
		// xterm.js injects <style> elements, hence 'unsafe-inline' for styles only.
		// blob: images are the composer's previews of files being attached.
		hd.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		h.ServeHTTP(w, r)
	})
}

// fresh makes a page that predates the running build reload itself. Without
// it, htmx swaps new markup into a page still running the old scripts and
// styles (after a daemon upgrade, say), leaving controls unstyled or inert.
// Pages send their build with every htmx request (hx-headers on <body>); one
// that sends none or another gets HX-Refresh, and htmx reloads it.
func (s *server) fresh(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("HX-Request") == "true" && r.Header.Get("X-Lolad-Build") != s.buildID() {
			w.Header().Set("HX-Refresh", "true")
		}
		h.ServeHTTP(w, r)
	})
}
func (s *server) buildID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.build
}

// ---------------------------------------------------------------- templates

func (s *server) load() error {
	base := template.New("").Funcs(funcs(s))
	var err error
	if base, err = base.ParseFS(s.files, "templates/layout.html", "templates/partials/*.html"); err != nil {
		return err
	}
	names, err := fs.Glob(s.files, "templates/pages/*.html")
	if err != nil {
		return err
	}
	pages := map[string]*template.Template{}
	for _, n := range names {
		t, err := template.Must(base.Clone()).ParseFS(s.files, n)
		if err != nil {
			return err
		}
		pages[strings.TrimSuffix(path.Base(n), ".html")] = t
	}
	hashes := map[string]string{}
	err = fs.WalkDir(s.files, "static", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(s.files, p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		hashes[strings.TrimPrefix(p, "static/")] = hex.EncodeToString(sum[:5])
		return nil
	})
	if err != nil {
		return err
	}
	keys := make([]string, 0, len(hashes))
	for p, h := range hashes {
		keys = append(keys, p+":"+h)
	}
	slices.Sort(keys)
	sum := sha256.Sum256([]byte(strings.Join(keys, "\n")))
	s.mu.Lock()
	s.base, s.pages, s.hashes, s.build = base, pages, hashes, hex.EncodeToString(sum[:6])
	s.mu.Unlock()
	return nil
}

func (s *server) templates() (*template.Template, map[string]*template.Template) {
	if s.reload {
		if err := s.load(); err != nil {
			s.log.Error("reload templates", "err", err)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.base, s.pages
}

// layout is what every full page gets.
type layout struct {
	Version string
	Title   string
	Nav     string // inbox | agents | org | traffic | new
	Class   string // extra body class
	Me      core.User
	Badges  badges
	Data    any
	Request *http.Request
}

type badges struct {
	OOB     bool
	Unread  int // mail to the user, unread
	Pending int // questions from agents the user hasn't answered
	Alert   int // agents crashed, failed or waiting
}

func (s *server) badges(r *http.Request) badges {
	var b badges
	if convs, err := s.svc.Conversations(r.Context()); err == nil {
		for _, c := range convs {
			b.Unread += c.Unread
		}
		for _, asks := range s.openAsks(r.Context(), convs) {
			b.Pending += len(asks)
		}
	}
	if agents, err := s.svc.List(r.Context()); err == nil {
		for _, a := range agents {
			if attention(a.State) {
				b.Alert++
			}
		}
	}
	return b
}

func (s *server) page(w http.ResponseWriter, r *http.Request, status int, name string, l layout) {
	_, pages := s.templates()
	t, ok := pages[name]
	if !ok {
		http.Error(w, "no page "+name, http.StatusInternalServerError)
		return
	}
	l.Version = s.version
	l.Me, l.Request = s.svc.Me(), r
	l.Badges = s.badges(r)
	if l.Nav == "" {
		l.Nav = name
	}
	s.write(w, status, t, "layout", l)
}

// fragment renders one named template from the shared partials.
func (s *server) fragment(w http.ResponseWriter, status int, name string, data any) {
	base, _ := s.templates()
	s.write(w, status, base, name, data)
}

func (s *server) write(w http.ResponseWriter, status int, t *template.Template, name string, data any) {
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, name, data); err != nil {
		s.log.Error("render", "template", name, "err", err)
		http.Error(w, "render error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}

func (s *server) static(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.URL.Path, "/static/")
	if r.URL.Query().Get("v") != "" && !s.reload {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	sub, _ := fs.Sub(s.files, "static")
	http.ServeFileFS(w, r, sub, p)
}

func (s *server) asset(p string) string {
	s.mu.Lock()
	h := s.hashes[p]
	s.mu.Unlock()
	return "/static/" + p + "?v=" + h
}

// pageStyles lists static/pages/*.css. Each screen keeps its own styles there
// so app.css holds only the shared tokens, shell and components.
func (s *server) pageStyles() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for p := range s.hashes {
		if strings.HasPrefix(p, "pages/") && strings.HasSuffix(p, ".css") {
			out = append(out, p)
		}
	}
	slices.Sort(out)
	return out
}

func (s *server) notFound(w http.ResponseWriter, r *http.Request) {
	s.page(w, r, http.StatusNotFound, "notfound", layout{Title: "Not found", Nav: "-", Data: r.URL.Path})
}

// ---------------------------------------------------------------- helpers

// isHX is true for htmx requests that want a fragment back. Boosted links and
// forms navigate like plain ones, so they get full pages and redirects.
func isHX(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true" && r.Header.Get("HX-Boosted") != "true"
}

// statusFor maps a service error to an HTTP status.
func statusFor(err error) int {
	switch {
	case errors.Is(err, core.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, core.ErrDenied):
		return http.StatusForbidden
	case errors.Is(err, core.ErrExists), errors.Is(err, core.ErrConflict):
		return http.StatusConflict
	case errors.Is(err, core.ErrInvalid), errors.Is(err, core.ErrNotRunning):
		return http.StatusUnprocessableEntity
	}
	return http.StatusInternalServerError
}

// fail reports an action error: a toast for htmx requests (no swap), plain
// text otherwise.
func (s *server) fail(w http.ResponseWriter, r *http.Request, err error) {
	code := statusFor(err)
	if code == http.StatusInternalServerError {
		s.log.Error("request", "path", r.URL.Path, "err", err)
	}
	if isHX(r) {
		toast(w, "error", err.Error())
		w.Header().Set("HX-Reswap", "none")
		w.WriteHeader(code)
		return
	}
	http.Error(w, err.Error(), code)
}

// toast asks the page to show a short message (see app.js).
func toast(w http.ResponseWriter, level, msg string) {
	w.Header().Set("HX-Trigger", fmt.Sprintf(`{"lolad:toast":{"level":%q,"message":%q}}`, level, msg))
}

// redirect finishes a successful form post that leads to another page.
func (s *server) redirect(w http.ResponseWriter, r *http.Request, to string) {
	if isHX(r) {
		w.Header().Set("HX-Redirect", to)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}
