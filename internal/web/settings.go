package web

import (
	"cmp"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/CamiloCaceres/lolad/internal/core"
)

// The agent settings area: /agents/{name}/settings/{tab}. Every tab is a
// server-rendered panel; its forms post back and get the panel again (htmx)
// or a redirect to it (plain forms). Secrets are write-only: no handler here
// ever puts an env value, or a password, into a response.

var settingsTabs = []settingsTab{
	{"runtime", "Model & runtime", "cpu"},
	{"skills", "Skills", "puzzle"},
	{"secrets", "Secrets", "key"},
	{"files", "Files", "file"},
	{"email", "Email", "mail"},
}

type settingsTab struct{ Key, Label, Icon string }

type settingsView struct {
	A     core.AgentStatus
	Tab   string
	Tabs  []settingsTab
	Flash string // what just happened
	Error string // why it didn't
	OOB   bool   // panel answers a post: also refresh the pending-restart banner

	// runtime
	Models []core.ModelOption
	Custom bool // the model isn't one of Models

	// skills
	Own     []core.Skill
	Library []skillGroup
	Inherit bool

	// secrets
	Env     []core.EnvVar
	EnvFile string
	NewKey  string // echoed after a refused add; the value never is

	// files
	Groups   []fileGroup
	File     *core.FileContent
	Draft    string // the text in the editor when it differs from File (conflict)
	Base     string // the hash the draft was based on, when that isn't File.Hash
	Conflict bool
	FileErr  string

	// email
	Email      core.EmailSettings
	Mod        bool
	EditAcct   string // address whose form is open after a refused save
	AcctDraft  *core.EmailAccount
	Providers  []provider
	WatchLolad bool
}

type skillGroup struct {
	Source, Label string
	Skills        []libSkill
}

type libSkill struct {
	core.Skill
	Added bool // the agent already has a skill with this name
}

type fileGroup struct {
	Key, Label string
	Files      []core.FileInfo
}

var fileGroupLabels = map[string]string{"instructions": "Instructions", "config": "Config", "brain": "Brain", "memory": "Memory", "skills": "Skills", "other": "Other"}

func (s *server) settingsData(r *http.Request, name, tab string) (settingsView, error) {
	ctx := r.Context()
	a, err := s.svc.Get(ctx, name)
	if err != nil {
		return settingsView{}, err
	}
	v := settingsView{A: a, Tab: tab, Tabs: settingsTabs}
	switch tab {
	case "runtime":
		v.Models = s.svc.Models(a.Runtime.Harness)
		v.Custom = a.Runtime.Model != "" && !slices.ContainsFunc(v.Models, func(m core.ModelOption) bool { return m.ID == a.Runtime.Model })
	case "skills":
		all, err := s.svc.Skills(ctx, name)
		if err != nil {
			return v, err
		}
		v.Inherit = a.Runtime.SkillsMode() == core.SkillsInherit
		have := map[string]bool{}
		for _, sk := range all {
			if sk.Source == "agent" {
				v.Own = append(v.Own, sk)
				have[sk.Name] = true
			}
		}
		for _, sk := range all {
			if sk.Source == "agent" {
				continue
			}
			i := slices.IndexFunc(v.Library, func(g skillGroup) bool { return g.Source == sk.Source })
			if i < 0 {
				label := "Your skills"
				if p, ok := strings.CutPrefix(sk.Source, "plugin:"); ok {
					label = "Plugin: " + p
				}
				v.Library = append(v.Library, skillGroup{Source: sk.Source, Label: label})
				i = len(v.Library) - 1
			}
			v.Library[i].Skills = append(v.Library[i].Skills, libSkill{Skill: sk, Added: have[sk.Name]})
		}
	case "secrets":
		if v.Env, err = s.svc.Env(ctx, name); err != nil {
			return v, err
		}
		v.EnvFile = cmp.Or(a.Runtime.EnvFile, ".env")
	case "files":
		files, err := s.svc.Files(ctx, name)
		if err != nil {
			return v, err
		}
		for _, f := range files {
			i := slices.IndexFunc(v.Groups, func(g fileGroup) bool { return g.Key == f.Group })
			if i < 0 {
				v.Groups = append(v.Groups, fileGroup{Key: f.Group, Label: cmp.Or(fileGroupLabels[f.Group], f.Group)})
				i = len(v.Groups) - 1
			}
			v.Groups[i].Files = append(v.Groups[i].Files, f)
		}
		if p := r.URL.Query().Get("path"); p != "" {
			fc, err := s.svc.ReadFile(ctx, name, p)
			if err != nil {
				v.FileErr = humanize(err, "")
				if errors.Is(err, core.ErrNotFound) {
					v.FileErr = p + " isn't one of the files lolad can edit."
				}
			} else {
				v.File = &fc
			}
		}
	case "email":
		if v.Email, err = s.svc.Email(ctx, name); err != nil {
			return v, err
		}
		v.Mod = v.Email.Watch == "mod"
		v.WatchLolad = v.Email.Watch == "lolad"
		v.Providers = providers
	}
	return v, nil
}

