// Package store implements the SQLite persistence layer for Klever Node Hub.
//
// DEPENDENCY NOTE: This package uses modernc.org/sqlite as a pure-Go SQLite
// driver (no CGO required). This enables cross-compilation to all target
// platforms without a C compiler. Sensitive fields (certificates, keys) are
// encrypted at the application level using the crypto module's AES-256-GCM.
package store

import (
	"database/sql"
	"fmt"
	"sync"

	_ "modernc.org/sqlite" // Pure-Go SQLite driver
)

// DB wraps the SQLite database connection with encryption support.
type DB struct {
	db  *sql.DB
	mu  sync.RWMutex // Serialize writes (SQLite limitation)
}

// Open opens or creates a SQLite database at the given path.
// Enables WAL mode and foreign keys.
func Open(dbPath string) (*DB, error) {
	sqlDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	// Enable WAL mode for better concurrent read performance
	if _, err := sqlDB.Exec("PRAGMA journal_mode=WAL"); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("enable WAL mode: %w", err)
	}

	// Enable foreign keys
	if _, err := sqlDB.Exec("PRAGMA foreign_keys=ON"); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}

	// Set busy timeout for concurrent access (5 seconds)
	if _, err := sqlDB.Exec("PRAGMA busy_timeout=5000"); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("set busy timeout: %w", err)
	}

	store := &DB{db: sqlDB}

	if err := store.migrate(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("run migrations: %w", err)
	}

	return store, nil
}

// Close closes the database connection.
func (d *DB) Close() error {
	return d.db.Close()
}

// SQL returns the underlying *sql.DB for advanced queries.
func (d *DB) SQL() *sql.DB {
	return d.db
}

// migrate runs all schema migrations idempotently.
func (d *DB) migrate() error {
	// Create migration tracking table
	if _, err := d.db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_version (
			version INTEGER PRIMARY KEY
		)
	`); err != nil {
		return fmt.Errorf("create schema_version table: %w", err)
	}

	currentVersion := 0
	row := d.db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_version")
	if err := row.Scan(&currentVersion); err != nil {
		return fmt.Errorf("get schema version: %w", err)
	}

	for i, migration := range migrations {
		version := i + 1
		if version <= currentVersion {
			continue
		}

		tx, err := d.db.Begin()
		if err != nil {
			return fmt.Errorf("begin migration %d: %w", version, err)
		}

		if _, err := tx.Exec(migration); err != nil {
			tx.Rollback()
			return fmt.Errorf("run migration %d: %w", version, err)
		}

		if _, err := tx.Exec("INSERT INTO schema_version (version) VALUES (?)", version); err != nil {
			tx.Rollback()
			return fmt.Errorf("record migration %d: %w", version, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", version, err)
		}
	}

	return nil
}

// migrations holds all schema migrations in order.
// Each entry is a SQL statement (or multiple statements separated by ;).
// New phases add entries — never modify existing ones.
var migrations = []string{
	// Migration 1: Phase 1 tables
	`CREATE TABLE IF NOT EXISTS settings (
		key   TEXT PRIMARY KEY,
		value TEXT NOT NULL
	);

	CREATE TABLE IF NOT EXISTS servers (
		id              TEXT PRIMARY KEY,
		name            TEXT NOT NULL,
		hostname        TEXT NOT NULL,
		ip_address      TEXT NOT NULL,
		os_info         TEXT DEFAULT '',
		agent_version   TEXT DEFAULT '',
		status          TEXT NOT NULL DEFAULT 'offline',
		last_heartbeat  INTEGER DEFAULT 0,
		certificate     BLOB,
		registered_at   INTEGER NOT NULL,
		updated_at      INTEGER NOT NULL,
		metadata        TEXT DEFAULT '{}'
	);

	CREATE TABLE IF NOT EXISTS nodes (
		id               TEXT PRIMARY KEY,
		server_id        TEXT NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
		name             TEXT NOT NULL,
		container_name   TEXT NOT NULL,
		node_type        TEXT NOT NULL DEFAULT 'validator',
		redundancy_level INTEGER DEFAULT 0,
		rest_api_port    INTEGER NOT NULL,
		display_name     TEXT DEFAULT '',
		docker_image_tag TEXT DEFAULT '',
		data_directory   TEXT NOT NULL,
		bls_public_key   TEXT DEFAULT '',
		status           TEXT NOT NULL DEFAULT 'stopped',
		created_at       INTEGER NOT NULL,
		updated_at       INTEGER NOT NULL,
		metadata         TEXT DEFAULT '{}'
	);

	CREATE INDEX IF NOT EXISTS idx_nodes_server ON nodes(server_id);`,
}
