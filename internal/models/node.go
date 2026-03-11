package models

// Node represents a Klever node managed by an agent.
type Node struct {
	ID              string         `json:"id"`
	ServerID        string         `json:"server_id"`
	Name            string         `json:"name"`             // e.g., "node1"
	ContainerName   string         `json:"container_name"`   // e.g., "klever-node1"
	NodeType        string         `json:"node_type"`        // "validator", "observer"
	RedundancyLevel int            `json:"redundancy_level"` // 0 = active, 1 = fallback
	RestAPIPort     int            `json:"rest_api_port"`
	DisplayName     string         `json:"display_name,omitempty"`
	DockerImageTag  string         `json:"docker_image_tag,omitempty"`
	DataDirectory   string         `json:"data_directory"`
	BLSPublicKey    string         `json:"bls_public_key,omitempty"`
	Status          string         `json:"status"` // "running", "stopped", "syncing", "error"
	CreatedAt       int64          `json:"created_at"`
	UpdatedAt       int64          `json:"updated_at"`
	Metadata        map[string]any `json:"metadata,omitempty"`
}