func (s *server) settings(w http.ResponseWriter, r *http.Request) {
	tab := cmp.Or(r.PathValue("tab"), "runtime")
	if !slices.ContainsFunc(settingsTabs, func(t settingsTab) bool { return t.Key == tab }) {
		s.notFound(w, r)
		return
	}
	v, err := s.settingsData(r, r.PathValue("name"), tab)
	if errors.Is(err, core.ErrNotFound) {
		s.notFound(w, r)
		return
	} else if err != nil {
		s.fail(w, r, err)
		return
	}
	s.page(w, r, http.StatusOK, "settings", layout{Title: v.A.Display + " settings", Nav: "agents", Class: "is-settings", Data: v})
}

// settled answers a settings post. On success: the panel again with a note
// (htmx) or a redirect to the tab. On a refused change: the panel with the
// reason, after set has put back what was typed.
func (s *server) settled(w http.ResponseWriter, r *http.Request, tab, flash string, err error, set func(*settingsView)) {
	name := r.PathValue("name")
	code := http.StatusOK
	if err != nil {
		code = statusFor(err)
		if code != http.StatusUnprocessableEntity && code != http.StatusConflict {
			s.fail(w, r, err)
			return
		}
	}
	if err == nil && !isHX(r) {
		to := "/agents/" + name + "/settings/" + tab
		if p := r.FormValue("path"); tab == "files" && p != "" {
			to += "?path=" + url.QueryEscape(p)
		}
		http.Redirect(w, r, to, http.StatusSeeOther)
		return
	}
	v, verr := s.settingsData(r, name, tab)
	if verr != nil {
		s.fail(w, r, verr)
		return
	}
	v.OOB = isHX(r)
	if err != nil {
		v.Error = humanize(err, "")
	} else {
		v.Flash = flash
		toast(w, "ok", flash)
	}
	if set != nil {
		set(&v)
	}
	if !isHX(r) {
		s.page(w, r, code, "settings", layout{Title: v.A.Display + " settings", Nav: "agents", Class: "is-settings", Data: v})
		return
	}
	s.fragment(w, code, "settings-"+tab, v)
}

// ---------------------------------------------------------------- runtime

func (s *server) saveRuntime(w http.ResponseWriter, r *http.Request) {
	ctx, name := r.Context(), r.PathValue("name")
	a, err := s.svc.Get(ctx, name)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	rt := a.Runtime
	rt.Model = field(r, "model")
	if rt.Model == "custom" {
		rt.Model = field(r, "model_custom")
	}
	rt.Permissions = core.Permissions(field(r, "permissions"))
	rt.OnWake = strings.TrimSpace(strings.ReplaceAll(r.FormValue("on_wake"), "\r\n", "\n"))
	rt.Rotate = ""
	if r.FormValue("rotate_on") == "on" {
		if rt.Rotate = field(r, "rotate"); rt.Rotate == "" {
			err = fmt.Errorf("%w: pick the time of the daily fresh start, or turn it off", core.ErrInvalid)
		}
	}
	rt.Autostart = defaultOn(r.FormValue("autostart") == "on")
	rt.WakeOnMessage = defaultOn(r.FormValue("wake_on_message") == "on")
	if err == nil {
		_, err = s.svc.UpdateRuntime(ctx, name, rt)
	}
	s.settled(w, r, "runtime", "Saved. Restart to apply.", err, func(v *settingsView) {
		if err != nil {
			v.A.Runtime = rt // show what was typed
			v.Custom = rt.Model != "" && !slices.ContainsFunc(v.Models, func(m core.ModelOption) bool { return m.ID == rt.Model })
		}
	})
}

// defaultOn stores a default-true switch: unset when on, false when off.
func defaultOn(on bool) *bool {
	if on {
		return nil
	}
	f := false
	return &f
}

