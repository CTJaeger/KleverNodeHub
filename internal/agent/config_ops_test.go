package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListConfigFiles(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create test files
	for _, name := range []string{"config.toml", "genesis.json", "readme.txt", "notes.md"} {
		if err := os.WriteFile(filepath.Join(configDir, name), []byte("test"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	files, err := ListConfigFiles(dir)
	if err != nil {
		t.Fatalf("ListConfigFiles: %v", err)
	}

	// Should include .toml and .json but not .txt or .md
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(files))
	}

	names := make(map[string]bool)
	for _, f := range files {
		names[f.Name] = true
	}
	if !names["config.toml"] {
		t.Error("missing config.toml")
	}
	if !names["genesis.json"] {
		t.Error("missing genesis.json")
	}
}

func TestReadConfigFile(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}

	content := "key = \"value\"\n"
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := ReadConfigFile(dir, "config.toml")
	if err != nil {
		t.Fatalf("ReadConfigFile: %v", err)
	}
	if got != content {
		t.Errorf("content = %q, want %q", got, content)
	}
}

func TestReadConfigFile_PathTraversal(t *testing.T) {
	dir := t.TempDir()
	_, err := ReadConfigFile(dir, "../../../etc/passwd")
	if err == nil {
		t.Error("expected error for path traversal")
	}
	if !strings.Contains(err.Error(), "traversal") && !strings.Contains(err.Error(), "extension") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestReadConfigFile_BadExtension(t *testing.T) {
	dir := t.TempDir()
	_, err := ReadConfigFile(dir, "script.sh")
	if err == nil {
		t.Error("expected error for disallowed extension")
	}
}

func TestWriteConfigFile_WithBackup(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Write initial content
	original := "original content"
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	// Write new content (should auto-backup)
	newContent := "new content"
	if err := WriteConfigFile(dir, "config.toml", newContent); err != nil {
		t.Fatalf("WriteConfigFile: %v", err)
	}

	// Verify new content
	data, _ := os.ReadFile(filepath.Join(configDir, "config.toml"))
	if string(data) != newContent {
		t.Errorf("new content = %q, want %q", string(data), newContent)
	}

	// Verify backup was created
	backupDir := filepath.Join(configDir, "backups")
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatalf("read backup dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 backup, got %d", len(entries))
	}

	// Verify backup content
	backupData, _ := os.ReadFile(filepath.Join(backupDir, entries[0].Name()))
	if string(backupData) != original {
		t.Errorf("backup content = %q, want %q", string(backupData), original)
	}
}

func TestBackupConfigFile(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(configDir, "test.toml"), []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := BackupConfigFile(dir, "test.toml"); err != nil {
		t.Fatalf("BackupConfigFile: %v", err)
	}

	backups, err := ListConfigBackups(dir, "test.toml")
	if err != nil {
		t.Fatalf("ListConfigBackups: %v", err)
	}
	if len(backups) != 1 {
		t.Fatalf("expected 1 backup, got %d", len(backups))
	}
	if !strings.HasPrefix(backups[0].Name, "test.toml.") {
		t.Errorf("backup name = %q, expected prefix test.toml.", backups[0].Name)
	}
}

func TestRestoreConfigBackup(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	backupDir := filepath.Join(configDir, "backups")
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Write current file
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte("current"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create a "backup"
	backupName := "config.toml.20260312-100000.bak"
	if err := os.WriteFile(filepath.Join(backupDir, backupName), []byte("old content"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := RestoreConfigBackup(dir, backupName); err != nil {
		t.Fatalf("RestoreConfigBackup: %v", err)
	}

	// Verify restored content
	data, _ := os.ReadFile(filepath.Join(configDir, "config.toml"))
	if string(data) != "old content" {
		t.Errorf("restored content = %q, want %q", string(data), "old content")
	}

	// Verify the current file was backed up before restore
	backups, _ := ListConfigBackups(dir, "config.toml")
	if len(backups) < 2 {
		t.Error("expected backup of current file before restore")
	}
}

func TestExtractOriginalName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"config.toml.20260312-100000.bak", "config.toml"},
		{"genesis.json.20260312-100000.bak", "genesis.json"},
		{"invalid.bak", ""},
		{"noext", ""},
	}

	for _, tt := range tests {
		got := extractOriginalName(tt.input)
		if got != tt.want {
			t.Errorf("extractOriginalName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestValidateConfigPath_Traversal(t *testing.T) {
	err := validateConfigPath("/opt/klever", "/etc/passwd")
	if err == nil {
		t.Error("expected error for path traversal")
	}
}

func TestValidateConfigExtension(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		{"config.toml", false},
		{"genesis.json", false},
		{"key.pem", false},
		{"config.yaml", false},
		{"script.sh", true},
		{"binary.exe", true},
		{"file.txt", true},
	}

	for _, tt := range tests {
		err := validateConfigExtension(tt.name)
		if (err != nil) != tt.wantErr {
			t.Errorf("validateConfigExtension(%q) error = %v, wantErr = %v", tt.name, err, tt.wantErr)
		}
	}
}
