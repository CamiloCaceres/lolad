package updater

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// LaunchdLabel is the agent that runs the daemon.
const LaunchdLabel = "dev.caceres.lolad"

type codesignInfo struct {
	TeamID string
	AdHoc  bool
	Signed bool
}

var teamRe = regexp.MustCompile(`(?m)^TeamIdentifier=(.*)$`)

// inspectSignature reads a bundle's signature with `codesign -dv`.
func inspectSignature(run runner, app string) codesignInfo {
	out, err := run("/usr/bin/codesign", "-dv", "--verbose=4", app)
	s := string(out)
	if err != nil && !strings.Contains(s, "Identifier=") {
		return codesignInfo{} // not signed at all
	}
	info := codesignInfo{Signed: true}
	if strings.Contains(s, "Signature=adhoc") {
		info.AdHoc = true
	}
	if m := teamRe.FindStringSubmatch(s); m != nil {
		t := strings.TrimSpace(m[1])
		if t != "" && t != "not set" {
			info.TeamID = t
		}
	}
	if info.TeamID == "" {
		info.AdHoc = true // no team: ad-hoc or unsigned for our purposes
	}
	return info
}

// verifyMacUpdate checks the new bundle against the running one. With
// AllowUnsigned and an ad-hoc running app, both checks are skipped.
func (u *Updater) verifyMacUpdate(newApp, runningApp string) error {
	run := u.cfg.run
	cur := inspectSignature(run, runningApp)
	if cur.AdHoc || !cur.Signed {
		if u.cfg.AllowUnsigned {
			return nil
		}
		return ErrRunningUnsigned
	}
	if out, err := run("/usr/bin/codesign", "--verify", "--deep", "--strict", newApp); err != nil {
		return fmt.Errorf("%w: codesign: %s", ErrSignatureInvalid, strings.TrimSpace(string(out)))
	}
	nw := inspectSignature(run, newApp)
	if nw.TeamID == "" || nw.TeamID != cur.TeamID {
		return fmt.Errorf("%w: team id %q, running app has %q", ErrSignatureInvalid, nw.TeamID, cur.TeamID)
	}
	return nil
}

// findApp returns the single top-level *.app inside dir.
func findApp(dir string) (string, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var found string
	for _, e := range ents {
		if e.IsDir() && strings.HasSuffix(e.Name(), ".app") {
			if found != "" {
				return "", errors.New("update contains more than one app")
			}
			found = filepath.Join(dir, e.Name())
		}
	}
	if found == "" {
		return "", errors.New("update contains no .app bundle")
	}
	return found, nil
}

// swapBundle replaces target with newApp: the old bundle is renamed aside,
// the new one moved in, then the old one deleted. If moving the new bundle
// in fails, the old one is put back. newApp and target must be on one volume.
func swapBundle(newApp, target string) error {
	aside := fmt.Sprintf("%s.old-%d", target, os.Getpid())
	_ = os.RemoveAll(aside)
	if err := os.Rename(target, aside); err != nil {
		return fmt.Errorf("move the old app aside: %w", err)
	}
	if err := os.Rename(newApp, target); err != nil {
		if rerr := os.Rename(aside, target); rerr != nil {
			return fmt.Errorf("install failed (%v) and rollback failed: %w", err, rerr)
		}
		return fmt.Errorf("install the new app: %w", err)
	}
	_ = os.RemoveAll(aside) // best effort; a leftover is harmless
	return nil
}

// applyMac is the whole macOS flow. exec hands off the relaunch.
func (u *Updater) applyMac(zipPath, target string, uid int, relaunch func(app string) error) error {
	target = filepath.Clean(target)
	parent := filepath.Dir(target)
	staging, err := os.MkdirTemp(parent, ".lolad-update-")
	if err != nil {
		return fmt.Errorf("cannot stage the update next to %s: %w", target, err)
	}
	defer os.RemoveAll(staging)
	if err := safeUnzip(zipPath, staging); err != nil {
		return fmt.Errorf("unpack update: %w", err)
	}
	newApp, err := findApp(staging)
	if err != nil {
		return err
	}
	if err := u.verifyMacUpdate(newApp, target); err != nil {
		return err
	}
	if err := swapBundle(newApp, target); err != nil {
		return err
	}
	os.RemoveAll(filepath.Dir(zipPath)) // downloaded artifact
	// The daemon runs from the bundle; restart it so it is the new one.
	// A failure here is not fatal: the app relaunch still proceeds.
	_, _ = u.cfg.run("/bin/launchctl", "kickstart", "-k", fmt.Sprintf("gui/%d/%s", uid, LaunchdLabel))
	if err := relaunch(target); err != nil {
		return fmt.Errorf("updated, but relaunch failed: %w", err)
	}
	u.quit()
	return nil
}
