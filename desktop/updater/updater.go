// Package updater checks for, downloads, verifies and applies Lolad desktop
// updates (docs/desktop.md §9). It has no dependency on the UI.
package updater

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

var (
	ErrOnboarding       = errors.New("onboarding is in progress; not updating now")
	ErrTestKey          = errors.New("this build still trusts the public test update key")
	ErrNoPlatform       = errors.New("the update has no build for this platform")
	ErrChecksum         = errors.New("downloaded update does not match the manifest")
	ErrManualInstall    = errors.New("this version is too old to update in place; install the new version manually")
	ErrRunningUnsigned  = errors.New("the running app is not signed; refusing to update (set AllowUnsigned for development)")
	ErrSignatureInvalid = errors.New("the update's code signature is not valid or not from the same publisher")
)

// Config configures an Updater. Only CurrentVersion is required.
type Config struct {
	CurrentVersion string
	ManifestURL    string            // default DefaultManifestURL
	PublicKey      ed25519.PublicKey // default: the compiled-in PublicKeyB64
	Platform       string            // default "<GOOS>-<GOARCH>"
	HTTPClient     *http.Client
	TempDir        string // downloads go in a fresh dir under this (default os.TempDir())

	// OnboardingFlag is the file whose existence means onboarding is in
	// progress. Default: DefaultOnboardingFlag().
	OnboardingFlag string
	// MultiplexerVersionFile holds the installed tmux/psmux version.
	// Default: DefaultMultiplexerFile().
	MultiplexerVersionFile string

	// AppPath is the running bundle (macOS: .../Lolad.app) or executable.
	// Default: derived from os.Executable.
	AppPath string
	// AllowUnsigned is a development switch: with an ad-hoc signed running
	// app it skips the code-signature checks, and it permits the test key.
	AllowUnsigned bool
	// Quit ends the app after an update has been handed off. Required for
	// Apply to finish the restart.
	Quit func()

	// run executes external commands (codesign, launchctl, open,
	// powershell); tests replace it.
	run runner
}

// Updater is safe for concurrent use of Check; Download/Apply should not run
// concurrently with each other.
type Updater struct {
	cfg Config
	pub ed25519.PublicKey
}

// CheckResult describes what Check found.
type CheckResult struct {
	Available         bool   // a newer, verified version exists for this platform
	Version           string // the new version
	NotesURL          string
	NeedsAgentRestart bool   // multiplexer_version differs: agents restart during the update
	ManualInstall     bool   // current < min_version: cannot update in place
	OnboardingBlocked bool   // onboarding flag present: do not install now
	Message           string // user-facing text
	Artifact          Artifact
	Manifest          *Manifest
}

// New validates cfg and returns an Updater.
func New(cfg Config) (*Updater, error) {
	if _, _, ok := parseVersion(cfg.CurrentVersion); !ok {
		return nil, fmt.Errorf("current version %q is not a version", cfg.CurrentVersion)
	}
	if cfg.ManifestURL == "" {
		cfg.ManifestURL = DefaultManifestURL
	}
	if err := ValidateURL(cfg.ManifestURL); err != nil {
		return nil, err
	}
	if cfg.Platform == "" {
		cfg.Platform = runtime.GOOS + "-" + runtime.GOARCH
	}
	if cfg.OnboardingFlag == "" {
		cfg.OnboardingFlag = DefaultOnboardingFlag()
	}
	if cfg.MultiplexerVersionFile == "" {
		cfg.MultiplexerVersionFile = DefaultMultiplexerFile()
	}
	if cfg.TempDir == "" {
		cfg.TempDir = os.TempDir()
	}
	if cfg.run == nil {
		cfg.run = execRunner
	}
	pub := cfg.PublicKey
	if pub == nil {
		if strings.TrimSpace(PublicKeyB64) == testKeyB64 && !cfg.AllowUnsigned {
			return nil, ErrTestKey
		}
		var err error
		if pub, err = publicKeyFromB64(PublicKeyB64); err != nil {
			return nil, err
		}
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 0}
	}
	// Redirects (GitHub release assets) must stay on allowed schemes.
	hc := *cfg.HTTPClient
	prev := hc.CheckRedirect
	hc.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		if err := ValidateURL(req.URL.String()); err != nil {
			return err
		}
		if prev != nil {
			return prev(req, via)
		}
		return nil
	}
	cfg.HTTPClient = &hc
	return &Updater{cfg: cfg, pub: pub}, nil
}

// OnboardingInProgress reports whether the flag file exists.
func (u *Updater) OnboardingInProgress() bool {
	_, err := os.Stat(u.cfg.OnboardingFlag)
	return err == nil
}

