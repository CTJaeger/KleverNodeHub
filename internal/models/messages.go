package models

// Message is the WebSocket message envelope between dashboard and agents.
type Message struct {
	ID        string `json:"id"`
	Type      string `json:"type"`    // "command", "response", "event", "stream"
	Action    string `json:"action"`
	Payload   any    `json:"payload,omitempty"`
	Timestamp int64  `json:"timestamp"`
}

// AgentInfo is sent by the agent on first connect.
type AgentInfo struct {
	Version  string `json:"version"`
	OS       string `json:"os"`
	Hostname string `json:"hostname"`
	IP       string `json:"ip"`
	PublicIP string `json:"public_ip,omitempty"`
}

// HeartbeatPayload is sent periodically by the agent.
type HeartbeatPayload struct {
	Timestamp int64          `json:"timestamp"`
	Metrics   *SystemMetrics `json:"metrics,omitempty"`
	PublicIP  string         `json:"public_ip,omitempty"`
}

// SystemMetrics holds system-level resource usage data collected by the agent.
type SystemMetrics struct {
	CPUPercent  float64 `json:"cpu_percent"`
	MemTotal    uint64  `json:"mem_total"`
	MemUsed     uint64  `json:"mem_used"`
	MemPercent  float64 `json:"mem_percent"`
	DiskTotal   uint64  `json:"disk_total"`
	DiskUsed    uint64  `json:"disk_used"`
	DiskPercent float64 `json:"disk_percent"`
	LoadAvg1    float64 `json:"load_avg_1"`
	LoadAvg5    float64 `json:"load_avg_5"`
	LoadAvg15   float64 `json:"load_avg_15"`
	CollectedAt int64   `json:"collected_at"`
}

// CommandResult is sent by the agent after executing a command.
type CommandResult struct {
	CommandID string `json:"command_id"`
	Success   bool   `json:"success"`
	Output    string `json:"output,omitempty"`
	Error     string `json:"error,omitempty"`
}

// RegistrationRequest is sent by the agent during initial registration.
type RegistrationRequest struct {
	Token    string `json:"token"`
	Hostname string `json:"hostname"`
	OS       string `json:"os"`
	IP       string `json:"ip"`
}

// RegistrationResponse is sent by the dashboard after successful registration.
type RegistrationResponse struct {
	ServerID  string `json:"server_id"`
	CertPEM   string `json:"cert_pem"`
	KeyPEM    string `json:"key_pem"`
	CACertPEM string `json:"ca_cert_pem"`
}

// DiscoveredNode is a node found during agent auto-discovery.
type DiscoveredNode struct {
	ContainerID     string `json:"container_id"`
	ContainerName   string `json:"container_name"`
	Status          string `json:"status"`
	RestAPIPort     int    `json:"rest_api_port"`
	DisplayName     string `json:"display_name,omitempty"`
	RedundancyLevel int    `json:"redundancy_level"`
	DockerImageTag  string `json:"docker_image_tag,omitempty"`
	DataDirectory   string `json:"data_directory,omitempty"`
	BLSPublicKey    string `json:"bls_public_key,omitempty"`
}

// DiscoveryReport is sent by the agent after scanning for Klever nodes.
type DiscoveryReport struct {
	Nodes []DiscoveredNode `json:"nodes"`
}

// NodeMetricsEvent contains metrics polled from a single Klever node's /node/status endpoint.
type NodeMetricsEvent struct {
	NodeID      string         `json:"node_id"`
	ServerID    string         `json:"server_id"`
	Metrics     map[string]any `json:"metrics,omitempty"`
	Error       string         `json:"error,omitempty"`
	CollectedAt int64          `json:"collected_at"`
}

// NodeNonceStallEvent is sent when a node's nonce stops incrementing.
type NodeNonceStallEvent struct {
	NodeID        string  `json:"node_id"`
	ServerID      string  `json:"server_id"`
	StuckNonce    uint64  `json:"stuck_nonce"`
	StallDuration float64 `json:"stall_duration_seconds"`
	DetectedAt    int64   `json:"detected_at"`
}

// ProvisionRequest is the request body for provisioning a new Klever node.
type ProvisionRequest struct {
	ServerID        string            `json:"server_id"`
	NodeName        string            `json:"node_name"`
	Network         string            `json:"network"`          // "mainnet" or "testnet"
	ImageTag        string            `json:"image_tag"`
	Port            int               `json:"port"`
	GenerateKeys    bool              `json:"generate_keys"`
	ConfigOverrides map[string]string `json:"config_overrides,omitempty"`
}

// ProvisionProgress is sent during provisioning to report step status.
type ProvisionProgress struct {
	JobID      string `json:"job_id"`
	ServerID   string `json:"server_id"`
	Step       int    `json:"step"`
	TotalSteps int    `json:"total_steps"`
	StepName   string `json:"step_name"`
	Status     string `json:"status"` // "running", "completed", "failed"
	Message    string `json:"message,omitempty"`
	Error      string `json:"error,omitempty"`
}
