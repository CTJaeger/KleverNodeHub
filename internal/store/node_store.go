package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/CTJaeger/KleverNodeHub/internal/models"
)

// NodeStore handles node persistence.
type NodeStore struct {
	db *DB
}

// NewNodeStore creates a new NodeStore.
func NewNodeStore(db *DB) *NodeStore {
	return &NodeStore{db: db}
}

// Create inserts a new node.
func (s *NodeStore) Create(node *models.Node) error {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()

	now := time.Now().Unix()
	if node.CreatedAt == 0 {
		node.CreatedAt = now
	}
	node.UpdatedAt = now

	metadata, err := json.Marshal(node.Metadata)
	if err != nil {
		metadata = []byte("{}")
	}

	_, err = s.db.db.Exec(`
		INSERT INTO nodes (id, server_id, name, container_name, node_type, redundancy_level, rest_api_port, display_name, docker_image_tag, data_directory, bls_public_key, status, created_at, updated_at, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		node.ID, node.ServerID, node.Name, node.ContainerName,
		node.NodeType, node.RedundancyLevel, node.RestAPIPort,
		node.DisplayName, node.DockerImageTag, node.DataDirectory,
		node.BLSPublicKey, node.Status, node.CreatedAt, node.UpdatedAt,
		string(metadata),
	)
	if err != nil {
		return fmt.Errorf("create node: %w", err)
	}
	return nil
}

// GetByID retrieves a node by ID.
func (s *NodeStore) GetByID(id string) (*models.Node, error) {
	row := s.db.db.QueryRow(`
		SELECT id, server_id, name, container_name, node_type, redundancy_level, rest_api_port, display_name, docker_image_tag, data_directory, bls_public_key, status, created_at, updated_at, metadata
		FROM nodes WHERE id = ?`, id)

	return scanNode(row)
}

// ListByServer retrieves all nodes for a server.
func (s *NodeStore) ListByServer(serverID string) ([]models.Node, error) {
	rows, err := s.db.db.Query(`
		SELECT id, server_id, name, container_name, node_type, redundancy_level, rest_api_port, display_name, docker_image_tag, data_directory, bls_public_key, status, created_at, updated_at, metadata
		FROM nodes WHERE server_id = ? ORDER BY name`, serverID)
	if err != nil {
		return nil, fmt.Errorf("list nodes by server: %w", err)
	}
	defer rows.Close()

	return scanNodes(rows)
}

// ListAll retrieves all nodes, optionally filtered by status.
func (s *NodeStore) ListAll(statusFilter string) ([]models.Node, error) {
	var query string
	var args []any

	if statusFilter != "" {
		query = `SELECT id, server_id, name, container_name, node_type, redundancy_level, rest_api_port, display_name, docker_image_tag, data_directory, bls_public_key, status, created_at, updated_at, metadata
			FROM nodes WHERE status = ? ORDER BY name`
		args = []any{statusFilter}
	} else {
		query = `SELECT id, server_id, name, container_name, node_type, redundancy_level, rest_api_port, display_name, docker_image_tag, data_directory, bls_public_key, status, created_at, updated_at, metadata
			FROM nodes ORDER BY name`
	}

	rows, err := s.db.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list all nodes: %w", err)
	}
	defer rows.Close()

	return scanNodes(rows)
}

// Update updates a node's fields.
func (s *NodeStore) Update(node *models.Node) error {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()

	node.UpdatedAt = time.Now().Unix()

	metadata, err := json.Marshal(node.Metadata)
	if err != nil {
		metadata = []byte("{}")
	}

	result, err := s.db.db.Exec(`
		UPDATE nodes SET server_id=?, name=?, container_name=?, node_type=?, redundancy_level=?, rest_api_port=?, display_name=?, docker_image_tag=?, data_directory=?, bls_public_key=?, status=?, updated_at=?, metadata=?
		WHERE id=?`,
		node.ServerID, node.Name, node.ContainerName, node.NodeType,
		node.RedundancyLevel, node.RestAPIPort, node.DisplayName,
		node.DockerImageTag, node.DataDirectory, node.BLSPublicKey,
		node.Status, node.UpdatedAt, string(metadata), node.ID,
	)
	if err != nil {
		return fmt.Errorf("update node: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("node not found: %s", node.ID)
	}
	return nil
}

// Delete removes a node by ID.
func (s *NodeStore) Delete(id string) error {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()

	result, err := s.db.db.Exec("DELETE FROM nodes WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete node: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("node not found: %s", id)
	}
	return nil
}

// UpdateStatus updates only the node's status.
func (s *NodeStore) UpdateStatus(id, status string) error {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()

	result, err := s.db.db.Exec(
		"UPDATE nodes SET status=?, updated_at=? WHERE id=?",
		status, time.Now().Unix(), id,
	)
	if err != nil {
		return fmt.Errorf("update node status: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("node not found: %s", id)
	}
	return nil
}

func scanNodeFromScanner(s scanner) (*models.Node, error) {
	var node models.Node
	var metadataStr string

	err := s.Scan(
		&node.ID, &node.ServerID, &node.Name, &node.ContainerName,
		&node.NodeType, &node.RedundancyLevel, &node.RestAPIPort,
		&node.DisplayName, &node.DockerImageTag, &node.DataDirectory,
		&node.BLSPublicKey, &node.Status, &node.CreatedAt, &node.UpdatedAt,
		&metadataStr,
	)
	if err != nil {
		return nil, fmt.Errorf("scan node: %w", err)
	}

	if metadataStr != "" {
		json.Unmarshal([]byte(metadataStr), &node.Metadata)
	}

	return &node, nil
}

func scanNode(row *sql.Row) (*models.Node, error) {
	return scanNodeFromScanner(row)
}

func scanNodes(rows *sql.Rows) ([]models.Node, error) {
	var nodes []models.Node
	for rows.Next() {
		node, err := scanNodeFromScanner(rows)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, *node)
	}
	return nodes, rows.Err()
}
