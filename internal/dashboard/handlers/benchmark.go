package handlers

import (
	"fmt"
	"net/http"
	"time"

	"github.com/CTJaeger/KleverNodeHub/internal/dashboard/ws"
	"github.com/CTJaeger/KleverNodeHub/internal/models"
	"github.com/CTJaeger/KleverNodeHub/internal/store"
)

const benchmarkTimeout = 6 * time.Minute // benchmark can take several minutes

// BenchmarkHandler handles server benchmark API requests.
type BenchmarkHandler struct {
	hub         *ws.Hub
	serverStore *store.ServerStore
}

// NewBenchmarkHandler creates a new BenchmarkHandler.
func NewBenchmarkHandler(hub *ws.Hub, serverStore *store.ServerStore) *BenchmarkHandler {
	return &BenchmarkHandler{
		hub:         hub,
		serverStore: serverStore,
	}
}

// HandleRunBenchmark starts a benchmark on a server's agent.
// POST /api/servers/{id}/benchmark
func (h *BenchmarkHandler) HandleRunBenchmark(w http.ResponseWriter, r *http.Request) {
	serverID := r.PathValue("id")
	if serverID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing server ID"})
		return
	}

	// Verify server exists
	if _, err := h.serverStore.GetByID(serverID); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "server not found"})
		return
	}

	// Check agent is online
	if !h.hub.IsConnected(serverID) {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "agent offline"})
		return
	}

	msg := &models.Message{
		ID:        fmt.Sprintf("cmd-server.benchmark-%d", time.Now().UnixNano()),
		Type:      "command",
		Action:    "server.benchmark",
		Payload:   map[string]any{},
		Timestamp: time.Now().Unix(),
	}

	cmdResult, err := h.hub.SendCommand(serverID, msg, benchmarkTimeout)
	if err != nil {
		writeJSON(w, http.StatusGatewayTimeout, map[string]string{"error": "benchmark timed out: " + err.Error()})
		return
	}

	if !cmdResult.Success {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": cmdResult.Error,
		})
		return
	}

	// cmdResult.Output is the JSON-encoded BenchmarkResult
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, cmdResult.Output)
}
