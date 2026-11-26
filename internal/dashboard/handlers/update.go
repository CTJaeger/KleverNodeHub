package handlers

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/CTJaeger/KleverNodeHub/internal/dashboard"
	"github.com/CTJaeger/KleverNodeHub/internal/dashboard/ws"
	"github.com/CTJaeger/KleverNodeHub/internal/models"
	"github.com/CTJaeger/KleverNodeHub/internal/release"
	"github.com/CTJaeger/KleverNodeHub/internal/store"
	"github.com/CTJaeger/KleverNodeHub/internal/version"
)

// Agent and dashboard binaries are downloaded only from these two places:
// <prefix><tag>/<asset>. Development continues at Test0rMaik/KleverNodeHub,
// whose tags carry "-knh."; CTJaeger/KleverNodeHub keeps the earlier releases
// up to the bridge release.
const (
	upstreamDownloadPrefix  = "https://github.com/CTJaeger/KleverNodeHub/releases/download/"
	successorDownloadPrefix = "https://github.com/Test0rMaik/KleverNodeHub/releases/download/"
)

// isSuccessorTag reports whether a release tag belongs to Test0rMaik/KleverNodeHub.
func isSuccessorTag(tag string) bool { return strings.Contains(tag, "-knh.") }

// downloadPrefixFor returns where the release with this tag is published.
func downloadPrefixFor(tag string) string {
	if isSuccessorTag(tag) {
		return successorDownloadPrefix
	}
	return upstreamDownloadPrefix
}

// agentBridgeVersion is the first agent that trusts the release keys of both
// repositories. Agents before it only accept releases of CTJaeger/KleverNodeHub
// (from v0.3.89 on they check the signature themselves), so they are moved to
// this version first and to the newer releases in a second update.
const agentBridgeVersion = "v0.3.93"

// needsBridge reports whether an agent running agentVersion has to go through
// agentBridgeVersion before it can install target.
func needsBridge(agentVersion, target string) bool {
	return version.Compare(agentVersion, agentBridgeVersion) < 0 && version.Compare(target, agentBridgeVersion) > 0
}

