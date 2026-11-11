package create

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/CamiloCaceres/lolad/internal/core"
)

// BundleEnv overrides the bundled lolabot directory (read-only source).
const BundleEnv = "LOLAD_LOLABOT_BUNDLE"

// TemplateConfig is what ResolveTemplate needs from the daemon configuration.
type TemplateConfig struct {
	// Configured is the user's lolabot_template. A directory that holds
	// CLAUDE.TEMPLATE.md wins. Do NOT pass a built-in default here: an absent or
	// empty directory simply falls through to the bundled copy.
	Configured string
	// StateDir is the lolad state directory; the bundle is copied to
	// <StateDir>/lolabot/<version>/ on first use.
	StateDir string
	// Version overrides the version directory name (default: the bundle's VERSION
	// file, else a short digest of its CLAUDE.TEMPLATE.md and setup.sh).
	Version string
	// Executable and Getenv are test seams (default os.Executable / os.Getenv).
	Executable func() (string, error)
	Getenv     func(string) string
}

// Template is a resolved lolabot template.
type Template struct {
	Dir string
	// UserSet is true when Dir came from the user's lolabot_template. Only such
	// a template may fall back to running its own setup.sh.
	UserSet bool
}

func hasTemplate(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, "CLAUDE.TEMPLATE.md"))
	return err == nil && info.Mode().IsRegular()
}

// BundleCandidates lists where a bundled lolabot may live, in priority order.
func BundleCandidates(cfg TemplateConfig) []string {
	getenv, exe := cfg.Getenv, cfg.Executable
	if getenv == nil {
		getenv = os.Getenv
	}
	if exe == nil {
		exe = os.Executable
	}
	var out []string
	if v := getenv(BundleEnv); v != "" {
		out = append(out, v)
	}
	if p, err := exe(); err == nil {
		if real, err := filepath.EvalSymlinks(p); err == nil {
			p = real
		}
		d := filepath.Dir(p)
		out = append(out,
			filepath.Join(d, "..", "Resources", "lolabot"), // Lolad.app/Contents/MacOS -> Contents/Resources
			filepath.Join(d, "..", "lolabot"),
			filepath.Join(d, "lolabot"), // Windows install dir
		)
	}
	return out
}

// ResolveTemplate returns the lolabot template to scaffold from: the
// user-set lolabot_template, else <state>/lolabot/<version>/ (copied from the
// bundle on first use), else a clear error. The bundle is never written to.
func ResolveTemplate(cfg TemplateConfig) (Template, error) {
	if cfg.Configured != "" && hasTemplate(cfg.Configured) {
		return Template{Dir: cfg.Configured, UserSet: true}, nil
	}
	var bundle string
	for _, c := range BundleCandidates(cfg) {
		if hasTemplate(c) {
			bundle = filepath.Clean(c)
			break
		}
	}
	if cfg.StateDir == "" {
		return Template{}, fmt.Errorf("%w: no state directory to hold the lolabot template", core.ErrInvalid)
	}
	version := cfg.Version
	if version == "" && bundle != "" {
		version = bundleVersion(bundle)
	}
	if version != "" {
		if strings.ContainsAny(version, `/\`) || version == "." || version == ".." {
			return Template{}, fmt.Errorf("%w: invalid lolabot version %q", core.ErrInvalid, version)
		}
		dest := filepath.Join(cfg.StateDir, "lolabot", version)
		if hasTemplate(dest) {
			return Template{Dir: dest}, nil
		}
		if bundle != "" {
			if err := installBundle(bundle, dest); err != nil {
				return Template{}, fmt.Errorf("copy bundled lolabot from %s to %s: %w", bundle, dest, err)
			}
			return Template{Dir: dest}, nil
		}
	}
	// A bundle-less install may still hold a previously copied version.
	if cfg.Version == "" {
		if dirs, _ := filepath.Glob(filepath.Join(cfg.StateDir, "lolabot", "*")); len(dirs) > 0 {
			for i := len(dirs) - 1; i >= 0; i-- {
				if hasTemplate(dirs[i]) {
					return Template{Dir: dirs[i]}, nil
				}
			}
		}
	}
	msg := "no lolabot template found: "
	if cfg.Configured != "" {
		msg += fmt.Sprintf("lolabot_template %q has no CLAUDE.TEMPLATE.md; ", cfg.Configured)
	} else {
		msg += "lolabot_template is not set; "
	}
	msg += fmt.Sprintf("no bundled lolabot (set %s, or install it next to the executable as ../Resources/lolabot, ../lolabot or lolabot) and no copy under %s",
		BundleEnv, filepath.Join(cfg.StateDir, "lolabot"))
	return Template{}, fmt.Errorf("%w: %s", core.ErrInvalid, msg)
}

func bundleVersion(bundle string) string {
	if data, err := os.ReadFile(filepath.Join(bundle, "VERSION")); err == nil {
		if v := strings.TrimSpace(string(data)); v != "" && !strings.ContainsAny(v, "/\\\r\n") && v != "." && v != ".." {
			return v
		}
	}
	h := sha256.New()
	for _, f := range []string{"CLAUDE.TEMPLATE.md", "setup.sh"} {
		if data, err := os.ReadFile(filepath.Join(bundle, f)); err == nil {
			h.Write([]byte(f))
			h.Write(data)
		}
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}

// installBundle copies src into dest through a sibling temp directory and an
// atomic rename, so a crash never leaves a half template that hasTemplate would
// accept. Concurrent first uses are safe: the loser's rename fails and it
// accepts the winner's directory.
func installBundle(src, dest string) error {
	parent := filepath.Dir(dest)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(parent, ".install-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	err = filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == ".venv" || d.Name() == "node_modules") {
			return filepath.SkipDir
		}
		target := filepath.Join(tmp, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o755)
		case d.Type()&os.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			if os.Symlink(link, target) == nil {
				return nil
			}
			return copyFileNoClobber(path, target, true)
		case d.Type().IsRegular():
			return copyFileNoClobber(path, target, false)
		}
		return nil
	})
	if err != nil {
		return err
	}
	_ = os.Chmod(tmp, 0o755)
	if err = os.Rename(tmp, dest); err != nil {
		if hasTemplate(dest) {
			return nil
		}
		return err
	}
	return nil
}