// ---------------------------------------------------------------- skills

func (s *server) saveSkillsMode(w http.ResponseWriter, r *http.Request) {
	ctx, name := r.Context(), r.PathValue("name")
	a, err := s.svc.Get(ctx, name)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	rt := a.Runtime
	rt.Skills = core.SkillsMode(field(r, "mode"))
	_, err = s.svc.UpdateRuntime(ctx, name, rt)
	msg := "Inherits your skills from the next launch."
	if rt.Skills == core.SkillsBarebones {
		msg = "Barebones from the next launch: only its own skills."
	}
	s.settled(w, r, "skills", msg, err, nil)
}

func (s *server) addSkill(w http.ResponseWriter, r *http.Request) {
	sk, err := s.svc.AddSkill(r.Context(), r.PathValue("name"), field(r, "from"))
	s.settled(w, r, "skills", "Copied "+sk.Name+" into the agent.", err, nil)
}

func (s *server) removeSkill(w http.ResponseWriter, r *http.Request) {
	id := field(r, "id")
	err := s.svc.RemoveSkill(r.Context(), r.PathValue("name"), id)
	s.settled(w, r, "skills", "Removed "+strings.TrimPrefix(id, "agent:")+".", err, nil)
}

// ---------------------------------------------------------------- secrets

func (s *server) setSecret(w http.ResponseWriter, r *http.Request) {
	key := field(r, "key")
	value := r.FormValue("value") // as typed: a secret may have meaningful spaces
	var err error
	if key == "" {
		err = fmt.Errorf("%w: name the variable", core.ErrInvalid)
	} else if value == "" {
		err = fmt.Errorf("%w: type a value for %s (to remove it, use Delete)", core.ErrInvalid, key)
	} else {
		err = s.svc.SetEnv(r.Context(), r.PathValue("name"), key, value)
	}
	s.settled(w, r, "secrets", fmt.Sprintf("Saved %s (%s).", key, plural(len([]rune(value)), "character", "characters")), err, func(v *settingsView) {
		if err != nil && r.FormValue("replace") == "" {
			v.NewKey = key
		}
	})
}

func (s *server) deleteSecret(w http.ResponseWriter, r *http.Request) {
	key := field(r, "key")
	err := s.svc.DeleteEnv(r.Context(), r.PathValue("name"), key)
	s.settled(w, r, "secrets", "Deleted "+key+".", err, nil)
}

// ---------------------------------------------------------------- files

func (s *server) saveFile(w http.ResponseWriter, r *http.Request) {
	ctx, name := r.Context(), r.PathValue("name")
	p := field(r, "path")
	content := strings.ReplaceAll(r.FormValue("content"), "\r\n", "\n")
	base := cmp.Or(r.FormValue("force_base"), r.FormValue("base"))
	_, err := s.svc.WriteFile(ctx, name, p, content, base)
	// The panel re-reads the file from ?path=.
	r.URL.RawQuery = url.Values{"path": {p}}.Encode()
	s.settled(w, r, "files", "Saved "+p+".", err, func(v *settingsView) {
		if errors.Is(err, core.ErrConflict) {
			v.Conflict, v.Draft, v.Base, v.Error = true, content, base, ""
		} else if err != nil && v.File != nil {
			v.Draft = content
		}
	})
}

func (s *server) previewFile(w http.ResponseWriter, r *http.Request) {
	content := r.FormValue("content")
	s.fragment(w, http.StatusOK, "md-preview", markdown(content))
}

// ---------------------------------------------------------------- email

type provider struct{ Domain, IMAP, SMTP string }

// providers fill in servers from the address's domain. Only providers whose
// IMAP is TLS on 993 and SMTP TLS on 465 are listed.
var providers = []provider{
	{"gmail.com", "imap.gmail.com", "smtp.gmail.com"},
	{"googlemail.com", "imap.gmail.com", "smtp.gmail.com"},
	{"fastmail.com", "imap.fastmail.com", "smtp.fastmail.com"},
	{"migadu.com", "imap.migadu.com", "smtp.migadu.com"},
	{"zoho.com", "imap.zoho.com", "smtp.zoho.com"},
	{"yahoo.com", "imap.mail.yahoo.com", "smtp.mail.yahoo.com"},
}

func providerFor(address string) (provider, bool) {
	_, domain, _ := strings.Cut(strings.ToLower(address), "@")
	for _, p := range providers {
		if p.Domain == domain {
			return p, true
		}
	}
	return provider{}, false
}

