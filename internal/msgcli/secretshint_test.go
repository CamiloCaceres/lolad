package msgcli

import (
	"strings"
	"testing"

	"github.com/CamiloCaceres/lolad/internal/core"
)

// Windows Codewhale agents get a PowerShell way to load the secrets file;
// everyone else the spec's POSIX one.
func TestSecretsLoadHint(t *testing.T) {
	if got := secretsLoadHint("linux"); got != "load with: set -a; . ./.user-channel/secrets.env; set +a" {
		t.Fatalf("posix: %q", got)
	}
	win := secretsLoadHint("windows")
	for _, want := range []string{"PowerShell", `Get-Content .user-channel\secrets.env`, `Set-Item "env:$($Matches[1])"`, `-replace "'\\''", "'"`} {
		if !strings.Contains(win, want) {
			t.Fatalf("windows hint lacks %q: %s", want, win)
		}
	}
	if strings.Contains(win, "set -a") {
		t.Fatal("POSIX form on Windows")
	}
	old := secretsGOOS
	secretsGOOS = "windows"
	defer func() { secretsGOOS = old }()
	lines := answerLines(umAnswer{Ask: "r1", Question: "Key?", Secrets: []string{"OPENAI_API_KEY"}}, umRequest{Fields: []core.AskField{{Name: "OPENAI_API_KEY", Type: core.FieldSecret}}}, secretsFile)
	if !strings.Contains(strings.Join(lines, "\n"), "OPENAI_API_KEY saved (load in PowerShell with:") {
		t.Fatalf("%q", lines)
	}
}
