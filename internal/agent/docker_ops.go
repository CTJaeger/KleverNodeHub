package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// ContainerConfig defines parameters for creating a Klever node container.
type ContainerConfig struct {
	Name            string `json:"name"`             // Container name, e.g. "klever-node1"
	ImageTag        string `json:"image_tag"`         // e.g. "v0.60.0" or "latest"
	DataDir         string `json:"data_dir"`          // Host data directory
	RestAPIPort     int    `json:"rest_api_port"`     // REST API port
	DisplayName     string `json:"display_name"`      // Node display name
	RedundancyLevel int    `json:"redundancy_level"`  // 0 = active, 1 = fallback
}

// containerCreateBody is the Docker API request body for container creation.
type containerCreateBody struct {
	Image      string            `json:"Image"`
	User       string            `json:"User,omitempty"`
	Entrypoint []string          `json:"Entrypoint,omitempty"`
	Cmd        []string          `json:"Cmd,omitempty"`
	HostConfig hostConfigBody    `json:"HostConfig"`
	Labels     map[string]string `json:"Labels,omitempty"`
}

type hostConfigBody struct {
	Binds       []string      `json:"Binds,omitempty"`
	NetworkMode string        `json:"NetworkMode,omitempty"`
	RestartPolicy restartPolicy `json:"RestartPolicy,omitempty"`
}

type restartPolicy struct {
	Name string `json:"Name"`
}

type containerCreateResponse struct {
	ID       string   `json:"Id"`
	Warnings []string `json:"Warnings"`
}

// PullImage pulls a Docker image from the registry.
func (d *DockerClient) PullImage(ctx context.Context, image string) error {
	u := fmt.Sprintf("http://localhost/%s/images/create?fromImage=%s",
		dockerAPIVersion, url.QueryEscape(image))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("pull image %s: %w", image, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("pull image %s: HTTP %d: %s", image, resp.StatusCode, string(body))
	}

	// Docker streams JSON progress — consume it all
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

// PullImageWithProgress pulls a Docker image and streams progress updates.
func (d *DockerClient) PullImageWithProgress(ctx context.Context, image string, onProgress func(status string)) error {
	u := fmt.Sprintf("http://localhost/%s/images/create?fromImage=%s",
		dockerAPIVersion, url.QueryEscape(image))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("pull image %s: %w", image, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("pull image %s: HTTP %d: %s", image, resp.StatusCode, string(body))
	}

	// Read streaming JSON lines
	decoder := json.NewDecoder(resp.Body)
	for decoder.More() {
		var msg struct {
			Status   string `json:"status"`
			Progress string `json:"progress"`
			Error    string `json:"error"`
		}
		if err := decoder.Decode(&msg); err != nil {
			break
		}
		if msg.Error != "" {
			return fmt.Errorf("pull error: %s", msg.Error)
		}
		if onProgress != nil {
			status := msg.Status
			if msg.Progress != "" {
				status += " " + msg.Progress
			}
			onProgress(status)
		}
	}

	return nil
}

// CreateContainer creates a new Klever node container.
func (d *DockerClient) CreateContainer(ctx context.Context, cfg *ContainerConfig) (string, error) {
	if err := validateContainerConfig(cfg); err != nil {
		return "", err
	}

	image := fmt.Sprintf("%s:%s", kleverImage, cfg.ImageTag)

	// Build command args matching KleverNodeManagement script
	args := []string{
		"--log-save",
		"--use-log-view",
		fmt.Sprintf("--rest-api-interface=0.0.0.0:%d", cfg.RestAPIPort),
		"--start-in-epoch",
	}

	if cfg.DisplayName != "" {
		args = append(args, fmt.Sprintf("--display-name=%s", cfg.DisplayName))
	}

	if cfg.RedundancyLevel > 0 {
		args = append(args, fmt.Sprintf("--redundancy-level=%d", cfg.RedundancyLevel))
	}

	body := containerCreateBody{
		Image:      image,
		User:       "999:999",
		Entrypoint: []string{"/usr/local/bin/validator"},
		Cmd:        args,
		HostConfig: hostConfigBody{
			Binds: []string{
				cfg.DataDir + "/config:/opt/klever-blockchain/config/node",
				cfg.DataDir + "/db:/opt/klever-blockchain/db",
				cfg.DataDir + "/logs:/opt/klever-blockchain/logs",
				cfg.DataDir + "/wallet:/opt/klever-blockchain/wallet",
			},
			NetworkMode: "host",
			RestartPolicy: restartPolicy{Name: "unless-stopped"},
		},
		Labels: map[string]string{
			"managed-by": "klever-node-hub",
		},
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal container config: %w", err)
	}

	u := fmt.Sprintf("http://localhost/%s/containers/create?name=%s",
		dockerAPIVersion, url.QueryEscape(cfg.Name))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(jsonBody))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("create container %s: %w", cfg.Name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("create container %s: HTTP %d: %s", cfg.Name, resp.StatusCode, string(respBody))
	}

	var createResp containerCreateResponse
	if err := json.NewDecoder(resp.Body).Decode(&createResp); err != nil {
		return "", fmt.Errorf("decode create response: %w", err)
	}

	return createResp.ID, nil
}

