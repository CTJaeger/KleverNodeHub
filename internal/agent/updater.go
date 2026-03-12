package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// UpdateResult holds the result of an agent binary update.
type UpdateResult struct {
	OldVersion string `json:"old_version"`
	NewVersion string `json:"new_version"`
	BackupPath string `json:"backup_path"`
	NeedsRestart bool `json:"needs_restart"`
}

// VerifyAndReplaceBinary verifies the checksum of new binary data and replaces
// the current agent binary. Keeps a backup of the previous version for rollback.
func VerifyAndReplaceBinary(binaryData []byte, expectedChecksum string, agentConfigDir string) (*UpdateResult, error) {
	// Verify checksum
	actualChecksum := sha256Hex(binaryData)
	if actualChecksum != expectedChecksum {
		return nil, fmt.Errorf("checksum mismatch: expected %s, got %s", expectedChecksum, actualChecksum)
	}

	// Get current executable path
	execPath, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("get executable path: %w", err)
	}
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return nil, fmt.Errorf("resolve symlinks: %w", err)
	}

	// Create backup directory
	backupDir := filepath.Join(agentConfigDir, "backups")
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return nil, fmt.Errorf("create backup dir: %w", err)
	}

	// Backup current binary
	backupName := filepath.Base(execPath) + ".backup"
	backupPath := filepath.Join(backupDir, backupName)

	currentData, err := os.ReadFile(execPath)
	if err != nil {
		return nil, fmt.Errorf("read current binary: %w", err)
	}
	if err := os.WriteFile(backupPath, currentData, 0755); err != nil {
		return nil, fmt.Errorf("write backup: %w", err)
	}

	// Write new binary to temp file in same directory (for atomic rename)
	dir := filepath.Dir(execPath)
	tmpFile := filepath.Join(dir, ".agent-update-tmp")
	if err := os.WriteFile(tmpFile, binaryData, 0755); err != nil {
		return nil, fmt.Errorf("write temp binary: %w", err)
	}

	// Atomic replace (rename)
	if err := os.Rename(tmpFile, execPath); err != nil {
		// On Windows, can't rename over running executable — write beside it
		if runtime.GOOS == "windows" {
			// Move current to .old, new to current
			oldPath := execPath + ".old"
			_ = os.Remove(oldPath)
			if err := os.Rename(execPath, oldPath); err != nil {
				_ = os.Remove(tmpFile)
				return nil, fmt.Errorf("rename current binary: %w", err)
			}
			if err := os.Rename(tmpFile, execPath); err != nil {
				// Rollback
				_ = os.Rename(oldPath, execPath)
				return nil, fmt.Errorf("rename new binary: %w", err)
			}
		} else {
			_ = os.Remove(tmpFile)
			return nil, fmt.Errorf("replace binary: %w", err)
		}
	}

	return &UpdateResult{
		BackupPath:   backupPath,
		NeedsRestart: true,
	}, nil
}

// RollbackBinary restores the previous agent binary from backup.
func RollbackBinary(backupPath string) error {
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("get executable path: %w", err)
	}
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return fmt.Errorf("resolve symlinks: %w", err)
	}

	backupData, err := os.ReadFile(backupPath)
	if err != nil {
		return fmt.Errorf("read backup: %w", err)
	}

	if err := os.WriteFile(execPath, backupData, 0755); err != nil {
		return fmt.Errorf("restore binary: %w", err)
	}

	return nil
}

// SHA256Hex computes the SHA-256 hex digest of data.
func SHA256Hex(data []byte) string {
	return sha256Hex(data)
}

func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}
