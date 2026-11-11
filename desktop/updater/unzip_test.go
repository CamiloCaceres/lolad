package updater

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func unzipTo(t *testing.T, ents []zent) (string, error) {
	t.Helper()
	tmp := t.TempDir()
	z := writeTmp(t, tmp, "a.zip", buildZip(t, ents))
	dest := filepath.Join(tmp, "out")
	os.Mkdir(dest, 0o755)
	return dest, safeUnzip(z, dest)
}

func TestUnzipOK(t *testing.T) {
	dest, err := unzipTo(t, []zent{
		{"A.app/Contents/MacOS/x", "bin", 0o755},
		{"A.app/Contents/Frameworks/F.framework/Versions/A/lib", "lib", 0o644},
		{"A.app/Contents/Frameworks/F.framework/Versions/Current", "A", fs.ModeSymlink | 0o755},
		{"A.app/Contents/Frameworks/F.framework/lib", "Versions/Current/lib", fs.ModeSymlink | 0o755},
	})
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(dest, "A.app/Contents/MacOS/x"))
	if err != nil || st.Mode().Perm()&0o100 == 0 {
		t.Fatalf("exec bit lost: %v %v", st, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dest, "A.app/Contents/Frameworks/F.framework/lib")); string(b) != "lib" {
		t.Fatalf("relative symlink broken: %q", b)
	}
}

func TestUnzipRejects(t *testing.T) {
	bad := map[string][]zent{
		"traversal":      {{"../evil", "x", 0o644}},
		"nested ..":      {{"a/../../evil", "x", 0o644}},
		"absolute":       {{"/etc/evil", "x", 0o644}},
		"backslash":      {{"a\\..\\evil", "x", 0o644}},
		"drive":          {{"C:evil", "x", 0o644}},
		"abs symlink":    {{"l", "/etc", fs.ModeSymlink | 0o755}},
		"escape symlink": {{"l", "../..", fs.ModeSymlink | 0o755}},
		"deep escape":    {{"a/b/l", "../../../x", fs.ModeSymlink | 0o755}},
		"write via link": {{"l", ".", fs.ModeSymlink | 0o755}, {"l/../../evil", "x", 0o644}},
		"chain escape": {
			{"a", ".", fs.ModeSymlink | 0o755},
			{"s", "a/..", fs.ModeSymlink | 0o755},
		},
		"device": {{"dev", "", fs.ModeDevice | 0o644}},
	}
	for name, ents := range bad {
		dest, err := unzipTo(t, ents)
		if err == nil {
			t.Errorf("%s: accepted", name)
		}
		if _, e := os.Stat(filepath.Join(filepath.Dir(dest), "evil")); e == nil {
			t.Errorf("%s: wrote outside the destination", name)
		}
	}
}

func TestUnzipWriteThroughSymlinkDir(t *testing.T) {
	outside := t.TempDir()
	tmp := t.TempDir()
	dest := filepath.Join(tmp, "out")
	os.Mkdir(dest, 0o755)
	// pre-existing symlink in dest pointing out (as if left by an earlier entry)
	os.Symlink(outside, filepath.Join(dest, "d"))
	z := writeTmp(t, tmp, "a.zip", buildZip(t, []zent{{"d/evil", "x", 0o644}}))
	if err := safeUnzip(z, dest); err == nil {
		t.Fatal("wrote through a symlinked directory")
	}
	if _, err := os.Stat(filepath.Join(outside, "evil")); err == nil {
		t.Fatal("file landed outside")
	}
}
