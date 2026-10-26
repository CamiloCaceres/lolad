package web_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/CamiloCaceres/lolad/internal/core"
	"github.com/CamiloCaceres/lolad/internal/web"
	"github.com/CamiloCaceres/lolad/internal/web/fake"
)

// spy records what the dashboard asks the service to send.
type spy struct {
	core.Service
	mu   sync.Mutex
	sent []core.Outgoing
}

func (s *spy) UserSend(ctx context.Context, out core.Outgoing) (core.Mail, error) {
	s.mu.Lock()
	s.sent = append(s.sent, out)
	s.mu.Unlock()
	return s.Service.UserSend(ctx, out)
}

func (s *spy) Sent() []core.Outgoing {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.sent)
}

type env struct {
	t   *testing.T
	svc *spy
	srv *httptest.Server
}

func setup(t *testing.T) *env {
	t.Helper()
	f := fake.New(fake.Options{})
	t.Cleanup(f.Close)
	sp := &spy{Service: f}
	h := web.New(sp, web.Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &env{t: t, svc: sp, srv: srv}
}

var noRedirect = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func (e *env) do(method, path string, form url.Values, hx bool) (*http.Response, string) {
	e.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequest(method, e.srv.URL+path, body)
	if err != nil {
		e.t.Fatal(err)
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if hx {
		req.Header.Set("HX-Request", "true")
	}
	resp, err := noRedirect.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func (e *env) get(path string) (int, string) {
	resp, body := e.do("GET", path, nil, false)
	return resp.StatusCode, body
}

func TestPages(t *testing.T) {
	e := setup(t)
	for _, tc := range []struct {
		path, want string
	}{
		{"/", "Inbox"},
		{"/inbox/lola", "Ship pricing today?"},
		{"/inbox/scout", "No messages with Scout yet"},
		{"/agents", "Ledger"},
		{"/agents/lola", "Live terminal"},
		{"/agents/quill", "restart in"},
		{"/org", "Who can message whom"},
		{"/new", "Create agent"},
		{"/traffic", "Who said what to whom"},
		{"/traffic?peer=quill", "Tighten pricing page copy"},
		{"/fragments/conversations?active=lola", `id="conv-list"`},
		{"/fragments/nav", `id="nav-badges"`},
		{"/fragments/nav?part=alert", `id="nav-alert"`},
		{"/fragments/fleet", `id="fleet"`},
		{"/inbox/lola/messages", "Ship pricing today?"},
		{"/inbox/lola/head", `id="thread-head"`},
		{"/agents/lola/part/head", `id="agent-head"`},
		{"/agents/lola/part/events", `id="agent-events"`},
		{"/agents/lola/part/mail", `id="agent-mail"`},
		{"/agents/lola/part/facts", `id="agent-facts"`},
		{"/static/app.css", "--you"},
		{"/static/vendor/htmx.min.js", "htmx"},
		{"/static/vendor/xterm.js", "Terminal"},
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
	for _, p := range []string{"/nope", "/agents/nobody", "/inbox/nobody", "/agents/lola/part/secrets"} {
		if code, _ := e.get(p); code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", p, code)
		}
	}
}

// A fresh install has no agents: every page says what lolad is and points to
// creating or adopting one, instead of empty tables.
func TestFirstRun(t *testing.T) {
	f := fake.New(fake.Options{Empty: true})
	defer f.Close()
	srv := httptest.NewServer(web.New(f, web.Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}))
	defer srv.Close()
	e := &env{t: t, svc: &spy{Service: f}, srv: srv}
	for _, tc := range []struct {
		path      string
		want, not []string
	}{
		{"/", []string{"Welcome to lolad", `href="/new"`, `href="/new?mode=adopt"`}, []string{"caught up", "popovertarget=\"new-message\""}},
		{"/agents", []string{"No agents yet", `href="/new?mode=adopt"`}, []string{"<table"}},
		{"/org", []string{"A team starts with a head", "Create a team"}, []string{`class="matrix"`, "Test a message"}},
		{"/traffic", []string{"No mail yet", `href="/new"`}, []string{`id="peer-filter"`}},
		{"/new?mode=adopt", []string{`id="mode-adopt" value="adopt" checked`}, nil},
	} {
		code, body := e.get(tc.path)
		if code != 200 {
			t.Errorf("GET %s = %d", tc.path, code)
		}
		for _, w := range tc.want {
			if !strings.Contains(body, w) {
				t.Errorf("GET %s: missing %q", tc.path, w)
			}
		}
		for _, n := range tc.not {
			if strings.Contains(body, n) {
				t.Errorf("GET %s: should not contain %q", tc.path, n)
			}
		}
	}
}

func TestPagesDoNotClaimIntegratorPrefixes(t *testing.T) {
	e := setup(t)
	for _, p := range []string{"/api/agents", "/v1/health", "/hook/lola/x"} {
		if code, _ := e.get(p); code != http.StatusNotFound {
			t.Errorf("GET %s = %d; the dashboard must leave it to the integrator", p, code)
		}
	}
}

func lastFrom(t *testing.T, svc core.Service, peer string, kind string) core.Mail {
	t.Helper()
	msgs, err := svc.Conversation(context.Background(), peer, 100, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		uc, _ := m.Context["userchat"].(map[string]any)
		if m.FromName == peer && (kind == "" || uc["kind"] == kind) {
			return m
		}
	}
	t.Fatalf("no message from %s of kind %q", peer, kind)
	return core.Mail{}
}

func userchat(t *testing.T, out core.Outgoing) map[string]any {
	t.Helper()
	uc, ok := out.Context["userchat"].(map[string]any)
	if !ok {
		t.Fatalf("context.userchat missing or wrong type: %#v", out.Context)
	}
	return uc
}

func TestSendCallsUserSend(t *testing.T) {
	e := setup(t)
	last := lastFrom(t, e.svc, "lola", "")
	resp, body := e.do("POST", "/inbox/lola", url.Values{"body": {"Hold the Founders tier, ship the rest."}, "reply_to": {last.ID}}, true)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	sent := e.svc.Sent()
	if len(sent) != 1 {
		t.Fatalf("UserSend called %d times, want 1", len(sent))
	}
	out := sent[0]
	if out.To != "lola" || out.Body != "Hold the Founders tier, ship the rest." || out.InReplyTo != last.ID {
		t.Errorf("sent %+v", out)
	}
	if uc := userchat(t, out); uc["kind"] != "reply" {
		t.Errorf("userchat kind = %v, want reply", uc["kind"])
	}
	if !strings.HasPrefix(out.Subject, "Re: ") {
		t.Errorf("subject %q, want a reply subject", out.Subject)
	}
	if !strings.Contains(body, "Hold the Founders tier") || !strings.Contains(body, `hx-swap-oob="true"`) {
		t.Errorf("response should carry the new message and out-of-band list updates")
	}

	// A plain form post (no htmx) redirects back to the thread.
	resp, _ = e.do("POST", "/inbox/scout", url.Values{"body": {"Welcome aboard."}}, false)
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/inbox/scout" {
		t.Errorf("plain post: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	if out := e.svc.Sent()[1]; userchat(t, out)["kind"] != "say" || out.InReplyTo != "" || out.Subject != "Welcome aboard." {
		t.Errorf("first message to a new peer: %+v", out)
	}
}

func TestSendRejectsEmpty(t *testing.T) {
	e := setup(t)
	resp, body := e.do("POST", "/inbox/lola", url.Values{"body": {"   "}}, true)
	if resp.StatusCode != http.StatusUnprocessableEntity || resp.Header.Get("HX-Retarget") != "#composer-error" {
		t.Errorf("status %d retarget %q", resp.StatusCode, resp.Header.Get("HX-Retarget"))
	}
	if !strings.Contains(body, "Write a message first") {
		t.Errorf("body: %s", body)
	}
	if n := len(e.svc.Sent()); n != 0 {
		t.Errorf("UserSend called %d times", n)
	}
}

func TestDecisionButtons(t *testing.T) {
	e := setup(t)
	code, page := e.get("/inbox/lola")
	ask := lastFrom(t, e.svc, "lola", "ask")
	if code != 200 || !strings.Contains(page, `value="Hold until Thursday"`) || !strings.Contains(page, `name="id" value="`+ask.ID+`"`) {
		t.Fatalf("ask options not rendered as buttons")
	}

	resp, body := e.do("POST", "/inbox/lola/decide", url.Values{"id": {ask.ID}, "option": {"Hold until Thursday"}}, true)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	sent := e.svc.Sent()
	if len(sent) != 1 {
		t.Fatalf("UserSend called %d times", len(sent))
	}
	out := sent[0]
	uc := userchat(t, out)
	if uc["kind"] != "decision" || uc["decision"] != "Hold until Thursday" {
		t.Errorf("userchat = %v, want {kind: decision, decision: Hold until Thursday}", uc)
	}
	if out.InReplyTo != ask.ID || out.To != "lola" || out.Body != "Hold until Thursday" || out.Subject != "Re: Ship pricing today?" {
		t.Errorf("sent %+v", out)
	}
	// The answered ask now shows the choice and retires its buttons.
	if !strings.Contains(body, `option is-chosen`) || !strings.Contains(body, " disabled") {
		t.Errorf("answered ask not marked")
	}

	// Options not offered by the ask, and asks from someone else, are refused.
	for _, f := range []url.Values{
		{"id": {ask.ID}, "option": {"Burn it all down"}},
		{"id": {"msg_0_nothere"}, "option": {"Hold until Thursday"}},
	} {
		resp, _ := e.do("POST", "/inbox/lola/decide", f, true)
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("decide %v = %d, want 422", f, resp.StatusCode)
		}
	}
	if n := len(e.svc.Sent()); n != 1 {
		t.Errorf("UserSend called %d times, want 1", n)
	}
}

// lolad msg tell-user puts the message kind in the subject; that must not show
// as a heading, and answers still need a real (non-empty) subject.
func TestPlaceholderSubjects(t *testing.T) {
	f := fake.New(fake.Options{})
	defer f.Close()
	sp := &spy{Service: f}
	srv := httptest.NewServer(web.New(sp, web.Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}))
	defer srv.Close()
	e := &env{t: t, svc: sp, srv: srv}
	ask, err := f.AgentSend(context.Background(), "ultron", core.Outgoing{To: "milo", Subject: "ask", Body: "Coffee or tea?",
		Context: map[string]any{"userchat": map[string]any{"kind": "ask", "options": []string{"Coffee", "Tea"}}}})
	if err != nil {
		t.Fatal(err)
	}
	_, page := e.get("/inbox/ultron")
	if strings.Contains(page, `<p class="bubble-subj">ask</p>`) || !strings.Contains(page, "Coffee or tea?") {
		t.Error("placeholder subject rendered as a heading")
	}
	if _, home := e.get("/"); !strings.Contains(home, "Coffee or tea?") {
		t.Error("home should list the open question by its text")
	}
	resp, body := e.do("POST", "/inbox/ultron/decide", url.Values{"id": {ask.ID}, "option": {"Tea"}}, true)
	if resp.StatusCode != 200 {
		t.Fatalf("decide: %d %s", resp.StatusCode, body)
	}
	if got := sp.Sent()[0].Subject; got != "Re: Coffee or tea?" {
		t.Errorf("decision subject %q", got)
	}
}

