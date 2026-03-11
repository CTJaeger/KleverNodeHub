package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/CTJaeger/KleverNodeHub/internal/dashboard/ws"
	"github.com/CTJaeger/KleverNodeHub/internal/models"
	"github.com/CTJaeger/KleverNodeHub/internal/store"
)

// LogHandler handles log-related API requests.
type LogHandler struct {
	hub       *ws.Hub
	nodeStore *store.NodeStore
}

// NewLogHandler creates a new LogHandler.
func NewLogHandler(hub *ws.Hub, nodeStore *store.NodeStore) *LogHandler {
	return &LogHandler{
		hub:       hub,
		nodeStore: nodeStore,
	}
}

// HandleFetchLogs handles GET /api/nodes/{id}/logs?tail=100&since=<timestamp>
func (h *LogHandler) HandleFetchLogs(w http.ResponseWriter, r *http.Request) {
	nodeID := r.PathValue("id")
	if nodeID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing node ID"})
		return
	}

	node, err := h.nodeStore.GetByID(nodeID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "node not found"})
		return
	}

	if !h.hub.IsConnected(node.ServerID) {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "agent offline"})
		return
	}

	// Parse query parameters
	tail := 100
	if t := r.URL.Query().Get("tail"); t != "" {
		if v, err := strconv.Atoi(t); err == nil && v > 0 {
			tail = v
		}
	}

	var since int64
	if s := r.URL.Query().Get("since"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			since = v
		}
	}

	msg := &models.Message{
		ID:     fmt.Sprintf("cmd-logs-%d", time.Now().UnixNano()),
		Type:   "command",
		Action: "node.logs",
		Payload: map[string]any{
			"container_name": node.ContainerName,
			"tail":           float64(tail),
			"since":          float64(since),
		},
		Timestamp: time.Now().Unix(),
	}

	result, err := h.hub.SendCommand(node.ServerID, msg, 30*time.Second)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	if !result.Success {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": result.Error})
		return
	}

	// Output is JSON array of LogLine objects
	var lines []json.RawMessage
	if err := json.Unmarshal([]byte(result.Output), &lines); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"lines": []any{}, "raw": result.Output})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"lines": lines})
}
