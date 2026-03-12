package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/CTJaeger/KleverNodeHub/internal/models"
)

// ServerStore handles server persistence.
type ServerStore struct {
	db *DB
}

// NewServerStore creates a new ServerStore.
func NewServerStore(db *DB) *ServerStore {
	return &ServerStore{db: db}
}

// Create inserts a new server.
func (s *ServerStore) Create(server *models.Server) error {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()

	now := time.Now().Unix()
	if server.RegisteredAt == 0 {
		server.RegisteredAt = now
	}
	server.UpdatedAt = now

	metadata, err := json.Marshal(server.Metadata)
	if err != nil {
		metadata = []byte("{}")
	}

	_, err = s.db.db.Exec(`
		INSERT INTO servers (id, name, hostname, ip_address, public_ip, region, os_info, agent_version, status, last_heartbeat, certificate, registered_at, updated_at, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		server.ID, server.Name, server.Hostname, server.IPAddress,
		server.PublicIP, server.Region,
		server.OSInfo, server.AgentVersion, server.Status, server.LastHeartbeat,
		server.Certificate, server.RegisteredAt, server.UpdatedAt, string(metadata),
	)
	if err != nil {
		return fmt.Errorf("create server: %w", err)
	}
	return nil
}

// GetByID retrieves a server by ID.
func (s *ServerStore) GetByID(id string) (*models.Server, error) {
	row := s.db.db.QueryRow(`
		SELECT id, name, hostname, ip_address, public_ip, region, os_info, agent_version, status, last_heartbeat, certificate, registered_at, updated_at, metadata
		FROM servers WHERE id = ?`, id)

	return scanServer(row)
}

// List retrieves all servers.
func (s *ServerStore) List() ([]models.Server, error) {
	rows, err := s.db.db.Query(`
		SELECT id, name, hostname, ip_address, public_ip, region, os_info, agent_version, status, last_heartbeat, certificate, registered_at, updated_at, metadata
		FROM servers ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list servers: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var servers []models.Server
	for rows.Next() {
		srv, err := scanServerRow(rows)
		if err != nil {
			return nil, err
		}
		servers = append(servers, *srv)
	}
	return servers, rows.Err()
}

// Update updates a server's fields.
func (s *ServerStore) Update(server *models.Server) error {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()

	server.UpdatedAt = time.Now().Unix()

	metadata, err := json.Marshal(server.Metadata)
	if err != nil {
		metadata = []byte("{}")
	}

	result, err := s.db.db.Exec(`
		UPDATE servers SET name=?, hostname=?, ip_address=?, public_ip=?, region=?, os_info=?, agent_version=?, status=?, last_heartbeat=?, certificate=?, updated_at=?, metadata=?
		WHERE id=?`,
		server.Name, server.Hostname, server.IPAddress, server.PublicIP, server.Region,
		server.OSInfo, server.AgentVersion, server.Status, server.LastHeartbeat,
		server.Certificate, server.UpdatedAt, string(metadata), server.ID,
	)
	if err != nil {
		return fmt.Errorf("update server: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("server not found: %s", server.ID)
	}
	return nil
}

// Delete removes a server by ID.
func (s *ServerStore) Delete(id string) error {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()

	result, err := s.db.db.Exec("DELETE FROM servers WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete server: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("server not found: %s", id)
	}
	return nil
}

// UpdatePublicIP updates the server's public IP and region.
func (s *ServerStore) UpdatePublicIP(id, publicIP, region string) error {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()

	_, err := s.db.db.Exec(
		"UPDATE servers SET public_ip=?, region=?, updated_at=? WHERE id=?",
		publicIP, region, time.Now().Unix(), id,
	)
	if err != nil {
		return fmt.Errorf("update public ip: %w", err)
	}
	return nil
}

// UpdateHeartbeat updates only the heartbeat timestamp and sets status to online.
func (s *ServerStore) UpdateHeartbeat(id string, timestamp int64) error {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()

	result, err := s.db.db.Exec(
		"UPDATE servers SET last_heartbeat=?, status='online', updated_at=? WHERE id=?",
		timestamp, time.Now().Unix(), id,
	)
	if err != nil {
		return fmt.Errorf("update heartbeat: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("server not found: %s", id)
	}
	return nil
}

// scanner interface shared by *sql.Row and *sql.Rows
type scanner interface {
	Scan(dest ...any) error
}

func scanServerFromScanner(s scanner) (*models.Server, error) {
	var srv models.Server
	var metadataStr string
	var cert []byte

	err := s.Scan(
		&srv.ID, &srv.Name, &srv.Hostname, &srv.IPAddress,
		&srv.PublicIP, &srv.Region,
		&srv.OSInfo, &srv.AgentVersion, &srv.Status, &srv.LastHeartbeat,
		&cert, &srv.RegisteredAt, &srv.UpdatedAt, &metadataStr,
	)
	if err != nil {
		return nil, fmt.Errorf("scan server: %w", err)
	}

	srv.Certificate = cert
	if metadataStr != "" {
		_ = json.Unmarshal([]byte(metadataStr), &srv.Metadata)
	}

	return &srv, nil
}

func scanServer(row *sql.Row) (*models.Server, error) {
	return scanServerFromScanner(row)
}

func scanServerRow(rows *sql.Rows) (*models.Server, error) {
	return scanServerFromScanner(rows)
}