// UpdateHandler handles agent binary update API requests.
type UpdateHandler struct {
	hub            *ws.Hub
	updateStore    *dashboard.UpdateStore
	serverStore    *store.ServerStore
	versionChecker *dashboard.VersionChecker

	// allowUnsigned enables binary upload. Off by default: an uploaded binary
	// carries no release signature, so agents refuse it unless their own
	// config allows unsigned updates.
	allowUnsigned bool
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

// SetAllowUnsignedBinaries enables agent binary upload
// (--allow-unsigned-agent-binaries).
func (h *UpdateHandler) SetAllowUnsignedBinaries(v bool) { h.allowUnsigned = v }

// errUnsignedDisabled is returned by the upload route.
const errUnsignedDisabled = "custom agent binaries are disabled: only signed releases can be installed (start the dashboard with --allow-unsigned-agent-binaries to enable)"

// errUnsignedStored is returned when a stored binary carries no release
// signature: it is never handed to an agent unless unsigned builds are enabled.
const errUnsignedStored = "stored agent binary %s is not from a signed release: download the release again (Agent Update) and retry"

// deliverable reports whether a stored binary may be handed to agents.
func (h *UpdateHandler) deliverable(info *dashboard.AgentBinaryInfo) bool {
	return info != nil && (info.Signed() || h.allowUnsigned)
}

// buildAgentUpdatePayload chooses the update transport per agent. If the agent
// advertised the http-update capability it gets a download_path and pulls the
// binary over a separate HTTP request (keeping the ~15 MB off the WebSocket);
// otherwise it gets the binary base64-encoded inline, exactly as before — so
// older agents keep working with no change on their side.
func (h *UpdateHandler) buildAgentUpdatePayload(serverID string, binaryData []byte, info *dashboard.AgentBinaryInfo) map[string]any {
	payload := map[string]any{
		"version":  info.Version,
		"checksum": info.Checksum,
		"size":     info.Size,
	}
	// Agents that verify release signatures check these themselves; older
	// agents ignore the extra fields and keep relying on the checksum.
	if info.Signed() {
		payload["release_asset"] = info.Asset
		payload["release_checksums"] = info.Checksums
		payload["release_signature"] = info.Signature
	}
	if h.hub.AgentHasCapability(serverID, models.CapabilityHTTPUpdate) {
		payload["download_path"] = fmt.Sprintf("/api/agent/binary/%s?version=%s", serverID, url.QueryEscape(info.Version))
	} else {
		payload["data"] = base64.StdEncoding.EncodeToString(binaryData)
	}
	return payload
}

// binaryFor picks the stored agent binary for one agent: the requested version
// (or the newest stored one), or the bridge version first if the agent cannot
// install that yet.
func (h *UpdateHandler) binaryFor(agentVersion, requested, osName, arch string) ([]byte, *dashboard.AgentBinaryInfo, error) {
	target := requested
	if target == "" {
		info := h.updateStore.Get(osName, arch)
		if info == nil {
			return nil, nil, fmt.Errorf("no binary for %s/%s", osName, arch)
		}
		target = info.Version
	}
	if needsBridge(agentVersion, target) {
		target = agentBridgeVersion
	}
	return h.updateStore.GetBinaryVersion(target, osName, arch)
}

// HandleUploadBinary handles POST /api/agent/upload
// Expects multipart form: version, os, arch, binary (file)
func (h *UpdateHandler) HandleUploadBinary(w http.ResponseWriter, r *http.Request) {
	if !h.allowUnsigned {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": errUnsignedDisabled})
		return
	}
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

// HandleAgentBinary handles GET /api/agent/binary/{server_id}?version=...
// Called by the AGENT (not the browser) to fetch its update binary over a
// separate HTTP request instead of receiving it base64-encoded inside the
// WebSocket command — this keeps the ~15 MB transfer off the WS connection so it
// can't hold the write lock, stall pongs and get the agent dropped. Auth is by
// server_id existence (the same model the WebSocket upgrade uses); the agent
// binary is a public release artifact anyway. Deliberately NOT behind the
// browser-session auth middleware, since the agent has no browser session.
func (h *UpdateHandler) HandleAgentBinary(w http.ResponseWriter, r *http.Request) {
	serverID := r.PathValue("server_id")
	srv, err := h.serverStore.GetByID(serverID)
	if err != nil {
		http.Error(w, "unknown server", http.StatusNotFound)
		return
	}

	osName, arch := parseOSArch(srv.OSInfo)
	version := r.URL.Query().Get("version")
	var binaryData []byte
	var info *dashboard.AgentBinaryInfo
	if version != "" {
		binaryData, info, err = h.updateStore.GetBinaryVersion(version, osName, arch)
	} else {
		binaryData, info, err = h.updateStore.GetBinary(osName, arch)
	}
	if err != nil {
		http.Error(w, fmt.Sprintf("no binary for %s/%s: %v", osName, arch, err), http.StatusNotFound)
		return
	}
	if !h.deliverable(info) {
		http.Error(w, fmt.Sprintf(errUnsignedStored, info.Version), http.StatusForbidden)
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Agent-Version", info.Version)
	w.Header().Set("X-Agent-Checksum", info.Checksum)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(binaryData)))
	_, _ = w.Write(binaryData)
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
	binaryData, info, err := h.binaryFor(srv.AgentVersion, reqBody.Version, osName, arch)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf("no binary for %s/%s: %v", osName, arch, err)})
		return
	}
	if !h.deliverable(info) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": fmt.Sprintf(errUnsignedStored, info.Version)})
		return
	}

	// Send update command via WebSocket. Transport (HTTP download vs. inline
	// base64) is chosen per agent based on its advertised capabilities.
	msg := &models.Message{
		ID:        fmt.Sprintf("update-%d", time.Now().UnixNano()),
		Type:      "command",
		Action:    "agent.update",
		Payload:   h.buildAgentUpdatePayload(serverID, binaryData, info),
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

// HandleRestartAgent handles POST /api/agent/restart/{server_id}
// Sends a restart command to the agent over WebSocket. The agent acknowledges
// the command and then re-execs itself; systemd (or Docker --restart) brings
// it back up and the WebSocket reconnects on its own.
func (h *UpdateHandler) HandleRestartAgent(w http.ResponseWriter, r *http.Request) {
	serverID := r.PathValue("server_id")
	if serverID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "server_id required"})
		return
	}

	if !h.hub.IsConnected(serverID) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "agent is not connected"})
		return
	}

	msg := &models.Message{
		ID:        fmt.Sprintf("restart-%d", time.Now().UnixNano()),
		Type:      "command",
		Action:    "agent.restart",
		Timestamp: time.Now().Unix(),
	}

	// Short timeout — the agent acks before re-execing, so we expect a quick reply.
	result, err := h.hub.SendCommand(serverID, msg, 10*time.Second)
	if err != nil {
		writeJSON(w, http.StatusGatewayTimeout, map[string]string{"error": fmt.Sprintf("restart failed: %v", err)})
		return
	}

	if result.Error != "" {
		// An agent predating this feature rejects agent.restart at the
		// command whitelist. Translate that into a clear "update the agent"
		// message instead of leaking the raw validation error.
		if strings.Contains(result.Error, "command not allowed") {
			writeJSON(w, http.StatusConflict, map[string]string{
				"error": "this agent version does not support restart — update the agent first",
			})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": result.Error})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": "agent restarting",
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
		binaryData, info, err := h.binaryFor(srv.AgentVersion, reqBody.Version, osName, arch)
		if err != nil {
			results = append(results, updateResult{
				ServerID: srv.ID, Name: srv.Name,
				Success: false, Error: fmt.Sprintf("no binary for %s/%s", osName, arch),
			})
			continue
		}
		if !h.deliverable(info) {
			results = append(results, updateResult{
				ServerID: srv.ID, Name: srv.Name,
				Success: false, Error: fmt.Sprintf(errUnsignedStored, info.Version),
			})
			continue
		}

		msg := &models.Message{
			ID:        fmt.Sprintf("update-%s-%d", srv.ID, time.Now().UnixNano()),
			Type:      "command",
			Action:    "agent.update",
			Payload:   h.buildAgentUpdatePayload(srv.ID, binaryData, info),
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

// parseAgentAssetName checks if a release asset name is an agent binary
// and extracts the OS and arch. Supports both "agent-linux-amd64" and
// "klever-agent-linux-amd64" naming conventions.
func parseAgentAssetName(name string) (osName, arch string, ok bool) {
	var suffix string
	switch {
	case strings.HasPrefix(name, "klever-agent-"):
		suffix = strings.TrimPrefix(name, "klever-agent-")
	case strings.HasPrefix(name, "agent-"):
		suffix = strings.TrimPrefix(name, "agent-")
	default:
		return "", "", false
	}
	parts := strings.Split(suffix, "-")
	if len(parts) < 2 {
		return "", "", false
	}
	return parts[0], strings.TrimSuffix(parts[1], ".exe"), true
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
			osName, arch, isAgent := parseAgentAssetName(a.Name)
			if !isAgent {
				continue
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
	if !validReleaseURL(req.URL) {
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

	// Only a binary listed in the signed checksums of its release is stored.
	// URL format: <prefix><tag>/<asset>
	tag, asset := releaseURLParts(req.URL)
	sums, sig, err := fetchSignedChecksums(client, tag)
	if err == nil {
		err = release.VerifyAsset(sums, sig, asset, data)
	}
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "release verification failed: " + err.Error()})
		return
	}

	info, err := h.updateStore.StoreRelease(req.Version, req.OS, req.Arch, data, asset, sums, sig)
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

	results, err := h.downloadAgentRelease(targetRelease, needed)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "release verification failed: " + err.Error()})
		return
	}

	// Agents before the bridge version cannot install a newer release yet:
	// fetch the bridge release for their platforms as well, so "update" can
	// move them there first.
	if needsBridgeAny(servers, req.Tag) {
		bridgeNeeded := map[string]bool{}
		for _, srv := range servers {
			if needsBridge(srv.AgentVersion, req.Tag) {
				osName, arch := parseOSArch(srv.OSInfo)
				bridgeNeeded[osName+"/"+arch] = true
			}
		}
		var bridgeRelease *dashboard.ReleaseInfo
		for i := range releases {
			if releases[i].TagName == agentBridgeVersion {
				bridgeRelease = &releases[i]
				break
			}
		}
		if bridgeRelease == nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("release %s (needed for agents older than it) not found", agentBridgeVersion)})
			return
		}
		more, err := h.downloadAgentRelease(bridgeRelease, bridgeNeeded)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "release verification failed: " + err.Error()})
			return
		}
		results = append(results, more...)
	}

	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

