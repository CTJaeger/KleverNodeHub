package agent

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// systemConfigFiles are Klever system configs that get replaced on upgrade.
// These are NOT user-modified — they come directly from Klever.
var systemConfigFiles = []string{
	"api.yaml", "config.yaml", "enableEpochs.yaml", "external.yaml",
	"gasScheduleV1.yaml", "genesis.json", "nodesSetup.json",
}

// userConfigFiles are configs that contain user-specific values.
// These need merge logic — new defaults + preserved user values.
var userConfigFiles = []string{
	"config.toml",
}

// ConfigUpgradeResult describes what happened during a config upgrade.
type ConfigUpgradeResult struct {
	BackupDir      string   `json:"backup_dir"`
	BackupVersion  string   `json:"backup_version"`
	ReplacedFiles  []string `json:"replaced_files"`
	MergedFiles    []string `json:"merged_files"`
	SkippedFiles   []string `json:"skipped_files,omitempty"`
	DownloadSource string   `json:"download_source"` // "primary" or "fallback"
}

// UpgradeConfigs downloads new config files and replaces system configs while
// preserving user-specific values in config.toml.
//
// Steps:
//  1. Backup all current configs into a version-labeled directory
//  2. Download new configs (primary: Klever backup server, fallback: GitHub)
//  3. Replace system configs (yaml, json) completely
//  4. Merge config.toml: new defaults + preserved user values
func UpgradeConfigs(ctx context.Context, dataDir, network, versionLabel string) (*ConfigUpgradeResult, error) {
	configDir := filepath.Join(dataDir, "config")
	if _, err := os.Stat(configDir); os.IsNotExist(err) {
		return nil, fmt.Errorf("config directory does not exist: %s", configDir)
	}

	result := &ConfigUpgradeResult{
		BackupVersion: versionLabel,
	}

	// Step 1: Backup current configs with version label
	backupDir, err := backupConfigsWithVersion(configDir, versionLabel)
	if err != nil {
		return nil, fmt.Errorf("backup configs: %w", err)
	}
	result.BackupDir = backupDir
	log.Printf("config upgrade: backed up to %s", backupDir)

	// Step 2: Read user values from config.toml before overwrite
	userValues := readUserValues(configDir)

	// Step 3: Download new configs to a temp dir first
	tmpDir, err := os.MkdirTemp("", "klever-config-upgrade-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	source, err := downloadNewConfigs(ctx, network, tmpDir)
	if err != nil {
		return nil, fmt.Errorf("download configs: %w", err)
	}
	result.DownloadSource = source

	// Step 4: Replace system configs
	for _, fileName := range systemConfigFiles {
		srcPath := filepath.Join(tmpDir, fileName)
		dstPath := filepath.Join(configDir, fileName)

		srcData, err := os.ReadFile(srcPath)
		if err != nil {
			// File might not exist in download (optional file)
			result.SkippedFiles = append(result.SkippedFiles, fileName)
			continue
		}

		if err := os.WriteFile(dstPath, srcData, 0644); err != nil {
			return nil, fmt.Errorf("replace %s: %w", fileName, err)
		}
		result.ReplacedFiles = append(result.ReplacedFiles, fileName)
	}

	// Step 5: Merge config.toml — new defaults + user values
	for _, fileName := range userConfigFiles {
		srcPath := filepath.Join(tmpDir, fileName)
		dstPath := filepath.Join(configDir, fileName)

		newData, err := os.ReadFile(srcPath)
		if err != nil {
			// New config.toml not in download — keep current
			result.SkippedFiles = append(result.SkippedFiles, fileName)
			continue
		}

		content := string(newData)

		// Write back user-specific values
		for key, value := range userValues {
			content = replaceConfigValue(content, key, value)
		}

		if err := os.WriteFile(dstPath, []byte(content), 0644); err != nil {
			return nil, fmt.Errorf("merge %s: %w", fileName, err)
		}
		result.MergedFiles = append(result.MergedFiles, fileName)
	}

	log.Printf("config upgrade: replaced %d files, merged %d files (source: %s)",
		len(result.ReplacedFiles), len(result.MergedFiles), source)

	return result, nil
}

// backupConfigsWithVersion creates a versioned backup of all config files.
// Backup dir: <dataDir>/config/backups/<versionLabel>-<timestamp>/
func backupConfigsWithVersion(configDir, versionLabel string) (string, error) {
	timestamp := time.Now().Format("20060102-150405")
	safeVersion := sanitizeVersionLabel(versionLabel)
	backupName := fmt.Sprintf("%s-%s", safeVersion, timestamp)
	backupDir := filepath.Join(configDir, "backups", backupName)

	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return "", fmt.Errorf("create backup dir: %w", err)
	}

	entries, err := os.ReadDir(configDir)
	if err != nil {
		return "", fmt.Errorf("read config dir: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		srcPath := filepath.Join(configDir, entry.Name())
		data, err := os.ReadFile(srcPath)
		if err != nil {
			continue
		}
		if err := os.WriteFile(filepath.Join(backupDir, entry.Name()), data, 0644); err != nil {
			return "", fmt.Errorf("backup %s: %w", entry.Name(), err)
		}
	}

	return backupDir, nil
}

// sanitizeVersionLabel makes a version string safe for use as directory name.
func sanitizeVersionLabel(label string) string {
	if label == "" {
		return "unknown"
	}
	// Replace any path separators or spaces
	r := strings.NewReplacer("/", "-", "\\", "-", " ", "-", ":", "-")
	return r.Replace(label)
}

// readUserValues extracts user-specific TOML values from config.toml.
// These are the values that must be preserved across config upgrades.
func readUserValues(configDir string) map[string]string {
	values := make(map[string]string)

	configPath := filepath.Join(configDir, "config.toml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return values
	}

	// Extract known user-configurable fields
	userFields := []string{
		"NodeDisplayName",
	}

	for _, field := range userFields {
		if val := extractTOMLValue(string(data), field); val != "" {
			values[field] = val
		}
	}

	return values
}

// extractTOMLValue extracts a simple string value from TOML content.
// Handles: Key = "value" and Key = 'value'
func extractTOMLValue(content, key string) string {
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, key) {
			continue
		}
		rest := strings.TrimPrefix(trimmed, key)
		rest = strings.TrimSpace(rest)
		if !strings.HasPrefix(rest, "=") {
			continue
		}
		rest = strings.TrimPrefix(rest, "=")
		rest = strings.TrimSpace(rest)

		// Remove quotes
		if len(rest) >= 2 {
			if (rest[0] == '"' && rest[len(rest)-1] == '"') ||
				(rest[0] == '\'' && rest[len(rest)-1] == '\'') {
				return rest[1 : len(rest)-1]
			}
		}
		return rest
	}
	return ""
}

