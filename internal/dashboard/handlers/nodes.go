package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/CTJaeger/KleverNodeHub/internal/dashboard/ws"
	"github.com/CTJaeger/KleverNodeHub/internal/models"
	"github.com/CTJaeger/KleverNodeHub/internal/store"
)

const commandTimeout = 60 * time.Second

// NodeHandler handles node operation API requests.
type NodeHandler struct {
	hub       *ws.Hub
	nodeStore *store.NodeStore
}

// NewNodeHandler creates a new NodeHandler.
func NewNodeHandler(hub *ws.Hub, nodeStore *store.NodeStore) *NodeHandler {
	return &NodeHandler{
		hub:       hub,
		nodeStore: nodeStore,
	}
}

// nodeActionRequest is the request body for node actions.
type nodeActionRequest struct {
	Action string `json:"action,omitempty"` // Used only for batch
}

// batchRequest is the request body for batch operations.
type batchRequest struct {
	Action  string   `json:"action"`   // "node.start", "node.stop", "node.restart"
	NodeIDs []string `json:"node_ids"` // List of node IDs
}

// batchResultEntry is one result in a batch response.
type batchResultEntry struct {
	NodeID  string `json:"node_id"`
	Success bool   `json:"success"`
	Output  string `json:"output,omitempty"`
	Error   string `json:"error,omitempty"`
}

// HandleStart handles POST /api/nodes/:id/start
func (h *NodeHandler) HandleStart(w http.ResponseWriter, r *http.Request) {
	h.handleNodeAction(w, r, "node.start")
}

// HandleStop handles POST /api/nodes/:id/stop
func (h *NodeHandler) HandleStop(w http.ResponseWriter, r *http.Request) {
	h.handleNodeAction(w, r, "node.stop")
}

// HandleRestart handles POST /api/nodes/:id/restart
func (h *NodeHandler) HandleRestart(w http.ResponseWriter, r *http.Request) {
	h.handleNodeAction(w, r, "node.restart")
}

// HandleBatch handles POST /api/nodes/batch
func (h *NodeHandler) HandleBatch(w http.ResponseWriter, r *http.Request) {
	var req batchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	if req.Action == "" || len(req.NodeIDs) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "action and node_ids required"})
		return
	}

	results := make([]batchResultEntry, 0, len(req.NodeIDs))
	for _, nodeID := range req.NodeIDs {
		result := h.executeNodeCommand(nodeID, req.Action)
		results = append(results, result)
	}

	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

// handleNodeAction is the shared handler for single node operations.
func (h *NodeHandler) handleNodeAction(w http.ResponseWriter, r *http.Request, action string) {
	nodeID := extractNodeID(r)
	if nodeID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing node ID"})
		return
	}

	result := h.executeNodeCommand(nodeID, action)
	if result.Error != "" {
		status := http.StatusInternalServerError
		if result.Error == "node not found" {
			status = http.StatusNotFound
		} else if containsOffline(result.Error) {
			status = http.StatusServiceUnavailable
		}
		writeJSON(w, status, result)
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// executeNodeCommand looks up the node, finds the agent, and sends the command.
func (h *NodeHandler) executeNodeCommand(nodeID, action string) batchResultEntry {
	result := batchResultEntry{NodeID: nodeID}

	// Look up node
	node, err := h.nodeStore.GetByID(nodeID)
	if err != nil {
		result.Error = "node not found"
		return result
	}

	// Check agent is online
	if !h.hub.IsConnected(node.ServerID) {
		result.Error = fmt.Sprintf("agent offline for server %s", node.ServerID)
		return result
	}

	// Build command message
	msg := &models.Message{
		ID:     fmt.Sprintf("cmd-%s-%d", action, time.Now().UnixNano()),
		Type:   "command",
		Action: action,
		Payload: map[string]string{
			"container_name": node.ContainerName,
		},
		Timestamp: time.Now().Unix(),
	}

	// Send and wait for result
	cmdResult, err := h.hub.SendCommand(node.ServerID, msg, commandTimeout)
	if err != nil {
		result.Error = err.Error()
		return result
	}

	result.Success = cmdResult.Success
	result.Output = cmdResult.Output
	result.Error = cmdResult.Error

	// Update node status in DB if successful
	if cmdResult.Success && cmdResult.Output != "" {
		h.nodeStore.UpdateStatus(nodeID, cmdResult.Output)
	}

	return result
}

// extractNodeID extracts the node ID from the URL path.
// Expects URL pattern: /api/nodes/{id}/action
func extractNodeID(r *http.Request) string {
	// Use PathValue for Go 1.22+ routing
	if id := r.PathValue("id"); id != "" {
		return id
	}
	return ""
}

func containsOffline(s string) bool {
	return len(s) >= 7 && (s[:7] == "agent o" || s == "agent offline")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
