// Package create scaffolds and adopts lolabot instances. Cross-package services
// are injected through narrow interfaces; callers wire registration and reloads.
package create

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/CamiloCaceres/lolad/internal/core"
	"gopkg.in/yaml.v3"
)

type RuntimeWriter interface {
	WriteRuntime(dir string, runtime core.Runtime) error
}
type IdentityEnsurer interface {
	EnsureIdentity(context.Context, string, string) (core.Identity, error)
}
type OrgUpdater interface {
	Org(context.Context) (core.Org, error)
	SaveOrg(context.Context, core.Org) error
}
type ContactsLookup interface{ Contacts(string) []string }
type MailSender interface {
	Send(context.Context, string, core.Outgoing) (core.Mail, error)
}
type Starter interface {
	Start(context.Context, string) error
}

// Registrar reloads discovery before identity creation and optional Start.
type Registrar interface{ Reload(context.Context) error }

type Deps struct {
	TemplateDir, AgentsDir string
	Runtime                RuntimeWriter
	Identity               IdentityEnsurer
	Org                    OrgUpdater
	Contacts               ContactsLookup
	Mail                   MailSender
	Starter                Starter
	Registrar              Registrar
	// SetupEnv optionally replaces the setup process environment (useful offline).
	SetupEnv []string
}
type Creator struct{ deps Deps }

func New(deps Deps) *Creator { return &Creator{deps: deps} }

var validSlug = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
var separators = regexp.MustCompile(`[^a-z0-9]+`)

