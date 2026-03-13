package handlers

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/CTJaeger/KleverNodeHub/internal/dashboard"
	"github.com/CTJaeger/KleverNodeHub/internal/dashboard/ws"
	"github.com/CTJaeger/KleverNodeHub/internal/models"
	"github.com/CTJaeger/KleverNodeHub/internal/store"
)

// UpdateHandler handles agent binary update API requests.
type UpdateHandler struct {
	hub            *ws.Hub
	updateStore    *dashboard.UpdateStore
	serverStore    *store.ServerStore
	versionChecker *dashboard.VersionChecker
}

// NewUpdateHandler creates a new UpdateHandler.
func NewUpdateHandler(hub *ws.Hub, updateStore *dashboard.UpdateStore, serverStore *store.ServerStore, vc *dashboard.VersionChecker) *UpdateHandler {
	return &UpdateHandler{
		hub:            hub,
		updateStore:    updateStore,
		serverStore:    serverStore,
		versionChecker: vc,
	}
}

// HandleUploadBinary handles POST /api/agent/upload
// Expects multipart form: version, os, arch, binary (file)
func (h *UpdateHandler) HandleUploadBinary(w http.ResponseWriter, r *http.Request) {
	// Limit to 100MB
	r.Body = http.MaxBytesReader(w, r.Body, 100<<20)

	if err := r.ParseMultipartForm(100 << 20); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "request too large or invalid multipart"})
		return
	}

	version := r.FormValue("version")
	osName := r.FormValue("os")
	arch := r.FormValue("arch")

	if version == "" || osName == "" || arch == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "version, os, and arch are required"})
		return
	}

	file, _, err := r.FormFile("binary")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "binary file is required"})
		return
	}
	defer func() { _ = file.Close() }()

	data, err := io.ReadAll(file)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to read binary"})
		return
	}

	info, err := h.updateStore.Store(version, osName, arch, data)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"version":  info.Version,
		"checksum": info.Checksum,
		"size":     info.Size,
		"os":       info.OS,
		"arch":     info.Arch,
	})
}

// HandleListBinaries handles GET /api/agent/binaries
func (h *UpdateHandler) HandleListBinaries(w http.ResponseWriter, _ *http.Request) {
	binaries := h.updateStore.List()
	if binaries == nil {
		binaries = []*dashboard.AgentBinaryInfo{}
	}
	resp := map[string]any{
		"binaries":            binaries,
		"latest_version":      h.updateStore.LatestVersion(),
		"downloaded_versions": h.updateStore.DownloadedVersions(),
	}
	if latest := h.versionChecker.Latest(); latest != nil {
		resp["latest_release_version"] = latest.TagName
	}
	writeJSON(w, http.StatusOK, resp)
}

// HandleUpdateAgent handles POST /api/agent/update/{server_id}
// Sends the binary to the agent over WebSocket.
// Optional body: { "version": "v0.3.4" } to send a specific version.
func (h *UpdateHandler) HandleUpdateAgent(w http.ResponseWriter, r *http.Request) {
	serverID := r.PathValue("server_id")
	if serverID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "server_id required"})
		return
	}

	if !h.hub.IsConnected(serverID) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "agent is not connected"})
		return
	}

	// Parse optional version from body
	var reqBody struct {
		Version string `json:"version"`
	}
	_ = json.NewDecoder(r.Body).Decode(&reqBody)

	// Get server to determine OS/arch
	srv, err := h.serverStore.GetByID(serverID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "server not found"})
		return
	}

	osName, arch := parseOSArch(srv.OSInfo)
	var binaryData []byte
	var info *dashboard.AgentBinaryInfo
	if reqBody.Version != "" {
		binaryData, info, err = h.updateStore.GetBinaryVersion(reqBody.Version, osName, arch)
	} else {
		binaryData, info, err = h.updateStore.GetBinary(osName, arch)
	}
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf("no binary for %s/%s: %v", osName, arch, err)})
		return
	}

	// Send update command via WebSocket with binary as base64
	encoded := base64.StdEncoding.EncodeToString(binaryData)

	msg := &models.Message{
		ID:     fmt.Sprintf("update-%d", time.Now().UnixNano()),
		Type:   "command",
		Action: "agent.update",
		Payload: map[string]any{
			"version":  info.Version,
			"checksum": info.Checksum,
			"size":     info.Size,
			"data":     encoded,
		},
		Timestamp: time.Now().Unix(),
	}

	result, err := h.hub.SendCommand(serverID, msg, 120*time.Second)
	if err != nil {
		writeJSON(w, http.StatusGatewayTimeout, map[string]string{"error": fmt.Sprintf("update failed: %v", err)})
		return
	}

	if result.Error != "" {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": result.Error})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"output":  result.Output,
		"version": info.Version,
	})
}

