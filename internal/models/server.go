package models

// Server represents a registered agent/server.
type Server struct {
	ID            string         `json:"id"`
	Name          string         `json:"name"`
	Hostname      string         `json:"hostname"`
	IPAddress     string         `json:"ip_address"`
	OSInfo        string         `json:"os_info,omitempty"`
	AgentVersion  string         `json:"agent_version,omitempty"`
	Status        string         `json:"status"` // "online", "offline", "updating"
	LastHeartbeat int64          `json:"last_heartbeat"`
	Certificate   []byte         `json:"certificate,omitempty"` // Encrypted at rest
	RegisteredAt  int64          `json:"registered_at"`
	UpdatedAt     int64          `json:"updated_at"`
	Metadata      map[string]any `json:"metadata,omitempty"`
}
