package agent

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/CTJaeger/KleverNodeHub/internal/models"
)

func newMockDockerForExecutor(t *testing.T) (*DockerClient, func()) {
	t.Helper()
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "docker.sock")

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && contains(r.URL.Path, "/start"):
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && contains(r.URL.Path, "/stop"):
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && contains(r.URL.Path, "/restart"):
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && contains(r.URL.Path, "/json"):
			cj := containerJSON{ID: "test123", Name: "/test-container"}
			cj.State.Running = true
			cj.Config.Image = "kleverapp/klever-go:v0.60.0"
			json.NewEncoder(w).Encode(cj)
		default:
			http.NotFound(w, r)
		}
	})

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	server := &http.Server{Handler: mux}
	go server.Serve(listener)

	client := NewDockerClient(socketPath)
	return client, func() {
		server.Close()
		listener.Close()
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchSubstring(s, substr)
}

func searchSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestExecutor_Start(t *testing.T) {
	client, cleanup := newMockDockerForExecutor(t)
	defer cleanup()

	exec := NewExecutorWithClient(client)
	msg := &models.Message{
		ID:     "cmd-1",
		Type:   "command",
		Action: "node.start",
		Payload: map[string]any{
			"container_name": "klever-node1",
		},
	}

	result := exec.Execute(msg)
	if !result.Success {
		t.Errorf("expected success, got error: %s", result.Error)
	}
	if result.CommandID != "cmd-1" {
		t.Errorf("CommandID = %q, want cmd-1", result.CommandID)
	}
}

func TestExecutor_Stop(t *testing.T) {
	client, cleanup := newMockDockerForExecutor(t)
	defer cleanup()

	exec := NewExecutorWithClient(client)
	msg := &models.Message{
		ID:     "cmd-2",
		Type:   "command",
		Action: "node.stop",
		Payload: map[string]any{
			"container_name": "klever-node1",
		},
	}

	result := exec.Execute(msg)
	if !result.Success {
		t.Errorf("expected success, got error: %s", result.Error)
	}
}

func TestExecutor_Restart(t *testing.T) {
	client, cleanup := newMockDockerForExecutor(t)
	defer cleanup()

	exec := NewExecutorWithClient(client)
	msg := &models.Message{
		ID:     "cmd-3",
		Type:   "command",
		Action: "node.restart",
		Payload: map[string]any{
			"container_name": "klever-node1",
		},
	}

	result := exec.Execute(msg)
	if !result.Success {
		t.Errorf("expected success, got error: %s", result.Error)
	}
}

func TestExecutor_Status(t *testing.T) {
	client, cleanup := newMockDockerForExecutor(t)
	defer cleanup()

	exec := NewExecutorWithClient(client)
	msg := &models.Message{
		ID:     "cmd-4",
		Type:   "command",
		Action: "node.status",
		Payload: map[string]any{
			"container_name": "klever-node1",
		},
	}

	result := exec.Execute(msg)
	if !result.Success {
		t.Errorf("expected success, got error: %s", result.Error)
	}
	if result.Output != "running" {
		t.Errorf("Output = %q, want running", result.Output)
	}
}

func TestExecutor_RejectedCommand(t *testing.T) {
	client, cleanup := newMockDockerForExecutor(t)
	defer cleanup()

	exec := NewExecutorWithClient(client)
	msg := &models.Message{
		ID:     "cmd-5",
		Type:   "command",
		Action: "system.exec",
		Payload: map[string]any{
			"container_name": "klever-node1",
		},
	}

	result := exec.Execute(msg)
	if result.Success {
		t.Error("expected failure for rejected command")
	}
	if result.Error == "" {
		t.Error("expected error message for rejected command")
	}
}

func TestExecutor_MissingContainerName(t *testing.T) {
	client, cleanup := newMockDockerForExecutor(t)
	defer cleanup()

	exec := NewExecutorWithClient(client)
	msg := &models.Message{
		ID:     "cmd-6",
		Type:   "command",
		Action: "node.start",
		// No payload
	}

	result := exec.Execute(msg)
	if result.Success {
		t.Error("expected failure for missing container name")
	}
}

func TestExtractContainerName(t *testing.T) {
	tests := []struct {
		payload any
		want    string
	}{
		{map[string]any{"container_name": "klever-node1"}, "klever-node1"},
		{map[string]string{"container_name": "klever-node2"}, "klever-node2"},
		{map[string]any{"other": "value"}, ""},
		{nil, ""},
		{"string", ""},
	}

	for _, tt := range tests {
		got := extractContainerName(tt.payload)
		if got != tt.want {
			t.Errorf("extractContainerName(%v) = %q, want %q", tt.payload, got, tt.want)
		}
	}
}

func TestDockerStartContainer(t *testing.T) {
	client, cleanup := newMockDockerForExecutor(t)
	defer cleanup()

	err := client.StartContainer(context.Background(), "klever-node1")
	if err != nil {
		t.Errorf("StartContainer: %v", err)
	}
}

func TestDockerStopContainer(t *testing.T) {
	client, cleanup := newMockDockerForExecutor(t)
	defer cleanup()

	err := client.StopContainer(context.Background(), "klever-node1", 30)
	if err != nil {
		t.Errorf("StopContainer: %v", err)
	}
}

func TestDockerRestartContainer(t *testing.T) {
	client, cleanup := newMockDockerForExecutor(t)
	defer cleanup()

	err := client.RestartContainer(context.Background(), "klever-node1", 30)
	if err != nil {
		t.Errorf("RestartContainer: %v", err)
	}
}

func TestDockerGetContainerStatus(t *testing.T) {
	client, cleanup := newMockDockerForExecutor(t)
	defer cleanup()

	status, err := client.GetContainerStatus(context.Background(), "klever-node1")
	if err != nil {
		t.Errorf("GetContainerStatus: %v", err)
	}
	if status != "running" {
		t.Errorf("status = %q, want running", status)
	}
}

func TestBuildResultMessage(t *testing.T) {
	result := &models.CommandResult{
		CommandID: "cmd-1",
		Success:   true,
		Output:    "running",
	}

	msg := BuildResultMessage(result)
	if msg.Type != "response" {
		t.Errorf("Type = %q, want response", msg.Type)
	}
	if msg.Action != "command.result" {
		t.Errorf("Action = %q, want command.result", msg.Action)
	}
}
