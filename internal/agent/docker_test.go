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

func TestParseContainerToNode_Running(t *testing.T) {
	cj := &containerJSON{
		ID:   "abc123",
		Name: "/klever-node1",
	}
	cj.State.Status = "running"
	cj.State.Running = true
	cj.Config.Image = "kleverapp/klever-go:v0.60.0"
	cj.Config.Cmd = []string{
		"--rest-api-interface", "0.0.0.0:8080",
		"--display-name", "MyNode",
		"--redundancy-level", "1",
	}
	cj.Mounts = []mountPoint{
		{Source: "/opt/klever/node1/config", Destination: "/node/config", Type: "bind"},
		{Source: "/opt/klever/node1/db", Destination: "/node/db", Type: "bind"},
	}

	node := parseContainerToNode(cj)

	if node.ContainerID != "abc123" {
		t.Errorf("ContainerID = %q, want abc123", node.ContainerID)
	}
	if node.ContainerName != "klever-node1" {
		t.Errorf("ContainerName = %q, want klever-node1", node.ContainerName)
	}
	if node.Status != "running" {
		t.Errorf("Status = %q, want running", node.Status)
	}
	if node.DockerImageTag != "v0.60.0" {
		t.Errorf("DockerImageTag = %q, want v0.60.0", node.DockerImageTag)
	}
	if node.RestAPIPort != 8080 {
		t.Errorf("RestAPIPort = %d, want 8080", node.RestAPIPort)
	}
	if node.DisplayName != "MyNode" {
		t.Errorf("DisplayName = %q, want MyNode", node.DisplayName)
	}
	if node.RedundancyLevel != 1 {
		t.Errorf("RedundancyLevel = %d, want 1", node.RedundancyLevel)
	}
	if node.DataDirectory != "/opt/klever/node1" {
		t.Errorf("DataDirectory = %q, want /opt/klever/node1", node.DataDirectory)
	}
	if node.ConfigDirectory != "/opt/klever/node1/config" {
		t.Errorf("ConfigDirectory = %q, want /opt/klever/node1/config", node.ConfigDirectory)
	}
}

func TestParseContainerToNode_Stopped(t *testing.T) {
	cj := &containerJSON{
		ID:   "def456",
		Name: "/klever-backup",
	}
	cj.State.Status = "exited"
	cj.State.Running = false
	cj.Config.Image = "kleverapp/klever-go"
	cj.Config.Cmd = []string{}

	node := parseContainerToNode(cj)

	if node.Status != "stopped" {
		t.Errorf("Status = %q, want stopped", node.Status)
	}
	if node.RestAPIPort != 8080 {
		t.Errorf("RestAPIPort = %d, want 8080 (default)", node.RestAPIPort)
	}
	if node.DockerImageTag != "" {
		t.Errorf("DockerImageTag = %q, want empty (no tag)", node.DockerImageTag)
	}
}

func TestParseContainerToNode_EqualsFormat(t *testing.T) {
	cj := &containerJSON{
		ID:   "ghi789",
		Name: "/klever-node2",
	}
	cj.State.Running = true
	cj.Config.Image = "kleverapp/klever-go:latest"
	cj.Config.Cmd = []string{
		"--rest-api-interface=0.0.0.0:9090",
		"--display-name=TestNode",
		"--redundancy-level=0",
	}

	node := parseContainerToNode(cj)

	if node.RestAPIPort != 9090 {
		t.Errorf("RestAPIPort = %d, want 9090", node.RestAPIPort)
	}
	if node.DisplayName != "TestNode" {
		t.Errorf("DisplayName = %q, want TestNode", node.DisplayName)
	}
}

func TestParseContainerToNode_BindMountFallback(t *testing.T) {
	cj := &containerJSON{
		ID:   "jkl012",
		Name: "/klever-node3",
	}
	cj.State.Running = true
	cj.Config.Image = "kleverapp/klever-go:v0.59.0"
	cj.Config.Cmd = []string{}
	// No Mounts, but HostConfig.Binds set
	cj.HostConfig.Binds = []string{
		"/data/klever/config:/node/config:rw",
		"/data/klever/db:/node/db:rw",
	}

	node := parseContainerToNode(cj)

	if node.ConfigDirectory != "/data/klever/config" {
		t.Errorf("ConfigDirectory = %q, want /data/klever/config", node.ConfigDirectory)
	}
	if node.DataDirectory != "/data/klever" {
		t.Errorf("DataDirectory = %q, want /data/klever", node.DataDirectory)
	}
}

func TestParseStringArg(t *testing.T) {
	tests := []struct {
		args []string
		flag string
		want string
	}{
		{[]string{"--flag", "value"}, "--flag", "value"},
		{[]string{"--flag=value"}, "--flag", "value"},
		{[]string{"--other", "x"}, "--flag", ""},
		{[]string{"--flag"}, "--flag", ""}, // No value after flag
		{[]string{}, "--flag", ""},
	}

	for _, tt := range tests {
		got := parseStringArg(tt.args, tt.flag)
		if got != tt.want {
			t.Errorf("parseStringArg(%v, %q) = %q, want %q", tt.args, tt.flag, got, tt.want)
		}
	}
}