// downloadNewConfigs downloads configs to tmpDir from primary or fallback source.
// Returns the source name ("primary" or "fallback").
func downloadNewConfigs(ctx context.Context, network, tmpDir string) (string, error) {
	src, ok := configSources[network]
	if !ok {
		src = configSources["mainnet"] // default to mainnet
	}

	// Try primary: official tar.gz archive
	if err := downloadAndExtractConfig(ctx, src.URL, tmpDir, src.StripComponents); err == nil {
		return "primary", nil
	}

	log.Printf("config upgrade: primary source failed, trying fallback...")

	// Fallback: individual files from GitHub
	if err := downloadFallbackConfig(ctx, network, tmpDir); err != nil {
		return "", fmt.Errorf("both primary and fallback download failed: %w", err)
	}

	return "fallback", nil
}

// ListConfigVersionBackups lists all version-labeled config backups.
func ListConfigVersionBackups(dataDir string) ([]ConfigVersionBackup, error) {
	backupDir := filepath.Join(dataDir, "config", "backups")
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read backup dir: %w", err)
	}

	var backups []ConfigVersionBackup
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}

		// Count files in backup
		files, _ := os.ReadDir(filepath.Join(backupDir, entry.Name()))
		fileCount := 0
		for _, f := range files {
			if !f.IsDir() {
				fileCount++
			}
		}

		backups = append(backups, ConfigVersionBackup{
			Name:      entry.Name(),
			CreatedAt: info.ModTime().Unix(),
			FileCount: fileCount,
		})
	}

	return backups, nil
}

// ConfigVersionBackup represents a version-labeled config backup directory.
type ConfigVersionBackup struct {
	Name      string `json:"name"`
	CreatedAt int64  `json:"created_at"`
	FileCount int    `json:"file_count"`
}

// RestoreConfigVersion restores all config files from a version backup.
func RestoreConfigVersion(dataDir, backupName string) error {
	configDir := filepath.Join(dataDir, "config")
	backupDir := filepath.Join(configDir, "backups", backupName)

	if _, err := os.Stat(backupDir); os.IsNotExist(err) {
		return fmt.Errorf("backup not found: %s", backupName)
	}

	// Validate path
	if err := validateConfigPath(dataDir, backupDir); err != nil {
		return err
	}

	// Backup current before restoring
	if _, err := backupConfigsWithVersion(configDir, "pre-restore"); err != nil {
		return fmt.Errorf("pre-restore backup: %w", err)
	}

	entries, err := os.ReadDir(backupDir)
	if err != nil {
		return fmt.Errorf("read backup: %w", err)
	}

	restored := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(backupDir, entry.Name()))
		if err != nil {
			continue
		}
		if err := os.WriteFile(filepath.Join(configDir, entry.Name()), data, 0644); err != nil {
			return fmt.Errorf("restore %s: %w", entry.Name(), err)
		}
		restored++
	}

	log.Printf("restored %d config files from backup %s", restored, backupName)
	return nil
}
