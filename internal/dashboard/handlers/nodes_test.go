package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/CTJaeger/KleverNodeHub/internal/dashboard/ws"
	"github.com/CTJaeger/KleverNodeHub/internal/models"
	"github.com/CTJaeger/KleverNodeHub/internal/store"
)

func setupHandlerTest(t *testing.T) (*NodeHandler, *ws.Hub, func()) {
	t.Helper()
	tmpDir := t.TempDir()
	db, err := store.Open(tmpDir + "/test.db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}

	serverStore := store.NewServerStore(db)
	nodeStore := store.NewNodeStore(db)

	// Create test server and node
	_ = serverStore.Create(&models.Server{
		ID:           "srv-1",
		Name:         "Test Server",
		Hostname:     "test",
		IPAddress:    "127.0.0.1",
		RegisteredAt: time.Now().Unix(),
	})

	_ = nodeStore.Create(&models.Node{
		ID:            "node-1",
		ServerID:      "srv-1",
		Name:          "klever-node1",
		ContainerName: "klever-node1",
		NodeType:      "validator",
		RestAPIPort:   8080,
		DataDirectory: "/opt/klever/node1",
		Status:        "running",
		CreatedAt:     time.Now().Unix(),
	})

	hub := ws.NewHub(serverStore)
	handler := NewNodeHandler(hub, nodeStore)
	return handler, hub, func() { db.Close() }
}

func TestHandleStart_AgentOffline(t *testing.T) {
	handler, _, cleanup := setupHandlerTest(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/api/nodes/node-1/start", nil)
	req.SetPathValue("id", "node-1")
	w := httptest.NewRecorder()

	handler.HandleStart(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestHandleStart_NodeNotFound(t *testing.T) {
	handler, _, cleanup := setupHandlerTest(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/api/nodes/nonexistent/start", nil)
	req.SetPathValue("id", "nonexistent")
	w := httptest.NewRecorder()

	handler.HandleStart(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleStart_MissingID(t *testing.T) {
	handler, _, cleanup := setupHandlerTest(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/api/nodes//start", nil)
	// No PathValue set
	w := httptest.NewRecorder()

	handler.HandleStart(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleStart_Success(t *testing.T) {
	handler, hub, cleanup := setupHandlerTest(t)
	defer cleanup()

	// Connect the agent
	conn := hub.Register("srv-1")

	// Simulate agent: read command from SendCh, extract ID, respond
	go func() {
		// Wait for the command to arrive on the agent's send channel
		data := <-conn.SendCh
		var msg models.Message
		if err := json.Unmarshal(data, &msg); err != nil {
			return
		}
		// Respond with success
		hub.HandleResult(&models.CommandResult{
			CommandID: msg.ID,
			Success:   true,
			Output:    "running",
		})
	}()

	req := httptest.NewRequest(http.MethodPost, "/api/nodes/node-1/start", nil)
	req.SetPathValue("id", "node-1")
	w := httptest.NewRecorder()

	handler.HandleStart(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d (body: %s)", w.Code, http.StatusOK, w.Body.String())
	}
}

func TestHandleBatch_InvalidBody(t *testing.T) {
	handler, _, cleanup := setupHandlerTest(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/api/nodes/batch", bytes.NewReader([]byte("invalid")))
	w := httptest.NewRecorder()

	handler.HandleBatch(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleBatch_MissingFields(t *testing.T) {
	handler, _, cleanup := setupHandlerTest(t)
	defer cleanup()

	body, _ := json.Marshal(batchRequest{Action: "node.start"})
	req := httptest.NewRequest(http.MethodPost, "/api/nodes/batch", bytes.NewReader(body))
	w := httptest.NewRecorder()

	handler.HandleBatch(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleBatch_AgentOffline(t *testing.T) {
	handler, _, cleanup := setupHandlerTest(t)
	defer cleanup()

	body, _ := json.Marshal(batchRequest{
		Action:  "node.start",
		NodeIDs: []string{"node-1"},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/nodes/batch", bytes.NewReader(body))
	w := httptest.NewRecorder()

	handler.HandleBatch(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp map[string][]batchResultEntry
	_ = json.NewDecoder(w.Body).Decode(&resp)

	if len(resp["results"]) != 1 {
		t.Fatalf("expected 1 result, got %d", len(resp["results"]))
	}
	if resp["results"][0].Error == "" {
		t.Error("expected error for offline agent")
	}
}

func TestContainsOffline(t *testing.T) {
	if !containsOffline("agent offline: srv-1") {
		t.Error("should detect 'agent offline'")
	}
	if !containsOffline("agent offline") {
		t.Error("should detect exact 'agent offline'")
	}
	if containsOffline("some other error") {
		t.Error("should not match other errors")
	}
}

