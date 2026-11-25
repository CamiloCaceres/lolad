//go:build !windows

package harness

func normalizeLaunchEnv(env map[string]string) {}

// registryPATH is empty: only Windows keeps PATH in the registry.
var registryPATH = func() string { return "" }