// emailIn is EmailSettings ready to go back to SaveEmail: "mod" is detected,
// not something the dashboard sets.
func emailIn(e core.EmailSettings) core.EmailSettings {
	if e.Watch == "mod" {
		e.Watch = "none"
	}
	return e
}

func (s *server) saveAccount(w http.ResponseWriter, r *http.Request) {
	ctx, name := r.Context(), r.PathValue("name")
	cur, err := s.svc.Email(ctx, name)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	orig := field(r, "orig")
	acc := core.EmailAccount{Address: field(r, "address")}
	i := slices.IndexFunc(cur.Accounts, func(a core.EmailAccount) bool { return orig != "" && strings.EqualFold(a.Address, orig) })
	if i >= 0 {
		acc = cur.Accounts[i]
		acc.Address = field(r, "address")
	}
	acc.IMAPServer, acc.SMTPServer, acc.Username = field(r, "imap_server"), field(r, "smtp_server"), field(r, "username")
	if p, ok := providerFor(acc.Address); ok {
		acc.IMAPServer, acc.SMTPServer = cmp.Or(acc.IMAPServer, p.IMAP), cmp.Or(acc.SMTPServer, p.SMTP)
	}
	acc.IMAPPort, err = port(field(r, "imap_port"), 993, "IMAP")
	if err == nil {
		acc.SMTPPort, err = port(field(r, "smtp_port"), 465, "SMTP")
	}
	acc.Password = r.FormValue("password") // write-only; empty keeps the current one
	acc.PasswordSet = false
	in := emailIn(cur)
	in.Accounts = slices.Clone(cur.Accounts)
	if i >= 0 {
		in.Accounts[i] = acc
	} else {
		in.Accounts = append(in.Accounts, acc)
	}
	if err == nil {
		_, err = s.svc.SaveEmail(ctx, name, in)
	}
	verb := "Added "
	if i >= 0 {
		verb = "Saved "
	}
	s.settled(w, r, "email", verb+acc.Address+".", err, func(v *settingsView) {
		if err != nil {
			acc.Password = "" // never echoed
			v.EditAcct, v.AcctDraft = cmp.Or(orig, "new"), &acc
		}
	})
}

func port(s string, def int, what string) (int, error) {
	if s == "" {
		return def, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > 65535 {
		return 0, fmt.Errorf("%w: %s port must be a number from 1 to 65535", core.ErrInvalid, what)
	}
	return n, nil
}

func (s *server) deleteAccount(w http.ResponseWriter, r *http.Request) {
	ctx, name := r.Context(), r.PathValue("name")
	cur, err := s.svc.Email(ctx, name)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	addr := field(r, "address")
	in := emailIn(cur)
	in.Accounts = slices.DeleteFunc(slices.Clone(cur.Accounts), func(a core.EmailAccount) bool { return strings.EqualFold(a.Address, addr) })
	if strings.EqualFold(in.WatchAccount, addr) && in.Watch == "lolad" {
		in.Watch, in.WatchAccount = "none", ""
	}
	_, err = s.svc.SaveEmail(ctx, name, in)
	s.settled(w, r, "email", "Removed "+addr+". Its password stays in .env until you delete it.", err, nil)
}

func (s *server) saveEmailGeneral(w http.ResponseWriter, r *http.Request) {
	ctx, name := r.Context(), r.PathValue("name")
	cur, err := s.svc.Email(ctx, name)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	in := emailIn(cur)
	in.OperatorEmails = strings.FieldsFunc(r.FormValue("operators"), func(r rune) bool { return r == '\n' || r == ',' || r == ' ' || r == '\r' || r == '\t' })
	in.Watch = "none"
	if field(r, "watch") == "lolad" {
		in.Watch, in.WatchAccount, in.Every = "lolad", field(r, "watch_account"), field(r, "every")
	} else {
		in.WatchAccount, in.Every = "", ""
	}
	_, err = s.svc.SaveEmail(ctx, name, in)
	s.settled(w, r, "email", "Saved email settings.", err, nil)
}

func (s *server) testEmail(w http.ResponseWriter, r *http.Request) {
	addr := field(r, "address")
	res, err := s.svc.TestEmail(r.Context(), r.PathValue("name"), addr)
	if err != nil {
		res = core.EmailTest{IMAP: "not tried", SMTP: "not tried", Detail: humanize(err, "")}
	}
	s.fragment(w, http.StatusOK, "email-test", res)
}
