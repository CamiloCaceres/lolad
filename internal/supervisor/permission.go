package supervisor

import (
	"errors"
	"io/fs"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/CamiloCaceres/lolad/internal/core"
)

// permissionRetry is how often an agent blocked by macOS privacy controls is
// tried again. It does not count as a crash: once the user allows access the
// agent is back within seconds, and no crash budget is spent meanwhile.
const permissionRetry = 15 * time.Second

// ProblemMacOSPermission is the kind of the problem below.
const ProblemMacOSPermission = "macos_permission"

// permissionGOOS is runtime.GOOS; tests set it.
var permissionGOOS = runtime.GOOS

// Where the refused path is in a message: Go and shells say
// "open /path: operation not permitted", Node says "EPERM: operation not
// permitted, open '/path'". Failing both, any /Volumes/<name> names the drive.
var notPermittedPath = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(?:open|stat|lstat|chdir|read|readdirent|cd:?|mkdir|lchown|fchmodat|scandir)\s+"?(/[^":\n]+?)"?\s*:\s*operation not permitted`),
	regexp.MustCompile(`(?i)operation not permitted,\s*\w+\s+'(/[^'\n]+)'`),
	regexp.MustCompile(`(/Volumes/[^/'"\n]+)`),
}

// classifyPermission recognizes macOS refusing a file to the daemon or its
// agents: "operation not permitted" (EPERM) on the agent folder, its .env or
// its settings. On macOS that is the privacy controls (TCC: removable
// volumes, Documents, Desktop, ...), most often after a reboot or an update
// took the permission away; plain Unix permissions say "permission denied"
// (EACCES) instead. err may be nil when only text is known (what a dead pane
// printed). Other systems get no special handling.
func classifyPermission(err error, text string, now time.Time) (core.AgentProblem, bool) {
	if permissionGOOS != "darwin" {
		return core.AgentProblem{}, false
	}
	path := ""
	var pe *fs.PathError
	switch {
	case err != nil && errors.Is(err, syscall.EPERM):
		if errors.As(err, &pe) {
			path = pe.Path
		}
	case strings.Contains(strings.ToLower(text+" "+errText(err)), "operation not permitted"):
	default:
		return core.AgentProblem{}, false
	}
	detail := strings.TrimSpace(text)
	if detail == "" {
		detail = errText(err)
	}
	for _, re := range notPermittedPath {
		if path != "" {
			break
		}
		if m := re.FindStringSubmatch(detail); m != nil {
			path = strings.TrimSpace(m[1])
		}
	}
	place, setting := protectedPlace(path)
	return core.AgentProblem{
		Kind:     ProblemMacOSPermission,
		Title:    "macOS is blocking access to " + place,
		Hint:     "If macOS is asking whether Lolad may use " + place + ", click Allow. Otherwise open System Settings → Privacy & Security → Files & Folders, find lolad and turn on " + setting + " (or add lolad under Full Disk Access). It starts again by itself within seconds.",
		Detail:   detail,
		NeedsYou: true,
		Since:    now,
		Until:    now.Add(permissionRetry),
	}, true
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// protectedPlace names what macOS protects at path, in quotes, and the
// Files & Folders switch for it.
func protectedPlace(path string) (place, setting string) {
	p := filepath.ToSlash(path)
	if rest, ok := strings.CutPrefix(p, "/Volumes/"); ok {
		if v, _, _ := strings.Cut(rest, "/"); v != "" {
			return "“" + v + "”", "Removable Volumes"
		}
	}
	for _, f := range []struct{ dir, setting string }{
		{"/Documents", "Documents Folder"}, {"/Desktop", "Desktop Folder"}, {"/Downloads", "Downloads Folder"},
		{"/Library/Mobile Documents", "iCloud Drive"},
	} {
		if strings.HasPrefix(p, "/Users/") {
			parts := strings.SplitN(strings.TrimPrefix(p, "/Users/"), "/", 2)
			if len(parts) == 2 && strings.HasPrefix("/"+parts[1]+"/", f.dir+"/") {
				return "your " + strings.TrimPrefix(f.dir, "/") + " folder", f.setting
			}
		}
	}
	if path == "" {
		return "its folder", "the folder or drive it lives on"
	}
	return "“" + filepath.Base(path) + "”", "the folder or drive it lives on"
}

// blockedByMacOS reports an agent waiting for a macOS permission.
func blockedByMacOS(r *record) bool {
	return r.Status.Problem != nil && r.Status.Problem.Kind == ProblemMacOSPermission
}

// blockedOnUser reports an agent that cannot launch until the user does
// something (allow a folder, install its harness). It has no session on
// purpose, so its vanished session is not a crash.
func blockedOnUser(r *record) bool {
	return r.Status.Problem != nil && (r.Status.Problem.Kind == ProblemMacOSPermission || r.Status.Problem.Kind == ProblemHarnessMissing)
}

// block records p instead of a crash: the agent waits for the user
// (needs_you) and launches again at p.Until. Crashes and restarts are not
// counted.
func (s *Supervisor) block(r *record, now time.Time, p core.AgentProblem) {
	r.Status.LastError = p.Detail
	r.Status.NextRestart = p.Until
	s.setProblem(r, p)
}
