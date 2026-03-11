package handlers

import (
	"net/http"

	"github.com/CTJaeger/KleverNodeHub/internal/store"
)

// ServerHandler handles server API requests.
type ServerHandler struct {
	serverStore *store.ServerStore
	nodeStore   *store.NodeStore
}

// NewServerHandler creates a new ServerHandler.
func NewServerHandler(serverStore *store.ServerStore, nodeStore *store.NodeStore) *ServerHandler {
	return &ServerHandler{
		serverStore: serverStore,
		nodeStore:   nodeStore,
	}
}

// HandleList returns all servers.
// GET /api/servers
func (h *ServerHandler) HandleList(w http.ResponseWriter, r *http.Request) {
	servers, err := h.serverStore.List()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"servers": servers})
}

// HandleGet returns a single server by ID.
// GET /api/servers/{id}
func (h *ServerHandler) HandleGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing server ID"})
		return
	}

	server, err := h.serverStore.GetByID(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "server not found"})
		return
	}
	writeJSON(w, http.StatusOK, server)
}

// HandleListNodes returns all nodes, optionally filtered by server.
// GET /api/nodes
func (h *ServerHandler) HandleListNodes(w http.ResponseWriter, r *http.Request) {
	serverID := r.URL.Query().Get("server_id")

	var err error
	var result any

	if serverID != "" {
		nodes, e := h.nodeStore.ListByServer(serverID)
		err = e
		result = map[string]any{"nodes": nodes}
	} else {
		nodes, e := h.nodeStore.ListAll("")
		err = e
		result = map[string]any{"nodes": nodes}
	}

	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// HandleGetNode returns a single node by ID.
// GET /api/nodes/{id}
func (h *ServerHandler) HandleGetNode(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing node ID"})
		return
	}

	node, err := h.nodeStore.GetByID(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "node not found"})
		return
	}
	writeJSON(w, http.StatusOK, node)
}
