package handlers

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/CTJaeger/KleverNodeHub/internal/dashboard"
	"github.com/CTJaeger/KleverNodeHub/internal/version"
)

// SystemHandler handles system-level API endpoints.
type SystemHandler struct {
	versionChecker *dashboard.VersionChecker
}

// NewSystemHandler creates a new system handler.
func NewSystemHandler(vc *dashboard.VersionChecker) *SystemHandler {
	return &SystemHandler{versionChecker: vc}
}

// HandleVersionInfo returns current version and update availability.
// GET /api/system/version
func (h *SystemHandler) HandleVersionInfo(w http.ResponseWriter, _ *http.Request) {
	info := version.Get()
	result := map[string]any{
		"version":    info.Version,
		"git_commit": info.GitCommit,
		"build_time": info.BuildTime,
		"go_version": info.GoVersion,
		"os":         info.OS,
		"arch":       info.Arch,
		"uptime":     version.Uptime().String(),
		"is_docker":  isRunningInDocker(),
	}

	latest := h.versionChecker.Latest()
	if latest != nil {
		result["latest_version"] = latest.TagName
		result["has_update"] = latest.HasUpdate
		result["release_url"] = latest.HTMLURL
		result["checked_at"] = latest.CheckedAt.Format(time.RFC3339)
	}

	writeJSON(w, http.StatusOK, result)
}

// HandleSelfUpdate downloads and applies a self-update.
// POST /api/system/update
func (h *SystemHandler) HandleSelfUpdate(w http.ResponseWriter, _ *http.Request) {
	if isRunningInDocker() && !dockerSelfUpdateAvailable() {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "self-update requires Docker socket — mount /var/run/docker.sock",
		})
		return
	}

	// Force a fresh check so assets are up-to-date (CI may still be uploading when the user clicks update).
	h.versionChecker.ForceCheck()

	latest := h.versionChecker.Latest()
	if latest == nil || !latest.HasUpdate {
		writeJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "no update available",
		})
		return
	}

	// Docker mode: pull new image and recreate container (no binary needed)
	if isRunningInDocker() {
		log.Printf("self-update (docker): updating to %s", latest.TagName)

		writeJSON(w, http.StatusOK, map[string]any{
			"success":     true,
			"new_version": latest.TagName,
			"message":     "pulling image and recreating container...",
		})

		go func() {
			time.Sleep(500 * time.Millisecond)
			if err := dockerSelfUpdate(latest.TagName); err != nil {
				log.Printf("self-update (docker): FAILED: %v", err)
			}
		}()
		return
	}

	// Find the right asset for this OS/arch
	downloadURL := latest.FindAsset("klever-node-hub", runtime.GOOS, runtime.GOARCH)
	if downloadURL == "" {
		writeJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": fmt.Sprintf("no binary available for %s/%s", runtime.GOOS, runtime.GOARCH),
		})
		return
	}

	// Find checksum file
	checksumURL := ""
	for _, a := range latest.Assets {
		if strings.Contains(a.Name, "checksums") {
			checksumURL = a.BrowserDownloadURL
			break
		}
	}

	log.Printf("self-update: downloading %s from %s", latest.TagName, downloadURL)

	// Download binary
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Get(downloadURL)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "download failed: " + err.Error()})
		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("download returned %d", resp.StatusCode)})
		return
	}

	newBinary, err := io.ReadAll(io.LimitReader(resp.Body, 200<<20)) // 200 MB limit
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "read binary: " + err.Error()})
		return
	}

	// Verify checksum if available
	actualHash := sha256Sum(newBinary)
	if checksumURL != "" {
		if err := verifyChecksum(client, checksumURL, downloadURL, actualHash); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "checksum verification failed: " + err.Error()})
			return
		}
		log.Printf("self-update: checksum verified (%s)", actualHash[:12])
	}

	// Get current executable path
	execPath, err := os.Executable()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "cannot determine executable path: " + err.Error()})
		return
	}

	// Write new binary to temp file next to current executable
	tmpPath := execPath + ".update"
	if err := os.WriteFile(tmpPath, newBinary, 0755); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "write update: " + err.Error()})
		return
	}

	// Backup current binary
	backupPath := execPath + ".bak"
	if err := os.Rename(execPath, backupPath); err != nil {
		_ = os.Remove(tmpPath)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "backup current binary: " + err.Error()})
		return
	}

	// Move new binary into place
	if err := os.Rename(tmpPath, execPath); err != nil {
		// Try to restore backup
		_ = os.Rename(backupPath, execPath)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "install update: " + err.Error()})
		return
	}

	log.Printf("self-update: installed %s, restarting...", latest.TagName)

	// Send success response before restarting
	writeJSON(w, http.StatusOK, map[string]any{
		"success":     true,
		"new_version": latest.TagName,
		"message":     "update installed, restarting...",
	})

	// Schedule restart after response is sent
	go func() {
		time.Sleep(500 * time.Millisecond)
		restartProcess(execPath)
	}()
}

// HandleCheckUpdate forces a version re-check and returns the result.
// POST /api/system/check-update
func (h *SystemHandler) HandleCheckUpdate(w http.ResponseWriter, _ *http.Request) {
	h.versionChecker.ForceCheck()
	h.HandleVersionInfo(w, nil)
}

func sha256Sum(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func verifyChecksum(client *http.Client, checksumURL, binaryURL, actualHash string) error {
	resp, err := client.Get(checksumURL)
	if err != nil {
		return fmt.Errorf("download checksums: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read checksums: %w", err)
	}

	// Parse checksum file: "hash  filename" per line
	// Find the line matching our binary
	binaryName := binaryURL[strings.LastIndex(binaryURL, "/")+1:]
	for _, line := range strings.Split(string(body), "\n") {
		parts := strings.Fields(line)
		if len(parts) >= 2 && parts[1] == binaryName {
			expectedHash := parts[0]
			if !strings.EqualFold(actualHash, expectedHash) {
				return fmt.Errorf("hash mismatch: expected %s, got %s", expectedHash, actualHash)
			}
			return nil
		}
	}

	return fmt.Errorf("binary %s not found in checksum file", binaryName)
}
