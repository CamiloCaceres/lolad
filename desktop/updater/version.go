package updater

import (
	"strconv"
	"strings"
)

// compareVersions compares two dotted versions ("1.2.3", "v1.2", "1.2.3-rc1").
// It returns -1, 0 or 1. A pre-release sorts before its release. ok is false
// when either side is not a version at all.
func compareVersions(a, b string) (cmp int, ok bool) {
	ac, apre, aok := parseVersion(a)
	bc, bpre, bok := parseVersion(b)
	if !aok || !bok {
		return 0, false
	}
	for i := 0; i < len(ac) || i < len(bc); i++ {
		var x, y int
		if i < len(ac) {
			x = ac[i]
		}
		if i < len(bc) {
			y = bc[i]
		}
		if x != y {
			if x < y {
				return -1, true
			}
			return 1, true
		}
	}
	switch {
	case apre == bpre:
		return 0, true
	case apre == "":
		return 1, true
	case bpre == "":
		return -1, true
	case apre < bpre:
		return -1, true
	}
	return 1, true
}

func parseVersion(v string) (core []int, pre string, ok bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i]
	}
	if i := strings.IndexByte(v, '-'); i >= 0 {
		v, pre = v[:i], v[i+1:]
	}
	if v == "" {
		return nil, "", false
	}
	for _, p := range strings.Split(v, ".") {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return nil, "", false
		}
		core = append(core, n)
	}
	return core, pre, true
}
