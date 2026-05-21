//go:build windows

package handlers

// SweepStaleFinalizeHelpers is a no-op on Windows. The dashboard self-update
// flow it cleans up after is gated to non-Windows builds.
func SweepStaleFinalizeHelpers() {}
