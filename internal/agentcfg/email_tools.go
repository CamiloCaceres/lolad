package agentcfg

import (
	"bytes"
	_ "embed"
	"errors"
	"os"
	"strings"

	"github.com/CamiloCaceres/lolad/internal/core"
	"gopkg.in/yaml.v3"
)

// The email tools lolad installs into a lolabot agent (see NOTES.md, "Email
// serialization and tool upgrades"). A file is replaced only when its content
// is a version lolad knows: a lolabot release or an earlier lolad asset.

//go:embed assets/email.sh
var emailScript []byte

//go:embed assets/email-aim-secret.sh
var stockEmailScript []byte

//go:embed assets/env_secret.py
var envScript []byte

//go:embed assets/email_client.py
var emailClientScript []byte

//go:embed assets/config.py
var emailConfigScript []byte

//go:embed assets/attachment_guard.py
var attachmentGuardScript []byte

//go:embed assets/email-send.sh
var emailSendScript []byte

// stockEmail reports the aim-secret wrapper (AI Maestro's vault) that lolad
// has always upgraded to read .env.
func stockEmail(b []byte) bool {
	return bytes.Equal(bytes.TrimSpace(b), bytes.TrimSpace(stockEmailScript))
}

// contentHash identifies a known file version, ignoring outer whitespace.
func contentHash(b []byte) string { return hash(bytes.TrimSpace(b)) }

type emailTool struct {
	rel   string
	asset []byte
	mode  os.FileMode
	// stock: lolabot template releases (tools/ in the lolabot repository).
	// shipped: earlier versions of this lolad asset. Append the old hash here
	// whenever the asset changes, so agents that have it are brought up to date.
	stock, shipped []string
}

var (
	toolConfig = emailTool{rel: "tools/config.py", asset: emailConfigScript, mode: 0644,
		stock: []string{"cf346050df358931528bcc73e1c0925ea62bf256765eeac0e34f6969ad2557ec"}}
	toolGuard  = emailTool{rel: "tools/attachment_guard.py", asset: attachmentGuardScript, mode: 0644}
	toolSecret = emailTool{rel: "tools/env_secret.py", asset: envScript, mode: 0755}
	toolClient = emailTool{rel: "tools/email_client.py", asset: emailClientScript, mode: 0644,
		stock: []string{
			"9da67792b080f1e4d83f3b93f3f6ca5c76c7fe6150c9c90c8766f543da263f3b",
			"52086610239f34c1575411fb73f1073682150e0c8d3c3ca358552f7c58877366",
			"37bb37373cf9c0c54bc7a346731ebd64b2bdbaca9b7b55cb3ade764d81458663",
		}}
	toolWrapper = emailTool{rel: "tools/email.sh", asset: emailScript, mode: 0755,
		stock: []string{
			"0b9bf4f56d3b3fd9de878971c7f7f3f5300db74222c1217ee8ba1dae9a38bd03",
			"d954bbf8a067e837aaffa02146ed8f0eb5ea6392ad9454e2fec0064e2eab4875",
		},
		shipped: []string{
			"d05a775b5d529ed8d5dd6d3ceadc86d003da2415932cdf61aa9719325d58035b",
			"d4bc92b457a1a3421441b5655eef72cfc46a1b25480cd41031266df005fccf68",
		}}
	toolSend = emailTool{rel: "tools/email-send.sh", asset: emailSendScript, mode: 0755,
		stock: []string{"57bb639d5b9057d00c0ca3fa86f7da8a96f76ad67d391dc81e3d495b999cf06c"}}
)

// emailTools is the installed set, in write order: email.sh last among the
// files it needs, so an interrupted upgrade is simply planned again.
var emailTools = []emailTool{toolConfig, toolGuard, toolSecret, toolClient, toolWrapper, toolSend}

type toolState int

const (
	toolMissing toolState = iota
	toolCurrent
	toolStock   // a lolabot template release
	toolShipped // an earlier lolad asset
	toolCustom  // unknown content: never overwritten
)

func (t emailTool) state(b []byte, exists bool) toolState {
	if !exists {
		return toolMissing
	}
	h := contentHash(b)
	switch {
	case h == contentHash(t.asset):
		return toolCurrent
	case contains(t.stock, h):
		return toolStock
	case contains(t.shipped, h):
		return toolShipped
	}
	return toolCustom
}
func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

const (
	planNone     = ""
	planTemplate = "template"   // lolabot's plain tool set: install the full lolad set
	planAim      = "aim-secret" // the aim-secret wrapper: replace email.sh and env_secret.py
	planRefresh  = "lolad"      // lolad's set, with files from an earlier lolad version
)

type emailPlan struct {
	kind    string
	writes  []emailTool
	blocked string   // an upgrade is due but a file has local changes; nothing is written
	notes   []string // files left alone that do not block the upgrade
}

func (d *Dir) readTool(rel string) ([]byte, bool, error) {
	p, e := d.safe(rel, false)
	if errors.Is(e, core.ErrNotFound) {
		return nil, false, nil
	}
	if e != nil {
		return nil, false, e
	}
	b, e := os.ReadFile(p)
	if errors.Is(e, os.ErrNotExist) {
		return nil, false, nil
	}
	if e != nil {
		return nil, false, errors.New("cannot read email tool")
	}
	return b, true, nil
}

