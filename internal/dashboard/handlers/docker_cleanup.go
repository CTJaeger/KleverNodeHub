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

// DockerCleanupHandler serves the Docker image cleanup tool, which lists and
// removes unused klever-go images on a given server.
type DockerCleanupHandler struct {
	hub         *ws.Hub
	serverStore *store.ServerStore
}

// NewDockerCleanupHandler creates a new DockerCleanupHandler.
func NewDockerCleanupHandler(hub *ws.Hub, serverStore *store.ServerStore) *DockerCleanupHandler {
	return &DockerCleanupHandler{hub: hub, serverStore: serverStore}
}

// HandleListImages handles GET /api/servers/{id}/images
// Asks the server's agent for its local klever-go images.
func (h *DockerCleanupHandler) HandleListImages(w http.ResponseWriter, r *http.Request) {
	server, ok := h.lookupServer(w, r)
	if !ok {
		return
	}

	msg := &models.Message{
		ID:        fmt.Sprintf("cmd-images-list-%d", time.Now().UnixNano()),
		Type:      "command",
		Action:    "docker.images.list",
		Payload:   map[string]any{},
		Timestamp: time.Now().Unix(),
	}

	result, err := h.hub.SendCommand(server.ID, msg, 30*time.Second)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// removeImagesRequest is the request body for image removal.
type removeImagesRequest struct {
	ImageIDs []string `json:"image_ids"`
}

// HandleRemoveImages handles POST /api/servers/{id}/images/remove
// Tells the server's agent to delete the selected images.
func (h *DockerCleanupHandler) HandleRemoveImages(w http.ResponseWriter, r *http.Request) {
	server, ok := h.lookupServer(w, r)
	if !ok {
		return
	}

	var req removeImagesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if len(req.ImageIDs) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "image_ids is required"})
		return
	}

	msg := &models.Message{
		ID:     fmt.Sprintf("cmd-images-remove-%d", time.Now().UnixNano()),
		Type:   "command",
		Action: "docker.images.remove",
		Payload: map[string]any{
			"image_ids": req.ImageIDs,
		},
		Timestamp: time.Now().Unix(),
	}

	result, err := h.hub.SendCommand(server.ID, msg, 5*time.Minute)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// lookupServer resolves the {id} path value to a server and verifies its agent
// is online, writing the appropriate error response if not.
func (h *DockerCleanupHandler) lookupServer(w http.ResponseWriter, r *http.Request) (*models.Server, bool) {
	id := r.PathValue("id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing server ID"})
		return nil, false
	}
	server, err := h.serverStore.GetByID(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "server not found"})
		return nil, false
	}
	if !h.hub.IsConnected(server.ID) {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "agent offline"})
		return nil, false
	}
	return server, true
}
