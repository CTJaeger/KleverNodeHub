package store

import (
	"os"
	"path/filepath"
	"testing"
)

func openTestDB(t *testing.T) (*DB, func()) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	return db, func() { _ = db.Close(); _ = os.RemoveAll(dir) }
}

func TestAlertRuleCRUD(t *testing.T) {
	db, cleanup := openTestDB(t)
	defer cleanup()

	s := NewAlertStore(db)

	// Create
	rule := &AlertRule{
		ID: "test-1", Name: "Test Rule", Enabled: true,
		MetricName: "cpu_percent", Condition: "gt", Threshold: 90,
		DurationSec: 300, Severity: "warning", NodeFilter: "*", CooldownMin: 5,
	}
	if err := s.CreateRule(rule); err != nil {
		t.Fatalf("create rule: %v", err)
	}

	// Get
	got, err := s.GetRule("test-1")
	if err != nil {
		t.Fatalf("get rule: %v", err)
	}
	if got.Name != "Test Rule" {
		t.Errorf("name = %q, want Test Rule", got.Name)
	}
	if !got.Enabled {
		t.Error("expected enabled")
	}

	// List
	rules, err := s.ListRules()
	if err != nil {
		t.Fatalf("list rules: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}

	// Update
	rule.Name = "Updated Rule"
	rule.Threshold = 95
	if err := s.UpdateRule(rule); err != nil {
		t.Fatalf("update rule: %v", err)
	}
	got2, _ := s.GetRule("test-1")
	if got2.Name != "Updated Rule" {
		t.Errorf("name after update = %q, want Updated Rule", got2.Name)
	}
	if got2.Threshold != 95 {
		t.Errorf("threshold after update = %f, want 95", got2.Threshold)
	}

	// Delete
	if err := s.DeleteRule("test-1"); err != nil {
		t.Fatalf("delete rule: %v", err)
	}
	rules2, _ := s.ListRules()
	if len(rules2) != 0 {
		t.Errorf("expected 0 rules after delete, got %d", len(rules2))
	}
}

func TestAlertRuleEnabledFilter(t *testing.T) {
	db, cleanup := openTestDB(t)
	defer cleanup()

	s := NewAlertStore(db)

	_ = s.CreateRule(&AlertRule{ID: "r1", Name: "Active", Enabled: true, MetricName: "x", Condition: "gt", Threshold: 1, NodeFilter: "*"})
	_ = s.CreateRule(&AlertRule{ID: "r2", Name: "Disabled", Enabled: false, MetricName: "x", Condition: "gt", Threshold: 1, NodeFilter: "*"})

	enabled, err := s.ListEnabledRules()
	if err != nil {
		t.Fatalf("list enabled: %v", err)
	}
	if len(enabled) != 1 {
		t.Fatalf("expected 1 enabled rule, got %d", len(enabled))
	}
	if enabled[0].ID != "r1" {
		t.Errorf("expected r1, got %s", enabled[0].ID)
	}
}

func TestAlertRecordCRUD(t *testing.T) {
	db, cleanup := openTestDB(t)
	defer cleanup()

	s := NewAlertStore(db)

	alert := &AlertRecord{
		ID: "a1", RuleID: "r1", RuleName: "Test",
		NodeID: "n1", Severity: "warning", State: "firing",
		Message: "CPU high", FiredAt: 1000,
	}
	if err := s.CreateAlert(alert); err != nil {
		t.Fatalf("create alert: %v", err)
	}

	// List active
	active, _ := s.ListActiveAlerts()
	if len(active) != 1 {
		t.Fatalf("expected 1 active alert, got %d", len(active))
	}

	// Acknowledge
	if err := s.AcknowledgeAlert("a1"); err != nil {
		t.Fatalf("ack alert: %v", err)
	}

	// Resolve (keep acked flag)
	alert.Acked = true
	alert.State = "resolved"
	alert.ResolvedAt = 2000
	if err := s.UpdateAlert(alert); err != nil {
		t.Fatalf("update alert: %v", err)
	}

	active2, _ := s.ListActiveAlerts()
	if len(active2) != 0 {
		t.Errorf("expected 0 active alerts after resolve, got %d", len(active2))
	}

	history, _ := s.ListAlertHistory(10)
	if len(history) != 1 {
		t.Fatalf("expected 1 history, got %d", len(history))
	}
	if !history[0].Acked {
		t.Error("expected acked")
	}
	if history[0].State != "resolved" {
		t.Errorf("state = %q, want resolved", history[0].State)
	}
}

func TestGetActiveAlertByRuleAndNode(t *testing.T) {
	db, cleanup := openTestDB(t)
	defer cleanup()

	s := NewAlertStore(db)

	_ = s.CreateAlert(&AlertRecord{
		ID: "a1", RuleID: "r1", RuleName: "Test",
		NodeID: "n1", Severity: "warning", State: "firing",
	})

	got, err := s.GetActiveAlertByRuleAndNode("r1", "n1")
	if err != nil {
		t.Fatalf("get active: %v", err)
	}
	if got == nil {
		t.Fatal("expected alert, got nil")
	}
	if got.ID != "a1" {
		t.Errorf("id = %q, want a1", got.ID)
	}

	// Non-existent
	got2, err := s.GetActiveAlertByRuleAndNode("r1", "n2")
	if err != nil {
		t.Fatalf("get active: %v", err)
	}
	if got2 != nil {
		t.Error("expected nil for non-matching node")
	}
}

func TestRuleCount(t *testing.T) {
	db, cleanup := openTestDB(t)
	defer cleanup()

	s := NewAlertStore(db)

	count, _ := s.RuleCount()
	if count != 0 {
		t.Errorf("expected 0, got %d", count)
	}

	_ = s.CreateRule(&AlertRule{ID: "r1", Name: "A", MetricName: "x", Condition: "gt", Threshold: 1, NodeFilter: "*"})
	_ = s.CreateRule(&AlertRule{ID: "r2", Name: "B", MetricName: "x", Condition: "gt", Threshold: 1, NodeFilter: "*"})

	count2, _ := s.RuleCount()
	if count2 != 2 {
		t.Errorf("expected 2, got %d", count2)
	}
}

func TestBoolToInt(t *testing.T) {
	if boolToInt(true) != 1 {
		t.Error("boolToInt(true) != 1")
	}
	if boolToInt(false) != 0 {
		t.Error("boolToInt(false) != 0")
	}
}
