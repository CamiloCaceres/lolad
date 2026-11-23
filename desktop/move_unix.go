//go:build !windows

package main

import "syscall"

// readOnlyFS reports whether path sits on a read-only volume, such as a
// mounted disk image or a translocation mount.
func readOnlyFS(path string) bool {
	var st syscall.Statfs_t
	if syscall.Statfs(path, &st) != nil {
		return false
	}
	return uint64(st.Flags)&1 != 0 // MNT_RDONLY (darwin) / ST_RDONLY (linux)
}
