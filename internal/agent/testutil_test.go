package agent

import (
	"os"
	"path/filepath"
	"testing"
)

// shortSocketPath creates a Unix socket path short enough for macOS (104 char limit).
// t.TempDir() produces paths that are too long on macOS due to the
// /var/folders/.../T/TestName.../001/ structure.
func shortSocketPath(t *testing.T) (string, func()) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "knh-")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	sockPath := filepath.Join(dir, "d.sock")
	cleanup := func() { _ = os.RemoveAll(dir) }
	return sockPath, cleanup
}
