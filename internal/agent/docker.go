package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"time"
)

const (
	defaultDockerSocket = "/var/run/docker.sock"
	dockerAPIVersion    = "v1.41" // Docker Engine 20.10+
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
		Tty   bool     `json:"Tty"`
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
	ID    string   `json:"Id"`
	Names []string `json:"Names"`
	Image string   `json:"Image"`
	State string   `json:"State"` // "running", "exited"
}

// ListKleverContainers returns IDs of all containers using the klever-go image.
// We list ALL containers and filter by image name ourselves, because Docker's
// "ancestor" filter matches by image ID which can miss containers created from
// a different pull/digest of the same image name.
func (d *DockerClient) ListKleverContainers(ctx context.Context) ([]string, error) {
	u := fmt.Sprintf("http://localhost/%s/containers/json?all=true",
		dockerAPIVersion)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list containers: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

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
		// Match image name with or without tag (e.g. "kleverapp/klever-go:v1.7.15")
		if e.Image == kleverImage || strings.HasPrefix(e.Image, kleverImage+":") {
			ids = append(ids, e.ID)
		}
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
	defer func() { _ = resp.Body.Close() }()

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
	ContainerID     string  `json:"container_id"`
	ContainerName   string  `json:"container_name"`
	Status          string  `json:"status"` // "running" or "stopped"
	RestAPIPort     int     `json:"rest_api_port"`
	DisplayName     string  `json:"display_name,omitempty"`
	RedundancyLevel int     `json:"redundancy_level"`
	DockerImageTag  string  `json:"docker_image_tag,omitempty"`
	DataDirectory   string  `json:"data_directory,omitempty"`
	ConfigDirectory string  `json:"config_directory,omitempty"`
	BLSPublicKey    string  `json:"bls_public_key,omitempty"`
	CPUPercent      float64 `json:"cpu_percent"`
	MemUsed         uint64  `json:"mem_used"`
	MemLimit        uint64  `json:"mem_limit"`
	MemPercent      float64 `json:"mem_percent"`
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

		// Fetch container resource stats for running containers
		if node.Status == "running" {
			statsCtx, statsCancel := context.WithTimeout(ctx, 5*time.Second)
			stats, err := d.containerStats(statsCtx, id)
			statsCancel()
			if err != nil {
				log.Printf("container stats %s: %v", node.ContainerName, err)
			} else {
				node.CPUPercent = stats.cpuPercent
				node.MemUsed = stats.memUsed
				node.MemLimit = stats.memLimit
				if stats.memLimit > 0 {
					node.MemPercent = float64(stats.memUsed) / float64(stats.memLimit) * 100
				}
			}
		}

		nodes = append(nodes, node)
	}

	return nodes, nil
}

// containerStatsResult holds parsed container stats.
type containerStatsResult struct {
	cpuPercent float64
	memUsed    uint64
	memLimit   uint64
}