// RemoveContainer stops and removes a container.
func (d *DockerClient) RemoveContainer(ctx context.Context, containerName string, force bool) error {
	// Stop first (ignore errors if already stopped)
	_ = d.StopContainer(ctx, containerName, 30)

	forceParam := ""
	if force {
		forceParam = "&force=true"
	}

	u := fmt.Sprintf("http://localhost/%s/containers/%s?v=false%s",
		dockerAPIVersion, containerName, forceParam)

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, u, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("remove container %s: %w", containerName, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusNoContent, http.StatusOK:
		return nil
	case http.StatusNotFound:
		return nil // Already removed
	default:
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("remove container %s: HTTP %d: %s", containerName, resp.StatusCode, string(body))
	}
}

// RemoveDataDirectory removes the data directory for a node.
// Requires explicit confirmation to prevent accidental data loss.
func RemoveDataDirectory(dataDir string) error {
	if dataDir == "" || dataDir == "/" {
		return fmt.Errorf("refusing to remove dangerous path: %q", dataDir)
	}
	return os.RemoveAll(dataDir)
}

// UpgradeContainer upgrades a container to a new image tag.
// 1. Inspect current container for config
// 2. Pull new image
// 3. Stop and remove old container
// 4. Create new container with same config + new tag
// 5. Start new container
func (d *DockerClient) UpgradeContainer(ctx context.Context, containerName, newTag string) (string, error) {
	// 1. Get current container config
	cj, err := d.InspectContainer(ctx, containerName)
	if err != nil {
		return "", fmt.Errorf("inspect for upgrade: %w", err)
	}

	node := parseContainerToNode(cj)

	// 2. Pull new image
	newImage := fmt.Sprintf("%s:%s", kleverImage, newTag)
	if err := d.PullImage(ctx, newImage); err != nil {
		return "", fmt.Errorf("pull new image: %w", err)
	}

	// 3. Stop and remove old container
	if err := d.RemoveContainer(ctx, containerName, false); err != nil {
		return "", fmt.Errorf("remove old container: %w", err)
	}

	// 4. Create new container with same config
	cfg := &ContainerConfig{
		Name:            node.ContainerName,
		ImageTag:        newTag,
		DataDir:         node.DataDirectory,
		RestAPIPort:     node.RestAPIPort,
		DisplayName:     node.DisplayName,
		RedundancyLevel: node.RedundancyLevel,
	}

	containerID, err := d.CreateContainer(ctx, cfg)
	if err != nil {
		return "", fmt.Errorf("create upgraded container: %w", err)
	}

	// 5. Start new container
	if err := d.StartContainer(ctx, containerID); err != nil {
		return "", fmt.Errorf("start upgraded container: %w", err)
	}

	return containerID, nil
}

// ListLocalImages returns images matching the klever-go image.
func (d *DockerClient) ListLocalImages(ctx context.Context) ([]string, error) {
	filterJSON, _ := json.Marshal(map[string][]string{
		"reference": {kleverImage},
	})

	u := fmt.Sprintf("http://localhost/%s/images/json?filters=%s",
		dockerAPIVersion, url.QueryEscape(string(filterJSON)))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list images: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("list images: HTTP %d: %s", resp.StatusCode, string(body))
	}

	var images []struct {
		RepoTags []string `json:"RepoTags"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&images); err != nil {
		return nil, fmt.Errorf("decode images: %w", err)
	}

	var tags []string
	for _, img := range images {
		tags = append(tags, img.RepoTags...)
	}
	return tags, nil
}

// IsPortAvailable checks if a TCP port is available on the host.
func IsPortAvailable(port int) bool {
	// Try to listen — if we can, the port is free
	addr := fmt.Sprintf(":%d", port)
	ln, err := (&net.Dialer{}).DialContext(context.Background(), "tcp", addr)
	if err != nil {
		return true // Can't connect = port is free
	}
	_ = ln.Close()
	return false
}

// FindAvailablePort finds the next available port starting from startPort.
func FindAvailablePort(startPort int) int {
	for port := startPort; port < startPort+100; port++ {
		if IsPortAvailable(port) {
			return port
		}
	}
	return startPort // Fallback
}

// validateContainerConfig validates container creation parameters.
func validateContainerConfig(cfg *ContainerConfig) error {
	if cfg.Name == "" {
		return fmt.Errorf("container name is required")
	}
	if !containerNamePattern.MatchString(cfg.Name) {
		return fmt.Errorf("invalid container name: %q", cfg.Name)
	}
	if cfg.ImageTag == "" {
		return fmt.Errorf("image tag is required")
	}
	if cfg.DataDir == "" {
		return fmt.Errorf("data directory is required")
	}
	if cfg.RestAPIPort < 1 || cfg.RestAPIPort > 65535 {
		return fmt.Errorf("invalid REST API port: %d", cfg.RestAPIPort)
	}
	// Validate tag doesn't contain injection characters
	if strings.ContainsAny(cfg.ImageTag, " \t\n;|&$`") {
		return fmt.Errorf("invalid image tag: %q", cfg.ImageTag)
	}
	return nil
}

// EnsureDataDirs creates the required data directory structure.
func EnsureDataDirs(dataDir string) error {
	dirs := []string{
		dataDir + "/config",
		dataDir + "/db",
		dataDir + "/logs",
		dataDir + "/wallet",
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("create directory %s: %w", dir, err)
		}
	}
	return nil
}
