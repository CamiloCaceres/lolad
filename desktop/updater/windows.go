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

// verifyWinInstaller requires a valid Authenticode signature whose signer
// matches the running executable's, unless the running one is unsigned and
// AllowUnsigned is set.
func (u *Updater) verifyWinInstaller(installer, runningExe string) error {
	cs, csub, err := authenticode(u.cfg.run, runningExe)
	if err != nil {
		return err
	}
	if cs != "Valid" || csub == "" {
		if u.cfg.AllowUnsigned {
			return nil
		}
		return ErrRunningUnsigned
	}
	ns, nsub, err := authenticode(u.cfg.run, installer)
	if err != nil {
		return err
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
