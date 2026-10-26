package deliver

import (
	"os"
	"testing"

	"github.com/CamiloCaceres/lolad/internal/tmux"
)

// The real-tmux tests launch through `<this test binary> exec-env`.
func TestMain(m *testing.M) {
	tmux.ServeExecEnv()
	os.Exit(m.Run())
}
