package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGetKeyInfo_NoKey(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "config"), 0755); err != nil {
		t.Fatal(err)
	}

	info, err := GetKeyInfo(dir)
	if err != nil {
		t.Fatalf("GetKeyInfo: %v", err)
	}
	if info.BLSPublicKey != "" {
		t.Errorf("expected empty public key, got %q", info.BLSPublicKey)
	}
}

func TestGetKeyInfo_WithKey(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}

	pemContent := "-----BEGIN PRIVATE KEY for a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6-----\nbW9jayBwcml2YXRlIGtleSBkYXRh\n-----END PRIVATE KEY for a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6-----"
	if err := os.WriteFile(filepath.Join(configDir, "validatorKey.pem"), []byte(pemContent), 0600); err != nil {
		t.Fatal(err)
	}

	info, err := GetKeyInfo(dir)
	if err != nil {
		t.Fatalf("GetKeyInfo: %v", err)
	}
	expected := "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6"
	if info.BLSPublicKey != expected {
		t.Errorf("BLSPublicKey = %q, want %q", info.BLSPublicKey, expected)
	}
	if info.FileSize == 0 {
		t.Error("expected non-zero file size")
	}
}

func TestImportKey(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}

	pemContent := "-----BEGIN PRIVATE KEY for a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6-----\nbW9jayBwcml2YXRlIGtleSBkYXRh\n-----END PRIVATE KEY for a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6-----"

	info, err := ImportKey(dir, pemContent)
	if err != nil {
		t.Fatalf("ImportKey: %v", err)
	}
	if info.BLSPublicKey == "" {
		t.Error("expected non-empty public key after import")
	}

	// Verify file was written
	data, err := os.ReadFile(filepath.Join(configDir, "validatorKey.pem"))
	if err != nil {
		t.Fatalf("read imported key: %v", err)
	}
	if string(data) != pemContent {
		t.Error("imported key content mismatch")
	}
}

func TestImportKey_InvalidPEM(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "config"), 0755); err != nil {
		t.Fatal(err)
	}

	_, err := ImportKey(dir, "not a valid PEM file")
	if err == nil {
		t.Error("expected error for invalid PEM")
	}
}

func TestImportKey_BackupsExisting(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Write an existing key
	oldPEM := "-----BEGIN PRIVATE KEY for aabbcc-----\nold key\n-----END PRIVATE KEY for aabbcc-----"
	if err := os.WriteFile(filepath.Join(configDir, "validatorKey.pem"), []byte(oldPEM), 0600); err != nil {
		t.Fatal(err)
	}

	// Import new key
	newPEM := "-----BEGIN PRIVATE KEY for a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6-----\nnew key\n-----END PRIVATE KEY for a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6-----"
	_, err := ImportKey(dir, newPEM)
	if err != nil {
		t.Fatalf("ImportKey: %v", err)
	}

	// Check backup was created
	backupDir := filepath.Join(configDir, "key-backups")
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatalf("read backup dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 backup, got %d", len(entries))
	}
}

func TestExportKey(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}

	pemContent := "-----BEGIN PRIVATE KEY for abc123-----\ntest\n-----END PRIVATE KEY for abc123-----"
	if err := os.WriteFile(filepath.Join(configDir, "validatorKey.pem"), []byte(pemContent), 0600); err != nil {
		t.Fatal(err)
	}

	exported, err := ExportKey(dir)
	if err != nil {
		t.Fatalf("ExportKey: %v", err)
	}
	if exported != pemContent {
		t.Errorf("exported content mismatch")
	}
}

func TestExportKey_NoKey(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "config"), 0755); err != nil {
		t.Fatal(err)
	}

	_, err := ExportKey(dir)
	if err == nil {
		t.Error("expected error when no key exists")
	}
}

func TestBackupKey(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(configDir, "validatorKey.pem"), []byte("key data"), 0600); err != nil {
		t.Fatal(err)
	}

	if err := BackupKey(dir); err != nil {
		t.Fatalf("BackupKey: %v", err)
	}

	backups, err := ListKeyBackups(dir)
	if err != nil {
		t.Fatalf("ListKeyBackups: %v", err)
	}
	if len(backups) != 1 {
		t.Fatalf("expected 1 backup, got %d", len(backups))
	}
}

func TestListKeyBackups_Empty(t *testing.T) {
	dir := t.TempDir()
	backups, err := ListKeyBackups(dir)
	if err != nil {
		t.Fatalf("ListKeyBackups: %v", err)
	}
	if len(backups) != 0 {
		t.Errorf("expected 0 backups, got %d", len(backups))
	}
}