func TestOpeningThreadMarksRead(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	unread := func() int {
		convs, _ := e.svc.Conversations(ctx)
		for _, c := range convs {
			if c.Peer == "forge" {
				return c.Unread
			}
		}
		return -1
	}
	if unread() == 0 {
		t.Fatal("seed should have unread mail from forge")
	}
	if code, body := e.get("/inbox/forge"); code != 200 || !strings.Contains(body, `class="sep is-new"`) {
		t.Fatalf("thread: %d, new-divider shown: %v", code, strings.Contains(body, "is-new"))
	}
	if n := unread(); n != 0 {
		t.Errorf("unread after opening = %d", n)
	}
}

func TestPolicyDenialShownAtComposer(t *testing.T) {
	e := setup(t)
	org, _ := e.svc.Org(context.Background())
	org.Defaults.UserToAgents = "heads"
	if err := e.svc.SaveOrg(context.Background(), org); err != nil {
		t.Fatal(err)
	}
	_, page := e.get("/inbox/patch")
	if !strings.Contains(page, "doesn't let you message Patch") || strings.Contains(page, "data-composer") {
		t.Errorf("composer should be replaced by the policy reason")
	}
	resp, body := e.do("POST", "/inbox/patch", url.Values{"body": {"hi"}}, true)
	if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, "policy") {
		t.Errorf("denied send: %d %s", resp.StatusCode, body)
	}
}

