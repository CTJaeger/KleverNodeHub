package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// KeyInfo represents information about a validator key.
type KeyInfo struct {
	BLSPublicKey string `json:"bls_public_key"`
	KeyFile      string `json:"key_file"`
	FileSize     int64  `json:"file_size"`
	Modified     int64  `json:"modified"`
	HasBackup    bool   `json:"has_backup"`
}

// GetKeyInfo returns information about the validator key for a node.
func GetKeyInfo(dataDir string) (*KeyInfo, error) {
	configDir := filepath.Join(dataDir, "config")
	keyPath := filepath.Join(configDir, validatorKeyFile)

	info, err := os.Stat(keyPath)
	if err != nil {
		if os.IsNotExist(err) {
			return &KeyInfo{}, nil
		}
		return nil, fmt.Errorf("stat key file: %w", err)
	}

	pubKey, _ := ExtractBLSPublicKey(configDir)

	// Check for backups
	backupDir := filepath.Join(configDir, "key-backups")
	hasBackup := false
	if entries, err := os.ReadDir(backupDir); err == nil {
		hasBackup = len(entries) > 0
	}

	return &KeyInfo{
		BLSPublicKey: pubKey,
		KeyFile:      validatorKeyFile,
		FileSize:     info.Size(),
		Modified:     info.ModTime().Unix(),
		HasBackup:    hasBackup,
	}, nil
}

// GenerateKey runs the klever-go keygenerator to create a new BLS key pair.
func (d *DockerClient) GenerateKey(ctx context.Context, dataDir, imageTag string) (*KeyInfo, error) {
	walletDir := filepath.Join(dataDir, "wallet")
	if err := os.MkdirAll(walletDir, 0755); err != nil {
		return nil, fmt.Errorf("create wallet dir: %w", err)
	}

	image := fmt.Sprintf("%s:%s", kleverImage, imageTag)

	// Create a temporary container to run keygenerator
	body := containerCreateBody{
		Image:      image,
		User:       "999:999",
		Entrypoint: []string{"/usr/local/bin/keygenerator"},
		HostConfig: hostConfigBody{
			Binds: []string{
				walletDir + ":/opt/klever-blockchain/wallet",
			},
		},
	}

	containerName := fmt.Sprintf("klever-keygen-%d", time.Now().UnixNano())
	containerID, err := d.createContainerRaw(ctx, containerName, body)
	if err != nil {
		return nil, fmt.Errorf("create keygen container: %w", err)
	}

	// Start and wait for completion
	if err := d.StartContainer(ctx, containerID); err != nil {
		_ = d.RemoveContainer(ctx, containerID, true)
		return nil, fmt.Errorf("start keygen: %w", err)
	}

	// Wait for container to finish (poll status)
	waitCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	for {
		status, err := d.GetContainerStatus(waitCtx, containerID)
		if err != nil || status == "exited" || status == "stopped" || !strings.Contains(status, "running") {
			break
		}
		select {
		case <-waitCtx.Done():
			_ = d.RemoveContainer(ctx, containerID, true)
			return nil, fmt.Errorf("keygen timeout")
		case <-time.After(1 * time.Second):
			continue
		}
	}

	// Cleanup container
	_ = d.RemoveContainer(ctx, containerID, true)

	// Move generated key to config dir
	configDir := filepath.Join(dataDir, "config")
	generatedKey := filepath.Join(walletDir, validatorKeyFile)
	destKey := filepath.Join(configDir, validatorKeyFile)

	// Check if key was generated
	if _, err := os.Stat(generatedKey); os.IsNotExist(err) {
		return nil, fmt.Errorf("keygenerator did not produce a key file")
	}

	// Backup existing key if present
	if _, err := os.Stat(destKey); err == nil {
		if err := BackupKey(dataDir); err != nil {
			return nil, fmt.Errorf("backup existing key: %w", err)
		}
	}

	// Move key
	data, err := os.ReadFile(generatedKey)
	if err != nil {
		return nil, fmt.Errorf("read generated key: %w", err)
	}
	if err := os.WriteFile(destKey, data, 0600); err != nil {
		return nil, fmt.Errorf("write key to config: %w", err)
	}
	_ = os.Remove(generatedKey)

	return GetKeyInfo(dataDir)
}

// createContainerRaw creates a container with a raw body (for non-standard containers like keygen).
func (d *DockerClient) createContainerRaw(ctx context.Context, name string, body containerCreateBody) (string, error) {
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal body: %w", err)
	}

	u := fmt.Sprintf("http://localhost/%s/containers/create?name=%s",
		d.apiVersion, url.QueryEscape(name))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(jsonBody))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("create container: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("create container: HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var createResp containerCreateResponse
	if err := json.NewDecoder(resp.Body).Decode(&createResp); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	return createResp.ID, nil
}

// ImportKey imports a validator key from PEM content, backing up any existing key.
func ImportKey(dataDir, pemContent string) (*KeyInfo, error) {
	// Validate the PEM content
	pubKey, err := parseBLSPublicKeyFromPEM(pemContent)
	if err != nil {
		return nil, fmt.Errorf("invalid key format: %w", err)
	}
	if pubKey == "" {
		return nil, fmt.Errorf("no public key found in PEM data")
	}

	configDir := filepath.Join(dataDir, "config")
	destKey := filepath.Join(configDir, validatorKeyFile)

	// Backup existing key if present
	if _, err := os.Stat(destKey); err == nil {
		if err := BackupKey(dataDir); err != nil {
			return nil, fmt.Errorf("backup existing key: %w", err)
		}
	}

	if err := os.WriteFile(destKey, []byte(pemContent), 0600); err != nil {
		return nil, fmt.Errorf("write imported key: %w", err)
	}

	return GetKeyInfo(dataDir)
}

// ExportKey reads and returns the validator key PEM content.
func ExportKey(dataDir string) (string, error) {
	configDir := filepath.Join(dataDir, "config")
	keyPath := filepath.Join(configDir, validatorKeyFile)

	data, err := os.ReadFile(keyPath)
	if err != nil {
		return "", fmt.Errorf("read key: %w", err)
	}

	return string(data), nil
}

// BackupKey creates a timestamped backup of the validator key.
func BackupKey(dataDir string) error {
	configDir := filepath.Join(dataDir, "config")
	keyPath := filepath.Join(configDir, validatorKeyFile)

	data, err := os.ReadFile(keyPath)
	if err != nil {
		return fmt.Errorf("read key for backup: %w", err)
	}

	backupDir := filepath.Join(configDir, "key-backups")
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return fmt.Errorf("create backup dir: %w", err)
	}

	timestamp := time.Now().Format("20060102-150405")
	backupPath := filepath.Join(backupDir, fmt.Sprintf("%s.%s.bak", validatorKeyFile, timestamp))

	if err := os.WriteFile(backupPath, data, 0600); err != nil {
		return fmt.Errorf("write backup: %w", err)
	}

	return nil
}

// ListKeyBackups lists backup key files.
func ListKeyBackups(dataDir string) ([]ConfigFile, error) {
	backupDir := filepath.Join(dataDir, "config", "key-backups")
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read backup dir: %w", err)
	}

	var files []ConfigFile
	for _, entry := range entries {
		if entry.IsDir() {
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
