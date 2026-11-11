package setup

import (
	"os"
	"path/filepath"
	"strings"
)

// DefaultAgentsDirName is the folder created in the home directory.
const DefaultAgentsDirName = "Lolad Agents"

// DefaultAgentsDir is where agents live unless the user picks another place.
func DefaultAgentsDir(home string) string { return filepath.Join(home, DefaultAgentsDirName) }

// ExpandDir turns "~", "~/x" and relative-to-home input into an absolute path.
func ExpandDir(home, dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return ""
	}
	if dir == "~" {
		return home
	}
	if strings.HasPrefix(dir, "~/") || strings.HasPrefix(dir, `~\`) {
		return filepath.Join(home, dir[2:])
	}
	return filepath.Clean(dir)
}

// protectedMac lists the macOS locations whose access prompts (TCC) in the
// name of the app that started the process, which for a background agent
// nobody is there to answer.
func protectedMac(home string) (dirs []string) {
	for _, d := range []string{"Desktop", "Documents", "Downloads", "Library/Mobile Documents", "Library/CloudStorage"} {
		dirs = append(dirs, filepath.Join(home, d))
	}
	return append(dirs, "/Volumes")
}

// AgentsDirWarning says why dir is a poor home for background agents on goos,
// or returns "". Only macOS has such places.
func AgentsDirWarning(goos, home, dir string) string {
	if goos != "darwin" || dir == "" {
		return ""
	}
	dir = filepath.Clean(dir)
	for _, p := range protectedMac(home) {
		if dir == p || strings.HasPrefix(dir, p+string(filepath.Separator)) {
			name := filepath.Base(p)
			switch name {
			case "Mobile Documents":
				name = "iCloud Drive"
			case "CloudStorage":
				name = "a cloud-synced folder"
			case "Volumes":
				name = "an external or network drive"
			default:
				name = "your " + name + " folder"
			}
			return "This is inside " + name + ". macOS asks permission before apps there can be used, and agents run in the background where nobody can click Allow, so they would stall. " +
				"Choose a folder in your home folder such as " + DefaultAgentsDir(home) + ", or give Lolad access in System Settings > Privacy & Security > Files and Folders (or Full Disk Access)."
		}
	}
	return ""
}

// osHome is the user's home directory, "" when unknown.
func osHome() string { h, _ := os.UserHomeDir(); return h }
