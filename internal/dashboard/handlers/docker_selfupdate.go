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
	"sync"
	"time"
)

const dockerSocket = "/var/run/docker.sock"

// dockerSelfUpdateMu serialises concurrent self-update attempts. The
// orchestration mutates Docker state (rename, create, start) that two
// in-flight runs would race on, and a half-failed interleave can leave the
// container set in a state that needs manual recovery. The lock is acquired
// in the handler before writing the 200 response so a second click sees a
// 409 rather than a silent no-op.
var dockerSelfUpdateMu sync.Mutex

// dockerSelfUpdateAvailable checks if the Docker socket is mounted.
func dockerSelfUpdateAvailable() bool {
	_, err := os.Stat(dockerSocket)
	return err == nil
}

// dockerSelfUpdate pulls the new image, stages the replacement container, and
// hands off to a short-lived helper sidecar that finishes the swap after this
// container (the dashboard itself) is stopped. The helper is needed because the
// goroutine running this function dies the moment we stop our own container —
// so the actual port-binding start of the new container has to be done by
// something that outlives us.
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
	newImage, err := replaceImageTag(currentImage, targetTag)
	if err != nil {
		return fmt.Errorf("compute new image name: %w", err)
	}
	log.Printf("docker self-update: %s → %s", currentImage, newImage)

	// 4. Pull new image
	log.Printf("docker self-update: pulling %s", newImage)
	if err := client.pullImage(newImage); err != nil {
		return fmt.Errorf("pull image: %w", err)
	}

	// 5. Rename old container so the new one can claim the original name.
	containerName := ""
	if len(info.Name) > 0 {
		containerName = strings.TrimPrefix(info.Name, "/")
	}
	backupName := containerName + "-old-" + time.Now().Format("20060102-150405")
	log.Printf("docker self-update: renaming %s → %s", containerName, backupName)
	if err := client.renameContainer(containerID, backupName); err != nil {
		return fmt.Errorf("rename old container: %w", err)
	}

	// 6. Create the real replacement container in Created state. We can't start
	// it yet — the old container still holds the host port(s).
	info.Config.Image = newImage
	newID, err := client.createContainer(containerName, info)
	if err != nil {
		_ = client.renameContainer(containerID, containerName)
		return fmt.Errorf("create new container: %w", err)
	}
	log.Printf("docker self-update: created new container %s (stopped)", newID[:12])

	// 7. Create + start the finalize helper. It will wait for us to stop, then
	// start the new container (port is free at that point) and remove the old.
	helperName := containerName + "-finalize-" + time.Now().Format("20060102-150405")
	helperID, err := client.createFinalizeHelper(helperName, newImage, newID, containerID)
	if err != nil {
		_ = client.removeContainer(newID)
		_ = client.renameContainer(containerID, containerName)
		return fmt.Errorf("create finalize helper: %w", err)
	}
	if err := client.startContainer(helperID); err != nil {
		_ = client.removeContainer(helperID)
		_ = client.removeContainer(newID)
		_ = client.renameContainer(containerID, containerName)
		return fmt.Errorf("start finalize helper: %w", err)
	}
	log.Printf("docker self-update: started finalize helper %s — stopping self", helperID[:12])

	// 8. Stop ourselves. The helper takes over: it polls until we're stopped,
	// starts the new container, removes our (renamed) old container, and then
	// removes itself. We don't get past this line in the happy path — Docker
	// SIGTERMs the process. If the stop call itself errors (transient daemon
	// hiccup), the helper will time out after 60s waiting for us to stop and
	// exit with an error in its logs.
	if err := client.stopContainer(containerID, 10); err != nil {
		log.Printf("docker self-update: WARNING: stop self failed: %v (helper will time out; manual recovery may be required)", err)
	}
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
	ID    string `json:"Id"`
	Name  string `json:"Name"`
	State struct {
		Status  string `json:"Status"`
		Running bool   `json:"Running"`
	} `json:"State"`
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

// waitContainerStopped polls the container's state until it is no longer running,
// or until the timeout expires. A 404 from inspect (container already removed) is
// treated as "stopped" since the caller's intent is satisfied.
//
// Each poll has its own short timeout so a single wedged daemon call can't
// silently blow past the overall deadline — the shared dockerClient has a 5min
// HTTP timeout which is far too long for the polling path.
func (d *dockerClient) waitContainerStopped(id string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		info, err := d.inspectContainerCtx(ctx, id)
		cancel()
		if err != nil {
			if strings.Contains(err.Error(), "HTTP 404") {
				return nil
			}
			// Transient daemon hiccup (timeout, EOF). Keep polling within the
			// overall deadline rather than giving up on the first error.
			time.Sleep(500 * time.Millisecond)
			continue
		}
		if !info.State.Running {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for container %s to stop", id[:12])
}

// inspectContainerCtx is like inspectContainer but bounded by the supplied
// context. Used by polling loops that need each call to fail fast.
func (d *dockerClient) inspectContainerCtx(ctx context.Context, id string) (*containerInspect, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", "http://localhost/containers/"+id+"/json", nil)
	if err != nil {
		return nil, err
	}
	resp, err := d.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	var info containerInspect
	if err := json.Unmarshal(body, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// createFinalizeHelper creates a sidecar container that finishes the dashboard
// self-update flow after the calling (old) container has stopped. It runs the
// new dashboard image with --self-update-finalize, mounts the Docker socket so it
// can drive the daemon, and binds no host ports of its own.
func (d *dockerClient) createFinalizeHelper(name, image, newID, oldID string) (string, error) {
	body := map[string]any{
		"Image": image,
		"Cmd": []string{
			"--self-update-finalize", newID,
			"--replaces", oldID,
		},
		"HostConfig": map[string]any{
			"Binds":      []string{"/var/run/docker.sock:/var/run/docker.sock"},
			"AutoRemove": false, // helper self-removes on success; on failure it stays for inspection
			"RestartPolicy": map[string]any{
				"Name": "no",
			},
		},
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
//
// Refuses digest-pinned references (`image@sha256:...`) because swapping the
// tag of a pinned digest is meaningless. A naive LastIndex(":") split also
// breaks on registry-with-port references like `registry.local:5000/foo` —
// the tag is the substring after the last `:` that follows the last `/`.
func replaceImageTag(image, newTag string) (string, error) {
	if strings.Contains(image, "@") {
		return "", fmt.Errorf("cannot swap tag on digest-pinned image: %s", image)
	}
	lastSlash := strings.LastIndex(image, "/")
	afterSlash := image[lastSlash+1:]
	if idx := strings.LastIndex(afterSlash, ":"); idx >= 0 {
		return image[:lastSlash+1+idx] + ":" + newTag, nil
	}
	return image + ":" + newTag, nil
}
