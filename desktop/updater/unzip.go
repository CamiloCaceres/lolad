package updater

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const (
	maxZipEntries = 200000
	maxZipBytes   = 4 << 30
)

// safeUnzip extracts zipPath into dest (which must exist) refusing path
// traversal, absolute names, special files, and symlinks that point outside
// dest. Symlinks inside dest are kept: macOS frameworks need them.
func safeUnzip(zipPath, dest string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer zr.Close()
	if len(zr.File) > maxZipEntries {
		return errors.New("zip has too many entries")
	}
	root, err := filepath.EvalSymlinks(dest)
	if err != nil {
		return err
	}
	var total int64
	for _, f := range zr.File {
		name := f.Name
		if name == "" || strings.ContainsAny(name, "\x00\\") || strings.HasPrefix(name, "/") || (len(name) > 1 && name[1] == ':') {
			return fmt.Errorf("unsafe zip entry name %q", name)
		}
		clean := path.Clean(name)
		if clean == "." {
			continue
		}
		if clean == ".." || strings.HasPrefix(clean, "../") {
			return fmt.Errorf("zip entry %q escapes the destination", name)
		}
		target := filepath.Join(root, filepath.FromSlash(clean))
		if !within(root, target) {
			return fmt.Errorf("zip entry %q escapes the destination", name)
		}
		mode := f.Mode()
		switch {
		case mode.IsDir():
			if err := mkdirInside(root, target, 0o755); err != nil {
				return err
			}
		case mode&fs.ModeSymlink != 0:
			rc, err := f.Open()
			if err != nil {
				return err
			}
			b, err := io.ReadAll(io.LimitReader(rc, 4097))
			rc.Close()
			if err != nil || len(b) > 4096 || len(b) == 0 {
				return fmt.Errorf("bad symlink entry %q", name)
			}
			link := string(b)
			if strings.ContainsRune(link, 0) || filepath.IsAbs(link) || strings.HasPrefix(link, "/") {
				return fmt.Errorf("symlink %q has an absolute target", name)
			}
			if !within(root, filepath.Join(filepath.Dir(target), filepath.FromSlash(link))) {
				return fmt.Errorf("symlink %q points outside the update", name)
			}
			if err := mkdirInside(root, filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(link, target); err != nil {
				return err
			}
		case mode.IsRegular():
			total += int64(f.UncompressedSize64)
			if total > maxZipBytes {
				return errors.New("zip is too large when extracted")
			}
			if err := mkdirInside(root, filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := writeEntry(f, target, mode.Perm()|0o600); err != nil {
				return err
			}
		default:
			return fmt.Errorf("zip entry %q is a special file", name)
		}
	}
	return verifyLinks(root)
}

func writeEntry(f *zip.File, target string, perm fs.FileMode) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	n, err := io.Copy(out, io.LimitReader(rc, int64(f.UncompressedSize64)+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if n != int64(f.UncompressedSize64) {
		return fmt.Errorf("zip entry %q has a wrong size", f.Name)
	}
	return nil
}

// mkdirInside creates dir (and parents) only if no existing component of its
// path is a symlink leading outside root.
func mkdirInside(root, dir string, perm fs.FileMode) error {
	if err := os.MkdirAll(dir, perm); err != nil {
		return err
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return err
	}
	if !within(root, real) {
		return fmt.Errorf("%q resolves outside the update", dir)
	}
	return nil
}

// verifyLinks re-resolves every symlink after extraction: a chain of links
// can point outside even when each link looks fine on its own.
func verifyLinks(root string) error {
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink == 0 {
			return nil
		}
		real, err := filepath.EvalSymlinks(p)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil // dangling, already checked lexically
			}
			return err
		}
		if !within(root, real) {
			return fmt.Errorf("symlink %q resolves outside the update", p)
		}
		return nil
	})
}

func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
