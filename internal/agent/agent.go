package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/CTJaeger/KleverNodeHub/internal/models"
)

const (
	agentVersion   = "0.1.0"
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
	a.config.CACertPEM = resp.CACertPEM

	if err := a.SaveConfig(); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	log.Printf("registered as server %s", resp.ServerID)
	return nil
}

// BuildAgentInfo creates the agent.info payload.
func (a *Agent) BuildAgentInfo() *models.AgentInfo {
	hostname, _ := os.Hostname()
	return &models.AgentInfo{
		Version:  agentVersion,
		OS:       runtime.GOOS + "/" + runtime.GOARCH,
		Hostname: hostname,
	}
}

// BuildInfoMessage creates a complete agent.info message.
func (a *Agent) BuildInfoMessage() *models.Message {
	return &models.Message{
		ID:        fmt.Sprintf("info-%d", time.Now().UnixNano()),
		Type:      "event",
		Action:    "agent.info",
		Payload:   a.BuildAgentInfo(),
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
		log.Printf("discovery failed: %v", err)
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

// registerWithDashboard performs the HTTP-based registration.
// This is a placeholder — actual implementation will use the WebSocket connection.
func registerWithDashboard(dashboardURL string, req *models.RegistrationRequest) (*models.RegistrationResponse, error) {
	// TODO: Implement actual HTTP/WebSocket registration
	_ = dashboardURL
	_ = req
	return nil, fmt.Errorf("not yet implemented")
}
