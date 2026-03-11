package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/CTJaeger/KleverNodeHub/internal/dashboard"
	"github.com/CTJaeger/KleverNodeHub/internal/dashboard/ws"
	"github.com/CTJaeger/KleverNodeHub/internal/models"
	"github.com/CTJaeger/KleverNodeHub/internal/store"
)

// DockerHandler handles Docker-related API requests.
type DockerHandler struct {
	hub       *ws.Hub
	nodeStore *store.NodeStore
	tagCache  *dashboard.TagCache
}

// NewDockerHandler creates a new DockerHandler.
func NewDockerHandler(hub *ws.Hub, nodeStore *store.NodeStore, tagCache *dashboard.TagCache) *DockerHandler {
	return &DockerHandler{
		hub:       hub,
		nodeStore: nodeStore,
		tagCache:  tagCache,
	}
}

// HandleListTags returns available Docker image tags from Docker Hub.
// GET /api/docker/tags
func (h *DockerHandler) HandleListTags(w http.ResponseWriter, r *http.Request) {
	tags, err := h.tagCache.GetTags()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"tags": tags})
}

// upgradeRequest is the request body for upgrade/downgrade.
type upgradeRequest struct {
	ImageTag string `json:"image_tag"`
}

// HandleUpgrade handles POST /api/nodes/{id}/upgrade
func (h *DockerHandler) HandleUpgrade(w http.ResponseWriter, r *http.Request) {
	h.handleImageChange(w, r, "node.upgrade")
}

// HandleDowngrade handles POST /api/nodes/{id}/downgrade
func (h *DockerHandler) HandleDowngrade(w http.ResponseWriter, r *http.Request) {
	h.handleImageChange(w, r, "node.upgrade") // Same operation, different tag direction
}

func (h *DockerHandler) handleImageChange(w http.ResponseWriter, r *http.Request, action string) {
	nodeID := r.PathValue("id")
	if nodeID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing node ID"})
		return
	}

	var req upgradeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	if req.ImageTag == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "image_tag is required"})
		return
	}

	// Look up node
	node, err := h.nodeStore.GetByID(nodeID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "node not found"})
		return
	}

	// Check agent is online
	if !h.hub.IsConnected(node.ServerID) {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "agent offline"})
		return
	}

	// Build command
	msg := &models.Message{
		ID:     fmt.Sprintf("cmd-%s-%d", action, time.Now().UnixNano()),
		Type:   "command",
		Action: action,
		Payload: map[string]string{
			"container_name": node.ContainerName,
			"image_tag":      req.ImageTag,
		},
		Timestamp: time.Now().Unix(),
	}

	// Send and wait (longer timeout for image pull)
	result, err := h.hub.SendCommand(node.ServerID, msg, 5*time.Minute)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// Update node in DB
	if result.Success {
		node.DockerImageTag = req.ImageTag
		_ = h.nodeStore.Update(node)
	}

	writeJSON(w, http.StatusOK, result)
}
