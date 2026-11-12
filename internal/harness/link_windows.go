package harness

import (
	"os/exec"

	"github.com/CamiloCaceres/lolad/internal/winproc"
)

// linkDirFallback creates a directory junction when symlinks are not allowed
// (no Developer Mode or admin). Junctions need no privilege. Note os.Readlink
// reports a junction's target, so the "already linked" check above still works.
func linkDirFallback(source, target string, _ error) error {
	return winproc.Hide(exec.Command("cmd", "/c", "mklink", "/J", target, source)).Run()
}
