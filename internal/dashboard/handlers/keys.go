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

const keyCommandTimeout = 120 * time.Second

// KeyHandler handles validator key management API requests.
type KeyHandler struct {
	hub       *ws.Hub
	nodeStore *store.NodeStore
}

// NewKeyHandler creates a new KeyHandler.
func NewKeyHandler(hub *ws.Hub, nodeStore *store.NodeStore) *KeyHandler {
	return &KeyHandler{
		hub:       hub,
		nodeStore: nodeStore,
	}
}

// HandleGetKeyInfo handles GET /api/nodes/{id}/keys
func (h *KeyHandler) HandleGetKeyInfo(w http.ResponseWriter, r *http.Request) {
	node, err := h.getNode(r)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}

	result, err := h.sendKeyCommand(node, "key.info", map[string]string{
		"data_dir": node.DataDirectory,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	var keyInfo json.RawMessage
	if err := json.Unmarshal([]byte(result.Output), &keyInfo); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"raw": result.Output})
		return
	}
	writeJSON(w, http.StatusOK, keyInfo)
}

// keyGenerateRequest is the request body for key generation.
type keyGenerateRequest struct {
	ImageTag string `json:"image_tag"`
}

// HandleGenerateKey handles POST /api/nodes/{id}/keys/generate
func (h *KeyHandler) HandleGenerateKey(w http.ResponseWriter, r *http.Request) {
	node, err := h.getNode(r)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}

	var req keyGenerateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if req.ImageTag == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "image_tag required"})
		return
	}

	result, err := h.sendKeyCommand(node, "key.generate", map[string]string{
		"data_dir":  node.DataDirectory,
		"image_tag": req.ImageTag,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	var keyInfo json.RawMessage
	if err := json.Unmarshal([]byte(result.Output), &keyInfo); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"success": result.Success, "raw": result.Output})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "key_info": keyInfo})
}

// keyImportRequest is the request body for key import.
type keyImportRequest struct {
	PEMContent string `json:"pem_content"`
}

// HandleImportKey handles POST /api/nodes/{id}/keys/import
func (h *KeyHandler) HandleImportKey(w http.ResponseWriter, r *http.Request) {
	node, err := h.getNode(r)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}

	var req keyImportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if req.PEMContent == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "pem_content required"})
		return
	}

	result, err := h.sendKeyCommand(node, "key.import", map[string]string{
		"data_dir":    node.DataDirectory,
		"pem_content": req.PEMContent,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"success": result.Success, "output": result.Output})
}

// HandleExportKey handles GET /api/nodes/{id}/keys/export
func (h *KeyHandler) HandleExportKey(w http.ResponseWriter, r *http.Request) {
	node, err := h.getNode(r)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}

	result, err := h.sendKeyCommand(node, "key.export", map[string]string{
		"data_dir": node.DataDirectory,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"pem_content": result.Output})
}

// HandleListKeyBackups handles GET /api/nodes/{id}/keys/backups
func (h *KeyHandler) HandleListKeyBackups(w http.ResponseWriter, r *http.Request) {
	node, err := h.getNode(r)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}

	result, err := h.sendKeyCommand(node, "key.backups", map[string]string{
		"data_dir": node.DataDirectory,
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

func (h *KeyHandler) getNode(r *http.Request) (*models.Node, error) {
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

func (h *KeyHandler) sendKeyCommand(node *models.Node, action string, payload map[string]string) (*models.CommandResult, error) {
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

	return h.hub.SendCommand(node.ServerID, msg, keyCommandTimeout)
}