func TestParseIntArg(t *testing.T) {
	tests := []struct {
		args       []string
		flag       string
		defaultVal int
		want       int
	}{
		{[]string{"--port", "9090"}, "--port", 8080, 9090},
		{[]string{"--port", "0.0.0.0:9090"}, "--port", 8080, 9090},
		{[]string{"--port=8888"}, "--port", 8080, 8888},
		{[]string{}, "--port", 8080, 8080},
		{[]string{"--port", "invalid"}, "--port", 8080, 8080},
	}

	for _, tt := range tests {
		got := parseIntArg(tt.args, tt.flag, tt.defaultVal)
		if got != tt.want {
			t.Errorf("parseIntArg(%v, %q, %d) = %d, want %d", tt.args, tt.flag, tt.defaultVal, got, tt.want)
		}
	}
}

func TestParentDir(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/opt/klever/node1/config", "/opt/klever/node1"},
		{"/opt/klever/node1/config/", "/opt/klever/node1"},
		{"/single", "/single"},
		{"noparent", "noparent"},
	}

	for _, tt := range tests {
		got := parentDir(tt.path)
		if got != tt.want {
			t.Errorf("parentDir(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

// TestDiscoverNodesWithMockDocker uses a mock Docker API server over Unix socket.
func TestDiscoverNodesWithMockDocker(t *testing.T) {
	// Create a temporary Unix socket with short path (macOS 104 char limit)
	socketPath, sockCleanup := shortSocketPath(t)
	defer sockCleanup()

	// Mock data
	listResponse := []containerListEntry{
		{ID: "container1", Names: []string{"/klever-node1"}, Image: "kleverapp/klever-go:v0.60.0", State: "running"},
	}
	inspectResponse := containerJSON{
		ID:   "container1",
		Name: "/klever-node1",
	}
	inspectResponse.State.Status = "running"
	inspectResponse.State.Running = true
	inspectResponse.Config.Image = "kleverapp/klever-go:v0.60.0"
	inspectResponse.Config.Cmd = []string{"--rest-api-interface", "0.0.0.0:8080", "--display-name", "TestNode"}
	inspectResponse.Mounts = []mountPoint{
		{Source: "/opt/klever/config", Destination: "/node/config", Type: "bind"},
	}

	// Start mock server
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch { //nolint:gocritic,staticcheck
		case r.URL.Path == "/v1.41/containers/json":
			_ = json.NewEncoder(w).Encode(listResponse)
		case r.URL.Path == "/v1.41/containers/container1/json":
			_ = json.NewEncoder(w).Encode(inspectResponse)
		default:
			http.NotFound(w, r)
		}
	})

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen unix socket: %v", err)
	}
	defer func() { _ = listener.Close() }()

	server := &http.Server{Handler: mux}
	go func() { _ = server.Serve(listener) }()
	defer func() { _ = server.Close() }()

	// Test discovery
	client := NewDockerClient(socketPath)
	nodes, err := client.DiscoverNodes(context.Background())
	if err != nil {
		t.Fatalf("DiscoverNodes: %v", err)
	}

	if len(nodes) != 1 {
		t.Fatalf("got %d nodes, want 1", len(nodes))
	}

	n := nodes[0]
	if n.ContainerID != "container1" {
		t.Errorf("ContainerID = %q, want container1", n.ContainerID)
	}
	if n.ContainerName != "klever-node1" {
		t.Errorf("ContainerName = %q, want klever-node1", n.ContainerName)
	}
	if n.Status != "running" {
		t.Errorf("Status = %q, want running", n.Status)
	}
	if n.RestAPIPort != 8080 {
		t.Errorf("RestAPIPort = %d, want 8080", n.RestAPIPort)
	}
	if n.DisplayName != "TestNode" {
		t.Errorf("DisplayName = %q, want TestNode", n.DisplayName)
	}
}

// TestDiscoverNodesEmpty tests discovery on a server with no Klever containers.
func TestDiscoverNodesEmpty(t *testing.T) {
	socketPath, sockCleanup := shortSocketPath(t)
	defer sockCleanup()

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]containerListEntry{})
	})

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen unix socket: %v", err)
	}
	defer func() { _ = listener.Close() }()

	server := &http.Server{Handler: mux}
	go func() { _ = server.Serve(listener) }()
	defer func() { _ = server.Close() }()

	client := NewDockerClient(socketPath)
	nodes, err := client.DiscoverNodes(context.Background())
	if err != nil {
		t.Fatalf("DiscoverNodes: %v", err)
	}

	if len(nodes) != 0 {
		t.Errorf("got %d nodes, want 0", len(nodes))
	}
}

