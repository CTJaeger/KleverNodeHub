package handlers

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CTJaeger/KleverNodeHub/internal/store"
)

func setupSettingsHandler(t *testing.T) *SettingsHandler {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(); _ = os.RemoveAll(dir) })
	return NewSettingsHandler(store.NewSettingsStore(db))
}

func TestHandleGetAll(t *testing.T) {
	h := setupSettingsHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	w := httptest.NewRecorder()
	h.HandleGetAll(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	body := w.Body.String()
	if !strings.Contains(body, "settings") {
		t.Error("expected settings in response")
	}
	if !strings.Contains(body, "defaults") {
		t.Error("expected defaults in response")
	}
}

func TestHandleUpdate(t *testing.T) {
	h := setupSettingsHandler(t)

	body := `{"dashboard_name":"My Hub","hot_retention_days":"14"}`
	req := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.HandleUpdate(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	// Verify the values were stored
	req2 := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	w2 := httptest.NewRecorder()
	h.HandleGetAll(w2, req2)

	resp := w2.Body.String()
	if !strings.Contains(resp, "My Hub") {
		t.Error("expected updated dashboard_name in response")
	}
	if !strings.Contains(resp, `"14"`) {
		t.Error("expected updated hot_retention_days")
	}
}

func TestHandleUpdate_UnknownKey(t *testing.T) {
	h := setupSettingsHandler(t)

	body := `{"unknown_key":"value"}`
	req := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.HandleUpdate(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleGetSingle(t *testing.T) {
	h := setupSettingsHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/settings/dashboard_name", nil)
	req.SetPathValue("key", "dashboard_name")
	w := httptest.NewRecorder()
	h.HandleGetSingle(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	body := w.Body.String()
	if !strings.Contains(body, "Klever Node Hub") {
		t.Error("expected default dashboard_name")
	}
}

func TestHandleUpdateSingle(t *testing.T) {
	h := setupSettingsHandler(t)

	body := `{"value":"30"}`
	req := httptest.NewRequest(http.MethodPut, "/api/settings/metrics_interval_sec", strings.NewReader(body))
	req.SetPathValue("key", "metrics_interval_sec")
	w := httptest.NewRecorder()
	h.HandleUpdateSingle(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHandleUpdateSingle_UnknownKey(t *testing.T) {
	h := setupSettingsHandler(t)

	body := `{"value":"x"}`
	req := httptest.NewRequest(http.MethodPut, "/api/settings/bogus_key", strings.NewReader(body))
	req.SetPathValue("key", "bogus_key")
	w := httptest.NewRecorder()
	h.HandleUpdateSingle(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleResetDefaults(t *testing.T) {
	h := setupSettingsHandler(t)

	// Set a custom value first
	update := `{"dashboard_name":"Custom"}`
	req := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(update))
	w := httptest.NewRecorder()
	h.HandleUpdate(w, req)

	// Reset general category
	req2 := httptest.NewRequest(http.MethodPost, "/api/settings/reset?category=general", nil)
	w2 := httptest.NewRecorder()
	h.HandleResetDefaults(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w2.Code, http.StatusOK)
	}

	// Verify it was reset
	req3 := httptest.NewRequest(http.MethodGet, "/api/settings/dashboard_name", nil)
	req3.SetPathValue("key", "dashboard_name")
	w3 := httptest.NewRecorder()
	h.HandleGetSingle(w3, req3)

	if !strings.Contains(w3.Body.String(), "Klever Node Hub") {
		t.Error("expected default value after reset")
	}
}

func TestHandleResetDefaults_InvalidCategory(t *testing.T) {
	h := setupSettingsHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/api/settings/reset?category=nonexistent", nil)
	w := httptest.NewRecorder()
	h.HandleResetDefaults(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}
