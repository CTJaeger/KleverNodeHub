//go:build !windows

package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

const dockerSocket = "/var/run/docker.sock"

// dockerSelfUpdateAvailable checks if the Docker socket is mounted.
func dockerSelfUpdateAvailable() bool {
	_, err := os.Stat(dockerSocket)
	return err == nil
}

// dockerSelfUpdate pulls the new image and recreates the dashboard container.
func dockerSelfUpdate(targetTag string) error {
	client := newDockerSocketClient()

	// 1. Find our own container ID
	containerID, err := getSelfContainerID()
	if err != nil {
		return fmt.Errorf("detect own container: %w", err)
	}
	log.Printf("docker self-update: own container ID = %s", containerID[:12])

	// 2. Inspect current container to get config
	info, err := client.inspectContainer(containerID)
	if err != nil {
		return fmt.Errorf("inspect container: %w", err)
	}

	// 3. Determine new image name
	currentImage := info.Config.Image
	newImage := replaceImageTag(currentImage, targetTag)
	log.Printf("docker self-update: %s → %s", currentImage, newImage)

	// 4. Pull new image
	log.Printf("docker self-update: pulling %s", newImage)
	if err := client.pullImage(newImage); err != nil {
		return fmt.Errorf("pull image: %w", err)
	}

	// 5. Rename old container (so we can create new one with same name)
	containerName := ""
	if len(info.Name) > 0 {
		containerName = strings.TrimPrefix(info.Name, "/")
	}
	backupName := containerName + "-old-" + time.Now().Format("20060102-150405")
	log.Printf("docker self-update: renaming %s → %s", containerName, backupName)
	if err := client.renameContainer(containerID, backupName); err != nil {
		return fmt.Errorf("rename old container: %w", err)
	}

	// 6. Create new container with same config but new image
	info.Config.Image = newImage
	newID, err := client.createContainer(containerName, info)
	if err != nil {
		// Rollback: rename old container back
		_ = client.renameContainer(containerID, containerName)
		return fmt.Errorf("create new container: %w", err)
	}
	log.Printf("docker self-update: created new container %s", newID[:12])

	// 7. Start new container
	if err := client.startContainer(newID); err != nil {
		// Rollback: remove new, rename old back
		_ = client.removeContainer(newID)
		_ = client.renameContainer(containerID, containerName)
		return fmt.Errorf("start new container: %w", err)
	}
	log.Printf("docker self-update: started new container, stopping old...")

	// 8. Stop and remove old container
	_ = client.stopContainer(containerID, 10)
	_ = client.removeContainer(containerID)

	return nil
}

// --- Minimal Docker socket client ---

type dockerClient struct {
	http *http.Client
}

func newDockerSocketClient() *dockerClient {
	return &dockerClient{
		http: &http.Client{
			Transport: &http.Transport{
				DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
					return net.DialTimeout("unix", dockerSocket, 5*time.Second)
				},
			},
			Timeout: 5 * time.Minute,
		},
	}
}

func (d *dockerClient) get(path string) ([]byte, error) {
	resp, err := d.http.Get("http://localhost" + path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
}

func (d *dockerClient) post(path string, body io.Reader) ([]byte, error) {
	var contentType string
	if body != nil {
		contentType = "application/json"
	}
	resp, err := d.http.Post("http://localhost"+path, contentType, body)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(data))
	}
	return data, nil
}

func (d *dockerClient) doDelete(path string) error {
	req, _ := http.NewRequest("DELETE", "http://localhost"+path, nil)
	resp, err := d.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// containerInspect is a minimal subset of Docker's container inspect response.
type containerInspect struct {
	ID     string `json:"Id"`
	Name   string `json:"Name"`
	Config struct {
		Image        string            `json:"Image"`
		Env          []string          `json:"Env"`
		Cmd          []string          `json:"Cmd"`
		Entrypoint   []string          `json:"Entrypoint"`
		Labels       map[string]string `json:"Labels"`
		ExposedPorts map[string]any    `json:"ExposedPorts"`
	} `json:"Config"`
	HostConfig      json.RawMessage `json:"HostConfig"`
	NetworkSettings struct {
		Networks map[string]json.RawMessage `json:"Networks"`
	} `json:"NetworkSettings"`
}

func (d *dockerClient) inspectContainer(id string) (*containerInspect, error) {
	data, err := d.get("/containers/" + id + "/json")
	if err != nil {
		return nil, err
	}
	var info containerInspect
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

func (d *dockerClient) pullImage(image string) error {
	_, err := d.post("/images/create?fromImage="+image, nil)
	return err
}

func (d *dockerClient) renameContainer(id, newName string) error {
	_, err := d.post("/containers/"+id+"/rename?name="+newName, nil)
	return err
}

func (d *dockerClient) createContainer(name string, old *containerInspect) (string, error) {
	// Build networking config from old container
	networkingConfig := map[string]any{}
	if old.NetworkSettings.Networks != nil {
		endpointsConfig := map[string]json.RawMessage{}
		for netName, netCfg := range old.NetworkSettings.Networks {
			endpointsConfig[netName] = netCfg
		}
		networkingConfig["EndpointsConfig"] = endpointsConfig
	}

	body := map[string]any{
		"Image":            old.Config.Image,
		"Env":              old.Config.Env,
		"Cmd":              old.Config.Cmd,
		"Entrypoint":       old.Config.Entrypoint,
		"Labels":           old.Config.Labels,
		"ExposedPorts":     old.Config.ExposedPorts,
		"HostConfig":       old.HostConfig,
		"NetworkingConfig": networkingConfig,
	}

	jsonBody, _ := json.Marshal(body)
	data, err := d.post("/containers/create?name="+name, strings.NewReader(string(jsonBody)))
	if err != nil {
		return "", err
	}

	var result struct {
		ID string `json:"Id"`
	}
	_ = json.Unmarshal(data, &result)
	return result.ID, nil
}

func (d *dockerClient) startContainer(id string) error {
	_, err := d.post("/containers/"+id+"/start", nil)
	return err
}

func (d *dockerClient) stopContainer(id string, timeoutSec int) error {
	_, err := d.post(fmt.Sprintf("/containers/%s/stop?t=%d", id, timeoutSec), nil)
	return err
}

func (d *dockerClient) removeContainer(id string) error {
	return d.doDelete("/containers/" + id + "?force=true")
}

// getSelfContainerID reads the container ID from /proc/self/cgroup or hostname.
func getSelfContainerID() (string, error) {
	// In Docker, hostname is the container ID (short form)
	hostname, err := os.Hostname()
	if err != nil {
		return "", err
	}
	// Hostname in Docker is typically the 12-char container ID
	if len(hostname) >= 12 {
		return hostname, nil
	}
	return "", fmt.Errorf("hostname %q doesn't look like a container ID", hostname)
}

// replaceImageTag replaces the tag portion of an image reference.
// e.g. "ctjaeger/klever-node-hub:v0.3.40" → "ctjaeger/klever-node-hub:v0.3.42"
func replaceImageTag(image, newTag string) string {
	if idx := strings.LastIndex(image, ":"); idx >= 0 {
		return image[:idx] + ":" + newTag
	}
	return image + ":" + newTag
}
