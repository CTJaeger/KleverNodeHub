package handlers

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/CTJaeger/KleverNodeHub/internal/dashboard"
	"github.com/CTJaeger/KleverNodeHub/internal/dashboard/ws"
	"github.com/CTJaeger/KleverNodeHub/internal/models"
	"github.com/CTJaeger/KleverNodeHub/internal/store"
)

// UpdateHandler handles agent binary update API requests.
type UpdateHandler struct {
	hub         *ws.Hub
	updateStore *dashboard.UpdateStore
	serverStore *store.ServerStore
}

// NewUpdateHandler creates a new UpdateHandler.
func NewUpdateHandler(hub *ws.Hub, updateStore *dashboard.UpdateStore, serverStore *store.ServerStore) *UpdateHandler {
	return &UpdateHandler{
		hub:         hub,
		updateStore: updateStore,
		serverStore: serverStore,
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
	writeJSON(w, http.StatusOK, map[string]any{
		"binaries":       binaries,
		"latest_version": h.updateStore.LatestVersion(),
	})
}

// HandleUpdateAgent handles POST /api/agent/update/{server_id}
// Sends the binary to the agent over WebSocket.
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

	// Get server to determine OS/arch
	srv, err := h.serverStore.GetByID(serverID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "server not found"})
		return
	}

	osName, arch := parseOSArch(srv.OSInfo)
	binaryData, info, err := h.updateStore.GetBinary(osName, arch)
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
func (h *UpdateHandler) HandleUpdateAll(w http.ResponseWriter, _ *http.Request) {
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
		binaryData, info, err := h.updateStore.GetBinary(osName, arch)
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
