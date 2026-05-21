//go:build windows

package handlers

import "sync"

// dockerSelfUpdateMu is referenced cross-platform by system.go but only
// meaningful on non-Windows. Kept here as a stub so the package still builds.
var dockerSelfUpdateMu sync.Mutex

// dockerSelfUpdateAvailable returns false on Windows.
func dockerSelfUpdateAvailable() bool {
	return false
}

// dockerSelfUpdate is not supported on Windows.
func dockerSelfUpdate(_ string) error {
	return nil
}
