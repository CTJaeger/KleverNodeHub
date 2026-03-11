package store

import (
	"fmt"
)

// SettingsStore handles key-value settings persistence.
type SettingsStore struct {
	db *DB
}

// NewSettingsStore creates a new SettingsStore.
func NewSettingsStore(db *DB) *SettingsStore {
	return &SettingsStore{db: db}
}

// Get retrieves a setting value by key. Returns empty string if not found.
func (s *SettingsStore) Get(key string) (string, error) {
	var value string
	err := s.db.db.QueryRow("SELECT value FROM settings WHERE key = ?", key).Scan(&value)
	if err != nil {
		if err.Error() == "sql: no rows in result set" {
			return "", nil
		}
		return "", fmt.Errorf("get setting %q: %w", key, err)
	}
	return value, nil
}

// Set stores a setting value. Creates or updates.
func (s *SettingsStore) Set(key, value string) error {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()

	_, err := s.db.db.Exec(
		"INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = ?",
		key, value, value,
	)
	if err != nil {
		return fmt.Errorf("set setting %q: %w", key, err)
	}
	return nil
}

// GetAll retrieves all settings as a map.
func (s *SettingsStore) GetAll() (map[string]string, error) {
	rows, err := s.db.db.Query("SELECT key, value FROM settings")
	if err != nil {
		return nil, fmt.Errorf("get all settings: %w", err)
	}
	defer func() { _ = rows.Close() }()

	result := make(map[string]string)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("scan setting: %w", err)
		}
		result[key] = value
	}
	return result, rows.Err()
}

// Delete removes a setting by key.
func (s *SettingsStore) Delete(key string) error {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()

	_, err := s.db.db.Exec("DELETE FROM settings WHERE key = ?", key)
	if err != nil {
		return fmt.Errorf("delete setting %q: %w", key, err)
	}
	return nil
}
