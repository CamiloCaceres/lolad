package main

import "testing"

func TestNeedsMove(t *testing.T) {
	ro := func(p string) bool { return p == "/Volumes/Lolad/Lolad.app" }
	home := "/Users/ada"
	for b, want := range map[string]bool{
		"":                                  false,
		"/Applications/Lolad.app":           false,
		"/Users/ada/Applications/Lolad.app": false,
		"/Users/ada/Downloads/Lolad.app":    true,
		"/Volumes/Lolad/Lolad.app":          true,
		"/private/var/folders/x/T/AppTranslocation/ABC/d/Lolad.app": true,
		"/Users/ada/src/lolad/desktop/dist/Lolad.app":               false,
		"/Applications-old/Lolad.app":                               false,
	} {
		if got := needsMove(b, home, ro); got != want {
			t.Errorf("needsMove(%q) = %v, want %v", b, got, want)
		}
	}
}