func TestCrossOriginWritesRefused(t *testing.T) {
	e := setup(t)
	req, _ := http.NewRequest("POST", e.srv.URL+"/agents/lola/stop", nil)
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("cross-site POST = %d, want 403", resp.StatusCode)
	}
	if a, _ := e.svc.Get(context.Background(), "lola"); a.State == core.StateStopped {
		t.Error("lola was stopped by a cross-site request")
	}
}

func TestControls(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	resp, body := e.do("POST", "/agents/scout/start", url.Values{}, true)
	if resp.StatusCode != 200 || !strings.Contains(body, `id="agent-head"`) {
		t.Fatalf("start: %d", resp.StatusCode)
	}
	if !strings.Contains(resp.Header.Get("HX-Trigger"), "Starting scout") {
		t.Errorf("toast header %q", resp.Header.Get("HX-Trigger"))
	}
	if a, _ := e.svc.Get(ctx, "scout"); a.State == core.StateStopped || a.Desired != "up" {
		t.Errorf("scout after start: %s/%s", a.State, a.Desired)
	}
	resp, body = e.do("POST", "/agents/scout/stop", url.Values{"from": {"fleet"}}, true)
	if resp.StatusCode != 200 || !strings.Contains(body, `id="fleet"`) {
		t.Errorf("stop from fleet should answer with the fleet: %d", resp.StatusCode)
	}
	if a, _ := e.svc.Get(ctx, "scout"); a.State != core.StateStopped {
		t.Errorf("scout after stop: %s", a.State)
	}
	for _, p := range []string{"/agents/lola/restart", "/agents/lola/fresh", "/agents/lola/interrupt"} {
		if resp, _ := e.do("POST", p, url.Values{}, true); resp.StatusCode != 200 {
			t.Errorf("POST %s = %d", p, resp.StatusCode)
		}
	}
	if resp, _ := e.do("POST", "/agents/lola/explode", url.Values{}, true); resp.StatusCode != 404 {
		t.Errorf("unknown action = %d", resp.StatusCode)
	}
	resp, _ = e.do("POST", "/agents/nobody/start", url.Values{}, true)
	if resp.StatusCode != 404 || resp.Header.Get("HX-Reswap") != "none" || !strings.Contains(resp.Header.Get("HX-Trigger"), "error") {
		t.Errorf("start unknown agent: %d %v", resp.StatusCode, resp.Header)
	}
}

