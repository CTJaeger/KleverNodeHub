package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/CTJaeger/KleverNodeHub/internal/dashboard/ws"
	"github.com/CTJaeger/KleverNodeHub/internal/models"
)

// ProvisionHandler handles node provisioning requests.
type ProvisionHandler struct {
	hub *ws.Hub
}

// NewProvisionHandler creates a new provision handler.
func NewProvisionHandler(hub *ws.Hub) *ProvisionHandler {
	return &ProvisionHandler{hub: hub}
}

// HandleProvision starts a provisioning job on the target server's agent.
// POST /api/nodes/provision
func (h *ProvisionHandler) HandleProvision(w http.ResponseWriter, r *http.Request) {
	var req models.ProvisionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	// Validate required fields
	if req.ServerID == "" {
		http.Error(w, "server_id is required", http.StatusBadRequest)
		return
	}
	if req.NodeName == "" {
		http.Error(w, "node_name is required", http.StatusBadRequest)
		return
	}
	if req.Network == "" {
		req.Network = "mainnet"
	}
	if req.ImageTag == "" {
		req.ImageTag = "latest"
	}
	if req.Port <= 0 {
		req.Port = 8080
	}

	// Check agent is connected
	if !h.hub.IsConnected(req.ServerID) {
		http.Error(w, "server agent is not connected", http.StatusServiceUnavailable)
		return
	}

	// Build command message
	jobID := fmt.Sprintf("prov-%d", time.Now().UnixNano())

	msg := &models.Message{
		ID:     jobID,
		Type:   "command",
		Action: "node.provision",
		Payload: map[string]any{
			"server_id":        req.ServerID,
			"node_name":        req.NodeName,
			"network":          req.Network,
			"image_tag":        req.ImageTag,
			"port":             req.Port,
			"generate_keys":    req.GenerateKeys,
			"config_overrides": req.ConfigOverrides,
		},
		Timestamp: time.Now().Unix(),
	}

	// Send command to agent with extended timeout for provisioning
	result, err := h.hub.SendCommand(req.ServerID, msg, 10*time.Minute)
	if err != nil {
		http.Error(w, "provisioning failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"job_id":  jobID,
		"success": result.Success,
		"output":  result.Output,
		"error":   result.Error,
	})
}
