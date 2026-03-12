package handlers

import (
	"net/http"

	"github.com/CTJaeger/KleverNodeHub/internal/models"
	"github.com/CTJaeger/KleverNodeHub/internal/store"
)

// ServerHandler handles server API requests.
type ServerHandler struct {
	serverStore  *store.ServerStore
	nodeStore    *store.NodeStore
	metricsStore *store.MetricsStore
}

// NewServerHandler creates a new ServerHandler.
func NewServerHandler(serverStore *store.ServerStore, nodeStore *store.NodeStore, metricsStore *store.MetricsStore) *ServerHandler {
	return &ServerHandler{
		serverStore:  serverStore,
		nodeStore:    nodeStore,
		metricsStore: metricsStore,
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

// nodeMetricNames are the metrics enriched on node list responses.
var nodeMetricNames = []string{"klv_nonce", "klv_is_syncing", "klv_probable_highest_nonce"}

// HandleListNodes returns all nodes, optionally filtered by server.
// GET /api/nodes
func (h *ServerHandler) HandleListNodes(w http.ResponseWriter, r *http.Request) {
	serverID := r.URL.Query().Get("server_id")

	var nodes []models.Node
	var err error

	if serverID != "" {
		nodes, err = h.nodeStore.ListByServer(serverID)
	} else {
		nodes, err = h.nodeStore.ListAll("")
	}

	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// Enrich nodes with latest metrics (nonce, sync state)
	if h.metricsStore != nil {
		for i := range nodes {
			latest, _ := h.metricsStore.LatestNodeMetrics(nodes[i].ID, nodeMetricNames)
			if len(latest) > 0 {
				if nodes[i].Metadata == nil {
					nodes[i].Metadata = make(map[string]any)
				}
				for k, v := range latest {
					nodes[i].Metadata[k] = v
				}
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"nodes": nodes})
}

// HandleDelete removes a server by ID.
// DELETE /api/servers/{id}
func (h *ServerHandler) HandleDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing server ID"})
		return
	}

	if err := h.serverStore.Delete(id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// HandleDeleteNode removes a node by ID.
// DELETE /api/nodes/{id}
func (h *ServerHandler) HandleDeleteNode(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing node ID"})
		return
	}

	if err := h.nodeStore.Delete(id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
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