func team(org core.Org, id string) *core.Team {
	for i := range org.Teams {
		if org.Teams[i].ID == id {
			return &org.Teams[i]
		}
	}
	return nil
}

func TestOrgSaveRoundTrip(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	org := func() core.Org { o, _ := e.svc.Org(ctx); return o }

	// Add a sub-team; its head moves out of the team it was in.
	resp, body := e.do("POST", "/org/teams", url.Values{
		"name": {"Research"}, "parent": {"growth"}, "head": {"scout"}, "members": {"ultron"}, "open": {"on"},
		"instructions": {"Dig, then report to Juno."},
	}, true)
	if resp.StatusCode != 200 || !strings.Contains(body, "Team research added") {
		t.Fatalf("add team: %d %s", resp.StatusCode, body)
	}
	o := org()
	r := team(o, "research")
	if r == nil || r.Name != "Research" || r.Parent != "growth" || r.Head != "scout" || !r.Open || !slices.Equal(r.Members, []string{"ultron"}) || r.Instructions != "Dig, then report to Juno." {
		t.Fatalf("research = %+v", r)
	}
	if slices.Contains(team(o, "growth").Members, "scout") {
		t.Error("scout should have moved out of growth")
	}
	if !strings.Contains(body, "Research") || !strings.Contains(body, `id="org-body"`) {
		t.Error("org body not re-rendered with the new team")
	}

	// Edit it.
	if resp, _ := e.do("POST", "/org/teams/research", url.Values{"name": {"Research & Intel"}, "parent": {"hq"}, "head": {"scout"}}, true); resp.StatusCode != 200 {
		t.Fatalf("save team: %d", resp.StatusCode)
	}
	if r := team(org(), "research"); r.Name != "Research & Intel" || r.Parent != "hq" || r.Open || len(r.Members) != 0 {
		t.Errorf("edited research = %+v", r)
	}

	// Rules: add, move, delete.
	before := len(org().Rules)
	if resp, _ := e.do("POST", "/org/rules", url.Values{"from": {"team:research"}, "to": {"forge"}, "allow": {"allow"}, "both": {"on"}, "note": {"research feeds eng"}}, true); resp.StatusCode != 200 {
		t.Fatalf("add rule: %d", resp.StatusCode)
	}
	rules := org().Rules
	want := core.Rule{From: "team:research", To: "forge", Allow: true, Both: true, Note: "research feeds eng"}
	if len(rules) != before+1 || rules[before] != want {
		t.Fatalf("rules = %+v", rules)
	}
	if d := e.svc.Check(ctx, "forge", "scout"); !d.Allow || !strings.HasPrefix(d.Reason, "rule") {
		t.Errorf("new rule not in effect: %+v", d)
	}
	e.do("POST", "/org/rules/"+strconv.Itoa(before)+"/up", url.Values{}, true)
	if org().Rules[before-1] != want {
		t.Error("rule did not move up")
	}
	e.do("POST", "/org/rules/0/delete", url.Values{}, true)
	if n := len(org().Rules); n != before {
		t.Errorf("after delete %d rules, want %d", n, before)
	}

	// Defaults.
	if resp, _ := e.do("POST", "/org/defaults", url.Values{"user_to_agents": {"heads"}, "agents_to_user": {"all"}, "independents": {"all"}}, true); resp.StatusCode != 200 {
		t.Fatalf("defaults: %d", resp.StatusCode)
	}
	if d := org().Defaults; d != (core.Defaults{UserToAgents: "heads", AgentsToUser: "all", Vertical: false, Independents: "all"}) {
		t.Errorf("defaults = %+v", d)
	}

	// Invalid changes are refused with the reason and leave the org alone.
	o = org()
	resp, body = e.do("POST", "/org/teams/research", url.Values{"name": {"Loop"}, "parent": {"research"}}, true)
	if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, "flash is-error") {
		t.Errorf("cyclic parent: %d", resp.StatusCode)
	}
	if resp, _ := e.do("POST", "/org/teams", url.Values{"name": {"Engineering"}, "id": {"eng"}}, true); resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("duplicate team id: %d", resp.StatusCode)
	}
	if got := org(); team(got, "research").Name != team(o, "research").Name || len(got.Teams) != len(o.Teams) {
		t.Error("org changed by a refused save")
	}

	// Remove: sub-teams move up a level.
	e.do("POST", "/org/teams", url.Values{"name": {"Archive"}, "parent": {"research"}}, true)
	e.do("POST", "/org/teams/research/delete", url.Values{}, true)
	if got := org(); team(got, "research") != nil || team(got, "archive").Parent != "hq" {
		t.Errorf("after delete: %+v", got.Teams)
	}
}

