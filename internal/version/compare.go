package version

import (
	"fmt"
	"strings"
)

// Compare compares two release versions ("v" prefix optional): 1 if a is
// newer, -1 if older, 0 if equal. Releases of Test0rMaik/KleverNodeHub share
// the X.Y.Z and add -knh.N; at the same X.Y.Z the higher N is newer, and any
// -knh.N is newer than the plain X.Y.Z.
func Compare(a, b string) int {
	aParts := strings.Split(strings.TrimPrefix(a, "v"), ".")
	bParts := strings.Split(strings.TrimPrefix(b, "v"), ".")

	for i := 0; i < 3; i++ {
		var aNum, bNum int
		if i < len(aParts) {
			_, _ = fmt.Sscanf(aParts[i], "%d", &aNum)
		}
		if i < len(bParts) {
			_, _ = fmt.Sscanf(bParts[i], "%d", &bNum)
		}
		if aNum > bNum {
			return 1
		}
		if aNum < bNum {
			return -1
		}
	}
	if ra, rb := forkRevision(a), forkRevision(b); ra != rb {
		if ra > rb {
			return 1
		}
		return -1
	}
	return 0
}

// forkRevision extracts N from a suffix like "0.3.93-knh.1" (0 if absent).
func forkRevision(v string) int {
	_, suffix, ok := strings.Cut(v, "-knh.")
	if !ok {
		return 0
	}
	var n int
	_, _ = fmt.Sscanf(suffix, "%d", &n)
	return n
}
