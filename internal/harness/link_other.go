//go:build !windows

package harness

// linkDirFallback: symlinks work on Unix, so there is nothing else to try.
func linkDirFallback(source, target string, err error) error { return err }
