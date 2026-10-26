package web_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/CamiloCaceres/lolad/internal/core"
	"github.com/CamiloCaceres/lolad/internal/web"
	"github.com/CamiloCaceres/lolad/internal/web/fake"
)

// cfgSpy records the settings calls the dashboard makes.
type cfgSpy struct {
	core.Service
	mu       sync.Mutex
	runtimes []core.Runtime
	emails   []core.EmailSettings
	added    []string
	removed  []string
	env      map[string]string
}

func (s *cfgSpy) UpdateRuntime(ctx context.Context, name string, rt core.Runtime) (core.AgentStatus, error) {
	s.mu.Lock()
	s.runtimes = append(s.runtimes, rt)
	s.mu.Unlock()
	return s.Service.UpdateRuntime(ctx, name, rt)
}

func (s *cfgSpy) SaveEmail(ctx context.Context, name string, e core.EmailSettings) (core.EmailSettings, error) {
	s.mu.Lock()
	s.emails = append(s.emails, e)
	s.mu.Unlock()
	return s.Service.SaveEmail(ctx, name, e)
}

func (s *cfgSpy) AddSkill(ctx context.Context, name, from string) (core.Skill, error) {
	s.mu.Lock()
	s.added = append(s.added, from)
	s.mu.Unlock()
	return s.Service.AddSkill(ctx, name, from)
}

func (s *cfgSpy) RemoveSkill(ctx context.Context, name, id string) error {
	s.mu.Lock()
	s.removed = append(s.removed, id)
	s.mu.Unlock()
	return s.Service.RemoveSkill(ctx, name, id)
}

func (s *cfgSpy) SetEnv(ctx context.Context, name, key, value string) error {
	s.mu.Lock()
	s.env[key] = value
	s.mu.Unlock()
	return s.Service.SetEnv(ctx, name, key, value)
}

type cfgEnv struct {
	*env
	cfg *cfgSpy
}

func setupSettings(t *testing.T) *cfgEnv {
	t.Helper()
	f := fake.New(fake.Options{})
	t.Cleanup(f.Close)
	cs := &cfgSpy{Service: f, env: map[string]string{}}
	h := web.New(cs, web.Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &cfgEnv{env: &env{t: t, svc: &spy{Service: cs}, srv: srv}, cfg: cs}
}

func TestSettingsPages(t *testing.T) {
	e := setupSettings(t)
	for _, tc := range []struct{ path, want string }{
		{"/agents/ultron/settings", "Model &amp; runtime"},
		{"/agents/ultron/settings/runtime", `value="claude-opus-5-5" checked`},
		{"/agents/ultron/settings/skills", "Inherit my skills"},
		{"/agents/iris/settings/skills", "Not visible under Barebones"},
		{"/agents/ultron/settings/secrets", "GOOGLE_CALENDAR_TOKEN"},
		{"/agents/ultron/settings/secrets", "email: ultron@mail.example.com"},
		{"/agents/lola/settings/files", "Pick a file"},
		{"/agents/lola/settings/files?path=CLAUDE.md", "lolad section between its markers is regenerated"},
		{"/agents/lola/settings/files?path=nope.md", "isn&#39;t one of the files"},
		{"/agents/ultron/settings/email", "A mail mod watches this inbox"},
		{"/agents/iris/settings/email", "aim-secret"},
		{"/agents/patch/settings/email", "use email yet"},
		{"/agents/lola/settings", "Pending restart"},
		{"/agents/lola", "Restart to apply"},
		{"/agents/lola", `href="/agents/lola/settings"`},
		{"/inbox/lola", `href="/agents/lola/settings"`},
		{"/agents/lola/part/pending", `id="pending-settings"`},
	} {
		code, body := e.get(tc.path)
		if code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", tc.path, code)
			continue
		}
		if !strings.Contains(body, tc.want) {
			t.Errorf("GET %s: body lacks %q", tc.path, tc.want)
		}
	}
	for _, p := range []string{"/agents/nobody/settings", "/agents/lola/settings/nope"} {
		if code, _ := e.get(p); code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", p, code)
		}
	}
	if _, body := e.get("/agents/ultron"); strings.Contains(body, "Pending restart") {
		t.Error("ultron has nothing pending but shows the banner")
	}
}

