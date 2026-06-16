package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/CTJaeger/KleverNodeHub/internal/dashboard/ws"
	"github.com/CTJaeger/KleverNodeHub/internal/store"
)

func setupProvisionTest(t *testing.T) (*ProvisionHandler, func()) {
	t.Helper()
	tmpDir := t.TempDir()
	db, err := store.Open(tmpDir + "/test.db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	serverStore := store.NewServerStore(db)
	nodeStore := store.NewNodeStore(db)
	hub := ws.NewHub(serverStore, nodeStore)
	return NewProvisionHandler(hub), func() { _ = db.Close() }
}

func postProvision(t *testing.T, h *ProvisionHandler, body map[string]any) int {
	t.Helper()
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/nodes/provision", bytes.NewReader(buf))
	w := httptest.NewRecorder()
	h.HandleProvision(w, req)
	return w.Code
}

func TestHandleProvision_RejectsInvalidNodeName(t *testing.T) {
	h, cleanup := setupProvisionTest(t)
	defer cleanup()

	bad := []string{"-leading-dash", "has space", "slash/name", "semi;colon", "$(inject)", ".dotstart"}
	for _, name := range bad {
		code := postProvision(t, h, map[string]any{
			"server_id": "srv-1",
			"node_name": name,
		})
		if code != http.StatusBadRequest {
			t.Errorf("node_name=%q: status = %d, want 400", name, code)
		}
	}
}

func TestHandleProvision_RejectsInvalidRedundancyLevel(t *testing.T) {
	h, cleanup := setupProvisionTest(t)
	defer cleanup()

	code := postProvision(t, h, map[string]any{
		"server_id":        "srv-1",
		"node_name":        "validator-01",
		"redundancy_level": 2,
	})
	if code != http.StatusBadRequest {
		t.Errorf("redundancy_level=2: status = %d, want 400", code)
	}
}

func TestHandleProvision_ValidNameAgentOffline(t *testing.T) {
	h, cleanup := setupProvisionTest(t)
	defer cleanup()

	// Valid name + valid redundancy level pass validation, then fail at the
	// agent-connectivity check (no agent connected in the test) — proving the
	// validation gate let a well-formed request through.
	code := postProvision(t, h, map[string]any{
		"server_id":        "srv-1",
		"node_name":        "validator-01",
		"redundancy_level": 1,
	})
	if code != http.StatusServiceUnavailable {
		t.Errorf("valid request: status = %d, want 503 (agent offline)", code)
	}
}

func TestHandleProvision_RejectsInvalidSyncMode(t *testing.T) {
	h, cleanup := setupProvisionTest(t)
	defer cleanup()

	code := postProvision(t, h, map[string]any{
		"server_id": "srv-1",
		"node_name": "validator-01",
		"sync_mode": "turbo",
	})
	if code != http.StatusBadRequest {
		t.Errorf("sync_mode=turbo: status = %d, want 400", code)
	}
}
