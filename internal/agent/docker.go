package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultDockerSocket = "/var/run/docker.sock"
	dockerAPIVersion    = "v1.24" // Minimum API version for inspect
	kleverImage         = "kleverapp/klever-go"
)

// DockerClient talks to the Docker Engine API via Unix socket.
type DockerClient struct {
	httpClient *http.Client
	host       string // socket path
}

// NewDockerClient creates a client connected to the Docker socket.
func NewDockerClient(socketPath string) *DockerClient {
	if socketPath == "" {
		socketPath = defaultDockerSocket
	}

	transport := &http.Transport{
		DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
			return net.DialTimeout("unix", socketPath, 5*time.Second)
		},
	}

	return &DockerClient{
		httpClient: &http.Client{Transport: transport, Timeout: 30 * time.Second},
		host:       socketPath,
	}
}

// containerJSON is the minimal subset of Docker's container inspect response.
type containerJSON struct {
	ID    string `json:"Id"`
	Name  string `json:"Name"`
	State struct {
		Status  string `json:"Status"` // "running", "exited", etc.
		Running bool   `json:"Running"`
	} `json:"State"`
	Config struct {
		Image string   `json:"Image"`
		Cmd   []string `json:"Cmd"`
	} `json:"Config"`
	HostConfig struct {
		Binds []string `json:"Binds"` // "host:container[:opts]"
	} `json:"HostConfig"`
	Mounts []mountPoint `json:"Mounts"`
}

type mountPoint struct {
	Source      string `json:"Source"`
	Destination string `json:"Destination"`
	Type        string `json:"Type"` // "bind", "volume", etc.
}

// containerListEntry is the minimal subset of Docker's container list response.
type containerListEntry struct {
	ID    string `json:"Id"`
	Names []string `json:"Names"`
	Image string `json:"Image"`
	State string `json:"State"` // "running", "exited"
}

// ListKleverContainers returns IDs of all containers using the klever-go image.
func (d *DockerClient) ListKleverContainers(ctx context.Context) ([]string, error) {
	// List all containers (including stopped) filtered by ancestor image
	filterJSON, _ := json.Marshal(map[string][]string{
		"ancestor": {kleverImage},
	})

	u := fmt.Sprintf("http://localhost/%s/containers/json?all=true&filters=%s",
		dockerAPIVersion, url.QueryEscape(string(filterJSON)))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list containers: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("list containers: HTTP %d: %s", resp.StatusCode, string(body))
	}

	var entries []containerListEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return nil, fmt.Errorf("decode container list: %w", err)
	}

	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		ids = append(ids, e.ID)
	}
	return ids, nil
}

// InspectContainer returns detailed information about a container.
func (d *DockerClient) InspectContainer(ctx context.Context, containerID string) (*containerJSON, error) {
	u := fmt.Sprintf("http://localhost/%s/containers/%s/json", dockerAPIVersion, containerID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("inspect container %s: %w", containerID, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("inspect container %s: HTTP %d: %s", containerID, resp.StatusCode, string(body))
	}

	var cj containerJSON
	if err := json.NewDecoder(resp.Body).Decode(&cj); err != nil {
		return nil, fmt.Errorf("decode inspect %s: %w", containerID, err)
	}

	return &cj, nil
}

// DiscoveredNode holds the information extracted from a running Klever container.
type DiscoveredNode struct {
	ContainerID     string `json:"container_id"`
	ContainerName   string `json:"container_name"`
	Status          string `json:"status"` // "running" or "stopped"
	RestAPIPort     int    `json:"rest_api_port"`
	DisplayName     string `json:"display_name,omitempty"`
	RedundancyLevel int    `json:"redundancy_level"`
	DockerImageTag  string `json:"docker_image_tag,omitempty"`
	DataDirectory   string `json:"data_directory,omitempty"`
	ConfigDirectory string `json:"config_directory,omitempty"`
	BLSPublicKey    string `json:"bls_public_key,omitempty"`
}

// DiscoverNodes scans Docker for all Klever containers and extracts their configuration.
func (d *DockerClient) DiscoverNodes(ctx context.Context) ([]DiscoveredNode, error) {
	ids, err := d.ListKleverContainers(ctx)
	if err != nil {
		return nil, err
	}

	nodes := make([]DiscoveredNode, 0, len(ids))
	for _, id := range ids {
		cj, err := d.InspectContainer(ctx, id)
		if err != nil {
			continue // Skip containers we can't inspect
		}

		node := parseContainerToNode(cj)
		nodes = append(nodes, node)
	}

	return nodes, nil
}

// parseContainerToNode extracts node parameters from a container inspect result.
func parseContainerToNode(cj *containerJSON) DiscoveredNode {
	node := DiscoveredNode{
		ContainerID:   cj.ID,
		ContainerName: strings.TrimPrefix(cj.Name, "/"),
	}

	// Status
	if cj.State.Running {
		node.Status = "running"
	} else {
		node.Status = "stopped"
	}

	// Docker image tag
	if parts := strings.SplitN(cj.Config.Image, ":", 2); len(parts) == 2 {
		node.DockerImageTag = parts[1]
	}

	// Parse command-line arguments
	node.RestAPIPort = parseIntArg(cj.Config.Cmd, "--rest-api-interface", 8080)
	node.DisplayName = parseStringArg(cj.Config.Cmd, "--display-name")
	node.RedundancyLevel = parseIntArg(cj.Config.Cmd, "--redundancy-level", 0)

	// Extract directories from mounts
	for _, m := range cj.Mounts {
		switch {
		case strings.HasSuffix(m.Destination, "/node/config"):
			node.ConfigDirectory = m.Source
			// Data directory is the parent of config mount's source
			node.DataDirectory = parentDir(m.Source)
		case strings.HasSuffix(m.Destination, "/node/db"):
			if node.DataDirectory == "" {
				node.DataDirectory = parentDir(m.Source)
			}
		}
	}

	// Fallback: check bind mounts if Mounts is empty
	if node.ConfigDirectory == "" {
		for _, bind := range cj.HostConfig.Binds {
			parts := strings.SplitN(bind, ":", 3)
			if len(parts) >= 2 && strings.HasSuffix(parts[1], "/node/config") {
				node.ConfigDirectory = parts[0]
				node.DataDirectory = parentDir(parts[0])
				break
			}
		}
	}

	return node
}

// parseStringArg extracts a string value from command args like ["--flag", "value"].
func parseStringArg(args []string, flag string) string {
	for i, arg := range args {
		if arg == flag && i+1 < len(args) {
			return args[i+1]
		}
		// Handle --flag=value format
		if strings.HasPrefix(arg, flag+"=") {
			return strings.TrimPrefix(arg, flag+"=")
		}
	}
	return ""
}

// parseIntArg extracts an integer value from command args. Returns defaultVal if not found.
func parseIntArg(args []string, flag string, defaultVal int) int {
	s := parseStringArg(args, flag)
	if s == "" {
		return defaultVal
	}

	// Extract port number from address like "0.0.0.0:8080"
	if idx := strings.LastIndex(s, ":"); idx >= 0 {
		s = s[idx+1:]
	}

	var val int
	if _, err := fmt.Sscanf(s, "%d", &val); err != nil {
		return defaultVal
	}
	return val
}

// parentDir returns the parent directory of a path.
func parentDir(path string) string {
	// Simple string-based parent (works for Unix paths)
	path = strings.TrimSuffix(path, "/")
	if idx := strings.LastIndex(path, "/"); idx > 0 {
		return path[:idx]
	}
	return path
}