// agentDownloadResult reports one per-platform release download.
type agentDownloadResult struct {
	Version string `json:"version"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

// needsBridgeAny reports whether any registered agent has to go through the
// bridge version before it can install target.
func needsBridgeAny(servers []models.Server, target string) bool {
	for _, srv := range servers {
		if needsBridge(srv.AgentVersion, target) {
			return true
		}
	}
	return false
}

// downloadAgentRelease downloads the agent binaries of rel for the needed
// os/arch combinations, verifies them against the signed checksums of that
// release and stores them.
func (h *UpdateHandler) downloadAgentRelease(rel *dashboard.ReleaseInfo, needed map[string]bool) ([]agentDownloadResult, error) {
	client := &http.Client{Timeout: 5 * time.Minute}
	sums, sig, err := fetchSignedChecksums(client, rel.TagName)
	if err != nil {
		return nil, err
	}

	var results []agentDownloadResult
	for _, asset := range rel.Assets {
		osName, arch, isAgent := parseAgentAssetName(asset.Name)
		if !isAgent || !needed[osName+"/"+arch] {
			continue
		}
		res := agentDownloadResult{Version: rel.TagName, OS: osName, Arch: arch}

		// SSRF protection
		if !validReleaseURL(asset.BrowserDownloadURL) {
			res.Error = "invalid URL"
			results = append(results, res)
			continue
		}

		resp, err := client.Get(asset.BrowserDownloadURL)
		if err != nil {
			res.Error = err.Error()
			results = append(results, res)
			continue
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, 200<<20))
		_ = resp.Body.Close()
		if err != nil || resp.StatusCode != http.StatusOK {
			res.Error = fmt.Sprintf("download failed (%d)", resp.StatusCode)
			results = append(results, res)
			continue
		}

		if verr := release.VerifyAsset(sums, sig, asset.Name, data); verr != nil {
			res.Error = verr.Error()
			results = append(results, res)
			continue
		}

		if _, err := h.updateStore.StoreRelease(rel.TagName, osName, arch, data, asset.Name, sums, sig); err != nil {
			res.Error = err.Error()
			results = append(results, res)
			continue
		}
		res.Success = true
		results = append(results, res)
	}
	return results, nil
}

// releaseURLParts splits <prefix><tag>/<asset> into tag and asset.
func releaseURLParts(u string) (tag, asset string) {
	for _, prefix := range []string{upstreamDownloadPrefix, successorDownloadPrefix} {
		if rest, ok := strings.CutPrefix(u, prefix); ok {
			tag, asset, _ = strings.Cut(rest, "/")
			return tag, asset
		}
	}
	return "", ""
}

// validReleaseURL reports whether u is exactly an asset download URL of the
// releases: right host/owner/repo for its tag, no query/fragment, no dot
// segments.
func validReleaseURL(u string) bool {
	tag, asset := releaseURLParts(u)
	if tag == "" || asset == "" || strings.Contains(asset, "/") {
		return false
	}
	if !strings.HasPrefix(u, downloadPrefixFor(tag)) {
		return false
	}
	pu, err := url.Parse(u)
	if err != nil || pu.Host != "github.com" || pu.RawQuery != "" || pu.Fragment != "" {
		return false
	}
	return path.Clean(pu.Path) == pu.Path
}

// fetchSignedChecksums downloads a release's checksums.txt and its signature
// and verifies the signature. Both files are required: an unsigned release
// is never installed.
func fetchSignedChecksums(client *http.Client, tag string) (sums, sig []byte, err error) {
	if tag == "" || strings.ContainsAny(tag, "/?#") || tag != path.Clean(tag) {
		return nil, nil, fmt.Errorf("invalid release tag %q", tag)
	}
	get := func(name string) ([]byte, error) {
		resp, err := client.Get(downloadPrefixFor(tag) + tag + "/" + name)
		if err != nil {
			return nil, fmt.Errorf("download %s: %w", name, err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode == http.StatusNotFound && name == release.SignatureFile {
			return nil, fmt.Errorf("release %s is not signed (releases before v0.3.89 carry no signature)", tag)
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("download %s: HTTP %d", name, resp.StatusCode)
		}
		return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	}
	if sums, err = get(release.ChecksumsFile); err != nil {
		return nil, nil, err
	}
	if sig, err = get(release.SignatureFile); err != nil {
		return nil, nil, err
	}
	if err := release.VerifyChecksums(sums, sig); err != nil {
		return nil, nil, err
	}
	// The signed version line must name this tag, or an older release's
	// signed files could be served under a new tag.
	if got := release.Tag(sums); got != tag {
		return nil, nil, fmt.Errorf("signed checksums are for release %q, not %q", got, tag)
	}
	return sums, sig, nil
}
