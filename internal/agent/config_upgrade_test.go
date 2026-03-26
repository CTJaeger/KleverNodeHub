package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatal(err)
	}
}

func mustWriteFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
}

func TestBackupConfigsWithVersion(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	mustMkdirAll(t, configDir)

	mustWriteFile(t, filepath.Join(configDir, "config.toml"), []byte("NodeDisplayName = \"MyNode\""))
	mustWriteFile(t, filepath.Join(configDir, "genesis.json"), []byte("{}"))

	backupDir, err := backupConfigsWithVersion(configDir, "v1.2.3")
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(backupDir, "v1.2.3") {
		t.Errorf("backup dir should contain version label, got %s", backupDir)
	}

	// Check files were copied
	entries, _ := os.ReadDir(backupDir)
	if len(entries) != 2 {
		t.Errorf("expected 2 backed up files, got %d", len(entries))
	}
}

func TestSanitizeVersionLabel(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"v1.2.3", "v1.2.3"},
		{"", "unknown"},
		{"v1/2", "v1-2"},
		{"tag with spaces", "tag-with-spaces"},
	}
	for _, tt := range tests {
		got := sanitizeVersionLabel(tt.input)
		if got != tt.want {
			t.Errorf("sanitizeVersionLabel(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestExtractTOMLValue(t *testing.T) {
	content := `
[Node]
  NodeDisplayName = "TestValidator"
  Port = 8080
  SomeFlag = true
`
	tests := []struct {
		key  string
		want string
	}{
		{"NodeDisplayName", "TestValidator"},
		{"Port", "8080"},
		{"SomeFlag", "true"},
		{"NonExistent", ""},
	}
	for _, tt := range tests {
		got := extractTOMLValue(content, tt.key)
		if got != tt.want {
			t.Errorf("extractTOMLValue(%q) = %q, want %q", tt.key, got, tt.want)
		}
	}
}

func TestReadUserValues(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	mustMkdirAll(t, configDir)

	content := `[Node]
  NodeDisplayName = "Validator-1"
`
	mustWriteFile(t, filepath.Join(configDir, "config.toml"), []byte(content))

	values := readUserValues(configDir)
	if values["NodeDisplayName"] != "Validator-1" {
		t.Errorf("expected NodeDisplayName=Validator-1, got %q", values["NodeDisplayName"])
	}
}

func TestListConfigVersionBackups(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	backupDir := filepath.Join(configDir, "backups")

	mustMkdirAll(t, filepath.Join(backupDir, "v1.0.0-20260313-100000"))
	mustWriteFile(t, filepath.Join(backupDir, "v1.0.0-20260313-100000", "config.toml"), []byte("test"))

	mustMkdirAll(t, filepath.Join(backupDir, "v1.1.0-20260313-120000"))
	mustWriteFile(t, filepath.Join(backupDir, "v1.1.0-20260313-120000", "config.toml"), []byte("test"))
	mustWriteFile(t, filepath.Join(backupDir, "v1.1.0-20260313-120000", "genesis.json"), []byte("{}"))

	// Individual .bak file — should now be included as a grouped entry
	mustWriteFile(t, filepath.Join(backupDir, "config.toml.20260312-090000.bak"), []byte("old"))

	backups, err := ListConfigVersionBackups(dir)
	if err != nil {
		t.Fatal(err)
	}

	if len(backups) != 3 {
		t.Fatalf("expected 3 backups (2 version + 1 save), got %d", len(backups))
	}

	// Check file counts
	found := map[string]bool{}
	for _, b := range backups {
		found[b.Name] = true
		if b.Name == "v1.0.0-20260313-100000" && b.FileCount != 1 {
			t.Errorf("v1.0.0 backup should have 1 file, got %d", b.FileCount)
		}
		if b.Name == "v1.1.0-20260313-120000" && b.FileCount != 2 {
			t.Errorf("v1.1.0 backup should have 2 files, got %d", b.FileCount)
		}
		if b.Name == "save-20260312-090000" && b.FileCount != 1 {
			t.Errorf("save backup should have 1 file, got %d", b.FileCount)
		}
	}
	if !found["save-20260312-090000"] {
		t.Error("expected save-20260312-090000 backup from .bak file")
	}
}

func TestRestoreConfigVersion(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	backupDir := filepath.Join(configDir, "backups", "v1.0.0-20260313-100000")
	mustMkdirAll(t, backupDir)

	mustWriteFile(t, filepath.Join(configDir, "config.toml"), []byte("current"))
	mustWriteFile(t, filepath.Join(backupDir, "config.toml"), []byte("old-version"))

	if err := RestoreConfigVersion(dir, "v1.0.0-20260313-100000"); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(configDir, "config.toml"))
	if string(data) != "old-version" {
		t.Errorf("expected restored content 'old-version', got %q", string(data))
	}

	// Check pre-restore backup was created
	backups, _ := ListConfigVersionBackups(dir)
	found := false
	for _, b := range backups {
		if strings.Contains(b.Name, "pre-restore") {
			found = true
		}
	}
	if !found {
		t.Error("pre-restore backup should have been created")
	}
}