func TestOrgMatrixAndCheck(t *testing.T) {
	e := setup(t)
	_, page := e.get("/org")
	if !strings.Contains(page, `data-from="scout" data-to="forge" data-reason="rule 2 denies (scout→team:eng)"`) {
		t.Error("matrix cell lacks its reason")
	}
	resp, body := e.do("POST", "/org/check", url.Values{"from": {"lola"}, "to": {"forge"}}, true)
	if resp.StatusCode != 200 || !strings.Contains(body, "allowed") || !strings.Contains(body, "vertical") {
		t.Errorf("check: %d %s", resp.StatusCode, body)
	}
	_, body = e.do("POST", "/org/check", url.Values{"from": {"ultron"}, "to": {"juno"}}, true)
	if !strings.Contains(body, "denied") || !strings.Contains(body, "independents") {
		t.Errorf("check deny: %s", body)
	}
}

func TestCreateAndAdopt(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	resp, _ := e.do("POST", "/new", url.Values{
		"name": {"Nova"}, "role": {"Designer"}, "harness": {"codewhale"}, "permissions": {"bypass"},
		"team": {"growth"}, "mission": {"Own the design system."}, "start": {"on"},
	}, true)
	if resp.StatusCode != http.StatusNoContent || resp.Header.Get("HX-Redirect") != "/inbox/nova?created=1" {
		t.Fatalf("create: %d redirect %q", resp.StatusCode, resp.Header.Get("HX-Redirect"))
	}
	a, err := e.svc.Get(ctx, "nova")
	if err != nil || a.Role != "Designer" || a.Runtime.Harness != core.HarnessCodewhale || a.Team != "growth" {
		t.Errorf("nova = %+v, %v", a, err)
	}

	resp, body := e.do("POST", "/new", url.Values{"name": {"Nova"}, "harness": {"claude"}}, true)
	if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, "already exists") || !strings.Contains(body, `value="Nova"`) {
		t.Errorf("duplicate create: %d", resp.StatusCode)
	}

	resp, _ = e.do("POST", "/adopt", url.Values{"dir": {"/Users/you/agents/jugui"}, "harness": {"claude"}}, false)
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/inbox/jugui?adopted=1" {
		t.Errorf("adopt: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	resp, body = e.do("POST", "/adopt", url.Values{"dir": {"relative/dir"}, "harness": {"claude"}}, true)
	if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, "absolute") || !strings.Contains(body, `id="mode-adopt" value="adopt" checked`) {
		t.Errorf("bad adopt: %d", resp.StatusCode)
	}
}

