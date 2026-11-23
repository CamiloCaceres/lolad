package cli

import (
	"path/filepath"
	"testing"
)

func TestTemporaryLocation(t *testing.T) {
	if !temporaryLocation("/private/var/folders/ab/T/AppTranslocation/1234/d/Lolad.app/Contents/Helpers/lolad") {
		t.Error("translocated app not refused")
	}
	if temporaryLocation("/Applications/Lolad.app/Contents/Helpers/lolad") {
		t.Error("/Applications refused")
	}
	if temporaryLocation(filepath.Join(t.TempDir(), "lolad")) {
		t.Error("writable dir refused")
	}
}
