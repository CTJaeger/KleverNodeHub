package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// VersionChange represents one observed version change for a node.
type VersionChange struct {
	ID         int64  `json:"id"`
	NodeID     string `json:"node_id"`
	ServerID   string `json:"server_id"`
	Version    string `json:"version"`
	DetectedAt int64  `json:"detected_at"`
	Evaluated  bool   `json:"evaluated"`
}

// VersionHistoryStore persists node version changes for regression detection.
type VersionHistoryStore struct {
	db *DB
}

// NewVersionHistoryStore creates a new VersionHistoryStore.
func NewVersionHistoryStore(db *DB) *VersionHistoryStore {
	return &VersionHistoryStore{db: db}
}

// RecordVersion records a version for a node, but only if it differs from
// the most recently recorded version. Returns true if a new change row was
// inserted (i.e. the version actually changed), false if unchanged.
func (s *VersionHistoryStore) RecordVersion(nodeID, serverID, version string, detectedAt int64) (bool, error) {
	if nodeID == "" || version == "" {
		return false, nil
	}

	last, err := s.LatestVersion(nodeID)
	if err != nil {
		return false, err
	}
	if last == version {
		return false, nil
	}

	_, err = s.db.db.Exec(
		`INSERT INTO node_version_history (node_id, server_id, version, detected_at, evaluated)
		 VALUES (?, ?, ?, ?, 0)`,
		nodeID, serverID, version, detectedAt,
	)
	if err != nil {
		return false, fmt.Errorf("insert version history: %w", err)
	}
	return true, nil
}

// LatestVersion returns the most recently recorded version for a node,
// or "" if the node has no history yet.
func (s *VersionHistoryStore) LatestVersion(nodeID string) (string, error) {
	var version string
	err := s.db.db.QueryRow(
		`SELECT version FROM node_version_history
		 WHERE node_id = ? ORDER BY detected_at DESC, id DESC LIMIT 1`,
		nodeID,
	).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("latest version: %w", err)
	}
	return version, nil
}

// PendingEvaluations returns version changes that are not yet evaluated and
// whose detection time is between minAge and maxAge seconds ago. The lower
// bound gives the new version time to settle (so initial sync noise is past);
// the upper bound stops us from evaluating ancient changes after a restart.
func (s *VersionHistoryStore) PendingEvaluations(now, minAgeSec, maxAgeSec int64) ([]VersionChange, error) {
	rows, err := s.db.db.Query(
		`SELECT id, node_id, server_id, version, detected_at, evaluated
		 FROM node_version_history
		 WHERE evaluated = 0
		   AND detected_at <= ?
		   AND detected_at >= ?
		 ORDER BY detected_at ASC`,
		now-minAgeSec, now-maxAgeSec,
	)
	if err != nil {
		return nil, fmt.Errorf("pending evaluations: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var changes []VersionChange
	for rows.Next() {
		var c VersionChange
		if err := rows.Scan(&c.ID, &c.NodeID, &c.ServerID, &c.Version, &c.DetectedAt, &c.Evaluated); err != nil {
			return nil, fmt.Errorf("scan version change: %w", err)
		}
		changes = append(changes, c)
	}
	return changes, rows.Err()
}

// PreviousChange returns the version-change row immediately before the given
// change for the same node (used to know what the node upgraded *from* and
// when). Returns nil if this is the node's first recorded version.
func (s *VersionHistoryStore) PreviousChange(change VersionChange) (*VersionChange, error) {
	var c VersionChange
	err := s.db.db.QueryRow(
		`SELECT id, node_id, server_id, version, detected_at, evaluated
		 FROM node_version_history
		 WHERE node_id = ? AND detected_at < ?
		 ORDER BY detected_at DESC, id DESC LIMIT 1`,
		change.NodeID, change.DetectedAt,
	).Scan(&c.ID, &c.NodeID, &c.ServerID, &c.Version, &c.DetectedAt, &c.Evaluated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("previous change: %w", err)
	}
	return &c, nil
}

// MarkEvaluated flags a version change as evaluated so the regression
// detector never alerts on it twice.
func (s *VersionHistoryStore) MarkEvaluated(id int64) error {
	_, err := s.db.db.Exec(`UPDATE node_version_history SET evaluated = 1 WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("mark evaluated: %w", err)
	}
	return nil
}

// LastChange returns the most recent version change for a node (evaluated or
// not), or nil if the node has no history. Used by the passive performance
// report on the node detail page.
func (s *VersionHistoryStore) LastChange(nodeID string) (*VersionChange, error) {
	var c VersionChange
	err := s.db.db.QueryRow(
		`SELECT id, node_id, server_id, version, detected_at, evaluated
		 FROM node_version_history
		 WHERE node_id = ? ORDER BY detected_at DESC, id DESC LIMIT 1`,
		nodeID,
	).Scan(&c.ID, &c.NodeID, &c.ServerID, &c.Version, &c.DetectedAt, &c.Evaluated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("last change: %w", err)
	}
	return &c, nil
}