func TestRuntimeSave(t *testing.T) {
	e := setupSettings(t)
	ctx := context.Background()
	before, _ := e.cfg.Get(ctx, "ultron")
	form := url.Values{
		"model": {"custom"}, "model_custom": {"claude-opus-5-5[1m]"},
		"permissions": {"default"}, "on_wake": {"Check the calendar.\r\nThen mail."},
		"rotate_on": {"on"}, "rotate": {"03:30"},
		"wake_on_message": {"on"}, // autostart unchecked
	}
	resp, body := e.do("POST", "/agents/ultron/settings/runtime", form, true)
	if resp.StatusCode != 200 {
		t.Fatalf("save: %d %s", resp.StatusCode, body)
	}
	if len(e.cfg.runtimes) != 1 {
		t.Fatalf("UpdateRuntime called %d times", len(e.cfg.runtimes))
	}
	rt := e.cfg.runtimes[0]
	if rt.Model != "claude-opus-5-5[1m]" || rt.Permissions != core.PermDefault || rt.OnWake != "Check the calendar.\nThen mail." || rt.Rotate != "03:30" {
		t.Errorf("runtime = %+v", rt)
	}
	if rt.Autostart == nil || *rt.Autostart || rt.WakeOnMessage != nil {
		t.Errorf("switches: autostart %v, wake %v; want false, unset", rt.Autostart, rt.WakeOnMessage)
	}
	if rt.Harness != before.Runtime.Harness || len(rt.Triggers) != len(before.Runtime.Triggers) || rt.Skills != before.Runtime.Skills {
		t.Errorf("fields the form doesn't edit changed: %+v", rt)
	}
	if !strings.Contains(body, `id="settings-panel"`) || !strings.Contains(body, `hx-swap-oob="true"`) || !strings.Contains(body, "Pending restart") {
		t.Error("answer should be the panel plus the pending-restart banner, out of band")
	}
	if !strings.Contains(resp.Header.Get("HX-Trigger"), "Restart to apply") {
		t.Errorf("toast %q", resp.Header.Get("HX-Trigger"))
	}
	if a, _ := e.cfg.Get(ctx, "ultron"); !a.PendingRestart || !strings.Contains(a.PendingReason, "model changed") {
		t.Errorf("pending = %v %q", a.PendingRestart, a.PendingReason)
	}

	// a listed model, no rotation, both switches on: defaults stay unset
	resp, _ = e.do("POST", "/agents/ultron/settings/runtime", url.Values{"model": {"claude-haiku-5-5"}, "permissions": {"bypass"}, "autostart": {"on"}, "wake_on_message": {"on"}}, false)
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/agents/ultron/settings/runtime" {
		t.Errorf("plain post: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	if rt := e.cfg.runtimes[1]; rt.Model != "claude-haiku-5-5" || rt.Rotate != "" || rt.Autostart != nil || rt.WakeOnMessage != nil {
		t.Errorf("second save = %+v", rt)
	}

	// refused: shown in the panel, with what was typed
	resp, body = e.do("POST", "/agents/ultron/settings/runtime", url.Values{"model": {"custom"}, "model_custom": {"has space"}, "permissions": {"bypass"}}, true)
	if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, "A model id has no spaces") || !strings.Contains(body, `value="has space"`) {
		t.Errorf("invalid model: %d", resp.StatusCode)
	}
	resp, body = e.do("POST", "/agents/ultron/settings/runtime", url.Values{"rotate_on": {"on"}, "permissions": {"bypass"}}, true)
	if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, "Pick the time") {
		t.Errorf("rotate without a time: %d", resp.StatusCode)
	}
	if len(e.cfg.runtimes) != 3 { // the rotate one never reached the service
		t.Errorf("UpdateRuntime calls = %d, want 3", len(e.cfg.runtimes))
	}
}

func TestRestartFromSettings(t *testing.T) {
	e := setupSettings(t)
	resp, body := e.do("POST", "/agents/lola/restart", url.Values{"from": {"settings"}}, true)
	if resp.StatusCode != 200 || !strings.Contains(body, `id="pending-settings"`) || strings.Contains(body, "Pending restart") {
		t.Errorf("restart from settings: %d %s", resp.StatusCode, body)
	}
	if a, _ := e.cfg.Get(context.Background(), "lola"); a.PendingRestart {
		t.Error("restart left the change pending")
	}
}

