package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/CTJaeger/KleverNodeHub/internal/store"
)

// AlertHandler handles alert-related API requests.
type AlertHandler struct {
	alertStore *store.AlertStore
}

// NewAlertHandler creates a new AlertHandler.
func NewAlertHandler(alertStore *store.AlertStore) *AlertHandler {
	return &AlertHandler{alertStore: alertStore}
}

// HandleListRules handles GET /api/alerts/rules
func (h *AlertHandler) HandleListRules(w http.ResponseWriter, _ *http.Request) {
	rules, err := h.alertStore.ListRules()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if rules == nil {
		rules = []store.AlertRule{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"rules": rules})
}

// HandleCreateOrUpdateRule handles POST /api/alerts/rules
func (h *AlertHandler) HandleCreateOrUpdateRule(w http.ResponseWriter, r *http.Request) {
	var rule store.AlertRule
	if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	if rule.Name == "" || rule.MetricName == "" || rule.Condition == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name, metric_name, and condition are required"})
		return
	}

	if rule.ID == "" {
		// New rule — generate ID
		rule.ID = "rule-" + strconv.FormatInt(time.Now().UnixNano(), 36)
		rule.CreatedAt = time.Now().Unix()
		if rule.NodeFilter == "" {
			rule.NodeFilter = "*"
		}
		if rule.CooldownMin == 0 {
			rule.CooldownMin = 5
		}
		if err := h.alertStore.CreateRule(&rule); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"rule": rule})
		return
	}

	// Update existing rule
	if err := h.alertStore.UpdateRule(&rule); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rule": rule})
}

// HandleDeleteRule handles DELETE /api/alerts/rules/{id}
func (h *AlertHandler) HandleDeleteRule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "rule id required"})
		return
	}

	if err := h.alertStore.DeleteRule(id); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// HandleListActiveAlerts handles GET /api/alerts
func (h *AlertHandler) HandleListActiveAlerts(w http.ResponseWriter, _ *http.Request) {
	alerts, err := h.alertStore.ListActiveAlerts()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if alerts == nil {
		alerts = []store.AlertRecord{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"alerts": alerts})
}

// HandleAlertHistory handles GET /api/alerts/history
func (h *AlertHandler) HandleAlertHistory(w http.ResponseWriter, r *http.Request) {
	limitStr := r.URL.Query().Get("limit")
	limit := 100
	if limitStr != "" {
		if v, err := strconv.Atoi(limitStr); err == nil && v > 0 {
			limit = v
		}
	}

	alerts, err := h.alertStore.ListAlertHistory(limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if alerts == nil {
		alerts = []store.AlertRecord{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"alerts": alerts})
}

// HandleAcknowledgeAlert handles POST /api/alerts/{id}/acknowledge
func (h *AlertHandler) HandleAcknowledgeAlert(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "alert id required"})
		return
	}

	if err := h.alertStore.AcknowledgeAlert(id); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}
