package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/CamiloCaceres/lolad/internal/amp"
	"github.com/CamiloCaceres/lolad/internal/core"
	"github.com/CamiloCaceres/lolad/internal/fed"
	"github.com/CamiloCaceres/lolad/internal/policy"
	"github.com/CamiloCaceres/lolad/internal/store"
)

// renameApp builds a real app in temp directories (temp HOME and state, a
// unique tmux socket, no agent started) under tenant "home", with local
// mail, one queued remote message, an open and a used invite, and a
// connections.yaml. It is closed before it returns: what remains is on disk.
func renameApp(t *testing.T) (config string, p renamePaths) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", filepath.Join(dir, "home"))
	t.Setenv("LOLAD_STATE", filepath.Join(dir, "state"))
	for _, k := range []string{"LOLAD_URL", "LOLAD_AGENT", "TMUX", "LOLAD_HUB_URL"} {
		t.Setenv(k, "")
	}
	config = filepath.Join(dir, "config", "config.yaml")
	agent := filepath.Join(dir, "maple")
	os.MkdirAll(agent, 0o700)
	os.MkdirAll(filepath.Dir(config), 0o700)
	os.WriteFile(filepath.Join(agent, "lolabot.yaml"), []byte("agent:\n  name: Maple\nruntime:\n  harness: claude\n  autostart: false\n"), 0o600)
	os.WriteFile(config, []byte("listen: 127.0.0.1:4193\nagents: ["+agent+"]\ntmux_socket: lolad-test-rename-"+filepath.Base(dir)+"\n"), 0o600)
	// An install from before v0.2: org.yaml says home.
	if e := policy.CreateOrg(filepath.Join(filepath.Dir(config), "org.yaml"), policy.OrgYAML("home", "ana", "Ana")); e != nil {
		t.Fatal(e)
	}
	a, e := New(t.Context(), config)
	if e != nil {
		t.Fatal(e)
	}
	ctx := t.Context()
	maple := a.Registry.List()[0].Name
	if _, e = a.Provider.Send(ctx, maple, core.Outgoing{To: "ana", Subject: "hi", Body: "local mail"}); e != nil {
		t.Fatal(e)
	}
	if _, e = a.Provider.Send(ctx, "ana", core.Outgoing{To: maple + "@home.lolad.local", Subject: "re", Body: "full address"}); e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC()
	mail, _ := json.Marshal(core.Mail{ID: "msg_remote_1", From: "ana@home.lolad.local", To: "ben@ben.lolad.local", FromName: "ana", ToName: "ben@ben.lolad.local", Remote: "ben.lolad.local", CreatedAt: now})
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO amp_messages(id,from_name,to_name,thread_id,created_at,status,mail,wire,remote,delivery,delivery_error) VALUES('msg_remote_1','ana','ben@ben.lolad.local','msg_remote_1',?,'read',?,'{"envelope":{"from":"ana@home.lolad.local"},"signed":"old"}','ben.lolad.local','queued','')`, []any{now.Format(time.RFC3339Nano), string(mail)}},
		{`INSERT INTO fed_outbox(id,peer,next_at,created_at) VALUES('msg_remote_1','ben.lolad.local',0,?)`, []any{now.UnixMilli()}},
		{`INSERT INTO fed_invites(secret_hash,expires,kind) VALUES('open',?,'friend')`, []any{now.Add(time.Hour).Unix()}},
		{`INSERT INTO fed_invites(secret_hash,expires,kind,used_at,used_by) VALUES('used',?,'friend',1,'ben.lolad.local')`, []any{now.Add(time.Hour).Unix()}},
	} {
		if _, e = a.DB.ExecContext(ctx, q.sql, q.args...); e != nil {
			t.Fatal(e)
		}
	}
	p = a.renamePaths()
	os.WriteFile(p.conns(), []byte("peers: []\ngrants: []\n# kept as is\n"), 0o600)
	if e = a.Close(); e != nil {
		t.Fatal(e)
	}
	return config, p
}

func writePlan(t *testing.T, p renamePaths, from, to string) {
	t.Helper()
	b, _ := json.Marshal(renamePlan{From: from, To: to, At: time.Now().UTC(), Anyway: true})
	if e := writeFileAtomic(p.plan(), b); e != nil {
		t.Fatal(e)
	}
}

// snapshot is everything a rename may change, made comparable: file bytes,
// identity rows, mail (mail JSON, wire, delivery), outbox and invites.
func snapshot(t *testing.T, p renamePaths) map[string]string {
	t.Helper()
	s := map[string]string{}
	for k, f := range map[string]string{"org": p.org(), "conns": p.conns()} {
		b, e := os.ReadFile(f)
		if e != nil {
			b = []byte("<absent>")
		}
		s[k] = string(b)
	}
	ids, _ := filepath.Glob(filepath.Join(p.state, "identities", "*", "identity.json"))
	for _, f := range ids {
		b, _ := os.ReadFile(f)
		s[f] = string(b)
	}
	db, e := store.Open(p.db())
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	for _, q := range []string{
		`SELECT name||' '||address FROM amp_identities ORDER BY name`,
		`SELECT id||' '||mail||' '||wire||' '||COALESCE(delivery,'')||' '||COALESCE(delivery_error,'') FROM amp_messages ORDER BY id`,
		`SELECT id FROM fed_outbox ORDER BY id`,
		`SELECT secret_hash FROM fed_invites ORDER BY secret_hash`,
	} {
		rows, e := db.Query(q)
		if e != nil {
			t.Fatal(e)
		}
		var out []string
		for rows.Next() {
			var v string
			rows.Scan(&v)
			out = append(out, v)
		}
		rows.Close()
		s[q] = strings.Join(out, "\n")
	}
	return s
}

func diff(t *testing.T, want, got map[string]string) {
	t.Helper()
	var keys []string
	for k := range want {
		keys = append(keys, k)
	}
	for k := range got {
		if _, ok := want[k]; !ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		if want[k] != got[k] {
			t.Errorf("%s:\nwant %q\n got %q", k, want[k], got[k])
		}
	}
}

func apply(t *testing.T, p renamePaths, hook func(string) error) (*core.RenameStep, error) {
	t.Helper()
	return applyPendingRename(context.Background(), p, time.Now(), hook)
}

// A rename rewrites our addresses and nothing else that it should not: the
// signed wire stays, queued remote mail is marked not sent, open invites go,
// connections end, org.yaml keeps its comments.
func TestRenameApplies(t *testing.T) {
	_, p := renameApp(t)
	writePlan(t, p, "home", "milo-pc")
	step, e := apply(t, p, nil)
	if e != nil || step == nil || step.Error != "" || step.To != "milo-pc" {
		t.Fatal(step, e)
	}
	after := snapshot(t, p)
	org, _ := os.ReadFile(p.org())
	if !strings.Contains(string(org), "tenant: milo-pc") || !strings.Contains(string(org), "<name>@milo-pc.lolad.local") || strings.Contains(string(org), "home") {
		t.Fatalf("org.yaml:\n%s", org)
	}
	if o, e := policy.Load(p.org()); e != nil || o.Tenant != "milo-pc" || o.User.Name != "ana" {
		t.Fatal(o, e)
	}
	all := strings.Join(mapValues(after), "\n")
	for _, k := range []string{`SELECT name||' '||address FROM amp_identities ORDER BY name`} {
		if strings.Contains(after[k], "@home.lolad.local") || !strings.Contains(after[k], "ana ana@milo-pc.lolad.local") {
			t.Fatal(after[k])
		}
	}
	mails := after[`SELECT id||' '||mail||' '||wire||' '||COALESCE(delivery,'')||' '||COALESCE(delivery_error,'') FROM amp_messages ORDER BY id`]
	if db, e := store.Open(p.db()); e == nil {
		var n int
		db.QueryRow(`SELECT count(*) FROM amp_messages WHERE mail LIKE '%@home.lolad.local%'`).Scan(&n)
		var w int
		db.QueryRow(`SELECT count(*) FROM amp_messages WHERE wire LIKE '%@home.lolad.local%'`).Scan(&w)
		db.Close()
		if n != 0 || w != 3 {
			t.Fatal("old address left in mail, or wire rewritten:", n, w)
		}
	}
	if !strings.Contains(mails, `{"envelope":{"from":"ana@home.lolad.local"},"signed":"old"} failed `+fed.RenameNotSent) {
		t.Fatal("wire changed or remote mail not marked:\n", mails)
	}
	if after[`SELECT id FROM fed_outbox ORDER BY id`] != "" || after[`SELECT secret_hash FROM fed_invites ORDER BY secret_hash`] != "used" {
		t.Fatal(after)
	}
	if !strings.Contains(after["conns"], "peers: []") || strings.Contains(after["conns"], "kept as is") {
		t.Fatal(after["conns"])
	}
	if strings.Contains(all, `"provider": "home.lolad.local"`) {
		t.Fatal("identity.json still under home")
	}
	if _, e := os.Stat(p.plan()); !os.IsNotExist(e) {
		t.Fatal("plan left behind")
	}
	if l := readRenameStep(p.last()); l == nil || l.To != "milo-pc" || l.Error != "" {
		t.Fatal(l)
	}
	// Credentials load under the new provider (msgcli reads them per call).
	c, _, e := amp.ReadCredentials(p.state, "ana")
	if e != nil || c.Address != "ana@milo-pc.lolad.local" || c.Provider != "milo-pc.lolad.local" {
		t.Fatal(c, e)
	}
	// Running it again finds nothing to do.
	if s, e := apply(t, p, nil); s != nil || e != nil {
		t.Fatal(s, e)
	}
	diff(t, after, snapshot(t, p))
}

func mapValues(m map[string]string) []string {
	var out []string
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

// The wire column is never touched.
func TestRenameKeepsWire(t *testing.T) {
	_, p := renameApp(t)
	read := func() map[string]string {
		db, e := store.Open(p.db())
		if e != nil {
			t.Fatal(e)
		}
		defer db.Close()
		out := map[string]string{}
		rows, _ := db.Query(`SELECT id,wire FROM amp_messages`)
		for rows.Next() {
			var id, w string
			rows.Scan(&id, &w)
			out[id] = w
		}
		rows.Close()
		return out
	}
	before := read()
	writePlan(t, p, "home", "milo-pc")
	if _, e := apply(t, p, nil); e != nil {
		t.Fatal(e)
	}
	if after := read(); len(before) < 3 || !reflect.DeepEqual(before, after) {
		t.Fatalf("wire changed:\n%v\n%v", before, after)
	}
}

// A crash before any step, or between any two, is finished by the next
// start: the result is the same as a rename that never crashed.
func TestRenameCrashAtEveryStep(t *testing.T) {
	_, clean := renameApp(t)
	writePlan(t, clean, "home", "milo-pc")
	if _, e := apply(t, clean, nil); e != nil {
		t.Fatal(e)
	}
	want := normalize(snapshot(t, clean), clean)
	if w := strings.Join(mapValues(want), "\n"); !strings.Contains(w, "ana@milo-pc.lolad.local milo-pc.lolad.local") || !strings.Contains(w, "tenant: milo-pc") || !strings.Contains(w, fed.RenameNotSent) {
		t.Fatal("reference state is not a finished rename:", w)
	}
	for _, s := range renameSteps {
		t.Run(s, func(t *testing.T) {
			_, p := renameApp(t)
			writePlan(t, p, "home", "milo-pc")
			if _, e := apply(t, p, func(at string) error {
				if at == s {
					return errCrash
				}
				return nil
			}); !errors.Is(e, errCrash) {
				t.Fatal("no crash at", s, e)
			}
			step, e := apply(t, p, nil)
			if e != nil || step == nil || step.Error != "" {
				t.Fatal(step, e)
			}
			diff(t, want, normalize(snapshot(t, p), p))
			if _, e := os.Stat(p.plan()); !os.IsNotExist(e) {
				t.Fatal("plan left behind")
			}
		})
	}
	// Crashing twice in a row, at two different steps, still finishes.
	_, p := renameApp(t)
	writePlan(t, p, "home", "milo-pc")
	for _, s := range []string{"identities", "org"} {
		if _, e := apply(t, p, func(at string) error {
			if at == s {
				return errCrash
			}
			return nil
		}); !errors.Is(e, errCrash) {
			t.Fatal(e)
		}
	}
	if _, e := apply(t, p, nil); e != nil {
		t.Fatal(e)
	}
	diff(t, want, normalize(snapshot(t, p), p))
}

// normalize drops what differs between two runs by construction: temp paths
// and per-install keys in identity files are reduced to their address lines.
func normalize(s map[string]string, p renamePaths) map[string]string {
	out := map[string]string{}
	for k, v := range s {
		if strings.HasPrefix(k, p.state) {
			rel, _ := filepath.Rel(p.state, k)
			var c struct{ Address, Provider string }
			json.Unmarshal([]byte(v), &c)
			out[rel] = c.Address + " " + c.Provider
			continue
		}
		if strings.Contains(k, "amp_messages") {
			v = scrubTimes(v)
		}
		out[k] = v
	}
	return out
}

// scrubTimes keeps only the address fields, delivery and wire of each mail
// row: ids, times and signatures differ between two installs.
func scrubTimes(rows string) string {
	var out []string
	for _, l := range strings.Split(rows, "\n") {
		i := strings.IndexByte(l, ' ')
		if i < 0 {
			continue
		}
		var m core.Mail
		dec := json.NewDecoder(strings.NewReader(l[i+1:]))
		if dec.Decode(&m) != nil {
			continue
		}
		rest := l[i+1+int(dec.InputOffset()):]
		if strings.Contains(rest, `"signed":"old"`) {
			out = append(out, m.From+" "+m.To+rest)
		} else {
			out = append(out, m.From+" "+m.To+" "+rest[strings.LastIndex(rest, "}")+1:])
		}
	}
	sort.Strings(out)
	return strings.Join(out, "\n")
}

// An error at any step before org.yaml is written puts everything back:
// lolad starts under the old name and the failure is recorded.
func TestRenameRollbackAtEveryStep(t *testing.T) {
	for _, s := range renameSteps[:5] {
		t.Run(s, func(t *testing.T) {
			_, p := renameApp(t)
			before := snapshot(t, p)
			writePlan(t, p, "home", "milo-pc")
			step, e := apply(t, p, func(at string) error {
				if at == s {
					return errors.New("disk full")
				}
				return nil
			})
			if e != nil || step == nil || !strings.Contains(step.Error, "disk full") {
				t.Fatal(step, e)
			}
			diff(t, before, snapshot(t, p))
			if l := readRenameStep(p.last()); l == nil || l.Error == "" || l.To != "milo-pc" {
				t.Fatal(l)
			}
			if _, e := os.Stat(p.plan()); !os.IsNotExist(e) {
				t.Fatal("plan left behind")
			}
		})
	}
}

// A real failure inside a step (org.yaml cannot be replaced) after the
// database and files were rewritten is undone too.
func TestRenameRollbackOnRealFailure(t *testing.T) {
	_, p := renameApp(t)
	before := snapshot(t, p)
	writePlan(t, p, "home", "milo-pc")
	step, e := apply(t, p, func(at string) error {
		if at == "org" {
			// org.yaml no longer parses, so SetTenant fails inside the step.
			os.WriteFile(p.org(), []byte("tenant: [broken\n"), 0o600)
		}
		return nil
	})
	if e != nil || step == nil || step.Error == "" {
		t.Fatal(step, e)
	}
	diff(t, before, snapshot(t, p))
}

// org.yaml already under the new name means the rename committed: only
// the cleanup is left, never a rollback.
func TestRenameRollsForwardAfterOrg(t *testing.T) {
	_, p := renameApp(t)
	writePlan(t, p, "home", "milo-pc")
	if _, e := apply(t, p, func(at string) error {
		if at == "finish" {
			return errCrash
		}
		return nil
	}); !errors.Is(e, errCrash) {
		t.Fatal(e)
	}
	mid := snapshot(t, p)
	step, e := apply(t, p, func(at string) error { return errors.New("ignored after commit") })
	if e != nil || step == nil || step.Error != "" {
		t.Fatal(step, e)
	}
	diff(t, mid, snapshot(t, p))
}

// A plan whose "from" no longer matches org.yaml is dropped untouched.
func TestRenameStalePlan(t *testing.T) {
	_, p := renameApp(t)
	before := snapshot(t, p)
	writePlan(t, p, "other", "milo-pc")
	step, e := apply(t, p, nil)
	if e != nil || step == nil || step.Error == "" {
		t.Fatal(step, e)
	}
	diff(t, before, snapshot(t, p))
}

// The request half, through a whole app: refusals, the outbox 409, the
// plan, the restart signal, and the next start under the new name.
func TestRenameLoladRequestAndRestart(t *testing.T) {
	config, p := renameApp(t)
	a, e := New(t.Context(), config)
	if e != nil {
		t.Fatal(e)
	}
	ctx := t.Context()
	for _, in := range []string{"", "  ", "home", "HOME", "-", "x"} {
		if _, e := a.RenameLolad(ctx, core.RenameRequest{Name: in}); !errors.Is(e, core.ErrInvalid) {
			t.Errorf("%q: %v", in, e)
		}
	}
	v, e := a.LoladName(ctx)
	if e != nil || v.Name != "home" || v.Outbox != 1 || v.OpenLinks != 1 || v.Pending != nil {
		t.Fatalf("%+v %v", v, e)
	}
	var ob OutboxError
	if _, e := a.RenameLolad(ctx, core.RenameRequest{Name: "Milo PC"}); !errors.As(e, &ob) || ob.N != 1 || !errors.Is(e, core.ErrConflict) {
		t.Fatal(e)
	}
	if c, _ := a.CheckLoladName(ctx, "Milo PC"); !c.OK || c.Name != "milo-pc" || !strings.Contains(c.Message, "ana@milo-pc.lolad.local") {
		t.Fatal(c)
	}
	if c, _ := a.CheckLoladName(ctx, "home"); c.OK || c.Message == "" {
		t.Fatal(c)
	}
	select {
	case <-a.RestartRequested():
		t.Fatal("restart before any rename")
	default:
	}
	res, e := a.RenameLolad(ctx, core.RenameRequest{Name: "Milo PC", Anyway: true})
	if e != nil || res.Name != "milo-pc" || !res.Restarting {
		t.Fatal(res, e)
	}
	select {
	case <-a.RestartRequested():
	case <-time.After(5 * time.Second):
		t.Fatal("no restart requested")
	}
	if _, e := a.RenameLolad(ctx, core.RenameRequest{Name: "other"}); !errors.Is(e, core.ErrConflict) {
		t.Fatal("second rename while one waits:", e)
	}
	if v, _ = a.LoladName(ctx); v.Pending == nil || v.Pending.To != "milo-pc" {
		t.Fatal(v.Pending)
	}
	a.Close()

	b, e := New(t.Context(), config)
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close()
	if v, _ = b.LoladName(ctx); v.Name != "milo-pc" || v.Pending != nil || v.Last == nil || v.Last.From != "home" || v.Outbox != 0 || v.OpenLinks != 0 {
		t.Fatalf("%+v", v)
	}
	if b.Info(ctx).Provider != "milo-pc.lolad.local" {
		t.Fatal(b.Info(ctx).Provider)
	}
	// Mail works under the new name, with bare names and full addresses,
	// and the old address is refused with a reason.
	maple := b.Registry.List()[0].Name
	if _, e = b.Provider.Send(ctx, "ana", core.Outgoing{To: maple + "@milo-pc.lolad.local", Subject: "after", Body: "x"}); e != nil {
		t.Fatal(e)
	}
	if _, e = b.Provider.Send(ctx, maple, core.Outgoing{To: "ana", Subject: "after", Body: "y"}); e != nil {
		t.Fatal(e)
	}
	inbox, _ := b.Provider.Traffic(ctx, 100, time.Time{})
	if len(inbox) != 5 {
		t.Fatal(len(inbox))
	}
	for _, m := range inbox {
		if strings.HasSuffix(m.From, "@home.lolad.local") || strings.HasSuffix(m.To, "@home.lolad.local") {
			t.Fatal("old address in", m.ID, m.From, m.To)
		}
	}
	// The agent's instructions carry the new address.
	ag := b.Registry.List()[0]
	if md, e := os.ReadFile(filepath.Join(ag.Dir, "CLAUDE.md")); e == nil && (!strings.Contains(string(md), "@milo-pc.lolad.local") || strings.Contains(string(md), "@home.lolad.local")) {
		t.Fatal(string(md))
	}
	_ = p
}

// sanity: the snapshot helper sees the fixtures (guards the tests above
// against comparing two empty states).
func TestRenameFixture(t *testing.T) {
	_, p := renameApp(t)
	s := snapshot(t, p)
	if !strings.Contains(s[`SELECT name||' '||address FROM amp_identities ORDER BY name`], "ana ana@home.lolad.local") || s[`SELECT id FROM fed_outbox ORDER BY id`] != "msg_remote_1" {
		t.Fatal(s)
	}
	var n int
	db, _ := sql.Open("sqlite", p.db())
	defer db.Close()
	db.QueryRow(`SELECT count(*) FROM amp_messages`).Scan(&n)
	if n != 3 {
		t.Fatal(n)
	}
}