func TestSkills(t *testing.T) {
	e := setupSettings(t)
	ctx := context.Background()
	resp, body := e.do("POST", "/agents/ultron/settings/skills/add", url.Values{"from": {"user:herdr"}}, true)
	if resp.StatusCode != 200 || len(e.cfg.added) != 1 || e.cfg.added[0] != "user:herdr" {
		t.Fatalf("add: %d %v", resp.StatusCode, e.cfg.added)
	}
	if !strings.Contains(body, "/settings/files?path=.claude%2fskills%2fherdr%2fSKILL.md") {
		t.Error("added skill should be listed with a link to its SKILL.md")
	}
	resp, body = e.do("POST", "/agents/ultron/settings/skills/add", url.Values{"from": {"user:herdr"}}, true)
	if resp.StatusCode != http.StatusConflict || !strings.Contains(body, "already has a skill named") {
		t.Errorf("duplicate add: %d", resp.StatusCode)
	}
	resp, body = e.do("POST", "/agents/ultron/settings/skills/remove", url.Values{"id": {"agent:herdr"}}, true)
	if resp.StatusCode != 200 || len(e.cfg.removed) != 1 || strings.Contains(body, "herdr%2fSKILL.md") {
		t.Errorf("remove: %d %v", resp.StatusCode, e.cfg.removed)
	}
	sks, _ := e.cfg.Skills(ctx, "ultron")
	for _, sk := range sks {
		if sk.ID == "agent:herdr" {
			t.Error("herdr still an agent skill")
		}
	}
	resp, body = e.do("POST", "/agents/ultron/settings/skills/mode", url.Values{"mode": {"barebones"}}, true)
	if resp.StatusCode != 200 || len(e.cfg.runtimes) != 1 || e.cfg.runtimes[0].Skills != core.SkillsBarebones {
		t.Fatalf("mode: %d %+v", resp.StatusCode, e.cfg.runtimes)
	}
	if !strings.Contains(body, `value="barebones" checked`) || !strings.Contains(body, "Not visible under Barebones") {
		t.Error("panel doesn't show barebones")
	}
}

func TestSecretsAreWriteOnly(t *testing.T) {
	e := setupSettings(t)
	const secret = "sk-live-Zq81PRIVATE-do-not-echo"
	check := func(what, body string) {
		t.Helper()
		if strings.Contains(body, secret) || strings.Contains(body, "PRIVATE") {
			t.Errorf("%s: response contains the secret value", what)
		}
	}
	for _, hx := range []bool{true, false} {
		resp, body := e.do("POST", "/agents/ultron/settings/secrets", url.Values{"key": {"STRIPE_KEY"}, "value": {secret}}, hx)
		check("add", body)
		check("add headers", resp.Header.Get("HX-Trigger"))
		if hx && (resp.StatusCode != 200 || !strings.Contains(body, "STRIPE_KEY") || !strings.Contains(body, "31 chars")) {
			t.Errorf("add (htmx): %d", resp.StatusCode)
		}
		if !hx && resp.StatusCode != http.StatusSeeOther {
			t.Errorf("add (plain): %d", resp.StatusCode)
		}
	}
	if e.cfg.env["STRIPE_KEY"] != secret {
		t.Errorf("SetEnv got %q", e.cfg.env["STRIPE_KEY"])
	}
	resp, body := e.do("POST", "/agents/ultron/settings/secrets", url.Values{"key": {"STRIPE_KEY"}, "value": {secret + "2"}, "replace": {"1"}}, true)
	check("replace", body)
	if resp.StatusCode != 200 {
		t.Errorf("replace: %d", resp.StatusCode)
	}
	resp, body = e.do("POST", "/agents/ultron/settings/secrets", url.Values{"key": {"9BAD KEY"}, "value": {secret}}, true)
	check("refused add", body)
	if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, "not a valid name") || !strings.Contains(body, `value="9BAD KEY"`) {
		t.Errorf("bad key: %d", resp.StatusCode)
	}
	if strings.Contains(body, `type="password" name="value" required autocomplete="off" spellcheck="false" autocapitalize="off" data-1p-ignore data-lpignore="true" placeholder="pasted once, never shown again" value`) {
		t.Error("value field prefilled")
	}
	for _, p := range []string{"/agents/ultron/settings/secrets", "/agents/ultron/settings/email", "/agents/ultron", "/agents/ultron/settings/files?path=lolabot.yaml"} {
		_, body := e.get(p)
		check(p, body)
		if strings.Contains(body, strings.Repeat("c", 163)) || strings.Contains(body, strings.Repeat("u", 24)) {
			t.Errorf("%s shows a seeded secret", p)
		}
	}
	_, body = e.get("/agents/ultron/settings/secrets")
	if !strings.Contains(body, `type="password"`) || !strings.Contains(body, `autocomplete="off"`) {
		t.Error("value inputs should be password fields without autocomplete")
	}
	resp, body = e.do("POST", "/agents/ultron/settings/secrets/delete", url.Values{"key": {"STRIPE_KEY"}}, true)
	if resp.StatusCode != 200 || strings.Contains(body, "STRIPE_KEY</span>") {
		t.Errorf("delete: %d", resp.StatusCode)
	}
}

