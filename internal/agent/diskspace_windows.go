//go:build windows

package agent

import "math"

// freeDiskSpace on Windows is a stub — the agent runs on Linux in production
// and a chain-DB restore only ever happens there. Returning "effectively
// unlimited" makes the preflight a no-op so the package builds and tests on
// Windows dev machines without pulling in syscall specifics.
func freeDiskSpace(_ string) (uint64, error) {
	return math.MaxUint64, nil
}