// planEmail decides, from exact file contents, what upgradeEmail would write.
func (d *Dir) planEmail() (emailPlan, error) {
	var p emailPlan
	states := map[string]toolState{}
	for _, t := range emailTools {
		b, ok, e := d.readTool(t.rel)
		if e != nil {
			return p, e
		}
		states[t.rel] = t.state(b, ok)
		if t.rel == toolWrapper.rel && ok && stockEmail(b) {
			p.kind = planAim
		}
	}
	wrapper, client := states[toolWrapper.rel], states[toolClient.rel]
	if wrapper == toolMissing {
		return emailPlan{}, nil
	}
	switch {
	case p.kind == planAim:
	case wrapper == toolCustom:
		p.blocked = "tools/email.sh has local changes, so lolad leaves the email tools as they are; it must read each account's password_secret from the env file"
		return p, nil
	case wrapper == toolStock || client == toolStock:
		p.kind = planTemplate
	default:
		p.kind = planRefresh
	}
	if p.kind == planTemplate {
		if _, ok, e := d.readTool("tools/email_sanitizer.py"); e != nil {
			return p, e
		} else if !ok {
			p.blocked = "tools/email.sh is lolabot's template version, which cannot read passwords from the env file, but tools/email_sanitizer.py is missing, so lolad cannot update the email tools"
			return p, nil
		}
	}
	for _, t := range emailTools {
		s := states[t.rel]
		if s == toolCurrent {
			continue
		}
		write := false
		switch {
		case t.rel == toolSecret.rel:
			// lolad's own helper for email.sh: restored whenever it differs (backup kept).
			write = true
		case p.kind == planAim:
			write = t.rel == toolWrapper.rel
		case p.kind == planRefresh:
			write = s == toolShipped // keep the rest: only earlier lolad versions are refreshed
		case s == toolStock || s == toolShipped:
			write = true
		case t.rel == toolSend.rel: // optional: never added, and local changes do not block
			if s == toolCustom {
				p.notes = append(p.notes, "tools/email-send.sh has local changes; lolad left it as is")
			}
		case s == toolMissing:
			write = true
		default:
			p.blocked = t.rel + " has local changes, so lolad leaves the email tools as they are (tools/email.sh is lolabot's template version, which cannot read passwords from the env file)"
			p.writes = nil
			return p, nil
		}
		if write {
			p.writes = append(p.writes, t)
		}
	}
	if len(p.writes) == 0 {
		p.kind = planNone
	}
	return p, nil
}

// note describes a pending plan for EmailSettings.Notes.
func (p emailPlan) note() string {
	switch p.kind {
	case planAim:
		return "email.sh uses aim-secret; lolad will update it to read .env"
	case planTemplate:
		return "tools/email.sh is lolabot's template version, which cannot read passwords from .env; lolad will install its email tools (" + p.files() + "), keeping the originals as .lolad-orig"
	case planRefresh:
		return "lolad will update its email tools (" + p.files() + ") to the current version"
	}
	return ""
}
func (p emailPlan) files() string {
	var names []string
	for _, t := range p.writes {
		names = append(names, strings.TrimPrefix(t.rel, "tools/"))
	}
	return strings.Join(names, ", ")
}

// upgradeEmail installs lolad's email tools when the agent has a version lolad
// knows (see planEmail). Every replaced file is first saved as .lolad-orig,
// unless such a backup already exists. It returns notes on what it did.
func (d *Dir) upgradeEmail() ([]string, error) {
	p, e := d.planEmail()
	if e != nil || p.blocked != "" {
		return nil, e
	}
	done, e := d.applyEmail(p)
	if len(done) == 0 {
		return nil, e
	}
	return append([]string{upgradedNote(done)}, p.notes...), e
}
func (d *Dir) applyEmail(p emailPlan) ([]string, error) {
	var done []string
	for _, t := range p.writes {
		path, e := d.safe(t.rel, true)
		if e != nil {
			return done, e
		}
		old, e := os.ReadFile(path)
		if e == nil {
			backup, e := d.safe(t.rel+".lolad-orig", true)
			if e != nil {
				return done, e
			}
			if _, e = os.Lstat(backup); errors.Is(e, os.ErrNotExist) {
				st, e := os.Stat(path)
				if e != nil {
					return done, errors.New("cannot read email tool")
				}
				if e = atomicWrite(backup, old, st.Mode()); e != nil {
					return done, e
				}
			} else if e != nil {
				return done, errors.New("cannot check email tool backup")
			}
		} else if !errors.Is(e, os.ErrNotExist) {
			return done, errors.New("cannot read email tool")
		}
		if e = atomicWrite(path, t.asset, t.mode); e != nil {
			return done, e
		}
		done = append(done, t.rel)
	}
	return done, nil
}
func upgradedNote(files []string) string {
	return "Updated " + strings.Join(files, ", ") + " to read passwords from .env; originals kept as .lolad-orig"
}

// emailSecretsInEnv reports whether the agent's email logins name their
// passwords (password_secret) and every one of them is set in the env file:
// then lolad's email.sh can log in, and upgrading a known tool version is safe
// to do without being asked.
func (d *Dir) emailSecretsInEnv(envFile string) bool {
	_, n, e := d.readYAML("brain/credentials.yaml")
	if e != nil {
		return false
	}
	acc := nodeGet(n, "email_accounts")
	if acc == nil || acc.Kind != yaml.MappingNode {
		return false
	}
	lines, e := d.envValues(envFile)
	if e != nil {
		return false
	}
	env := map[string]string{}
	for _, l := range lines {
		env[l.key] = l.value
	}
	found := false
	for i := 0; i+1 < len(acc.Content); i += 2 {
		var key string
		decode(nodeGet(acc.Content[i+1], "password_secret"), &key)
		if key == "" {
			continue
		}
		if env[key] == "" {
			return false
		}
		found = true
	}
	return found
}