func TestFileSave(t *testing.T) {
	e := setupSettings(t)
	ctx := context.Background()
	fc, err := e.cfg.ReadFile(ctx, "lola", "brain/people.md")
	if err != nil {
		t.Fatal(err)
	}
	resp, body := e.do("POST", "/agents/lola/settings/files", url.Values{"path": {fc.Path}, "base": {fc.Hash}, "content": {"# People\r\n\nMario.\n"}}, true)
	if resp.StatusCode != 200 || !strings.Contains(body, "Mario.") {
		t.Fatalf("save: %d", resp.StatusCode)
	}
	now, _ := e.cfg.ReadFile(ctx, "lola", fc.Path)
	if now.Content != "# People\n\nMario.\n" {
		t.Errorf("saved %q", now.Content)
	}

	// someone else writes; our stale save is refused and keeps our text
	if _, err := e.cfg.WriteFile(ctx, "lola", fc.Path, "# People\n\nTheirs.\n", now.Hash); err != nil {
		t.Fatal(err)
	}
	theirs, _ := e.cfg.ReadFile(ctx, "lola", fc.Path)
	resp, body = e.do("POST", "/agents/lola/settings/files", url.Values{"path": {fc.Path}, "base": {now.Hash}, "content": {"# People\n\nMine.\n"}}, true)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("stale save: %d", resp.StatusCode)
	}
	for _, want := range []string{"changed on disk after you opened it", "Mine.", "Reload from disk", `name="force_base" value="` + theirs.Hash + `"`, `name="base" value="` + now.Hash + `"`} {
		if !strings.Contains(body, want) {
			t.Errorf("conflict answer lacks %q", want)
		}
	}
	if got, _ := e.cfg.ReadFile(ctx, "lola", fc.Path); got.Content != theirs.Content {
		t.Error("a refused save changed the file")
	}
	// overwrite with mine
	resp, _ = e.do("POST", "/agents/lola/settings/files", url.Values{"path": {fc.Path}, "base": {now.Hash}, "force_base": {theirs.Hash}, "content": {"# People\n\nMine.\n"}}, true)
	if got, _ := e.cfg.ReadFile(ctx, "lola", fc.Path); resp.StatusCode != 200 || got.Content != "# People\n\nMine.\n" {
		t.Errorf("overwrite: %d %q", resp.StatusCode, got.Content)
	}
	// plain form: redirect back to the file
	cur, _ := e.cfg.ReadFile(ctx, "lola", fc.Path)
	resp, _ = e.do("POST", "/agents/lola/settings/files", url.Values{"path": {fc.Path}, "base": {cur.Hash}, "content": {"x"}}, false)
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/agents/lola/settings/files?path=brain%2Fpeople.md" {
		t.Errorf("plain save: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
}

func TestFilePreviewIsSafe(t *testing.T) {
	e := setupSettings(t)
	resp, body := e.do("POST", "/agents/lola/settings/files/preview", url.Values{"content": {"# Hi\n\n<script>alert(1)</script> [x](javascript:alert(1)) **b**"}}, true)
	if resp.StatusCode != 200 || !strings.Contains(body, "<h1>Hi</h1>") || !strings.Contains(body, "<strong>b</strong>") {
		t.Errorf("preview: %d %s", resp.StatusCode, body)
	}
	if strings.Contains(body, "<script") || strings.Contains(body, `href="javascript`) {
		t.Errorf("preview not escaped: %s", body)
	}
}

