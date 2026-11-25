package harness

import (
	"os"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// Windows environment keys are case-insensitive. Normalize keys that the
// harness reads explicitly and supply HOME for Unix-oriented agent tooling.
// Every spelling of PATH ("Path" is what Windows uses) becomes one PATH.
func normalizeLaunchEnv(env map[string]string) {
	var paths []string
	for key, value := range env {
		if strings.EqualFold(key, "PATH") {
			if key == "PATH" {
				paths = append([]string{value}, paths...)
			} else {
				paths = append(paths, value)
			}
			delete(env, key)
		}
	}
	if len(paths) > 0 {
		env["PATH"] = strings.Join(paths, ";")
	}
	if env["HOME"] == "" {
		env["HOME"], _ = os.UserHomeDir()
	}
}

// registryPATH is the Path a process started now would get: the machine
// value, then the user's, with %VARIABLES% expanded. lolad's own PATH was
// fixed when it started, so a tool installed since (claude's installer adds
// %USERPROFILE%\.local\bin to the user Path) is missing from it until the
// user signs out. A test replaces it.
var registryPATH = func() string {
	var parts []string
	for _, k := range []struct {
		root registry.Key
		path string
	}{
		{registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Session Manager\Environment`},
		{registry.CURRENT_USER, `Environment`},
	} {
		if v := readRegPath(k.root, k.path); v != "" {
			parts = append(parts, v)
		}
	}
	return strings.Join(parts, ";")
}

func readRegPath(root registry.Key, path string) string {
	key, err := registry.OpenKey(root, path, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer key.Close()
	v, typ, err := key.GetStringValue("Path")
	if err != nil {
		return ""
	}
	if typ == registry.EXPAND_SZ {
		if x, err := registry.ExpandString(v); err == nil {
			v = x
		}
	}
	return v
}