// HandleUpdateAll handles POST /api/agent/update/all
// Sequentially updates all connected agents.
// Optional body: { "version": "v0.3.4" } to send a specific version.
func (h *UpdateHandler) HandleUpdateAll(w http.ResponseWriter, r *http.Request) {
	var reqBody struct {
		Version string `json:"version"`
	}
	_ = json.NewDecoder(r.Body).Decode(&reqBody)

	servers, err := h.serverStore.List()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	type updateResult struct {
		ServerID string `json:"server_id"`
		Name     string `json:"name"`
		Success  bool   `json:"success"`
		Error    string `json:"error,omitempty"`
		Version  string `json:"version,omitempty"`
	}

	var results []updateResult

	for _, srv := range servers {
		if !h.hub.IsConnected(srv.ID) {
			results = append(results, updateResult{
				ServerID: srv.ID, Name: srv.Name,
				Success: false, Error: "agent not connected",
			})
			continue
		}

		osName, arch := parseOSArch(srv.OSInfo)
		var binaryData []byte
		var info *dashboard.AgentBinaryInfo
		if reqBody.Version != "" {
			binaryData, info, err = h.updateStore.GetBinaryVersion(reqBody.Version, osName, arch)
		} else {
			binaryData, info, err = h.updateStore.GetBinary(osName, arch)
		}
		if err != nil {
			results = append(results, updateResult{
				ServerID: srv.ID, Name: srv.Name,
				Success: false, Error: fmt.Sprintf("no binary for %s/%s", osName, arch),
			})
			continue
		}

		encoded := base64.StdEncoding.EncodeToString(binaryData)
		msg := &models.Message{
			ID:     fmt.Sprintf("update-%s-%d", srv.ID, time.Now().UnixNano()),
			Type:   "command",
			Action: "agent.update",
			Payload: map[string]any{
				"version":  info.Version,
				"checksum": info.Checksum,
				"size":     info.Size,
				"data":     encoded,
			},
			Timestamp: time.Now().Unix(),
		}

		cmdResult, err := h.hub.SendCommand(srv.ID, msg, 120*time.Second)
		if err != nil {
			results = append(results, updateResult{
				ServerID: srv.ID, Name: srv.Name,
				Success: false, Error: err.Error(),
			})
			continue
		}

		if cmdResult.Error != "" {
			results = append(results, updateResult{
				ServerID: srv.ID, Name: srv.Name,
				Success: false, Error: cmdResult.Error,
			})
		} else {
			results = append(results, updateResult{
				ServerID: srv.ID, Name: srv.Name,
				Success: true, Version: info.Version,
			})
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

// HandleLatestVersion handles GET /api/agent/version
func (h *UpdateHandler) HandleLatestVersion(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"latest_version": h.updateStore.LatestVersion(),
	})
}

// parseOSArch extracts OS and arch from the OSInfo string (e.g. "linux/amd64").
func parseOSArch(osInfo string) (string, string) {
	for i := 0; i < len(osInfo); i++ {
		if osInfo[i] == '/' {
			return osInfo[:i], osInfo[i+1:]
		}
	}
	return "linux", "amd64" // default
}

// ParseOSArch is exported for testing.
func ParseOSArch(osInfo string) (string, string) {
	return parseOSArch(osInfo)
}

// HandleGitHubReleases handles GET /api/agent/releases
// Returns available releases from GitHub with agent binary assets.
func (h *UpdateHandler) HandleGitHubReleases(w http.ResponseWriter, _ *http.Request) {
	releases, err := h.versionChecker.FetchReleases(10)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("fetch releases: %v", err)})
		return
	}

	type assetInfo struct {
		Name string `json:"name"`
		URL  string `json:"url"`
		Size int64  `json:"size"`
		OS   string `json:"os"`
		Arch string `json:"arch"`
	}

	type releaseEntry struct {
		TagName     string      `json:"tag_name"`
		Body        string      `json:"body"`
		PublishedAt string      `json:"published_at"`
		Assets      []assetInfo `json:"assets"`
	}

	var result []releaseEntry
	for _, rel := range releases {
		var agentAssets []assetInfo
		for _, a := range rel.Assets {
			if !strings.HasPrefix(a.Name, "klever-agent-") {
				continue
			}
			// Parse OS/arch from name: klever-agent-linux-amd64
			parts := strings.Split(strings.TrimPrefix(a.Name, "klever-agent-"), "-")
			osName, arch := "", ""
			if len(parts) >= 2 {
				osName = parts[0]
				arch = parts[1]
			}
			agentAssets = append(agentAssets, assetInfo{
				Name: a.Name,
				URL:  a.BrowserDownloadURL,
				Size: a.Size,
				OS:   osName,
				Arch: arch,
			})
		}
		if len(agentAssets) == 0 {
			continue // Skip releases without agent binaries
		}
		result = append(result, releaseEntry{
			TagName:     rel.TagName,
			Body:        rel.Body,
			PublishedAt: rel.PublishAt,
			Assets:      agentAssets,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{"releases": result})
}

// HandleDownloadFromRelease handles POST /api/agent/download-release
// Downloads an agent binary from a GitHub release and stores it locally.
func (h *UpdateHandler) HandleDownloadFromRelease(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL     string `json:"url"`
		Version string `json:"version"`
		OS      string `json:"os"`
		Arch    string `json:"arch"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	if req.URL == "" || req.Version == "" || req.OS == "" || req.Arch == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "url, version, os, and arch are required"})
		return
	}

	// SSRF protection: only allow GitHub release downloads from our repo
	const allowedPrefix = "https://github.com/CTJaeger/KleverNodeHub/releases/download/"
	if !strings.HasPrefix(req.URL, allowedPrefix) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "URL must be a KleverNodeHub GitHub release asset"})
		return
	}

	// Download binary
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Get(req.URL)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("download failed: %v", err)})
		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("download returned %d", resp.StatusCode)})
		return
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, 200<<20)) // 200 MB limit
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("read binary: %v", err)})
		return
	}

	// Try to verify checksum if checksums file is available
	// URL format: https://github.com/CTJaeger/KleverNodeHub/releases/download/v0.3.3/klever-agent-linux-amd64
	urlParts := strings.Split(req.URL, "/")
	if len(urlParts) >= 2 {
		tag := urlParts[len(urlParts)-2]
		filename := urlParts[len(urlParts)-1]
		checksumURL := allowedPrefix + tag + "/checksums.txt"
		actualHash := sha256hex(data)
		if err := verifyAgentChecksum(client, checksumURL, filename, actualHash); err != nil {
			// Checksum verification is best-effort — log but don't block
			// (older releases may not have checksums file)
			_ = err
		}
	}

	info, err := h.updateStore.Store(req.Version, req.OS, req.Arch, data)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"version":  info.Version,
		"checksum": info.Checksum,
		"size":     info.Size,
		"os":       info.OS,
		"arch":     info.Arch,
	})
}

// HandleDownloadReleaseAuto handles POST /api/agent/download-release-auto
// Automatically downloads the right agent binaries for all registered server architectures.
func (h *UpdateHandler) HandleDownloadReleaseAuto(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Tag string `json:"tag"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Tag == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "tag is required"})
		return
	}

	// Determine which OS/arch combos are needed from registered servers
	servers, err := h.serverStore.List()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	needed := map[string]bool{}
	for _, srv := range servers {
		osName, arch := parseOSArch(srv.OSInfo)
		needed[osName+"/"+arch] = true
	}
	if len(needed) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no servers registered"})
		return
	}

	// Fetch releases to find the matching tag
	releases, err := h.versionChecker.FetchReleases(20)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("fetch releases: %v", err)})
		return
	}

	var targetRelease *dashboard.ReleaseInfo
	for i := range releases {
		if releases[i].TagName == req.Tag {
			targetRelease = &releases[i]
			break
		}
	}
	if targetRelease == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf("release %s not found", req.Tag)})
		return
	}

	// Download each needed binary
	const allowedPrefix = "https://github.com/CTJaeger/KleverNodeHub/releases/download/"
	client := &http.Client{Timeout: 5 * time.Minute}

	type dlResult struct {
		OS      string `json:"os"`
		Arch    string `json:"arch"`
		Success bool   `json:"success"`
		Error   string `json:"error,omitempty"`
	}
	var results []dlResult

	for _, asset := range targetRelease.Assets {
		if !strings.HasPrefix(asset.Name, "klever-agent-") {
			continue
		}
		parts := strings.Split(strings.TrimPrefix(asset.Name, "klever-agent-"), "-")
		if len(parts) < 2 {
			continue
		}
		osName, arch := parts[0], parts[1]
		// Strip .exe suffix from arch
		arch = strings.TrimSuffix(arch, ".exe")

		if !needed[osName+"/"+arch] {
			continue
		}

		// SSRF protection
		if !strings.HasPrefix(asset.BrowserDownloadURL, allowedPrefix) {
			results = append(results, dlResult{OS: osName, Arch: arch, Success: false, Error: "invalid URL"})
			continue
		}

		resp, err := client.Get(asset.BrowserDownloadURL)
		if err != nil {
			results = append(results, dlResult{OS: osName, Arch: arch, Success: false, Error: err.Error()})
			continue
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, 200<<20))
		_ = resp.Body.Close()
		if err != nil || resp.StatusCode != http.StatusOK {
			results = append(results, dlResult{OS: osName, Arch: arch, Success: false, Error: fmt.Sprintf("download failed (%d)", resp.StatusCode)})
			continue
		}

		_, err = h.updateStore.Store(req.Tag, osName, arch, data)
		if err != nil {
			results = append(results, dlResult{OS: osName, Arch: arch, Success: false, Error: err.Error()})
			continue
		}

		results = append(results, dlResult{OS: osName, Arch: arch, Success: true})
	}

	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

func sha256hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// verifyAgentChecksum downloads the checksums file and verifies the binary hash.
func verifyAgentChecksum(client *http.Client, checksumURL, filename, actualHash string) error {
	resp, err := client.Get(checksumURL)
	if err != nil {
		return fmt.Errorf("download checksums: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("checksums returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read checksums: %w", err)
	}

	for _, line := range strings.Split(string(body), "\n") {
		parts := strings.Fields(line)
		if len(parts) == 2 && parts[1] == filename {
			if parts[0] != actualHash {
				return fmt.Errorf("checksum mismatch: expected %s, got %s", parts[0], actualHash)
			}
			return nil // Match found, checksum valid
		}
	}

	return fmt.Errorf("file %s not found in checksums", filename)
}