func TestEmailSave(t *testing.T) {
	e := setupSettings(t)
	ctx := context.Background()
	const pw = "hunter2-PRIVATE-app-password"
	form := url.Values{"orig": {""}, "address": {"iris@gmail.com"}, "password": {pw}}
	resp, body := e.do("POST", "/agents/iris/settings/email/account", form, true)
	if resp.StatusCode != 200 {
		t.Fatalf("add account: %d %s", resp.StatusCode, body)
	}
	if strings.Contains(body, pw) || strings.Contains(resp.Header.Get("HX-Trigger"), pw) {
		t.Error("response echoes the password")
	}
	if len(e.cfg.emails) != 1 {
		t.Fatalf("SaveEmail calls %d", len(e.cfg.emails))
	}
	got := e.cfg.emails[0].Accounts[0]
	if got.Password != pw || got.IMAPServer != "imap.gmail.com" || got.SMTPServer != "smtp.gmail.com" || got.IMAPPort != 993 || got.SMTPPort != 465 {
		t.Errorf("SaveEmail account = %+v", got)
	}
	if !strings.Contains(body, "iris@gmail.com") || !strings.Contains(body, "password set") || !strings.Contains(body, "tools/email.sh was updated") {
		t.Error("panel should list the account, its password as set, and the upgrade note")
	}

	// edit without a password keeps the stored one
	form = url.Values{"orig": {"iris@gmail.com"}, "address": {"iris@gmail.com"}, "imap_server": {"imap.gmail.com"}, "imap_port": {"993"}, "smtp_server": {"smtp.gmail.com"}, "smtp_port": {"465"}, "username": {"iris.login@gmail.com"}}
	resp, body = e.do("POST", "/agents/iris/settings/email/account", form, true)
	if resp.StatusCode != 200 || e.cfg.emails[1].Accounts[0].Password != "" || e.cfg.emails[1].Accounts[0].Username != "iris.login@gmail.com" {
		t.Errorf("edit: %d %+v", resp.StatusCode, e.cfg.emails[1].Accounts)
	}
	if s, _ := e.cfg.Email(ctx, "iris"); !s.Accounts[0].PasswordSet {
		t.Error("edit dropped the password")
	}
	for _, p := range []string{"/agents/iris/settings/email", "/agents/iris/settings/secrets"} {
		if _, body := e.get(p); strings.Contains(body, pw) {
			t.Errorf("%s shows the password", p)
		}
	}

	// refused account: typed fields come back, the password doesn't
	form = url.Values{"orig": {""}, "address": {"nobody"}, "imap_server": {"imap.x.io"}, "password": {pw}}
	resp, body = e.do("POST", "/agents/iris/settings/email/account", form, true)
	if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, "not an email address") || !strings.Contains(body, `value="imap.x.io"`) || strings.Contains(body, pw) {
		t.Errorf("bad account: %d", resp.StatusCode)
	}

	// trusted senders and lolad watch
	resp, body = e.do("POST", "/agents/iris/settings/email", url.Values{"operators": {"you@example.com\r\nops@example.com"}, "watch": {"lolad"}, "watch_account": {"iris@gmail.com"}, "every": {"3m"}}, true)
	last := e.cfg.emails[len(e.cfg.emails)-1]
	if resp.StatusCode != 200 || len(last.OperatorEmails) != 2 || last.Watch != "lolad" || last.WatchAccount != "iris@gmail.com" || last.Every != "3m" {
		t.Errorf("general: %d %+v", resp.StatusCode, last)
	}
	if !strings.Contains(body, "watched by lolad") {
		t.Error("watched account not marked")
	}

	// the detected mod is never sent back as a watch mode
	e.do("POST", "/agents/ultron/settings/email", url.Values{"operators": {"you@example.com"}, "watch": {"none"}}, true)
	if last := e.cfg.emails[len(e.cfg.emails)-1]; last.Watch != "none" {
		t.Errorf("ultron watch sent as %q", last.Watch)
	}
	if _, body := e.get("/agents/ultron/settings/email"); !strings.Contains(body, "A mail mod watches this inbox") {
		t.Error("mod no longer shown")
	}

	// remove
	resp, _ = e.do("POST", "/agents/iris/settings/email/account/delete", url.Values{"address": {"iris@gmail.com"}}, true)
	if s, _ := e.cfg.Email(ctx, "iris"); resp.StatusCode != 200 || len(s.Accounts) != 0 || s.Watch == "lolad" {
		t.Errorf("remove: %d %+v", resp.StatusCode, s)
	}

	// unsupported
	resp, _ = e.do("POST", "/agents/patch/settings/email", url.Values{"watch": {"none"}}, true)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("save on an agent without email.sh: %d", resp.StatusCode)
	}
}

func TestEmailTestResult(t *testing.T) {
	e := setupSettings(t)
	for _, tc := range []struct {
		agent, addr string
		want        []string
	}{
		{"ultron", "ultron@mail.example.com", []string{"etest is-ok", "logged in", "imap.migadu.com:993"}},
		{"ledger", "ops@example.com", []string{`etest-v is-ok`, `etest-v is-bad`, "535 5.7.8 Authentication failed", "app password"}},
		{"ultron", "nobody@example.com", []string{"is-skip", "not tried"}},
	} {
		resp, body := e.do("POST", "/agents/"+tc.agent+"/settings/email/test", url.Values{"address": {tc.addr}}, true)
		if resp.StatusCode != 200 {
			t.Errorf("%s test: %d", tc.addr, resp.StatusCode)
		}
		for _, w := range tc.want {
			if !strings.Contains(body, w) {
				t.Errorf("%s test lacks %q:\n%s", tc.addr, w, body)
			}
		}
		if strings.Contains(body, strings.Repeat("u", 24)) || strings.Contains(body, "bad-ooo") {
			t.Errorf("%s test shows the password", tc.addr)
		}
	}
}
