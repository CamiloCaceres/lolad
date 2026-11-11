package create

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// systemFacts are descriptive only: they land in CLAUDE.md so the agent knows
// what machine it lives on. Every probe is best effort and never needs a shell.
type systemFacts struct{ Name, OS, RAM, Storage, Hardware, Timezone string }

func detectSystem() systemFacts {
	f := systemFacts{OS: "Linux", RAM: "unknown", Storage: "unknown"}
	f.Name, _ = os.Hostname()
	switch runtime.GOOS {
	case "darwin":
		f.OS = "Darwin"
	case "windows":
		f.OS = "Windows"
	}
	if bytes := totalMemory(); bytes > 0 {
		f.RAM = fmt.Sprintf("%d GB", bytes/(1<<30))
	}
	if home, err := os.UserHomeDir(); err == nil {
		if bytes := totalDisk(home); bytes > 0 {
			f.Storage = humanBytes(bytes)
		}
	}
	f.Hardware = hardwareName()
	f.Timezone = timezoneName()
	return f
}

func humanBytes(b uint64) string {
	if b >= 1<<40 {
		return fmt.Sprintf("%.1f TB", float64(b)/(1<<40))
	}
	return fmt.Sprintf("%d GB", b/(1<<30))
}

func machineArch() string {
	switch runtime.GOARCH {
	case "amd64":
		if runtime.GOOS == "windows" {
			return "AMD64"
		}
		return "x86_64"
	}
	return runtime.GOARCH
}

func hardwareName() string {
	switch runtime.GOOS {
	case "darwin":
		if out, err := exec.Command("/usr/sbin/sysctl", "-n", "hw.model").Output(); err == nil {
			if s := strings.TrimSpace(string(out)); s != "" {
				return s
			}
		}
	case "linux":
		if data, err := os.ReadFile("/sys/devices/virtual/dmi/id/product_name"); err == nil {
			if s := strings.TrimSpace(string(data)); s != "" {
				return s
			}
		}
	}
	return machineArch() + " machine"
}

func timezoneName() string {
	if data, err := os.ReadFile("/etc/timezone"); err == nil {
		if s := strings.TrimSpace(string(data)); s != "" {
			return s
		}
	}
	// macOS and most Linux: /etc/localtime is a link into the zoneinfo database.
	if link, err := os.Readlink("/etc/localtime"); err == nil {
		link = filepath.ToSlash(link)
		if i := strings.Index(link, "zoneinfo/"); i >= 0 {
			return link[i+len("zoneinfo/"):]
		}
	}
	return "UTC"
}

func memoryFromProc(data []byte) uint64 {
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "MemTotal:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				if kb, err := strconv.ParseUint(fields[1], 10, 64); err == nil {
					return kb * 1024
				}
			}
		}
	}
	return 0
}
