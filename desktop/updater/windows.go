package updater

import (
	"fmt"
	"strings"
)

// authenticode returns the signature status and signer subject of a file,
// using PowerShell's Get-AuthenticodeSignature.
func authenticode(run runner, file string) (status, subject string, err error) {
	script := `$s = Get-AuthenticodeSignature -LiteralPath $args[0]; ` +
		`$subj = ''; if ($s.SignerCertificate) { $subj = $s.SignerCertificate.Subject }; ` +
		`Write-Output ($s.Status.ToString() + '|' + $subj)`
	out, err := run("powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script, file)
	if err != nil {
		return "", "", fmt.Errorf("powershell: %w: %s", err, strings.TrimSpace(string(out)))
	}
	line := strings.TrimSpace(string(out))
	if i := strings.LastIndex(line, "\n"); i >= 0 {
		line = strings.TrimSpace(line[i+1:])
	}
	status, subject, _ = strings.Cut(line, "|")
	return status, subject, nil
}

// verifyWinInstaller checks the installer against the running executable.
// As on macOS, the installer's SHA-256 is pinned by the signed manifest. A
// signed running app requires a Valid Authenticode signature from the same
// signer. An unsigned running app (the unsigned beta) accepts an unsigned
// installer, or a signed one whose signature is Valid (a broken signature is
// still refused). AllowUnsigned (development) skips the checks for an
// unsigned running app.
func (u *Updater) verifyWinInstaller(installer, runningExe string) error {
	cs, csub, err := authenticode(u.cfg.run, runningExe)
	if err != nil {
		return err
	}
	unsigned := cs != "Valid" || csub == ""
	if unsigned && u.cfg.AllowUnsigned {
		return nil
	}
	ns, nsub, err := authenticode(u.cfg.run, installer)
	if err != nil {
		return err
	}
	if unsigned {
		if ns == "NotSigned" || ns == "Valid" {
			return nil
		}
		return fmt.Errorf("%w: status %s", ErrSignatureInvalid, ns)
	}
	if ns != "Valid" {
		return fmt.Errorf("%w: status %s", ErrSignatureInvalid, ns)
	}
	if nsub != csub {
		return fmt.Errorf("%w: signer %q, running app has %q", ErrSignatureInvalid, nsub, csub)
	}
	return nil
}

// applyWin verifies and runs the installer silently; start must detach it.
func (u *Updater) applyWin(installer, runningExe string, start func(path string, args ...string) error) error {
	if err := u.verifyWinInstaller(installer, runningExe); err != nil {
		return err
	}
	if err := start(installer, "/S"); err != nil {
		return fmt.Errorf("run installer: %w", err)
	}
	u.quit() // the installer stops and replaces the app and daemon
	return nil
}
