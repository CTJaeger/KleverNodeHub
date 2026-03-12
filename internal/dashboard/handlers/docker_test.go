package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/CTJaeger/KleverNodeHub/internal/dashboard"
	"github.com/CTJaeger/KleverNodeHub/internal/dashboard/ws"
	"github.com/CTJaeger/KleverNodeHub/internal/models"
	"github.com/CTJaeger/KleverNodeHub/internal/store"
)

func setupDockerHandler(t *testing.T) (*DockerHandler, *ws.Hub, func()) {
	t.Helper()
	tmpDir := t.TempDir()
	db, err := store.Open(tmpDir + "/test.db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}

	serverStore := store.NewServerStore(db)
	nodeStore := store.NewNodeStore(db)

	_ = serverStore.Create(&models.Server{
		ID:           "srv-1",
		Name:         "Test Server",
		Hostname:     "test",
		IPAddress:    "127.0.0.1",
		RegisteredAt: time.Now().Unix(),
	})

	_ = nodeStore.Create(&models.Node{
		ID:             "node-1",
		ServerID:       "srv-1",
		Name:           "klever-node1",
		ContainerName:  "klever-node1",
		NodeType:       "validator",
		RestAPIPort:    8080,
		DockerImageTag: "v0.60.0",
		DataDirectory:  "/opt/klever/node1",
		Status:         "running",
		CreatedAt:      time.Now().Unix(),
	})

	hub := ws.NewHub(serverStore, nodeStore)
	tagCache := dashboard.NewTagCache()
	handler := NewDockerHandler(hub, nodeStore, tagCache)
	return handler, hub, func() { _ = db.Close() }
}

func TestHandleUpgrade_AgentOffline(t *testing.T) {
	handler, _, cleanup := setupDockerHandler(t)
	defer cleanup()

	body, _ := json.Marshal(upgradeRequest{ImageTag: "v0.61.0"})
	req := httptest.NewRequest(http.MethodPost, "/api/nodes/node-1/upgrade", bytes.NewReader(body))
	req.SetPathValue("id", "node-1")
	w := httptest.NewRecorder()

	handler.HandleUpgrade(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestHandleUpgrade_NodeNotFound(t *testing.T) {
	handler, _, cleanup := setupDockerHandler(t)
	defer cleanup()

	body, _ := json.Marshal(upgradeRequest{ImageTag: "v0.61.0"})
	req := httptest.NewRequest(http.MethodPost, "/api/nodes/nonexistent/upgrade", bytes.NewReader(body))
	req.SetPathValue("id", "nonexistent")
	w := httptest.NewRecorder()

	handler.HandleUpgrade(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleUpgrade_MissingTag(t *testing.T) {
	handler, _, cleanup := setupDockerHandler(t)
	defer cleanup()

	body, _ := json.Marshal(upgradeRequest{})
	req := httptest.NewRequest(http.MethodPost, "/api/nodes/node-1/upgrade", bytes.NewReader(body))
	req.SetPathValue("id", "node-1")
	w := httptest.NewRecorder()

	handler.HandleUpgrade(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleUpgrade_MissingID(t *testing.T) {
	handler, _, cleanup := setupDockerHandler(t)
	defer cleanup()

	body, _ := json.Marshal(upgradeRequest{ImageTag: "v0.61.0"})
	req := httptest.NewRequest(http.MethodPost, "/api/nodes//upgrade", bytes.NewReader(body))
	w := httptest.NewRecorder()

	handler.HandleUpgrade(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleUpgrade_Success(t *testing.T) {
	handler, hub, cleanup := setupDockerHandler(t)
	defer cleanup()

	conn := hub.Register("srv-1")

	// Simulate agent responding
	go func() {
		data := <-conn.SendCh
		var msg models.Message
		_ = json.Unmarshal(data, &msg)
		hub.HandleResult(&models.CommandResult{
			CommandID: msg.ID,
			Success:   true,
			Output:    "upgraded to v0.61.0",
		})
	}()

	body, _ := json.Marshal(upgradeRequest{ImageTag: "v0.61.0"})
	req := httptest.NewRequest(http.MethodPost, "/api/nodes/node-1/upgrade", bytes.NewReader(body))
	req.SetPathValue("id", "node-1")
	w := httptest.NewRecorder()

	handler.HandleUpgrade(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d (body: %s)", w.Code, http.StatusOK, w.Body.String())
	}
}

func TestHandleDowngrade(t *testing.T) {
	handler, _, cleanup := setupDockerHandler(t)
	defer cleanup()

	// Just test it's wired up (same flow as upgrade)
	body, _ := json.Marshal(upgradeRequest{ImageTag: "v0.59.0"})
	req := httptest.NewRequest(http.MethodPost, "/api/nodes/node-1/downgrade", bytes.NewReader(body))
	req.SetPathValue("id", "node-1")
	w := httptest.NewRecorder()

	handler.HandleDowngrade(w, req)

	// Will fail because agent is offline
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}
