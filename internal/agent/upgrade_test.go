package agent

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// newMockDockerForUpgrade creates a mock Docker API that simulates a full upgrade flow.
func newMockDockerForUpgrade(t *testing.T, failCreate bool) (*DockerClient, func()) {
	t.Helper()
	socketPath, sockCleanup := shortSocketPath(t)

	inspectResp := containerJSON{
		ID:   "old-container-123",
		Name: "/klever-node1",
	}
	inspectResp.State.Status = "running"
	inspectResp.State.Running = true
	inspectResp.Config.Image = "kleverapp/klever-go:v0.59.0"
	inspectResp.Config.Cmd = []string{
		"--rest-api-interface=0.0.0.0:8080",
		"--display-name=TestNode",
	}
	inspectResp.Mounts = []mountPoint{
		{Source: "/opt/klever/node1/config", Destination: "/node/config", Type: "bind"},
	}

	createCount := 0

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		switch {
		// Inspect container
		case r.Method == http.MethodGet && strings.HasSuffix(path, "/json") && strings.Contains(path, "/containers/"):
			_ = json.NewEncoder(w).Encode(inspectResp)

		// Pull image
		case r.Method == http.MethodPost && strings.Contains(path, "/images/create"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"Pulling complete"}`))

		// Stop container
		case r.Method == http.MethodPost && strings.Contains(path, "/stop"):
			w.WriteHeader(http.StatusNoContent)

		// Remove container
		case r.Method == http.MethodDelete && strings.Contains(path, "/containers/"):
			w.WriteHeader(http.StatusNoContent)

		// Create container
		case r.Method == http.MethodPost && strings.Contains(path, "/containers/create"):
			createCount++
			if failCreate && createCount == 1 {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"message":"simulated create failure"}`))
				return
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(containerCreateResponse{
				ID: "new-container-456",
			})

		// Start container
		case r.Method == http.MethodPost && strings.Contains(path, "/start"):
			w.WriteHeader(http.StatusNoContent)

		default:
			http.NotFound(w, r)
		}
	})

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	server := &http.Server{Handler: mux}
	go func() { _ = server.Serve(listener) }()

	client := NewDockerClient(socketPath)
	return client, func() {
		_ = server.Close()
		_ = listener.Close()
		sockCleanup()
	}
}

func TestUpgradeContainerWithRollback_Success(t *testing.T) {
	client, cleanup := newMockDockerForUpgrade(t, false)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var steps []string
	progress := func(step, total int, name, status string) {
		steps = append(steps, name+":"+status)
	}

	newID, err := client.UpgradeContainerWithRollback(ctx, "klever-node1", "v0.60.0", progress)
	if err != nil {
		t.Fatalf("UpgradeContainerWithRollback: %v", err)
	}

	if newID != "new-container-456" {
		t.Errorf("newID = %q, want new-container-456", newID)
	}

	// Verify progress steps were reported
	if len(steps) < 6 {
		t.Errorf("expected at least 6 progress reports, got %d: %v", len(steps), steps)
	}

	// Check key steps were reported
	hasSnapshot := false
	hasPulling := false
	hasVerifying := false
	for _, s := range steps {
		if strings.HasPrefix(s, "snapshot:") {
			hasSnapshot = true
		}
		if strings.HasPrefix(s, "pulling:") {
			hasPulling = true
		}
		if strings.HasPrefix(s, "verifying:") {
			hasVerifying = true
		}
	}
	if !hasSnapshot {
		t.Error("missing snapshot progress step")
	}
	if !hasPulling {
		t.Error("missing pulling progress step")
	}
	if !hasVerifying {
		t.Error("missing verifying progress step")
	}
}

func TestUpgradeContainerWithRollback_CreateFails_Rollback(t *testing.T) {
	client, cleanup := newMockDockerForUpgrade(t, true)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err := client.UpgradeContainerWithRollback(ctx, "klever-node1", "v0.60.0")
	if err == nil {
		t.Fatal("expected error when create fails")
	}

	if !strings.Contains(err.Error(), "rolled back") {
		t.Errorf("error should mention rollback, got: %v", err)
	}
}

func TestUpgradeContainerWithRollback_NoProgress(t *testing.T) {
	client, cleanup := newMockDockerForUpgrade(t, false)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Should work without progress callback
	newID, err := client.UpgradeContainerWithRollback(ctx, "klever-node1", "v0.60.0")
	if err != nil {
		t.Fatalf("UpgradeContainerWithRollback without progress: %v", err)
	}
	if newID == "" {
		t.Error("expected non-empty container ID")
	}
}

func TestUpgradeProgress_TotalSteps(t *testing.T) {
	client, cleanup := newMockDockerForUpgrade(t, false)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var totalSeen int
	progress := func(step, total int, name, status string) {
		totalSeen = total
	}

	_, err := client.UpgradeContainerWithRollback(ctx, "klever-node1", "v0.60.0", progress)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if totalSeen != 6 {
		t.Errorf("total steps = %d, want 6", totalSeen)
	}
}

func TestRollback(t *testing.T) {
	client, cleanup := newMockDockerForUpgrade(t, false)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	snapshot := &DiscoveredNode{
		ContainerName: "klever-node1",
		DataDirectory: "/opt/klever/node1",
		RestAPIPort:   8080,
		DisplayName:   "TestNode",
	}

	err := client.rollback(ctx, "klever-node1", "v0.59.0", snapshot)
	if err != nil {
		t.Errorf("rollback: %v", err)
	}
}
