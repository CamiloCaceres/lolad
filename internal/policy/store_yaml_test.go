package policy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadNamesFileAndLineOnYAMLError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "org.yaml")
	os.WriteFile(path, []byte("tenant: home\nuser:\n  name: a\n bad: [\n"), 0o600)
	_, err := Load(path)
	if err == nil || !strings.HasPrefix(err.Error(), path+", line ") || strings.Contains(err.Error(), "yaml:") {
		t.Fatalf("got %v", err)
	}
}
