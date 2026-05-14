package handlers

import (
	"net/http"
	"sort"
	"time"

	"github.com/CTJaeger/KleverNodeHub/internal/store"
)

// perfMetric is the Klever node metric used for the performance comparison.
const perfMetric = "klv_block_process_duration_ms"

// perfBaselineWindowSec is how far back before a version change the baseline
// performance is sampled.
const perfBaselineWindowSec = 24 * 3600

// perfMinPoints is the minimum number of data points needed in each window
// before a comparison is shown — below this the report is "not enough data".
const perfMinPoints = 10

// PerformanceHandler serves the passive per-node performance report shown on
// the node detail page. Unlike the regression alert, this never notifies —
// it just exposes the before/after comparison so operators can look.
type PerformanceHandler struct {
	versionStore *store.VersionHistoryStore
	metricsStore *store.MetricsStore
	nodeStore    *store.NodeStore
}

// NewPerformanceHandler creates a new PerformanceHandler.
func NewPerformanceHandler(versionStore *store.VersionHistoryStore, metricsStore *store.MetricsStore, nodeStore *store.NodeStore) *PerformanceHandler {
	return &PerformanceHandler{
		versionStore: versionStore,
		metricsStore: metricsStore,
		nodeStore:    nodeStore,
	}
}

// HandleNodePerformance handles GET /api/nodes/{id}/performance.
// Returns the block-processing median before vs. after the node's most
// recent version change. {"available": false} when there is no recorded
// version change yet or not enough metrics on either side.
func (h *PerformanceHandler) HandleNodePerformance(w http.ResponseWriter, r *http.Request) {
	nodeID := r.PathValue("id")
	if nodeID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing node ID"})
		return
	}
	if _, err := h.nodeStore.GetByID(nodeID); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "node not found"})
		return
	}

	change, err := h.versionStore.LastChange(nodeID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if change == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"available": false,
			"reason":    "no version change recorded yet",
		})
		return
	}

	now := time.Now().Unix()
	baseline, err := h.metricsStore.QueryRecent(nodeID, perfMetric, change.DetectedAt-perfBaselineWindowSec, change.DetectedAt)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	post, err := h.metricsStore.QueryRecent(nodeID, perfMetric, change.DetectedAt, now)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	if len(baseline) < perfMinPoints || len(post) < perfMinPoints {
		writeJSON(w, http.StatusOK, map[string]any{
			"available":  false,
			"reason":     "not enough data around the version change yet",
			"version":    change.Version,
			"changed_at": change.DetectedAt,
		})
		return
	}

	baselineMs := perfMedian(baseline)
	currentMs := perfMedian(post)
	pctChange := 0.0
	if baselineMs > 0 {
		pctChange = (currentMs - baselineMs) / baselineMs * 100
	}

	resp := map[string]any{
		"available":   true,
		"version":     change.Version,
		"changed_at":  change.DetectedAt,
		"baseline_ms": round1(baselineMs),
		"current_ms":  round1(currentMs),
		"pct_change":  round1(pctChange),
	}
	if prev, err := h.versionStore.PreviousChange(*change); err == nil && prev != nil {
		resp["previous_version"] = prev.Version
	}
	writeJSON(w, http.StatusOK, resp)
}

// perfMedian returns the 50th-percentile value — robust against the
// occasional cold-start spike.
func perfMedian(points []store.DataPoint) float64 {
	if len(points) == 0 {
		return 0
	}
	values := make([]float64, len(points))
	for i, p := range points {
		values[i] = p.Value
	}
	sort.Float64s(values)
	mid := len(values) / 2
	if len(values)%2 == 1 {
		return values[mid]
	}
	return (values[mid-1] + values[mid]) / 2
}

func round1(v float64) float64 {
	return float64(int64(v*10+0.5)) / 10
}
