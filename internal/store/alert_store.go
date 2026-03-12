package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// AlertRule defines a threshold-based alert rule.
type AlertRule struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Enabled     bool    `json:"enabled"`
	MetricName  string  `json:"metric_name"`
	Condition   string  `json:"condition"`    // gt, lt, eq, stall
	Threshold   float64 `json:"threshold"`
	DurationSec int     `json:"duration_sec"` // must breach for this many seconds
	Severity    string  `json:"severity"`     // critical, warning, info
	NodeFilter  string  `json:"node_filter"`  // "*" or specific node ID
	CooldownMin int     `json:"cooldown_min"` // minutes between re-alerts
	Builtin     bool    `json:"builtin"`      // true for default rules
	CreatedAt   int64   `json:"created_at"`
	UpdatedAt   int64   `json:"updated_at"`
}

// AlertRecord represents a stored alert instance.
type AlertRecord struct {
	ID         string `json:"id"`
	RuleID     string `json:"rule_id"`
	RuleName   string `json:"rule_name"`
	NodeID     string `json:"node_id,omitempty"`
	ServerID   string `json:"server_id,omitempty"`
	Severity   string `json:"severity"`
	State      string `json:"state"` // pending, firing, resolved
	Message    string `json:"message"`
	FiredAt    int64  `json:"fired_at,omitempty"`
	ResolvedAt int64  `json:"resolved_at,omitempty"`
	NotifiedAt int64  `json:"notified_at,omitempty"`
	Acked      bool   `json:"acked"`
	CreatedAt  int64  `json:"created_at"`
}

// AlertStore handles persistence for alert rules and alert history.
type AlertStore struct {
	db *DB
}

// NewAlertStore creates a new alert store.
func NewAlertStore(db *DB) *AlertStore {
	return &AlertStore{db: db}
}

// --- Alert Rules ---

