package devices

import (
	"cmp"
	"regexp"
	"strconv"
	"strings"
)

var (
	SemverPattern        = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)(\.(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*))*)?$`)
	FirmwareBuildPattern = regexp.MustCompile(`^[0-9a-f]{8}$`)
)

func CompareSemver(a, b string) int {
	coreA, preA, _ := strings.Cut(a, "-")
	coreB, preB, _ := strings.Cut(b, "-")
	pa, pb := strings.Split(coreA, "."), strings.Split(coreB, ".")
	for i := range min(len(pa), len(pb)) {
		if c := compareNumeric(pa[i], pb[i]); c != 0 {
			return c
		}
	}
	switch {
	case preA == preB:
		return 0
	case preA == "":
		return 1
	case preB == "":
		return -1
	}
	ia, ib := strings.Split(preA, "."), strings.Split(preB, ".")
	for i := range min(len(ia), len(ib)) {
		na, errA := strconv.ParseUint(ia[i], 10, 64)
		nb, errB := strconv.ParseUint(ib[i], 10, 64)
		var c int
		switch {
		case errA == nil && errB == nil:
			c = cmp.Compare(na, nb)
		case errA == nil:
			c = -1
		case errB == nil:
			c = 1
		default:
			c = strings.Compare(ia[i], ib[i])
		}
		if c != 0 {
			return c
		}
	}
	return cmp.Compare(len(ia), len(ib))
}

func compareNumeric(a, b string) int {
	if c := cmp.Compare(len(a), len(b)); c != 0 {
		return c
	}
	return strings.Compare(a, b)
}