func TestTrafficFilter(t *testing.T) {
	e := setup(t)
	_, body := e.get("/traffic?peer=quill")
	if !strings.Contains(body, "Tighten pricing page copy") || strings.Contains(body, "Pricing copy review") {
		t.Error("filter by participant failed")
	}
}

func wsURL(e *env, path string) string { return "ws" + strings.TrimPrefix(e.srv.URL, "http") + path }

func TestTerminalStreams(t *testing.T) {
	e := setup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c, _, err := websocket.Dial(ctx, wsURL(e, "/agents/lola/terminal"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	typ, data, err := c.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if typ != websocket.MessageBinary || len(data) == 0 || !strings.Contains(string(data), "lolad msg inbox") {
		t.Errorf("first frame: %v %q", typ, data)
	}
	c.Close(websocket.StatusNormalClosure, "")

	// Control mode: keystrokes reach the session (the fake echoes them).
	c, _, err = websocket.Dial(ctx, wsURL(e, "/agents/forge/terminal?mode=control"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	msg, _ := json.Marshal(map[string]any{"type": "input", "data": "status please"})
	if err := c.Write(ctx, websocket.MessageText, msg); err != nil {
		t.Fatal(err)
	}
	var seen strings.Builder
	for !strings.Contains(seen.String(), "status please") {
		_, data, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("input never echoed: %v", err)
		}
		seen.Write(data)
	}
}

func TestTerminalNotRunning(t *testing.T) {
	e := setup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, wsURL(e, "/agents/scout/terminal"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	_, _, err = c.Read(ctx)
	if got := websocket.CloseStatus(err); got != 4409 {
		t.Errorf("close status %d (%v), want 4409", got, err)
	}
}

func TestStreamSendsEvents(t *testing.T) {
	e := setup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", e.srv.URL+"/stream", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type %q", ct)
	}
	r := bufio.NewReader(resp.Body)
	if line, _ := r.ReadString('\n'); !strings.HasPrefix(line, "retry:") {
		t.Fatalf("first line %q", line)
	}
	if _, err := e.svc.UserSend(ctx, core.Outgoing{To: "ultron", Subject: "hi", Body: "hi"}); err != nil {
		t.Fatal(err)
	}
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("no mail event: %v", err)
		}
		if strings.HasPrefix(line, "data: ") {
			var ev struct {
				Kind, Agent, Class string
			}
			if err := json.Unmarshal([]byte(line[6:]), &ev); err != nil {
				t.Fatal(err)
			}
			if ev.Class == "mail" && ev.Agent == "ultron" {
				return
			}
		}
	}
}

func TestDevDirReloads(t *testing.T) {
	f := fake.New(fake.Options{})
	defer f.Close()
	srv := httptest.NewServer(web.New(f, web.Options{Dir: ".", Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}))
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/agents")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("dev mode /agents = %d", resp.StatusCode)
	}
}