// CreateRule inserts a new alert rule.
func (s *AlertStore) CreateRule(rule *AlertRule) error {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()

	now := time.Now().Unix()
	if rule.CreatedAt == 0 {
		rule.CreatedAt = now
	}
	rule.UpdatedAt = now

	_, err := s.db.db.Exec(`
		INSERT INTO alert_rules (id, name, enabled, metric_name, condition, threshold, duration_sec, severity, node_filter, cooldown_min, builtin, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rule.ID, rule.Name, boolToInt(rule.Enabled), rule.MetricName, rule.Condition,
		rule.Threshold, rule.DurationSec, rule.Severity, rule.NodeFilter,
		rule.CooldownMin, boolToInt(rule.Builtin), rule.CreatedAt, rule.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("create alert rule: %w", err)
	}
	return nil
}

// UpdateRule updates an existing alert rule.
func (s *AlertStore) UpdateRule(rule *AlertRule) error {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()

	rule.UpdatedAt = time.Now().Unix()

	result, err := s.db.db.Exec(`
		UPDATE alert_rules SET name=?, enabled=?, metric_name=?, condition=?, threshold=?, duration_sec=?, severity=?, node_filter=?, cooldown_min=?, updated_at=?
		WHERE id=?`,
		rule.Name, boolToInt(rule.Enabled), rule.MetricName, rule.Condition,
		rule.Threshold, rule.DurationSec, rule.Severity, rule.NodeFilter,
		rule.CooldownMin, rule.UpdatedAt, rule.ID,
	)
	if err != nil {
		return fmt.Errorf("update alert rule: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("alert rule not found: %s", rule.ID)
	}
	return nil
}

// DeleteRule removes an alert rule by ID.
func (s *AlertStore) DeleteRule(id string) error {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()

	result, err := s.db.db.Exec("DELETE FROM alert_rules WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete alert rule: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("alert rule not found: %s", id)
	}
	return nil
}

// GetRule retrieves a single alert rule by ID.
func (s *AlertStore) GetRule(id string) (*AlertRule, error) {
	row := s.db.db.QueryRow(`
		SELECT id, name, enabled, metric_name, condition, threshold, duration_sec, severity, node_filter, cooldown_min, builtin, created_at, updated_at
		FROM alert_rules WHERE id = ?`, id)
	return scanAlertRule(row)
}

// ListRules retrieves all alert rules.
func (s *AlertStore) ListRules() ([]AlertRule, error) {
	rows, err := s.db.db.Query(`
		SELECT id, name, enabled, metric_name, condition, threshold, duration_sec, severity, node_filter, cooldown_min, builtin, created_at, updated_at
		FROM alert_rules ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list alert rules: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var rules []AlertRule
	for rows.Next() {
		rule, err := scanAlertRuleRow(rows)
		if err != nil {
			return nil, err
		}
		rules = append(rules, *rule)
	}
	return rules, rows.Err()
}

// ListEnabledRules returns only enabled rules.
func (s *AlertStore) ListEnabledRules() ([]AlertRule, error) {
	rows, err := s.db.db.Query(`
		SELECT id, name, enabled, metric_name, condition, threshold, duration_sec, severity, node_filter, cooldown_min, builtin, created_at, updated_at
		FROM alert_rules WHERE enabled = 1 ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list enabled rules: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var rules []AlertRule
	for rows.Next() {
		rule, err := scanAlertRuleRow(rows)
		if err != nil {
			return nil, err
		}
		rules = append(rules, *rule)
	}
	return rules, rows.Err()
}

// RuleCount returns the total number of alert rules.
func (s *AlertStore) RuleCount() (int, error) {
	var count int
	err := s.db.db.QueryRow("SELECT COUNT(*) FROM alert_rules").Scan(&count)
	return count, err
}

// --- Alerts ---

// CreateAlert inserts a new alert record.
func (s *AlertStore) CreateAlert(alert *AlertRecord) error {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()

	if alert.CreatedAt == 0 {
		alert.CreatedAt = time.Now().Unix()
	}

	_, err := s.db.db.Exec(`
		INSERT INTO alerts (id, rule_id, rule_name, node_id, server_id, severity, state, message, fired_at, resolved_at, notified_at, acked, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		alert.ID, alert.RuleID, alert.RuleName, alert.NodeID, alert.ServerID,
		alert.Severity, alert.State, alert.Message,
		alert.FiredAt, alert.ResolvedAt, alert.NotifiedAt,
		boolToInt(alert.Acked), alert.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("create alert: %w", err)
	}
	return nil
}

// UpdateAlert updates an alert's state and timestamps.
func (s *AlertStore) UpdateAlert(alert *AlertRecord) error {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()

	_, err := s.db.db.Exec(`
		UPDATE alerts SET state=?, message=?, fired_at=?, resolved_at=?, notified_at=?, acked=?
		WHERE id=?`,
		alert.State, alert.Message, alert.FiredAt, alert.ResolvedAt,
		alert.NotifiedAt, boolToInt(alert.Acked), alert.ID,
	)
	if err != nil {
		return fmt.Errorf("update alert: %w", err)
	}
	return nil
}

// AcknowledgeAlert marks an alert as acknowledged.
func (s *AlertStore) AcknowledgeAlert(id string) error {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()

	result, err := s.db.db.Exec("UPDATE alerts SET acked = 1 WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("ack alert: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("alert not found: %s", id)
	}
	return nil
}

// ListActiveAlerts returns alerts in pending or firing state.
func (s *AlertStore) ListActiveAlerts() ([]AlertRecord, error) {
	rows, err := s.db.db.Query(`
		SELECT id, rule_id, rule_name, node_id, server_id, severity, state, message, fired_at, resolved_at, notified_at, acked, created_at
		FROM alerts WHERE state IN ('pending', 'firing')
		ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list active alerts: %w", err)
	}
	defer func() { _ = rows.Close() }()

	return scanAlerts(rows)
}

// ListAlertHistory returns recent alerts (all states) with a limit.
func (s *AlertStore) ListAlertHistory(limit int) ([]AlertRecord, error) {
	rows, err := s.db.db.Query(`
		SELECT id, rule_id, rule_name, node_id, server_id, severity, state, message, fired_at, resolved_at, notified_at, acked, created_at
		FROM alerts ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list alert history: %w", err)
	}
	defer func() { _ = rows.Close() }()

	return scanAlerts(rows)
}

// GetActiveAlertByRuleAndNode finds an active alert for a specific rule+node combo.
func (s *AlertStore) GetActiveAlertByRuleAndNode(ruleID, nodeID string) (*AlertRecord, error) {
	row := s.db.db.QueryRow(`
		SELECT id, rule_id, rule_name, node_id, server_id, severity, state, message, fired_at, resolved_at, notified_at, acked, created_at
		FROM alerts WHERE rule_id = ? AND node_id = ? AND state IN ('pending', 'firing')
		LIMIT 1`, ruleID, nodeID)

	alert, err := scanAlertRecord(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return alert, nil
}

// GetActiveAlertByRuleAndServer finds an active alert for a specific rule+server combo.
func (s *AlertStore) GetActiveAlertByRuleAndServer(ruleID, serverID string) (*AlertRecord, error) {
	row := s.db.db.QueryRow(`
		SELECT id, rule_id, rule_name, node_id, server_id, severity, state, message, fired_at, resolved_at, notified_at, acked, created_at
		FROM alerts WHERE rule_id = ? AND server_id = ? AND node_id = '' AND state IN ('pending', 'firing')
		LIMIT 1`, ruleID, serverID)

	alert, err := scanAlertRecord(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return alert, nil
}

// PurgeOldAlerts removes resolved alerts older than the given duration.
func (s *AlertStore) PurgeOldAlerts(olderThan time.Duration) (int64, error) {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()

	cutoff := time.Now().Add(-olderThan).Unix()
	result, err := s.db.db.Exec(
		"DELETE FROM alerts WHERE state = 'resolved' AND resolved_at < ?", cutoff)
	if err != nil {
		return 0, fmt.Errorf("purge old alerts: %w", err)
	}

	count, _ := result.RowsAffected()
	return count, nil
}

// --- Helpers ---

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

type alertRuleScanner interface {
	Scan(dest ...any) error
}

func scanAlertRuleFromScanner(s alertRuleScanner) (*AlertRule, error) {
	var rule AlertRule
	var enabled, builtin int

	err := s.Scan(
		&rule.ID, &rule.Name, &enabled, &rule.MetricName, &rule.Condition,
		&rule.Threshold, &rule.DurationSec, &rule.Severity, &rule.NodeFilter,
		&rule.CooldownMin, &builtin, &rule.CreatedAt, &rule.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scan alert rule: %w", err)
	}

	rule.Enabled = enabled != 0
	rule.Builtin = builtin != 0
	return &rule, nil
}

func scanAlertRule(row *sql.Row) (*AlertRule, error) {
	return scanAlertRuleFromScanner(row)
}

func scanAlertRuleRow(rows *sql.Rows) (*AlertRule, error) {
	return scanAlertRuleFromScanner(rows)
}

func scanAlertRecordFromScanner(s alertRuleScanner) (*AlertRecord, error) {
	var alert AlertRecord
	var acked int

	err := s.Scan(
		&alert.ID, &alert.RuleID, &alert.RuleName, &alert.NodeID, &alert.ServerID,
		&alert.Severity, &alert.State, &alert.Message,
		&alert.FiredAt, &alert.ResolvedAt, &alert.NotifiedAt,
		&acked, &alert.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scan alert: %w", err)
	}

	alert.Acked = acked != 0
	return &alert, nil
}

func scanAlertRecord(row *sql.Row) (*AlertRecord, error) {
	return scanAlertRecordFromScanner(row)
}

func scanAlerts(rows *sql.Rows) ([]AlertRecord, error) {
	var alerts []AlertRecord
	for rows.Next() {
		alert, err := scanAlertRecordFromScanner(rows)
		if err != nil {
			return nil, err
		}
		alerts = append(alerts, *alert)
	}
	return alerts, rows.Err()
}
