//go:build windows

package handlers

import "fmt"

// RunSelfUpdateFinalize is a no-op on Windows. The dashboard self-update flow
// it supports requires a Unix Docker socket and is gated to non-Windows builds.
func RunSelfUpdateFinalize(_, _ string) error {
	return fmt.Errorf("self-update finalize is not supported on Windows")
}
