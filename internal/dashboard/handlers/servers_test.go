package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/CTJaeger/KleverNodeHub/internal/models"
	"github.com/CTJaeger/KleverNodeHub/internal/store"
)

func setupServerHandler(t *testing.T) (*ServerHandler, func()) {
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
		Name:         "Server 1",
		Hostname:     "host1",
		IPAddress:    "10.0.0.1",
		Status:       "online",
		RegisteredAt: time.Now().Unix(),
	})

	_ = nodeStore.Create(&models.Node{
		ID:            "node-1",
		ServerID:      "srv-1",
		Name:          "klever-node1",
		ContainerName: "klever-node1",
		NodeType:      "validator",
		RestAPIPort:   8080,
		DataDirectory: "/opt/klever",
		Status:        "running",
		CreatedAt:     time.Now().Unix(),
	})

	metricsStore := store.NewMetricsStore(db)
	handler := NewServerHandler(serverStore, nodeStore, metricsStore)
	return handler, func() { _ = db.Close() }
}

func TestHandleListServers(t *testing.T) {
	handler, cleanup := setupServerHandler(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/api/servers", nil)
	w := httptest.NewRecorder()

	handler.HandleList(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp map[string][]models.Server
	_ = json.NewDecoder(w.Body).Decode(&resp)

	if len(resp["servers"]) != 1 {
		t.Errorf("expected 1 server, got %d", len(resp["servers"]))
	}
}

func TestHandleGetServer(t *testing.T) {
	handler, cleanup := setupServerHandler(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/api/servers/srv-1", nil)
	req.SetPathValue("id", "srv-1")
	w := httptest.NewRecorder()

	handler.HandleGet(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHandleGetServer_NotFound(t *testing.T) {
	handler, cleanup := setupServerHandler(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/api/servers/nonexistent", nil)
	req.SetPathValue("id", "nonexistent")
	w := httptest.NewRecorder()

	handler.HandleGet(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleListNodes(t *testing.T) {
	handler, cleanup := setupServerHandler(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/api/nodes", nil)
	w := httptest.NewRecorder()

	handler.HandleListNodes(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp map[string][]models.Node
	_ = json.NewDecoder(w.Body).Decode(&resp)

	if len(resp["nodes"]) != 1 {
		t.Errorf("expected 1 node, got %d", len(resp["nodes"]))
	}
}

func TestHandleListNodes_FilterByServer(t *testing.T) {
	handler, cleanup := setupServerHandler(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/api/nodes?server_id=srv-1", nil)
	w := httptest.NewRecorder()

	handler.HandleListNodes(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHandleGetNode(t *testing.T) {
	handler, cleanup := setupServerHandler(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/api/nodes/node-1", nil)
	req.SetPathValue("id", "node-1")
	w := httptest.NewRecorder()

	handler.HandleGetNode(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHandleGetNode_NotFound(t *testing.T) {
	handler, cleanup := setupServerHandler(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/api/nodes/nonexistent", nil)
	req.SetPathValue("id", "nonexistent")
	w := httptest.NewRecorder()

	handler.HandleGetNode(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}
