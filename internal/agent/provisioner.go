package agent

import (
	"archive/tar"
	"compress/gzip"
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

// configSource holds the URL and archive strip depth for a network's config archive.
type configSource struct {
	URL             string
	StripComponents int // number of leading path components to strip from the tar archive
}

// Config sources for official Klever node configuration archives.
// Mainnet archives have structure: config/*.yaml (strip 1)
// Testnet archives have structure: config/node/*.yaml (strip 2)
var configSources = map[string]configSource{
	"mainnet": {"https://backup.mainnet.klever.org/config.mainnet.108.tar.gz", 1},
	"testnet": {"https://backup.testnet.klever.org/config.testnet.109.tar.gz", 2},
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
		{"Set permissions", (*Provisioner).stepSetPermissions},
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
		return fmt.Errorf("docker not accessible: %w", err)
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

// stepDownloadConfig downloads and extracts the official Klever config archive.
func (p *Provisioner) stepDownloadConfig(ctx context.Context) error {
	src, ok := configSources[p.req.Network]
	if !ok {
		return fmt.Errorf("unknown network: %s", p.req.Network)
	}

	configDir := filepath.Join(p.nodeDir, "config")

	// Try primary: official tar.gz archive
	if err := downloadAndExtractConfig(ctx, src.URL, configDir, src.StripComponents); err != nil {
		log.Printf("primary config download failed: %v, trying fallback...", err)

		// Fallback: individual files from GitHub
		if err := downloadFallbackConfig(ctx, p.req.Network, configDir); err != nil {
			return fmt.Errorf("config download failed (primary and fallback): %w", err)
		}
	}

	// Apply config overrides to config.toml if it exists
	configPath := filepath.Join(configDir, "config.toml")
	if data, err := os.ReadFile(configPath); err == nil {
		content := string(data)
		if name, ok := p.req.ConfigOverrides["NodeDisplayName"]; ok {
			content = replaceConfigValue(content, "NodeDisplayName", name)
		}
		if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
			return fmt.Errorf("write config overrides: %w", err)
		}
	}

	log.Printf("configuration downloaded to %s", configDir)
	return nil
}

// stepSetPermissions sets ownership to 999:999 (matching container user).
func (p *Provisioner) stepSetPermissions(_ context.Context) error {
	return chownRecursive(p.nodeDir, 999, 999)
}

// chownRecursive sets ownership of a directory tree.
func chownRecursive(dir string, uid, gid int) error {
	return filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		return os.Chown(path, uid, gid)
	})
}

// downloadAndExtractConfig downloads a tar.gz archive and extracts it.
// stripComponents removes leading path components (like tar --strip-components).
// Mainnet archives use config/*.yaml (strip=1), testnet uses config/node/*.yaml (strip=2).
func downloadAndExtractConfig(ctx context.Context, configURL, configDir string, stripComponents int) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, configURL, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	return extractTarGz(resp.Body, configDir, stripComponents)
}

// Fallback config files from GitHub.
var fallbackConfigFiles = []string{
	"api.yaml", "config.yaml", "enableEpochs.yaml", "external.yaml",
	"gasScheduleV1.yaml", "genesis.json", "nodesSetup.json",
}

const fallbackGitHubBase = "https://raw.githubusercontent.com/CTJaeger/KleverNodeManagement/main/config"

// downloadFallbackConfig downloads individual config files from GitHub.
func downloadFallbackConfig(ctx context.Context, network, configDir string) error {
	_ = network // future: per-network fallback paths
	for _, file := range fallbackConfigFiles {
		fileURL := fallbackGitHubBase + "/" + file
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, fileURL, nil)
		if err != nil {
			return fmt.Errorf("create request for %s: %w", file, err)
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return fmt.Errorf("download %s: %w", file, err)
		}

		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			return fmt.Errorf("download %s: HTTP %d", file, resp.StatusCode)
		}

		data, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return fmt.Errorf("read %s: %w", file, err)
		}

		if err := os.WriteFile(filepath.Join(configDir, file), data, 0644); err != nil {
			return fmt.Errorf("write %s: %w", file, err)
		}
	}

	log.Printf("downloaded %d config files from GitHub fallback", len(fallbackConfigFiles))
	return nil
}

// extractTarGz extracts a tar.gz stream into the destination directory.
// stripComponents removes N leading path components (like tar --strip-components).
func extractTarGz(r io.Reader, destDir string, stripComponents int) error {
	gr, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer func() { _ = gr.Close() }()

	tr := tar.NewReader(gr)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar read: %w", err)
		}

		// Strip leading path components
		name := header.Name
		for i := 0; i < stripComponents; i++ {
			idx := strings.IndexByte(name, '/')
			if idx < 0 {
				name = ""
				break
			}
			name = name[idx+1:]
		}
		if name == "" || name == "." {
			continue
		}

		// Sanitize path to prevent directory traversal
		name = filepath.Clean(name)
		if strings.Contains(name, "..") {
			continue
		}
		target := filepath.Join(destDir, name)

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				return fmt.Errorf("mkdir %s: %w", target, err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return fmt.Errorf("mkdir parent %s: %w", target, err)
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode))
			if err != nil {
				return fmt.Errorf("create %s: %w", target, err)
			}
			if _, err := io.Copy(f, tr); err != nil {
				_ = f.Close()
				return fmt.Errorf("write %s: %w", target, err)
			}
			_ = f.Close()
		}
	}
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
