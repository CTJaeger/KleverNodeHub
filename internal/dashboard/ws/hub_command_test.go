package ws

import (
	"testing"
	"time"

	"github.com/CTJaeger/KleverNodeHub/internal/models"
	"github.com/CTJaeger/KleverNodeHub/internal/store"
)

func setupHubForCommand(t *testing.T) (*Hub, func()) {
	t.Helper()
	tmpDir := t.TempDir()
	db, err := store.Open(tmpDir + "/test.db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}

	serverStore := store.NewServerStore(db)
	// Create a test server
	_ = serverStore.Create(&models.Server{
		ID:           "srv-1",
		Name:         "Test Server",
		Hostname:     "test",
		IPAddress:    "127.0.0.1",
		RegisteredAt: time.Now().Unix(),
	})

	hub := NewHub(serverStore)
	return hub, func() { _ = db.Close() }
}

func TestSendCommand_AgentOffline(t *testing.T) {
	hub, cleanup := setupHubForCommand(t)
	defer cleanup()

	msg := &models.Message{
		ID:     "cmd-1",
		Type:   "command",
		Action: "node.start",
	}

	_, err := hub.SendCommand("srv-1", msg, 1*time.Second)
	if err == nil {
		t.Error("expected error for offline agent")
	}
}

func TestSendCommand_Success(t *testing.T) {
	hub, cleanup := setupHubForCommand(t)
	defer cleanup()

	// Register agent
	hub.Register("srv-1")

	msg := &models.Message{
		ID:     "cmd-2",
		Type:   "command",
		Action: "node.start",
	}

	// Simulate agent responding in background
	go func() {
		time.Sleep(50 * time.Millisecond)
		hub.HandleResult(&models.CommandResult{
			CommandID: "cmd-2",
			Success:   true,
			Output:    "running",
		})
	}()

	result, err := hub.SendCommand("srv-1", msg, 5*time.Second)
	if err != nil {
		t.Fatalf("SendCommand: %v", err)
	}
	if !result.Success {
		t.Error("expected success")
	}
	if result.Output != "running" {
		t.Errorf("Output = %q, want running", result.Output)
	}
}

func TestSendCommand_Timeout(t *testing.T) {
	hub, cleanup := setupHubForCommand(t)
	defer cleanup()

	hub.Register("srv-1")

	msg := &models.Message{
		ID:     "cmd-3",
		Type:   "command",
		Action: "node.start",
	}

	// No response — should timeout
	result, err := hub.SendCommand("srv-1", msg, 200*time.Millisecond)
	if err != nil {
		t.Fatalf("SendCommand: %v", err)
	}
	if result.Success {
		t.Error("expected failure on timeout")
	}
	if result.Error != "command timed out" {
		t.Errorf("Error = %q, want 'command timed out'", result.Error)
	}
}

func TestHandleResult_UnknownCommand(t *testing.T) {
	hub, cleanup := setupHubForCommand(t)
	defer cleanup()

	// Should not panic for unknown command IDs
	hub.HandleResult(&models.CommandResult{
		CommandID: "unknown-cmd",
		Success:   true,
	})

	if hub.PendingCount() != 0 {
		t.Error("pending count should be 0")
	}
}

func TestPendingCount(t *testing.T) {
	hub, cleanup := setupHubForCommand(t)
	defer cleanup()

	hub.Register("srv-1")

	if hub.PendingCount() != 0 {
		t.Errorf("initial pending = %d, want 0", hub.PendingCount())
	}

	// Start a command but don't respond
	msg := &models.Message{ID: "cmd-4", Type: "command", Action: "node.start"}
	go func() {
		_, _ = hub.SendCommand("srv-1", msg, 2*time.Second)
	}()

	time.Sleep(50 * time.Millisecond)
	if hub.PendingCount() != 1 {
		t.Errorf("pending = %d, want 1", hub.PendingCount())
	}

	// Resolve it
	hub.HandleResult(&models.CommandResult{CommandID: "cmd-4", Success: true})
	time.Sleep(50 * time.Millisecond)

	if hub.PendingCount() != 0 {
		t.Errorf("pending after resolve = %d, want 0", hub.PendingCount())
	}
}