// Check fetches and verifies the manifest. A manifest that is unsigned,
// mismatched or older than the installed version returns an error (callers
// should treat it as "no update"). A manifest equal to the installed version
// returns Available=false and no error.
func (u *Updater) Check(ctx context.Context) (*CheckResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.cfg.ManifestURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := u.cfg.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("update manifest: HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	m, err := ParseAndVerify(raw, u.pub)
	if err != nil {
		return nil, err
	}
	cmp, _ := compareVersions(m.Version, u.cfg.CurrentVersion)
	if cmp < 0 {
		return nil, fmt.Errorf("%w (manifest %s, installed %s)", ErrDowngrade, m.Version, u.cfg.CurrentVersion)
	}
	res := &CheckResult{Version: m.Version, NotesURL: m.NotesURL, Manifest: m}
	if cmp == 0 {
		res.Message = "Lolad is up to date."
		return res, nil
	}
	art, ok := m.Platforms[u.cfg.Platform]
	if !ok {
		return nil, fmt.Errorf("%w (%s)", ErrNoPlatform, u.cfg.Platform)
	}
	if err := ValidateURL(art.URL); err != nil {
		return nil, err
	}
	if len(art.SHA256) != 64 || art.Size <= 0 {
		return nil, errors.New("manifest artifact needs a sha256 and a size")
	}
	res.Available = true
	res.Artifact = art
	res.OnboardingBlocked = u.OnboardingInProgress()
	res.NeedsAgentRestart = m.MultiplexerVersion != "" && m.MultiplexerVersion != u.InstalledMultiplexerVersion()
	if m.MinVersion != "" {
		if c, ok := compareVersions(u.cfg.CurrentVersion, m.MinVersion); ok && c < 0 {
			res.ManualInstall = true
		}
	}
	res.Message = fmt.Sprintf("Lolad %s is available.", m.Version)
	switch {
	case res.ManualInstall:
		res.Message += " This version is too old to update in place; please download the new installer."
	case res.NeedsAgentRestart:
		res.Message += " Your agents will restart during the update."
	default:
		res.Message += " Your agents keep running."
	}
	return res, nil
}

// InstalledMultiplexerVersion reads multiplexer.version ("" if unreadable).
func (u *Updater) InstalledMultiplexerVersion() string {
	b, err := os.ReadFile(u.cfg.MultiplexerVersionFile)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// Download streams the artifact into a fresh temp directory, verifies size
// and SHA-256, and returns the file path. progress may be nil. It refuses
// while onboarding is in progress. The caller removes filepath.Dir(path)
// when done (Apply does so on success).
func (u *Updater) Download(ctx context.Context, res *CheckResult, progress func(done, total int64)) (string, error) {
	if res == nil || !res.Available {
		return "", errors.New("no update to download")
	}
	if res.ManualInstall {
		return "", ErrManualInstall
	}
	if u.OnboardingInProgress() {
		return "", ErrOnboarding
	}
	art := res.Artifact
	if err := ValidateURL(art.URL); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, art.URL, nil)
	if err != nil {
		return "", err
	}
	resp, err := u.cfg.HTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download: HTTP %d", resp.StatusCode)
	}
	dir, err := os.MkdirTemp(u.cfg.TempDir, "lolad-update-")
	if err != nil {
		return "", err
	}
	fail := func(e error) (string, error) { os.RemoveAll(dir); return "", e }
	name := filepath.Base(strings.SplitN(art.URL, "?", 2)[0])
	if name == "" || name == "." || name == "/" {
		name = "update.bin"
	}
	path := filepath.Join(dir, name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return fail(err)
	}
	h := sha256.New()
	var done int64
	buf := make([]byte, 64<<10)
	src := io.LimitReader(resp.Body, art.Size+1)
	for {
		n, rerr := src.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				f.Close()
				return fail(werr)
			}
			h.Write(buf[:n])
			done += int64(n)
			if progress != nil {
				progress(done, art.Size)
			}
			if done > art.Size {
				f.Close()
				return fail(fmt.Errorf("%w: larger than the declared %d bytes", ErrChecksum, art.Size))
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			f.Close()
			return fail(rerr)
		}
	}
	if err := f.Close(); err != nil {
		return fail(err)
	}
	if done != art.Size {
		return fail(fmt.Errorf("%w: got %d bytes, want %d", ErrChecksum, done, art.Size))
	}
	if !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), art.SHA256) {
		return fail(fmt.Errorf("%w: sha256 mismatch", ErrChecksum))
	}
	return path, nil
}

// Apply installs a downloaded artifact and restarts the app (platform
// specific). It re-checks the onboarding flag first.
func (u *Updater) Apply(path string) error {
	if u.OnboardingInProgress() {
		return ErrOnboarding
	}
	return u.platformApply(path)
}

// Install is Download followed by Apply.
func (u *Updater) Install(ctx context.Context, res *CheckResult, progress func(done, total int64)) error {
	p, err := u.Download(ctx, res, progress)
	if err != nil {
		return err
	}
	return u.Apply(p)
}

func (u *Updater) quit() {
	if u.cfg.Quit != nil {
		u.cfg.Quit()
	}
}
