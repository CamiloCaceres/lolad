//go:build !darwin && !linux && !windows

package create

func totalMemory() uint64     { return 0 }
func totalDisk(string) uint64 { return 0 }
