//go:build windows

package handlers

// dockerSelfUpdateAvailable returns false on Windows.
func dockerSelfUpdateAvailable() bool {
	return false
}

// dockerSelfUpdate is not supported on Windows.
func dockerSelfUpdate(_ string) error {
	return nil
}
