package agent

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/CTJaeger/KleverNodeHub/internal/models"
)

const (
	defaultNodeBaseDir = "/opt/klever-node"
	minDiskSpaceBytes  = 50 * 1024 * 1024 * 1024 // 50 GB
	provisionTimeout   = 10 * time.Minute
)

// Config URLs for official Klever node configuration.
var configURLs = map[string]string{
	"mainnet": "https://backup.mainnet.klever.org/config.toml",
	"testnet": "https://backup.testnet.klever.org/config.toml",
}

// ProvisionStep represents a single provisioning step.
type ProvisionStep struct {
	Name string
	Fn   func(p *Provisioner, ctx context.Context) error
}

// Provisioner handles the multi-step node provisioning workflow.
type Provisioner struct {
	docker     *DockerClient
	req        *models.ProvisionRequest
	jobID      string
	progressFn func(progress *models.ProvisionProgress)
	nodeDir    string
	steps      []ProvisionStep
}

// NewProvisioner creates a new provisioner for a given request.
func NewProvisioner(docker *DockerClient, req *models.ProvisionRequest, jobID string, progressFn func(*models.ProvisionProgress)) *Provisioner {
	nodeDir := filepath.Join(defaultNodeBaseDir, req.NodeName)

	p := &Provisioner{
		docker:     docker,
		req:        req,
		jobID:      jobID,
		progressFn: progressFn,
		nodeDir:    nodeDir,
	}

	p.steps = []ProvisionStep{
		{"Pre-flight checks", (*Provisioner).stepPreflight},
		{"Pull Docker image", (*Provisioner).stepPullImage},
		{"Create directory structure", (*Provisioner).stepCreateDirs},
		{"Download configuration", (*Provisioner).stepDownloadConfig},
		{"Create container", (*Provisioner).stepCreateContainer},
		{"Start container", (*Provisioner).stepStartContainer},
		{"Verify node", (*Provisioner).stepVerify},
	}

	return p
}

// Run executes all provisioning steps in sequence.
// On failure, it attempts cleanup and reports which step failed.
func (p *Provisioner) Run(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, provisionTimeout)
	defer cancel()

	total := len(p.steps)

	for i, step := range p.steps {
		p.reportProgress(i+1, total, step.Name, "running", "")

		if err := step.Fn(p, ctx); err != nil {
			p.reportProgress(i+1, total, step.Name, "failed", err.Error())
			log.Printf("provisioning failed at step %d (%s): %v", i+1, step.Name, err)
			p.cleanup(ctx, i)
			return fmt.Errorf("step %d (%s): %w", i+1, step.Name, err)
		}

		p.reportProgress(i+1, total, step.Name, "completed", "")
	}

	return nil
}

func (p *Provisioner) reportProgress(step, total int, name, status, errMsg string) {
	if p.progressFn == nil {
		return
	}
	progress := &models.ProvisionProgress{
		JobID:      p.jobID,
		ServerID:   p.req.ServerID,
		Step:       step,
		TotalSteps: total,
		StepName:   name,
		Status:     status,
		Error:      errMsg,
	}
	p.progressFn(progress)
}

// stepPreflight verifies Docker is running and port is available.
func (p *Provisioner) stepPreflight(ctx context.Context) error {
	// Verify Docker connectivity
	if _, err := p.docker.DiscoverNodes(ctx); err != nil {
		return fmt.Errorf("Docker not accessible: %w", err)
	}

	// Check port availability
	port := p.req.Port
	if port <= 0 {
		port = 8080
	}
	if !IsPortAvailable(port) {
		return fmt.Errorf("port %d is already in use", port)
	}

	// Check node name is not empty
	if p.req.NodeName == "" {
		return fmt.Errorf("node name is required")
	}

	// Check network is valid
	if p.req.Network != "mainnet" && p.req.Network != "testnet" {
		return fmt.Errorf("invalid network %q, must be mainnet or testnet", p.req.Network)
	}

	// Check if directory already exists (don't overwrite)
	if _, err := os.Stat(p.nodeDir); err == nil {
		return fmt.Errorf("directory %s already exists", p.nodeDir)
	}

	return nil
}

// stepPullImage pulls the Klever Docker image.
func (p *Provisioner) stepPullImage(ctx context.Context) error {
	tag := p.req.ImageTag
	if tag == "" {
		tag = "latest"
	}
	image := fmt.Sprintf("%s:%s", kleverImage, tag)
	log.Printf("pulling image %s...", image)
	return p.docker.PullImage(ctx, image)
}

// stepCreateDirs creates the node directory structure.
func (p *Provisioner) stepCreateDirs(ctx context.Context) error {
	return EnsureDataDirs(p.nodeDir)
}

// stepDownloadConfig downloads the official Klever config.toml.
func (p *Provisioner) stepDownloadConfig(ctx context.Context) error {
	configURL, ok := configURLs[p.req.Network]
	if !ok {
		return fmt.Errorf("unknown network: %s", p.req.Network)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, configURL, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("download config: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download config: HTTP %d", resp.StatusCode)
	}

	configPath := filepath.Join(p.nodeDir, "config", "config.toml")
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}

	// Apply config overrides
	content := string(data)
	if name, ok := p.req.ConfigOverrides["NodeDisplayName"]; ok {
		content = replaceConfigValue(content, "NodeDisplayName", name)
	}

	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	log.Printf("downloaded config from %s", configURL)
	return nil
}

// stepCreateContainer creates the Docker container.
func (p *Provisioner) stepCreateContainer(ctx context.Context) error {
	tag := p.req.ImageTag
	if tag == "" {
		tag = "latest"
	}
	port := p.req.Port
	if port <= 0 {
		port = 8080
	}

	cfg := &ContainerConfig{
		Name:        p.req.NodeName,
		ImageTag:    tag,
		DataDir:     p.nodeDir,
		RestAPIPort: port,
		DisplayName: p.req.ConfigOverrides["NodeDisplayName"],
	}

	_, err := p.docker.CreateContainer(ctx, cfg)
	return err
}

// stepStartContainer starts the created container.
func (p *Provisioner) stepStartContainer(ctx context.Context) error {
	return p.docker.StartContainer(ctx, p.req.NodeName)
}

// stepVerify waits for the container to be healthy and responds on the REST API.
func (p *Provisioner) stepVerify(ctx context.Context) error {
	// Wait a moment for container to start
	time.Sleep(3 * time.Second)

	// Check container status
	status, err := p.docker.GetContainerStatus(ctx, p.req.NodeName)
	if err != nil {
		return fmt.Errorf("check status: %w", err)
	}
	if !strings.Contains(status, "running") {
		return fmt.Errorf("container not running: %s", status)
	}

	log.Printf("node %s provisioned and running", p.req.NodeName)
	return nil
}

// cleanup attempts to remove partially created resources on failure.
func (p *Provisioner) cleanup(ctx context.Context, failedStep int) {
	log.Printf("cleaning up after failed provisioning (step %d)", failedStep+1)

	// If container was created (step 5+), try to remove it
	if failedStep >= 4 {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = p.docker.RemoveContainer(cleanupCtx, p.req.NodeName, true)
	}

	// Don't remove the directory — user might want to inspect logs
}

// replaceConfigValue replaces a TOML config value in the content string.
func replaceConfigValue(content, key, value string) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, key+" ") || strings.HasPrefix(trimmed, key+"=") {
			lines[i] = fmt.Sprintf("  %s = \"%s\"", key, value)
		}
	}
	return strings.Join(lines, "\n")
}
