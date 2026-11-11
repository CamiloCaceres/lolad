//go:build darwin || linux

package create

import (
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

func totalMemory() uint64 {
	if runtime.GOOS == "darwin" {
		out, err := exec.Command("/usr/sbin/sysctl", "-n", "hw.memsize").Output()
		if err != nil {
			return 0
		}
		n, _ := strconv.ParseUint(strings.TrimSpace(string(out)), 10, 64)
		return n
	}
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	return memoryFromProc(data)
}

func totalDisk(path string) uint64 {
	var st syscall.Statfs_t
	if syscall.Statfs(path, &st) != nil {
		return 0
	}
	return uint64(st.Blocks) * uint64(st.Bsize)
}
