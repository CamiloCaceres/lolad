package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// needsMove reports whether the app bundle runs from a place macOS does not
// keep: a translocated copy (opened from Downloads or the disk image without
// moving it), the disk image itself, or the Downloads folder. A login service
// registered from there would point at a path that is gone after a reboot.
func needsMove(bundle, home string, readOnly func(string) bool) bool {
	if bundle == "" {
		return false
	}
	if strings.Contains(bundle, "/AppTranslocation/") {
		return true
	}
	under := func(dir string) bool { return strings.HasPrefix(bundle, strings.TrimSuffix(dir, "/")+"/") }
	if under("/Applications") || under(filepath.Join(home, "Applications")) {
		return false
	}
	if under(filepath.Join(home, "Downloads")) {
		return true
	}
	return readOnly(bundle)
}

// currentBundle is the running .app, or "" outside a bundle.
func currentBundle() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return bundlePath(exe)
}

// moveTarget picks /Applications, or ~/Applications when the user cannot
// write to it (a standard account).
func moveTarget(home string) (string, error) {
	if canWrite("/Applications") {
		return "/Applications", nil
	}
	dir := filepath.Join(home, "Applications")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

func canWrite(dir string) bool {
	f, err := os.CreateTemp(dir, ".lolad-write-check-*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return true
}

// copyToApplications copies src into the Applications folder, replacing (to
// the Trash) an older copy there, and returns the new bundle's path.
func copyToApplications(src, home string) (string, error) {
	dir, err := moveTarget(home)
	if err != nil {
		return "", err
	}
	dest := filepath.Join(dir, "Lolad.app")
	if dest == src {
		return dest, nil
	}
	if _, err := os.Stat(dest); err == nil {
		if err := moveToTrash(dest); err != nil {
			return "", fmt.Errorf("could not move the older Lolad in %s to the Trash: %w", dir, err)
		}
	}
	if out, err := exec.Command("/usr/bin/ditto", src, dest).CombinedOutput(); err != nil {
		return "", fmt.Errorf("copying Lolad failed: %v %s", err, strings.TrimSpace(string(out)))
	}
	// The user already chose to open this app; the copy is the same app.
	_ = exec.Command("/usr/bin/xattr", "-dr", "com.apple.quarantine", dest).Run()
	return dest, nil
}

// offerMove asks once per launch to move Lolad to Applications. While the
// question is open the app does not start the daemon from the temporary
// location.
func (s *shell) offerMove(bundle string) {
	application.InvokeAsync(func() {
		d := s.app.Dialog.Question().SetTitle("Move Lolad to Applications?").SetMessage(
			"Lolad is running from a temporary place (the download or the disk image).\n\n" +
				"To keep your assistants running after your Mac restarts, Lolad needs to live in your Applications folder. Lolad will copy itself there and reopen.")
		move := d.AddButton("Move to Applications")
		move.OnClick(func() { go s.doMove(bundle) })
		later := d.AddButton("Not now")
		later.OnClick(func() { s.moveDone() })
		d.SetDefaultButton(move).SetCancelButton(later)
		d.Show()
	})
}

func (s *shell) moveDone() {
	s.mu.Lock()
	s.movePending = false
	s.mu.Unlock()
	go s.firstLaunchStart()
}

func (s *shell) doMove(bundle string) {
	home, _ := os.UserHomeDir()
	dest, err := copyToApplications(bundle, home)
	if err != nil {
		s.fail("Could not move Lolad", fmt.Errorf("%v\n\nDrag Lolad into the Applications folder yourself, then open it from there.", err))
		s.moveDone()
		return
	}
	// Reopen from the new place once this copy has quit (single instance).
	cmd := exec.Command("/bin/sh", "-c", `sleep 1; /usr/bin/open "$0"`, dest)
	cmd.Env = cleanEnv()
	detach(cmd)
	if err := cmd.Start(); err != nil {
		s.fail("Lolad was copied but could not reopen", fmt.Errorf("%v\n\nOpen Lolad from your Applications folder.", err))
	} else {
		_ = cmd.Process.Release()
	}
	application.InvokeAsync(func() { s.app.Quit() })
}
