package ws

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/CTJaeger/KleverNodeHub/internal/models"
	"github.com/CTJaeger/KleverNodeHub/internal/store"
)

func newTestHub(t *testing.T) (*Hub, *store.ServerStore) {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	ss := store.NewServerStore(db)
	ss.Create(&models.Server{ID: "srv-1", Name: "S1", Hostname: "h1", IPAddress: "1.2.3.4", Status: "offline"})

	hub := NewHub(ss)
	t.Cleanup(func() { hub.Stop() })
	return hub, ss
}

func TestRegisterAndUnregister(t *testing.T) {
	hub, _ := newTestHub(t)

	conn := hub.Register("srv-1")
	if conn == nil {
		t.Fatal("connection should not be nil")
	}
	if !hub.IsConnected("srv-1") {
		t.Error("should be connected")
	}
	if hub.ConnectedCount() != 1 {
		t.Errorf("count = %d, want 1", hub.ConnectedCount())
	}

	hub.Unregister("srv-1")
	if hub.IsConnected("srv-1") {
		t.Error("should not be connected after unregister")
	}
	if hub.ConnectedCount() != 0 {
		t.Errorf("count = %d, want 0", hub.ConnectedCount())
	}
}

func TestRegisterReplacesExisting(t *testing.T) {
	hub, _ := newTestHub(t)

	conn1 := hub.Register("srv-1")
	conn2 := hub.Register("srv-1")

	if conn1 == conn2 {
		t.Error("should be different connections")
	}
	if hub.ConnectedCount() != 1 {
		t.Errorf("count = %d, want 1", hub.ConnectedCount())
	}
}

func TestSendToConnectedAgent(t *testing.T) {
	hub, _ := newTestHub(t)
	hub.Register("srv-1")

	msg := &models.Message{
		ID:     "msg-1",
		Type:   "command",
		Action: "node.start",
	}

	err := hub.Send("srv-1", msg)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
}

func TestSendToDisconnectedAgent(t *testing.T) {
	hub, _ := newTestHub(t)

	err := hub.Send("srv-1", &models.Message{})
	if err == nil {
		t.Error("expected error sending to disconnected agent")
	}
}

func TestBroadcast(t *testing.T) {
	dir := t.TempDir()
	db, _ := store.Open(filepath.Join(dir, "test.db"))
	defer db.Close()
	ss := store.NewServerStore(db)
	ss.Create(&models.Server{ID: "srv-1", Name: "S1", Hostname: "h1", IPAddress: "1.2.3.4", Status: "offline"})
	ss.Create(&models.Server{ID: "srv-2", Name: "S2", Hostname: "h2", IPAddress: "5.6.7.8", Status: "offline"})

	hub := NewHub(ss)
	defer hub.Stop()

	hub.Register("srv-1")
	hub.Register("srv-2")

	hub.Broadcast(&models.Message{Action: "ping"})
	// No error = success (messages are queued)
}

func TestUnregisterNonexistent(t *testing.T) {
	hub, _ := newTestHub(t)
	// Should not panic
	hub.Unregister("nonexistent")
}

func TestHeartbeat(t *testing.T) {
	hub, _ := newTestHub(t)
	conn := hub.Register("srv-1")

	before := conn.LastHeartbeat()
	time.Sleep(10 * time.Millisecond)
	conn.UpdateHeartbeat()
	after := conn.LastHeartbeat()

	if !after.After(before) {
		t.Error("heartbeat time should advance")
	}
}

func TestIsConnected_False(t *testing.T) {
	hub, _ := newTestHub(t)
	if hub.IsConnected("nonexistent") {
		t.Error("should not be connected")
	}
}

func TestNewHub_CreatesDBFile(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	db, _ := store.Open(dbPath)
	defer db.Close()

	ss := store.NewServerStore(db)
	hub := NewHub(ss)
	defer hub.Stop()

	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Error("db file should exist")
	}
	_ = hub
}