func Slug(name string) string {
	return strings.Trim(separators.ReplaceAllString(strings.ToLower(name), "-"), "-")
}
func validateSlug(slug string) error {
	if !validSlug.MatchString(slug) {
		return fmt.Errorf("%w: slug must match %s", core.ErrInvalid, validSlug)
	}
	return nil
}
func runtimeDefaults(r core.Runtime) (core.Runtime, error) {
	if r.Harness != core.HarnessClaude && r.Harness != core.HarnessCodewhale {
		return r, fmt.Errorf("%w: unsupported harness %q", core.ErrInvalid, r.Harness)
	}
	if r.Permissions == "" {
		r.Permissions = core.PermBypass
	}
	if r.Permissions != core.PermBypass && r.Permissions != core.PermDefault {
		return r, fmt.Errorf("%w: unsupported permissions", core.ErrInvalid)
	}
	if r.OnWake == "" {
		r.OnWake = "Read CLAUDE.md, check lolad msg inbox, and continue your mission. Reply only when needed."
	}
	return r, nil
}
func (c *Creator) ready() error {
	if c.deps.Runtime == nil || c.deps.Identity == nil || c.deps.Org == nil || c.deps.Contacts == nil {
		return fmt.Errorf("%w: runtime, identity, org and contacts dependencies are required", core.ErrInvalid)
	}
	return nil
}
func (c *Creator) Create(ctx context.Context, req core.CreateRequest) (core.AgentStatus, error) {
	var zero core.AgentStatus
	if err := c.ready(); err != nil {
		return zero, err
	}
	if strings.TrimSpace(req.Name) == "" {
		return zero, fmt.Errorf("%w: name is required", core.ErrInvalid)
	}
	slug := req.Slug
	if slug == "" {
		slug = Slug(req.Name)
	}
	if err := validateSlug(slug); err != nil {
		return zero, err
	}
	dir := req.Dir
	if dir == "" {
		if c.deps.AgentsDir == "" {
			return zero, fmt.Errorf("%w: agents directory required", core.ErrInvalid)
		}
		dir = filepath.Join(c.deps.AgentsDir, slug)
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return zero, err
	}
	r, err := runtimeDefaults(core.Runtime{Slug: slug, Harness: req.Harness, Model: req.Model, Permissions: req.Permissions, OnWake: req.OnWake})
	if err != nil {
		return zero, err
	}
	org, err := c.deps.Org.Org(ctx)
	if err != nil {
		return zero, err
	}
	if err = validateTeam(org, req.Team, req.Head); err != nil {
		return zero, err
	}
	if req.Mission != "" && c.deps.Mail == nil || req.Start && c.deps.Starter == nil {
		return zero, fmt.Errorf("%w: mission requires mail and start requires starter", core.ErrInvalid)
	}
	if req.Role == "" {
		req.Role = "Personal Assistant"
	}
	if req.User == "" {
		req.User = org.User.Display
	}
	if req.User == "" {
		req.User = org.User.Name
	}
	// setup.sh interpolates these values into sed and YAML. Refuse metacharacters
	// rather than letting a display field corrupt generated files.
	for _, value := range []string{req.Name, req.Role, req.User, dir} {
		if strings.ContainsAny(value, "\n\r\"\\|&") {
			return zero, fmt.Errorf("%w: setup fields contain unsupported template characters", core.ErrInvalid)
		}
	}
	entries, err := os.ReadDir(dir)
	if err == nil && len(entries) > 0 {
		return zero, fmt.Errorf("%w: target directory is not empty", core.ErrExists)
	}
	if err != nil && !os.IsNotExist(err) {
		return zero, err
	}
	if err = os.MkdirAll(dir, 0755); err != nil {
		return zero, err
	}
	if err = c.setup(ctx, req.Name, req.Role, req.User, dir); err != nil {
		return zero, err
	}
	a := core.Agent{Name: slug, Display: req.Name, Role: req.Role, Dir: dir, Runtime: r}
	return c.finish(ctx, a, req.Team, req.Head, req.Mission, req.Start)
}
func validateTeam(org core.Org, team string, head bool) error {
	if head && team == "" {
		return fmt.Errorf("%w: head requires team", core.ErrInvalid)
	}
	if team != "" {
		for _, t := range org.Teams {
			if t.ID == team {
				if head && t.Head != "" {
					return fmt.Errorf("%w: team already has a head", core.ErrExists)
				}
				return nil
			}
		}
		return fmt.Errorf("%w: team %s", core.ErrNotFound, team)
	}
	return nil
}
func (c *Creator) Adopt(ctx context.Context, req core.AdoptRequest) (core.AgentStatus, error) {
	var zero core.AgentStatus
	if err := c.ready(); err != nil {
		return zero, err
	}
	if req.Dir == "" {
		return zero, fmt.Errorf("%w: directory is required", core.ErrInvalid)
	}
	dir, err := filepath.Abs(req.Dir)
	if err != nil {
		return zero, err
	}
	r, err := runtimeDefaults(req.Runtime)
	if err != nil {
		return zero, err
	}
	data, err := os.ReadFile(filepath.Join(dir, "lolabot.yaml"))
	if err != nil {
		return zero, err
	}
	var doc struct {
		Agent   struct{ Name, Role string }
		Runtime core.Runtime `yaml:"runtime"`
	}
	if err = yaml.Unmarshal(data, &doc); err != nil {
		return zero, err
	}
	// An explicit request overrides an existing stable slug; otherwise retain
	// the runtime identity before deriving one from the display name or path.
	slug := req.Slug
	if slug == "" {
		slug = r.Slug
	}
	if slug == "" {
		slug = doc.Runtime.Slug
	}
	if slug == "" {
		slug = Slug(doc.Agent.Name)
	}
	if slug == "" {
		slug = Slug(filepath.Base(dir))
	}
	if err = validateSlug(slug); err != nil {
		return zero, err
	}
	if slug != filepath.Base(dir) || req.Slug != "" || r.Slug != "" || doc.Runtime.Slug != "" {
		r.Slug = slug
	}
	if doc.Agent.Name == "" {
		doc.Agent.Name = slug
	}
	if _, err = os.ReadFile(filepath.Join(dir, "CLAUDE.md")); err != nil {
		return zero, err
	}
	return c.finish(ctx, core.Agent{Name: slug, Display: doc.Agent.Name, Role: doc.Agent.Role, Dir: dir, Runtime: r}, "", false, "", false)
}
func (c *Creator) finish(ctx context.Context, a core.Agent, team string, head bool, mission string, start bool) (core.AgentStatus, error) {
	status := core.AgentStatus{Agent: a, State: core.StateStopped, Desired: "down", Team: team, IsHead: head}
	fail := func(stage string, err error) (core.AgentStatus, error) {
		return status, fmt.Errorf("%s (files retained in %s; retry with adopt): %w", stage, a.Dir, err)
	}
	if err := c.deps.Runtime.WriteRuntime(a.Dir, a.Runtime); err != nil {
		return fail("runtime", err)
	}
	if err := writeHarness(a); err != nil {
		return fail("harness", err)
	}
	if c.deps.Registrar != nil {
		if err := c.deps.Registrar.Reload(ctx); err != nil {
			return fail("reload", err)
		}
	}
	id, err := c.deps.Identity.EnsureIdentity(ctx, a.Name, "agent")
	if err != nil {
		return fail("identity", err)
	}
	status.Address = id.Address
	if team != "" {
		org, err := c.deps.Org.Org(ctx)
		if err != nil {
			return fail("org", err)
		}
		if err = validateTeam(org, team, head); err != nil {
			return fail("org", err)
		}
		for i := range org.Teams {
			if org.Teams[i].ID == team {
				if head {
					org.Teams[i].Head = a.Name
				} else {
					org.Teams[i].Members = append(org.Teams[i].Members, a.Name)
				}
			}
		}
		if err = c.deps.Org.SaveOrg(ctx, org); err != nil {
			return fail("org", err)
		}
	}
	if err = c.RegenerateSection(a); err != nil {
		return fail("instructions", err)
	}
	if mission != "" {
		org, err := c.deps.Org.Org(ctx)
		if err != nil {
			return fail("mission", err)
		}
		if _, err = c.deps.Mail.Send(ctx, org.User.Name, core.Outgoing{To: a.Name, Subject: "Mission", Body: mission, Type: "task"}); err != nil {
			return fail("mission", err)
		}
	}
	if start {
		if err = c.deps.Starter.Start(ctx, a.Name); err != nil {
			return fail("start", err)
		}
		status.Desired = "up"
		status.State = core.StateStarting
	}
	return status, nil
}

// Run setup with closed stdin. On macOS the upstream script needs two GNU
// command spellings translated to BSD equivalents; the template is never edited.
func (c *Creator) setup(ctx context.Context, name, role, user, dir string) error {
	script := filepath.Join(c.deps.TemplateDir, "setup.sh")
	if c.deps.TemplateDir == "" {
		return fmt.Errorf("%w: lolabot template required", core.ErrInvalid)
	}
	cmd := exec.CommandContext(ctx, "/bin/bash", script, "--name", name, "--role", role, "--user", user, "--yes", dir)
	env := c.deps.SetupEnv
	if env == nil {
		env = os.Environ()
	}
	env, cleanup, err := setupCompatibility(env)
	if err != nil {
		return err
	}
	defer cleanup()
	cmd.Env = env
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("setup.sh: %w\n%s", err, output)
	}
	return nil
}
