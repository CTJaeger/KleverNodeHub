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
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/CTJaeger/KleverNodeHub/internal/crypto"
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

	// AllowUnsignedUpdates lets the hub install agent binaries that are not
	// part of a signed release. Only set by hand on the agent host: no hub
	// command can write this file, so a compromised hub cannot enable it.
	AllowUnsignedUpdates bool `json:"allow_unsigned_updates,omitempty"`

	// AllowedDataRoots, if set, limits the node data directories the hub may
	// use (config writes, key operations, container mounts). Local-only too.
	AllowedDataRoots []string `json:"allowed_data_roots,omitempty"`
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
// The returned config trusts the dashboard CA and presents the agent's
// client certificate, AND verifies the dashboard's server certificate
// against the CA the agent stored at registration time.
//
// The CA we already load was previously ignored: InsecureSkipVerify was
// set to true on the assumption that mTLS alone was sufficient. It is
// not — mTLS only ensures the dashboard authenticates the agent, not
// the reverse. Without verifying the dashboard's cert against the
// stored CA, an attacker who has stolen any client cert can intercept
// the connection.
//
// ServerName is pinned to "localhost" because crypto.DashboardTLSConfig
// issues the dashboard's server certificate with DNSNames: ["localhost"]
// regardless of the host/IP the agent dials. The cert is still signed
// by the trusted CA, so verifying against that SAN proves authenticity.
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
		Certificates: []tls.Certificate{cert},
		RootCAs:      caCertPool,
		ServerName:   "localhost",
		MinVersion:   tls.VersionTLS13,
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
// caFingerprint (SHA-256 of the hub CA, from the install command) pins the hub
// when its certificate is not publicly trusted, i.e. a direct connection.
func (a *Agent) Register(dashboardURL, token, caFingerprint string) error {
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

	resp, err := registerWithDashboard(dashboardURL, req, caFingerprint)
	if err != nil {
		return fmt.Errorf("registration failed: %w", err)
	}
	if caFingerprint != "" {
		ca, err := crypto.DecodeCertPEM([]byte(resp.CACertPEM))
		if err != nil || !strings.EqualFold(crypto.Fingerprint(ca), caFingerprint) {
			return fmt.Errorf("dashboard returned a CA that does not match --ca-fingerprint")
		}
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
		Version:      version.Version,
		OS:           runtime.GOOS + "/" + runtime.GOARCH,
		Hostname:     hostname,
		PublicIP:     publicIP,
		Capabilities: []string{models.CapabilityHTTPUpdate, models.CapabilityConfigPreview},
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

// fetchDashboardCA downloads the dashboard CA certificate and returns it only
// if it matches caFingerprint. Nothing secret is sent, so the connection itself
// needs no verification: the fingerprint check is what makes the result
// trustworthy. Returns nil if the dashboard doesn't offer the endpoint.
func fetchDashboardCA(dashboardURL, caFingerprint string) (*x509.Certificate, error) {
	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // result is checked against the pinned fingerprint
		},
	}
	resp, err := client.Get(strings.TrimRight(dashboardURL, "/") + "/api/agent/ca")
	if err != nil {
		return nil, fmt.Errorf("fetch dashboard CA: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch dashboard CA: HTTP %d", resp.StatusCode)
	}
	pemData, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, fmt.Errorf("fetch dashboard CA: %w", err)
	}
	ca, err := crypto.DecodeCertPEM(pemData)
	if err != nil {
		return nil, fmt.Errorf("fetch dashboard CA: %w", err)
	}
	if !strings.EqualFold(crypto.Fingerprint(ca), caFingerprint) {
		return nil, fmt.Errorf("dashboard CA does not match --ca-fingerprint: refusing to send the registration token")
	}
	return ca, nil
}

// registrationTLSConfig verifies the dashboard before the token is sent: either
// its certificate is publicly trusted for the URL's host (reverse proxy with a
// real certificate), or it is signed by the CA pinned by caFingerprint (direct
// connection to the self-signed dashboard). pinnedCA is that CA as fetched by
// fetchDashboardCA; without it the CA is looked for in the presented chain
// (dashboard v0.3.89).
func registrationTLSConfig(dashboardURL, caFingerprint string, pinnedCA *x509.Certificate) (*tls.Config, error) {
	u, err := url.Parse(dashboardURL)
	if err != nil || u.Hostname() == "" {
		return nil, fmt.Errorf("invalid dashboard URL %q", dashboardURL)
	}
	host := u.Hostname()
	sysPool, _ := x509.SystemCertPool()
	if sysPool == nil {
		sysPool = x509.NewCertPool()
	}
	return &tls.Config{
		ServerName:         host,
		MinVersion:         tls.VersionTLS13,
		InsecureSkipVerify: true, //nolint:gosec // VerifyPeerCertificate below enforces trust
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return fmt.Errorf("dashboard sent no certificate")
			}
			certs := make([]*x509.Certificate, 0, len(rawCerts))
			for _, raw := range rawCerts {
				c, err := x509.ParseCertificate(raw)
				if err != nil {
					return fmt.Errorf("parse dashboard certificate: %w", err)
				}
				certs = append(certs, c)
			}
			inter := x509.NewCertPool()
			for _, c := range certs[1:] {
				inter.AddCert(c)
			}
			if _, err := certs[0].Verify(x509.VerifyOptions{Roots: sysPool, Intermediates: inter, DNSName: host}); err == nil {
				return nil
			}
			if caFingerprint == "" {
				return fmt.Errorf("dashboard certificate is not publicly trusted for %q: pass --ca-fingerprint from the dashboard's install command", host)
			}
			candidates := certs[1:]
			if pinnedCA != nil {
				candidates = []*x509.Certificate{pinnedCA}
			}
			for _, c := range candidates {
				if !strings.EqualFold(crypto.Fingerprint(c), caFingerprint) {
					continue
				}
				roots := x509.NewCertPool()
				roots.AddCert(c)
				if _, err := certs[0].Verify(x509.VerifyOptions{Roots: roots, DNSName: "localhost"}); err == nil {
					return nil
				}
			}
			return fmt.Errorf("dashboard certificate does not match --ca-fingerprint: refusing to send the registration token")
		},
	}, nil
}

// registerWithDashboard performs the HTTP-based registration with the dashboard.
func registerWithDashboard(dashboardURL string, req *models.RegistrationRequest, caFingerprint string) (*models.RegistrationResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := dashboardURL + "/api/agent/register"

	var pinnedCA *x509.Certificate
	if caFingerprint != "" {
		if pinnedCA, err = fetchDashboardCA(dashboardURL, caFingerprint); err != nil {
			return nil, err
		}
	}
	tlsCfg, err := registrationTLSConfig(dashboardURL, caFingerprint, pinnedCA)
	if err != nil {
		return nil, err
	}
	client := &http.Client{
		Timeout:   30 * time.Second,
		Transport: &http.Transport{TLSClientConfig: tlsCfg},
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