// containerStats fetches a one-shot stats snapshot for a container.
func (d *DockerClient) containerStats(ctx context.Context, containerID string) (*containerStatsResult, error) {
	u := fmt.Sprintf("http://localhost/%s/containers/%s/stats?stream=false&one-shot=true",
		dockerAPIVersion, containerID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("create stats request: %w", err)
	}

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("container stats %s: %w", containerID, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("container stats %s: HTTP %d: %s", containerID, resp.StatusCode, string(body))
	}

	var raw struct {
		CPUStats struct {
			CPUUsage struct {
				TotalUsage uint64 `json:"total_usage"`
			} `json:"cpu_usage"`
			SystemCPUUsage uint64 `json:"system_cpu_usage"`
			OnlineCPUs     int    `json:"online_cpus"`
		} `json:"cpu_stats"`
		PreCPUStats struct {
			CPUUsage struct {
				TotalUsage uint64 `json:"total_usage"`
			} `json:"cpu_usage"`
			SystemCPUUsage uint64 `json:"system_cpu_usage"`
		} `json:"precpu_stats"`
		MemoryStats struct {
			Usage uint64 `json:"usage"`
			Limit uint64 `json:"limit"`
		} `json:"memory_stats"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode stats %s: %w", containerID, err)
	}

	// Calculate CPU percentage from delta
	cpuDelta := float64(raw.CPUStats.CPUUsage.TotalUsage - raw.PreCPUStats.CPUUsage.TotalUsage)
	sysDelta := float64(raw.CPUStats.SystemCPUUsage - raw.PreCPUStats.SystemCPUUsage)
	var cpuPercent float64
	if sysDelta > 0 && cpuDelta > 0 {
		cpuPercent = (cpuDelta / sysDelta) * float64(raw.CPUStats.OnlineCPUs) * 100.0
	}

	return &containerStatsResult{
		cpuPercent: cpuPercent,
		memUsed:    raw.MemoryStats.Usage,
		memLimit:   raw.MemoryStats.Limit,
	}, nil
}

// StartContainer starts a stopped container.
func (d *DockerClient) StartContainer(ctx context.Context, containerName string) error {
	u := fmt.Sprintf("http://localhost/%s/containers/%s/start", dockerAPIVersion, containerName)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("start container %s: %w", containerName, err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusNoContent, http.StatusOK:
		return nil // Success
	case http.StatusNotModified:
		return nil // Already running
	default:
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("start container %s: HTTP %d: %s", containerName, resp.StatusCode, string(body))
	}
}

// StopContainer stops a running container with a graceful timeout.
func (d *DockerClient) StopContainer(ctx context.Context, containerName string, timeoutSec int) error {
	u := fmt.Sprintf("http://localhost/%s/containers/%s/stop?t=%d", dockerAPIVersion, containerName, timeoutSec)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("stop container %s: %w", containerName, err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusNoContent, http.StatusOK:
		return nil // Success
	case http.StatusNotModified:
		return nil // Already stopped
	default:
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("stop container %s: HTTP %d: %s", containerName, resp.StatusCode, string(body))
	}
}

// RestartContainer restarts a container with a graceful timeout.
func (d *DockerClient) RestartContainer(ctx context.Context, containerName string, timeoutSec int) error {
	u := fmt.Sprintf("http://localhost/%s/containers/%s/restart?t=%d", dockerAPIVersion, containerName, timeoutSec)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("restart container %s: %w", containerName, err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusNoContent, http.StatusOK:
		return nil
	default:
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("restart container %s: HTTP %d: %s", containerName, resp.StatusCode, string(body))
	}
}

// GetContainerStatus returns the current status of a container ("running", "exited", etc.).
func (d *DockerClient) GetContainerStatus(ctx context.Context, containerName string) (string, error) {
	cj, err := d.InspectContainer(ctx, containerName)
	if err != nil {
		return "", err
	}
	if cj.State.Running {
		return "running", nil
	}
	return "stopped", nil
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
	// Supports both legacy (/node/config) and current (/opt/klever-blockchain/config/node) paths
	for _, m := range cj.Mounts {
		dest := m.Destination
		switch {
		case strings.HasSuffix(dest, "/node/config") || strings.HasSuffix(dest, "/config/node"):
			node.ConfigDirectory = m.Source
			node.DataDirectory = parentDir(m.Source)
		case strings.HasSuffix(dest, "/node/db") || strings.HasSuffix(dest, "/db"):
			if node.DataDirectory == "" {
				node.DataDirectory = parentDir(m.Source)
			}
		}
	}

	// Fallback: check bind mounts if Mounts is empty
	if node.ConfigDirectory == "" {
		for _, bind := range cj.HostConfig.Binds {
			parts := strings.SplitN(bind, ":", 3)
			if len(parts) >= 2 {
				dest := parts[1]
				if strings.HasSuffix(dest, "/node/config") || strings.HasSuffix(dest, "/config/node") {
					node.ConfigDirectory = parts[0]
					node.DataDirectory = parentDir(parts[0])
					break
				}
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
