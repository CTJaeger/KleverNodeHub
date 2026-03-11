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
}

// HeartbeatPayload is sent periodically by the agent.
type HeartbeatPayload struct {
	Timestamp int64   `json:"timestamp"`
	CPU       float64 `json:"cpu"`
	Mem       float64 `json:"mem"`
	Disk      float64 `json:"disk"`
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
	ServerID string `json:"server_id"`
	CertPEM  string `json:"cert_pem"`
	CACertPEM string `json:"ca_cert_pem"`
}
