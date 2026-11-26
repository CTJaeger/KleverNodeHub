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
	"github.com/CTJaeger/KleverNodeHub/internal/release"
	"github.com/CTJaeger/KleverNodeHub/internal/store"
	"github.com/CTJaeger/KleverNodeHub/internal/version"
)

// SystemHandler handles system-level API endpoints.
type SystemHandler struct {
	versionChecker *dashboard.VersionChecker
	serverStore    *store.ServerStore
}

// SetServerStore gives the handler the registered servers, needed to hold back
// the move to the new repository while agents still need the bridge release.
func (h *SystemHandler) SetServerStore(s *store.ServerStore) { h.serverStore = s }

// agentsBlockingMove lists the servers whose agent can no longer be updated
// once this dashboard runs a release of the new repository: v0.3.89 up to
// (not including) the bridge release verify updates themselves but only trust
// the previous release key. Older agents don't verify and can follow later.
func (h *SystemHandler) agentsBlockingMove(target string) ([]string, error) {
	if h.serverStore == nil || !isSuccessorTag(target) {
		return nil, nil
	}
	servers, err := h.serverStore.List()
	if err != nil {
		return nil, err
	}
	var names []string
	for _, s := range servers {
		v := s.AgentVersion
		if version.Compare(v, "v0.3.89") >= 0 && version.Compare(v, agentBridgeVersion) < 0 {
			name := s.DisplayName
			if name == "" {
				name = s.Name
			}
			names = append(names, fmt.Sprintf("%s (%s)", name, v))
		}
	}
	return names, nil
}

// NewSystemHandler creates a new system handler.
func NewSystemHandler(vc *dashboard.VersionChecker) *SystemHandler {
	return &SystemHandler{versionChecker: vc}
}

// HandleVersionInfo returns current version and update availability.
// GET /api/system/version
func (h *SystemHandler) HandleVersionInfo(w http.ResponseWriter, _ *http.Request) {
	info := version.Get()
	inDocker := isRunningInDocker()
	result := map[string]any{
		"version":    info.Version,
		"git_commit": info.GitCommit,
		"build_time": info.BuildTime,
		"go_version": info.GoVersion,
		"os":         info.OS,
		"arch":       info.Arch,
		"uptime":     version.Uptime().String(),
		"is_docker":  inDocker,
	}
	// Only meaningful inside Docker — true when /var/run/docker.sock is mounted,
	// which lets the dashboard pull a new image and recreate its own container.
	if inDocker {
		result["docker_self_update_available"] = dockerSelfUpdateAvailable()
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

	// Moving to the new repository: agents that would be cut off from updates
	// have to reach the bridge release first.
	blocking, err := h.agentsBlockingMove(latest.TagName)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": err.Error()})
		return
	}
	if len(blocking) > 0 {
		writeJSON(w, http.StatusConflict, map[string]any{
			"success": false,
			"message": fmt.Sprintf("First update the agents to %s (Agent Update), then the dashboard. After the update to %s the dashboard could no longer update these agents: %s. Remove servers you no longer use.",
				agentBridgeVersion, latest.TagName, strings.Join(blocking, ", ")),
		})
		return
	}

	// Docker mode: pull new image and recreate container (no binary needed)
	if isRunningInDocker() {
		// Single-flight: prevent two clicks from racing through the rename /
		// create / helper-spawn sequence concurrently. Unlock happens in the
		// goroutine; on the happy path the goroutine never returns (process is
		// killed mid-stop) and the lock leaks, which is fine — next dashboard
		// container starts with a fresh mutex.
		if !dockerSelfUpdateMu.TryLock() {
			writeJSON(w, http.StatusConflict, map[string]any{
				"success": false,
				"message": "a docker self-update is already in progress",
			})
			return
		}
		log.Printf("self-update (docker): updating to %s", latest.TagName)

		writeJSON(w, http.StatusOK, map[string]any{
			"success":     true,
			"new_version": latest.TagName,
			"message":     "pulling image and recreating container...",
		})

		go func() {
			defer dockerSelfUpdateMu.Unlock()
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

	// Only a binary listed in the signed checksums of this release is installed.
	sums, sig, err := fetchSignedChecksums(client, latest.TagName)
	if err == nil {
		err = release.VerifyAsset(sums, sig, release.HubAssetName(runtime.GOOS, runtime.GOARCH), newBinary)
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "release verification failed: " + err.Error()})
		return
	}
	log.Printf("self-update: release signature and checksum verified (%s)", sha256Sum(newBinary)[:12])

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
