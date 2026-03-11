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

const configCommandTimeout = 30 * time.Second

// ConfigHandler handles configuration management API requests.
type ConfigHandler struct {
	hub       *ws.Hub
	nodeStore *store.NodeStore
}

// NewConfigHandler creates a new ConfigHandler.
func NewConfigHandler(hub *ws.Hub, nodeStore *store.NodeStore) *ConfigHandler {
	return &ConfigHandler{
		hub:       hub,
		nodeStore: nodeStore,
	}
}

// HandleListFiles handles GET /api/nodes/{id}/config
func (h *ConfigHandler) HandleListFiles(w http.ResponseWriter, r *http.Request) {
	node, err := h.getNode(r)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}

	result, err := h.sendConfigCommand(node, "config.list", map[string]string{
		"data_dir": node.DataDirectory,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// Parse the output as JSON array
	var files []json.RawMessage
	if err := json.Unmarshal([]byte(result.Output), &files); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"files": []any{}, "raw": result.Output})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"files": files})
}

// HandleReadFile handles GET /api/nodes/{id}/config/{filename}
func (h *ConfigHandler) HandleReadFile(w http.ResponseWriter, r *http.Request) {
	node, err := h.getNode(r)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}

	fileName := r.PathValue("filename")
	if fileName == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "filename required"})
		return
	}

	result, err := h.sendConfigCommand(node, "config.read", map[string]string{
		"data_dir":  node.DataDirectory,
		"file_name": fileName,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"file_name": fileName,
		"content":   result.Output,
	})
}

// configWriteRequest is the request body for writing config files.
type configWriteRequest struct {
	Content  string `json:"content"`
	Restart  bool   `json:"restart"`
}

// HandleWriteFile handles PUT /api/nodes/{id}/config/{filename}
func (h *ConfigHandler) HandleWriteFile(w http.ResponseWriter, r *http.Request) {
	node, err := h.getNode(r)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}

	fileName := r.PathValue("filename")
	if fileName == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "filename required"})
		return
	}

	var req configWriteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	payload := map[string]string{
		"data_dir":  node.DataDirectory,
		"file_name": fileName,
		"content":   req.Content,
	}
	if req.Restart {
		payload["restart_container"] = node.ContainerName
	}

	result, err := h.sendConfigCommand(node, "config.write", payload)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success": result.Success,
		"output":  result.Output,
	})
}

// HandleListBackups handles GET /api/nodes/{id}/config/{filename}/backups
func (h *ConfigHandler) HandleListBackups(w http.ResponseWriter, r *http.Request) {
	node, err := h.getNode(r)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}

	fileName := r.PathValue("filename")
	if fileName == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "filename required"})
		return
	}

	result, err := h.sendConfigCommand(node, "config.backups", map[string]string{
		"data_dir":  node.DataDirectory,
		"file_name": fileName,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	var backups []json.RawMessage
	if err := json.Unmarshal([]byte(result.Output), &backups); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"backups": []any{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"backups": backups})
}

// restoreRequest is the request body for restoring from backup.
type restoreRequest struct {
	BackupName string `json:"backup_name"`
}

// HandleRestore handles POST /api/nodes/{id}/config/restore
func (h *ConfigHandler) HandleRestore(w http.ResponseWriter, r *http.Request) {
	node, err := h.getNode(r)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}

	var req restoreRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	if req.BackupName == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "backup_name required"})
		return
	}

	result, err := h.sendConfigCommand(node, "config.restore", map[string]string{
		"data_dir":    node.DataDirectory,
		"backup_name": req.BackupName,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success": result.Success,
		"output":  result.Output,
	})
}

// multiConfigPushRequest pushes same config to multiple nodes.
type multiConfigPushRequest struct {
	FileName string   `json:"file_name"`
	Content  string   `json:"content"`
	NodeIDs  []string `json:"node_ids"`
	Restart  bool     `json:"restart"`
}

// HandleMultiPush handles POST /api/config/push
func (h *ConfigHandler) HandleMultiPush(w http.ResponseWriter, r *http.Request) {
	var req multiConfigPushRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	if req.FileName == "" || len(req.NodeIDs) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "file_name and node_ids required"})
		return
	}

	type pushResult struct {
		NodeID  string `json:"node_id"`
		Success bool   `json:"success"`
		Output  string `json:"output,omitempty"`
		Error   string `json:"error,omitempty"`
	}

	results := make([]pushResult, 0, len(req.NodeIDs))
	for _, nodeID := range req.NodeIDs {
		entry := pushResult{NodeID: nodeID}

		node, err := h.nodeStore.GetByID(nodeID)
		if err != nil {
			entry.Error = "node not found"
			results = append(results, entry)
			continue
		}

		if !h.hub.IsConnected(node.ServerID) {
			entry.Error = "agent offline"
			results = append(results, entry)
			continue
		}

		payload := map[string]string{
			"data_dir":  node.DataDirectory,
			"file_name": req.FileName,
			"content":   req.Content,
		}
		if req.Restart {
			payload["restart_container"] = node.ContainerName
		}

		cmdResult, err := h.sendConfigCommand(node, "config.write", payload)
		if err != nil {
			entry.Error = err.Error()
		} else {
			entry.Success = cmdResult.Success
			entry.Output = cmdResult.Output
			entry.Error = cmdResult.Error
		}

		results = append(results, entry)
	}

	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

func (h *ConfigHandler) getNode(r *http.Request) (*models.Node, error) {
	nodeID := r.PathValue("id")
	if nodeID == "" {
		return nil, fmt.Errorf("missing node ID")
	}

	node, err := h.nodeStore.GetByID(nodeID)
	if err != nil {
		return nil, fmt.Errorf("node not found")
	}
	return node, nil
}

func (h *ConfigHandler) sendConfigCommand(node *models.Node, action string, payload map[string]string) (*models.CommandResult, error) {
	if !h.hub.IsConnected(node.ServerID) {
		return nil, fmt.Errorf("agent offline for server %s", node.ServerID)
	}

	msg := &models.Message{
		ID:        fmt.Sprintf("cmd-%s-%d", action, time.Now().UnixNano()),
		Type:      "command",
		Action:    action,
		Payload:   payload,
		Timestamp: time.Now().Unix(),
	}

	return h.hub.SendCommand(node.ServerID, msg, configCommandTimeout)
}
