package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/CamiloCaceres/lolad/internal/registry"
)

func TestTmuxProbeAgentReportsNonzeroExit(t *testing.T) {
	var out bytes.Buffer
	code := tmuxProbeAgent(strings.NewReader("probe\nexit\nnever\n"), &out)
	if code != 7 || !strings.Contains(out.String(), "LOLAD_PROBE_READY") || !strings.Contains(out.String(), "ECHO:probe") || strings.Contains(out.String(), "never") {
		t.Fatalf("code=%d output=%q", code, out.String())
	}
}
func TestDoctorTmuxNamesMissingBinary(t *testing.T) {
	var out bytes.Buffer
	doctorTmux(context.Background(), registry.Config{TmuxBin: "lolad-v4-windows-missing-binary", TmuxSocket: "never-touch-this-server"}, &out)
	if !strings.Contains(out.String(), "FIX  tmux conformance binary:") {
		t.Fatalf("output=%q", out.String())
	}
}
