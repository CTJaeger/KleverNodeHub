package agent

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func newMockDockerForOps(t *testing.T) (*DockerClient, func()) {
	t.Helper()
	socketPath, sockCleanup := shortSocketPath(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		switch {
		// Pull image
		case r.Method == http.MethodPost && containsPath(path, "/images/create"):
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"Pull complete"}`))

		// Create container
		case r.Method == http.MethodPost && containsPath(path, "/containers/create"):
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(containerCreateResponse{
				ID: "new-container-id-1234567890ab",
			})

		// Delete container
		case r.Method == http.MethodDelete && containsPath(path, "/containers/"):
			w.WriteHeader(http.StatusNoContent)

		// Start container
		case r.Method == http.MethodPost && containsPath(path, "/start"):
			w.WriteHeader(http.StatusNoContent)

		// Stop container
		case r.Method == http.MethodPost && containsPath(path, "/stop"):
			w.WriteHeader(http.StatusNoContent)

		// Restart container
		case r.Method == http.MethodPost && containsPath(path, "/restart"):
			w.WriteHeader(http.StatusNoContent)

		// Inspect container
		case r.Method == http.MethodGet && containsPath(path, "/containers/") && containsPath(path, "/json"):
			cj := containerJSON{ID: "test123", Name: "/klever-node1"}
			cj.State.Running = true
			cj.Config.Image = "kleverapp/klever-go:v0.60.0"
			cj.Config.Cmd = []string{
				"--rest-api-interface", "0.0.0.0:8080",
				"--display-name", "TestNode",
			}
			cj.Mounts = []mountPoint{
				{Source: "/opt/klever/config", Destination: "/opt/klever-blockchain/config/node"},
				{Source: "/opt/klever/db", Destination: "/opt/klever-blockchain/db"},
			}
			_ = json.NewEncoder(w).Encode(cj)

		// List images
		case r.Method == http.MethodGet && containsPath(path, "/images/json"):
			_ = json.NewEncoder(w).Encode([]struct {
				RepoTags []string `json:"RepoTags"`
			}{
				{RepoTags: []string{"kleverapp/klever-go:v0.60.0", "kleverapp/klever-go:latest"}},
			})

		// List containers
		case r.Method == http.MethodGet && containsPath(path, "/containers/json"):
			_ = json.NewEncoder(w).Encode([]containerListEntry{
				{ID: "test123", Names: []string{"/klever-node1"}, State: "running"},
			})

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
		sockCleanup()
	}
}

func containsPath(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestPullImage(t *testing.T) {
	client, cleanup := newMockDockerForOps(t)
	defer cleanup()

	err := client.PullImage(context.Background(), "kleverapp/klever-go:v0.60.0")
	if err != nil {
		t.Errorf("PullImage: %v", err)
	}
}

func TestPullImageWithProgress(t *testing.T) {
	client, cleanup := newMockDockerForOps(t)
	defer cleanup()

	var progressCalls int
	err := client.PullImageWithProgress(context.Background(), "kleverapp/klever-go:v0.60.0", func(status string) {
		progressCalls++
	})
	if err != nil {
		t.Errorf("PullImageWithProgress: %v", err)
	}
}

func TestCreateContainer(t *testing.T) {
	client, cleanup := newMockDockerForOps(t)
	defer cleanup()

	cfg := &ContainerConfig{
		Name:        "klever-node2",
		ImageTag:    "v0.60.0",
		DataDir:     t.TempDir(),
		RestAPIPort: 8080,
		DisplayName: "Test Node",
	}

	id, err := client.CreateContainer(context.Background(), cfg)
	if err != nil {
		t.Fatalf("CreateContainer: %v", err)
	}

	if id == "" {
		t.Error("expected non-empty container ID")
	}
}

func TestCreateContainer_Validation(t *testing.T) {
	client, cleanup := newMockDockerForOps(t)
	defer cleanup()

	tests := []struct {
		name string
		cfg  ContainerConfig
		desc string
	}{
		{"missing-name", ContainerConfig{ImageTag: "v1", DataDir: "/tmp", RestAPIPort: 8080}, "empty name"},
		{"missing-tag", ContainerConfig{Name: "test", DataDir: "/tmp", RestAPIPort: 8080}, "empty tag"},
		{"missing-dir", ContainerConfig{Name: "test", ImageTag: "v1", RestAPIPort: 8080}, "empty data dir"},
		{"bad-port", ContainerConfig{Name: "test", ImageTag: "v1", DataDir: "/tmp", RestAPIPort: 0}, "invalid port"},
		{"bad-name", ContainerConfig{Name: "../test", ImageTag: "v1", DataDir: "/tmp", RestAPIPort: 8080}, "invalid name"},
		{"bad-tag", ContainerConfig{Name: "test", ImageTag: "v1; rm -rf /", DataDir: "/tmp", RestAPIPort: 8080}, "injection tag"},
	}

	for _, tt := range tests {
		_, err := client.CreateContainer(context.Background(), &tt.cfg)
		if err == nil {
			t.Errorf("%s: expected error for %s", tt.name, tt.desc)
		}
	}
}

func TestRemoveContainer(t *testing.T) {
	client, cleanup := newMockDockerForOps(t)
	defer cleanup()

	err := client.RemoveContainer(context.Background(), "klever-node1", false)
	if err != nil {
		t.Errorf("RemoveContainer: %v", err)
	}
}

func TestUpgradeContainer(t *testing.T) {
	client, cleanup := newMockDockerForOps(t)
	defer cleanup()

	id, err := client.UpgradeContainer(context.Background(), "klever-node1", "v0.61.0")
	if err != nil {
		t.Fatalf("UpgradeContainer: %v", err)
	}

	if id == "" {
		t.Error("expected non-empty container ID")
	}
}

func TestListLocalImages(t *testing.T) {
	client, cleanup := newMockDockerForOps(t)
	defer cleanup()

	tags, err := client.ListLocalImages(context.Background())
	if err != nil {
		t.Fatalf("ListLocalImages: %v", err)
	}

	if len(tags) != 2 {
		t.Errorf("expected 2 tags, got %d", len(tags))
	}
}

func TestEnsureDataDirs(t *testing.T) {
	tmpDir := t.TempDir()
	dataDir := filepath.Join(tmpDir, "testnode")

	err := EnsureDataDirs(dataDir)
	if err != nil {
		t.Fatalf("EnsureDataDirs: %v", err)
	}

	// Check all directories exist
	for _, sub := range []string{"config", "db", "logs", "wallet"} {
		path := filepath.Join(dataDir, sub)
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("directory %s not created: %v", sub, err)
		} else if !info.IsDir() {
			t.Errorf("%s is not a directory", sub)
		}
	}
}

func TestRemoveDataDirectory_SafetyCheck(t *testing.T) {
	// Should refuse to remove root
	err := RemoveDataDirectory("/")
	if err == nil {
		t.Error("should refuse to remove /")
	}

	err = RemoveDataDirectory("")
	if err == nil {
		t.Error("should refuse to remove empty path")
	}
}

func TestValidateContainerConfig(t *testing.T) {
	valid := &ContainerConfig{
		Name:        "klever-node1",
		ImageTag:    "v0.60.0",
		DataDir:     "/opt/klever",
		RestAPIPort: 8080,
	}
	if err := validateContainerConfig(valid); err != nil {
		t.Errorf("valid config rejected: %v", err)
	}
}

func TestFindAvailablePort(t *testing.T) {
	// Should return a port >= 8080
	port := FindAvailablePort(8080)
	if port < 8080 || port > 8180 {
		t.Errorf("FindAvailablePort(8080) = %d, expected 8080-8180", port)
	}
}

func TestExtractContainerConfig(t *testing.T) {
	payload := map[string]any{
		"name":             "klever-node1",
		"image_tag":        "v0.60.0",
		"data_dir":         "/opt/klever",
		"rest_api_port":    float64(8080),
		"display_name":     "Test",
		"redundancy_level": float64(1),
	}

	cfg := extractContainerConfig(payload)
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if cfg.Name != "klever-node1" {
		t.Errorf("Name = %q", cfg.Name)
	}
	if cfg.RestAPIPort != 8080 {
		t.Errorf("RestAPIPort = %d", cfg.RestAPIPort)
	}
	if cfg.RedundancyLevel != 1 {
		t.Errorf("RedundancyLevel = %d", cfg.RedundancyLevel)
	}
}

func TestExtractContainerConfig_Nil(t *testing.T) {
	cfg := extractContainerConfig(nil)
	if cfg != nil {
		t.Error("expected nil for nil payload")
	}

	cfg = extractContainerConfig("string")
	if cfg != nil {
		t.Error("expected nil for non-map payload")
	}
}

func TestExtractStringField(t *testing.T) {
	payload := map[string]any{"key": "value"}
	if got := extractStringField(payload, "key"); got != "value" {
		t.Errorf("got %q, want value", got)
	}
	if got := extractStringField(payload, "missing"); got != "" {
		t.Errorf("got %q, want empty", got)
	}
	if got := extractStringField(nil, "key"); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}
