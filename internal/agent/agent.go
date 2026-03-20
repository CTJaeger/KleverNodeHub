package agent

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/CTJaeger/KleverNodeHub/internal/models"
	"github.com/CTJaeger/KleverNodeHub/internal/version"
)

const (
	configFileName = "agent.json"
)

// Config holds the agent's persistent configuration.
type Config struct {
	ServerID     string `json:"server_id"`
	DashboardURL string `json:"dashboard_url"`
	CertPEM      string `json:"cert_pem"`
	KeyPEM       string `json:"key_pem"`
	CACertPEM    string `json:"ca_cert_pem"`
	AgentPort    int    `json:"agent_port,omitempty"`
}

// Agent is the main agent process.
type Agent struct {
	config    *Config
	configDir string
}

// New creates a new Agent with the given config directory.
func New(configDir string) *Agent {
	return &Agent{
		configDir: configDir,
	}
}

// Config returns the agent's current configuration.
func (a *Agent) Config() *Config {
	return a.config
}

// TLSConfig builds a tls.Config using the agent's mTLS credentials.
// The returned config trusts the dashboard CA and presents the agent's client certificate.
func (a *Agent) TLSConfig() (*tls.Config, error) {
	if a.config == nil {
		return nil, fmt.Errorf("agent not configured")
	}

	cert, err := tls.X509KeyPair([]byte(a.config.CertPEM), []byte(a.config.KeyPEM))
	if err != nil {
		return nil, fmt.Errorf("load client certificate: %w", err)
	}

	caCertPool := x509.NewCertPool()
	if !caCertPool.AppendCertsFromPEM([]byte(a.config.CACertPEM)) {
		return nil, fmt.Errorf("parse CA certificate")
	}

	return &tls.Config{
		Certificates:       []tls.Certificate{cert},
		RootCAs:            caCertPool,
		InsecureSkipVerify: true, //nolint:gosec // dashboard uses self-signed cert; mTLS client auth is the real protection
	}, nil
}

// LoadConfig loads the agent configuration from disk.
func (a *Agent) LoadConfig() error {
	path := filepath.Join(a.configDir, configFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	a.config = &cfg
	return nil
}

// SaveConfig saves the agent configuration to disk.
func (a *Agent) SaveConfig() error {
	if err := os.MkdirAll(a.configDir, 0700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	data, err := json.MarshalIndent(a.config, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	path := filepath.Join(a.configDir, configFileName)
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	return nil
}

// IsRegistered returns true if the agent has a stored certificate.
func (a *Agent) IsRegistered() bool {
	return a.config != nil && a.config.CertPEM != ""
}

// Register performs initial registration with the dashboard.
func (a *Agent) Register(dashboardURL, token string) error {
	a.config = &Config{
		DashboardURL: dashboardURL,
	}

	hostname, _ := os.Hostname()

	req := &models.RegistrationRequest{
		Token:    token,
		Hostname: hostname,
		OS:       runtime.GOOS + "/" + runtime.GOARCH,
		IP:       "", // Will be filled by dashboard from connection
	}

	resp, err := registerWithDashboard(dashboardURL, req)
	if err != nil {
		return fmt.Errorf("registration failed: %w", err)
	}

	a.config.ServerID = resp.ServerID
	a.config.CertPEM = resp.CertPEM
	a.config.KeyPEM = resp.KeyPEM
	a.config.CACertPEM = resp.CACertPEM

	if err := a.SaveConfig(); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	log.Printf("registered as server %s", resp.ServerID)
	return nil
}

// BuildAgentInfo creates the agent.info payload.
func (a *Agent) BuildAgentInfo(publicIP string) *models.AgentInfo {
	hostname, _ := os.Hostname()
	return &models.AgentInfo{
		Version:  version.Version,
		OS:       runtime.GOOS + "/" + runtime.GOARCH,
		Hostname: hostname,
		PublicIP: publicIP,
	}
}

// BuildInfoMessage creates a complete agent.info message.
func (a *Agent) BuildInfoMessage(publicIP string) *models.Message {
	return &models.Message{
		ID:        fmt.Sprintf("info-%d", time.Now().UnixNano()),
		Type:      "event",
		Action:    "agent.info",
		Payload:   a.BuildAgentInfo(publicIP),
		Timestamp: time.Now().Unix(),
	}
}

// RunDiscovery scans the server for Klever nodes and returns a discovery report.
func (a *Agent) RunDiscovery(dockerSocket string) *models.DiscoveryReport {
	client := NewDockerClient(dockerSocket)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	discovered, err := client.DiscoverNodes(ctx)
	if err != nil {
		log.Printf("discovery: Docker not available or not running: %v", err)
		return &models.DiscoveryReport{Nodes: []models.DiscoveredNode{}}
	}

	nodes := make([]models.DiscoveredNode, 0, len(discovered))
	for _, d := range discovered {
		mn := models.DiscoveredNode{
			ContainerID:     d.ContainerID,
			ContainerName:   d.ContainerName,
			Status:          d.Status,
			RestAPIPort:     d.RestAPIPort,
			DisplayName:     d.DisplayName,
			RedundancyLevel: d.RedundancyLevel,
			DockerImageTag:  d.DockerImageTag,
			DataDirectory:   d.DataDirectory,
			CPUPercent:      d.CPUPercent,
			MemUsed:         d.MemUsed,
			MemLimit:        d.MemLimit,
			MemPercent:      d.MemPercent,
		}

		// Try to extract BLS public key from config directory
		if d.ConfigDirectory != "" {
			if blsKey, err := ExtractBLSPublicKey(d.ConfigDirectory); err == nil {
				mn.BLSPublicKey = blsKey
			}
		}

		nodes = append(nodes, mn)
	}

	return &models.DiscoveryReport{Nodes: nodes}
}

// BuildDiscoveryMessage creates a discovery report message.
func (a *Agent) BuildDiscoveryMessage(report *models.DiscoveryReport) *models.Message {
	return &models.Message{
		ID:        fmt.Sprintf("discovery-%d", time.Now().UnixNano()),
		Type:      "event",
		Action:    "agent.discovery",
		Payload:   report,
		Timestamp: time.Now().Unix(),
	}
}

// registerWithDashboard performs the HTTP-based registration with the dashboard.
func registerWithDashboard(dashboardURL string, req *models.RegistrationRequest) (*models.RegistrationResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := dashboardURL + "/api/agent/register"

	// Skip TLS verification for initial registration (dashboard uses self-signed cert)
	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // self-signed cert during registration
		},
	}

	httpResp, err := client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("POST %s: %w", url, err)
	}
	defer func() { _ = httpResp.Body.Close() }()

	if httpResp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(httpResp.Body)
		return nil, fmt.Errorf("registration failed: HTTP %d: %s", httpResp.StatusCode, string(respBody))
	}

	var resp models.RegistrationResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&resp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return &resp, nil
}
