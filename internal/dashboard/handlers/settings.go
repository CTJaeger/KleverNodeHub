package handlers

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/CTJaeger/KleverNodeHub/internal/store"
)

// SettingsHandler handles dashboard settings API requests.
type SettingsHandler struct {
	settings *store.SettingsStore
}

// NewSettingsHandler creates a new SettingsHandler.
func NewSettingsHandler(settings *store.SettingsStore) *SettingsHandler {
	return &SettingsHandler{settings: settings}
}

// settingsCategories defines which keys belong to which category.
var settingsCategories = map[string][]string{
	"general": {
		"dashboard_name",
	},
	"metrics": {
		"metrics_interval_sec",
		"node_poll_interval_sec",
		"hot_retention_days",
		"archive_retention_days",
	},
	"notifications": {
		"notify_default_severity",
	},
	"agents": {
		"heartbeat_timeout_sec",
		"agent_discovery_interval_sec",
	},
}

// settingsDefaults defines default values for settings.
var settingsDefaults = map[string]string{
	"dashboard_name":               "Klever Node Hub",
	"metrics_interval_sec":         "60",
	"node_poll_interval_sec":       "30",
	"hot_retention_days":           "7",
	"archive_retention_days":       "90",
	"notify_default_severity":      "warning",
	"heartbeat_timeout_sec":        "120",
	"agent_discovery_interval_sec": "300",
}

// HandleGetAll handles GET /api/settings — returns all settings grouped by category.
func (h *SettingsHandler) HandleGetAll(w http.ResponseWriter, _ *http.Request) {
	allSettings, err := h.settings.GetAll()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	grouped := make(map[string]map[string]string)
	for category, keys := range settingsCategories {
		group := make(map[string]string)
		for _, key := range keys {
			if val, ok := allSettings[key]; ok {
				group[key] = val
			} else if def, ok := settingsDefaults[key]; ok {
				group[key] = def
			}
		}
		grouped[category] = group
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"settings": grouped,
		"defaults": settingsDefaults,
	})
}

// HandleGetSingle handles GET /api/settings/{key}
func (h *SettingsHandler) HandleGetSingle(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if key == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "key required"})
		return
	}

	value, err := h.settings.Get(key)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if value == "" {
		if def, ok := settingsDefaults[key]; ok {
			value = def
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"key": key, "value": value})
}

// HandleUpdate handles PUT /api/settings — partial update of settings.
func (h *SettingsHandler) HandleUpdate(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}

	var updates map[string]string
	if err := json.Unmarshal(body, &updates); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}

	// Validate keys — only allow known settings
	allowed := make(map[string]bool)
	for _, keys := range settingsCategories {
		for _, k := range keys {
			allowed[k] = true
		}
	}

	for key, value := range updates {
		if !allowed[key] {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown setting: " + key})
			return
		}
		if err := h.settings.Set(key, value); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"updated": len(updates)})
}

// HandleUpdateSingle handles PUT /api/settings/{key}
func (h *SettingsHandler) HandleUpdateSingle(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if key == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "key required"})
		return
	}

	// Validate key
	allowed := make(map[string]bool)
	for _, keys := range settingsCategories {
		for _, k := range keys {
			allowed[k] = true
		}
	}
	if !allowed[key] {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown setting: " + key})
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}

	var payload struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}

	if err := h.settings.Set(key, payload.Value); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"key": key, "value": payload.Value})
}

// HandleResetDefaults handles POST /api/settings/reset — resets all settings to defaults.
func (h *SettingsHandler) HandleResetDefaults(w http.ResponseWriter, r *http.Request) {
	// Check if resetting a specific category
	category := r.URL.Query().Get("category")

	if category != "" {
		keys, ok := settingsCategories[category]
		if !ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown category: " + category})
			return
		}
		for _, key := range keys {
			if def, ok := settingsDefaults[key]; ok {
				if err := h.settings.Set(key, def); err != nil {
					writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
					return
				}
			}
		}
	} else {
		for key, value := range settingsDefaults {
			if err := h.settings.Set(key, value); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"reset": true})
}