// TestDiscoverNodesMultiple tests discovery with multiple containers.
func TestDiscoverNodesMultiple(t *testing.T) {
	socketPath, sockCleanup := shortSocketPath(t)
	defer sockCleanup()

	listResponse := []containerListEntry{
		{ID: "c1", Names: []string{"/klever-node1"}, Image: "kleverapp/klever-go:v0.60.0", State: "running"},
		{ID: "c2", Names: []string{"/klever-backup"}, Image: "kleverapp/klever-go:v0.59.0", State: "exited"},
	}

	inspectC1 := containerJSON{ID: "c1", Name: "/klever-node1"}
	inspectC1.State.Running = true
	inspectC1.Config.Image = "kleverapp/klever-go:v0.60.0"
	inspectC1.Config.Cmd = []string{"--rest-api-interface", "0.0.0.0:8080"}

	inspectC2 := containerJSON{ID: "c2", Name: "/klever-backup"}
	inspectC2.State.Running = false
	inspectC2.Config.Image = "kleverapp/klever-go:v0.59.0"
	inspectC2.Config.Cmd = []string{"--rest-api-interface", "0.0.0.0:9090", "--redundancy-level", "1"}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1.41/containers/json":
			_ = json.NewEncoder(w).Encode(listResponse)
		case "/v1.41/containers/c1/json":
			_ = json.NewEncoder(w).Encode(inspectC1)
		case "/v1.41/containers/c2/json":
			_ = json.NewEncoder(w).Encode(inspectC2)
		default:
			http.NotFound(w, r)
		}
	})

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen unix socket: %v", err)
	}
	defer func() { _ = listener.Close() }()

	server := &http.Server{Handler: mux}
	go func() { _ = server.Serve(listener) }()
	defer func() { _ = server.Close() }()

	client := NewDockerClient(socketPath)
	nodes, err := client.DiscoverNodes(context.Background())
	if err != nil {
		t.Fatalf("DiscoverNodes: %v", err)
	}

	if len(nodes) != 2 {
		t.Fatalf("got %d nodes, want 2", len(nodes))
	}

	if nodes[0].Status != "running" {
		t.Errorf("node[0].Status = %q, want running", nodes[0].Status)
	}
	if nodes[1].Status != "stopped" {
		t.Errorf("node[1].Status = %q, want stopped", nodes[1].Status)
	}
	if nodes[1].RedundancyLevel != 1 {
		t.Errorf("node[1].RedundancyLevel = %d, want 1", nodes[1].RedundancyLevel)
	}
}

// TestDockerClientNoSocket tests behavior when Docker socket is not available.
func TestDockerClientNoSocket(t *testing.T) {
	client := NewDockerClient("/nonexistent/docker.sock")
	_, err := client.ListKleverContainers(context.Background())
	if err == nil {
		t.Error("expected error for nonexistent socket, got nil")
	}
}

// TestBLSPublicKeyExtraction tests BLS key extraction from PEM files.
func TestBLSPublicKeyExtraction(t *testing.T) {
	// Create a mock validatorKey.pem
	tmpDir := t.TempDir()
	pemContent := `-----BEGIN PRIVATE KEY for a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6-----
bW9jayBwcml2YXRlIGtleSBkYXRh
-----END PRIVATE KEY for a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6-----`

	err := os.WriteFile(filepath.Join(tmpDir, "validatorKey.pem"), []byte(pemContent), 0600)
	if err != nil {
		t.Fatalf("write mock PEM: %v", err)
	}

	key, err := ExtractBLSPublicKey(tmpDir)
	if err != nil {
		t.Fatalf("ExtractBLSPublicKey: %v", err)
	}

	expected := "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6"
	if key != expected {
		t.Errorf("BLS key = %q, want %q", key, expected)
	}
}

func TestBLSPublicKeyExtractionMissingFile(t *testing.T) {
	_, err := ExtractBLSPublicKey(t.TempDir())
	if err == nil {
		t.Error("expected error for missing validatorKey.pem, got nil")
	}
}

func TestParseBLSPublicKeyFromPEM_InvalidHeader(t *testing.T) {
	_, err := parseBLSPublicKeyFromPEM("-----BEGIN RSA PRIVATE KEY-----\ndata\n-----END RSA PRIVATE KEY-----")
	if err == nil {
		t.Error("expected error for non-Klever PEM format, got nil")
	}
}

func TestParseBLSPublicKeyFromPEM_InvalidHex(t *testing.T) {
	_, err := parseBLSPublicKeyFromPEM("-----BEGIN PRIVATE KEY for ZZZZ-----\ndata\n-----END PRIVATE KEY for ZZZZ-----")
	if err == nil {
		t.Error("expected error for invalid hex, got nil")
	}
}

func TestParseBLSPublicKeyFromPEM_Empty(t *testing.T) {
	_, err := parseBLSPublicKeyFromPEM("")
	if err == nil {
		t.Error("expected error for empty PEM, got nil")
	}
}
