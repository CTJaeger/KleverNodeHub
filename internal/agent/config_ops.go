package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// allowedConfigExtensions restricts which files can be read/written.
var allowedConfigExtensions = map[string]bool{
	".toml": true,
	".json": true,
	".pem":  true,
	".yaml": true,
	".yml":  true,
	".cfg":  true,
}

// ConfigFile represents a configuration file's metadata.
type ConfigFile struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	Modified int64  `json:"modified"` // Unix timestamp
}

// ListConfigFiles lists configuration files in a node's config directory.
func ListConfigFiles(dataDir string) ([]ConfigFile, error) {
	configDir := filepath.Join(dataDir, "config")
	if err := validateConfigPath(dataDir, configDir); err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(configDir)
	if err != nil {
		return nil, fmt.Errorf("read config dir: %w", err)
	}

	var files []ConfigFile
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if !allowedConfigExtensions[ext] {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		files = append(files, ConfigFile{
			Name:     entry.Name(),
			Path:     filepath.Join(configDir, entry.Name()),
			Size:     info.Size(),
			Modified: info.ModTime().Unix(),
		})
	}

	return files, nil
}

// ReadConfigFile reads a configuration file's contents.
func ReadConfigFile(dataDir, fileName string) (string, error) {
	filePath := filepath.Join(dataDir, "config", fileName)
	if err := validateConfigPath(dataDir, filePath); err != nil {
		return "", err
	}
	if err := validateConfigExtension(fileName); err != nil {
		return "", err
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("read config file: %w", err)
	}

	return string(data), nil
}

// WriteConfigFile writes content to a configuration file, creating a backup first.
func WriteConfigFile(dataDir, fileName, content string) error {
	filePath := filepath.Join(dataDir, "config", fileName)
	if err := validateConfigPath(dataDir, filePath); err != nil {
		return err
	}
	if err := validateConfigExtension(fileName); err != nil {
		return err
	}

	// Auto-backup before write
	if _, err := os.Stat(filePath); err == nil {
		if err := BackupConfigFile(dataDir, fileName); err != nil {
			return fmt.Errorf("backup before write: %w", err)
		}
	}

	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		return fmt.Errorf("write config file: %w", err)
	}

	return nil
}

// BackupConfigFile creates a timestamped backup of a configuration file.
func BackupConfigFile(dataDir, fileName string) error {
	srcPath := filepath.Join(dataDir, "config", fileName)
	if err := validateConfigPath(dataDir, srcPath); err != nil {
		return err
	}

	backupDir := filepath.Join(dataDir, "config", "backups")
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return fmt.Errorf("create backup dir: %w", err)
	}

	data, err := os.ReadFile(srcPath)
	if err != nil {
		return fmt.Errorf("read for backup: %w", err)
	}

	timestamp := time.Now().Format("20060102-150405")
	backupName := fmt.Sprintf("%s.%s.bak", fileName, timestamp)
	backupPath := filepath.Join(backupDir, backupName)

	if err := os.WriteFile(backupPath, data, 0644); err != nil {
		return fmt.Errorf("write backup: %w", err)
	}

	return nil
}

// ListConfigBackups lists backup files for a specific config file.
func ListConfigBackups(dataDir, fileName string) ([]ConfigFile, error) {
	backupDir := filepath.Join(dataDir, "config", "backups")
	if err := validateConfigPath(dataDir, backupDir); err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(backupDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read backup dir: %w", err)
	}

	prefix := fileName + "."
	var files []ConfigFile
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		files = append(files, ConfigFile{
			Name:     entry.Name(),
			Path:     filepath.Join(backupDir, entry.Name()),
			Size:     info.Size(),
			Modified: info.ModTime().Unix(),
		})
	}

	return files, nil
}

// RestoreConfigBackup restores a config file from a backup.
func RestoreConfigBackup(dataDir, backupName string) error {
	backupDir := filepath.Join(dataDir, "config", "backups")
	backupPath := filepath.Join(backupDir, backupName)
	if err := validateConfigPath(dataDir, backupPath); err != nil {
		return err
	}

	// Extract original filename from backup name (e.g., "config.toml.20260312-100000.bak" -> "config.toml")
	originalName := extractOriginalName(backupName)
	if originalName == "" {
		return fmt.Errorf("cannot determine original filename from backup: %q", backupName)
	}

	data, err := os.ReadFile(backupPath)
	if err != nil {
		return fmt.Errorf("read backup: %w", err)
	}

	// Backup current before restoring
	destPath := filepath.Join(dataDir, "config", originalName)
	if _, err := os.Stat(destPath); err == nil {
		if err := BackupConfigFile(dataDir, originalName); err != nil {
			return fmt.Errorf("backup current before restore: %w", err)
		}
	}

	if err := os.WriteFile(destPath, data, 0644); err != nil {
		return fmt.Errorf("restore config: %w", err)
	}

	return nil
}

// validateConfigPath prevents path traversal attacks.
func validateConfigPath(dataDir, targetPath string) error {
	absDataDir, err := filepath.Abs(dataDir)
	if err != nil {
		return fmt.Errorf("resolve data dir: %w", err)
	}

	absTarget, err := filepath.Abs(targetPath)
	if err != nil {
		return fmt.Errorf("resolve target path: %w", err)
	}

	// Ensure target is within data directory
	if !strings.HasPrefix(absTarget, absDataDir+string(filepath.Separator)) && absTarget != absDataDir {
		return fmt.Errorf("path traversal blocked: %q is outside %q", targetPath, dataDir)
	}

	return nil
}

// validateConfigExtension checks if the file extension is allowed.
func validateConfigExtension(fileName string) error {
	ext := strings.ToLower(filepath.Ext(fileName))
	if !allowedConfigExtensions[ext] {
		return fmt.Errorf("file extension not allowed: %q", ext)
	}
	return nil
}

// extractOriginalName extracts the original filename from a backup name.
// e.g., "config.toml.20260312-100000.bak" -> "config.toml"
func extractOriginalName(backupName string) string {
	// Remove .bak suffix
	name := strings.TrimSuffix(backupName, ".bak")
	// Remove timestamp suffix (format: .YYYYMMDD-HHMMSS)
	idx := strings.LastIndex(name, ".")
	if idx <= 0 {
		return ""
	}
	return name[:idx]
}
