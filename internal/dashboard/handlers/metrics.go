package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/CTJaeger/KleverNodeHub/internal/store"
)

const defaultHotWindow = 7 * 24 * time.Hour

// MetricsHandler serves metrics query API endpoints.
type MetricsHandler struct {
	metricsStore *store.MetricsStore
}

// NewMetricsHandler creates a new metrics handler.
func NewMetricsHandler(metricsStore *store.MetricsStore) *MetricsHandler {
	return &MetricsHandler{metricsStore: metricsStore}
}

// HandleNodeMetrics returns time-series data for a specific node metric.
// GET /api/nodes/{id}/metrics?name=klv_nonce&from=<unix>&to=<unix>&resolution=raw|5m
func (h *MetricsHandler) HandleNodeMetrics(w http.ResponseWriter, r *http.Request) {
	nodeID := r.PathValue("id")
	if nodeID == "" {
		http.Error(w, "missing node id", http.StatusBadRequest)
		return
	}

	metricName := r.URL.Query().Get("name")
	if metricName == "" {
		http.Error(w, "missing name parameter", http.StatusBadRequest)
		return
	}

	from, to := parseTimeRange(r)
	resolution := r.URL.Query().Get("resolution")

	var result any
	var err error

	switch resolution {
	case "5m":
		result, err = h.metricsStore.QueryArchive(nodeID, metricName, from, to)
	case "raw":
		result, err = h.metricsStore.QueryRecent(nodeID, metricName, from, to)
	default:
		// Auto-select based on time range
		result, err = h.metricsStore.QueryAutoResolution(nodeID, metricName, from, to, defaultHotWindow)
	}

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

// HandleServerMetrics returns system metrics for a specific server.
// GET /api/servers/{id}/metrics?from=<unix>&to=<unix>
func (h *MetricsHandler) HandleServerMetrics(w http.ResponseWriter, r *http.Request) {
	serverID := r.PathValue("id")
	if serverID == "" {
		http.Error(w, "missing server id", http.StatusBadRequest)
		return
	}

	from, to := parseTimeRange(r)

	result, err := h.metricsStore.QuerySystemMetrics(serverID, from, to)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

// parseTimeRange extracts from/to Unix timestamps from query params.
// Defaults: from = 1 hour ago, to = now.
func parseTimeRange(r *http.Request) (int64, int64) {
	now := time.Now().Unix()
	from := now - 3600 // default: 1 hour ago
	to := now

	if v := r.URL.Query().Get("from"); v != "" {
		if parsed, err := strconv.ParseInt(v, 10, 64); err == nil {
			from = parsed
		}
	}
	if v := r.URL.Query().Get("to"); v != "" {
		if parsed, err := strconv.ParseInt(v, 10, 64); err == nil {
			to = parsed
		}
	}

	return from, to
}
