package alerting

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/CTJaeger/KleverNodeHub/internal/models"
	"github.com/CTJaeger/KleverNodeHub/internal/notify"
	"github.com/CTJaeger/KleverNodeHub/internal/store"
)

func setupTestDB(t *testing.T) (*store.AlertStore, *store.MetricsStore, *store.NodeStore, *store.ServerStore, func()) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}

	return store.NewAlertStore(db),
		store.NewMetricsStore(db),
		store.NewNodeStore(db),
		store.NewServerStore(db),
		func() { _ = db.Close(); _ = os.RemoveAll(dir) }
}

func TestCheckCondition(t *testing.T) {
	tests := []struct {
		cond      string
		value     float64
		threshold float64
		want      bool
	}{
		{"gt", 95.0, 90.0, true},
		{"gt", 85.0, 90.0, false},
		{"gt", 90.0, 90.0, false},
		{"lt", 2.0, 3.0, true},
		{"lt", 5.0, 3.0, false},
		{"eq", 1.0, 1.0, true},
		{"eq", 0.0, 1.0, false},
		{"unknown", 1.0, 1.0, false},
	}

	for _, tt := range tests {
		got := checkCondition(tt.cond, tt.value, tt.threshold)
		if got != tt.want {
			t.Errorf("checkCondition(%q, %f, %f) = %v, want %v", tt.cond, tt.value, tt.threshold, got, tt.want)
		}
	}
}

func TestFormatAlertMessage(t *testing.T) {
	rule := &store.AlertRule{
		MetricName: "cpu_percent",
		Condition:  "gt",
		Threshold:  90,
	}

	msg := formatAlertMessage(rule, "server:prod1", 95.5)
	if msg == "" {
		t.Error("expected non-empty message")
	}
	if len(msg) < 10 {
		t.Errorf("message too short: %q", msg)
	}
}

func TestFormatAlertMessage_AllConditions(t *testing.T) {
	conditions := []string{"gt", "lt", "eq", "stall", "other"}
	for _, cond := range conditions {
		rule := &store.AlertRule{
			MetricName: "test_metric",
			Condition:  cond,
			Threshold:  50,
		}
		msg := formatAlertMessage(rule, "node:test", 55.0)
		if msg == "" {
			t.Errorf("empty message for condition %q", cond)
		}
	}
}

func TestEvaluatorEnsureDefaults(t *testing.T) {
	alertStore, metricsStore, nodeStore, serverStore, cleanup := setupTestDB(t)
	defer cleanup()

	notifier := notify.NewManager()
	eval := NewEvaluator(alertStore, metricsStore, nodeStore, serverStore, notifier)

	// First call should create defaults
	eval.EnsureDefaults()

	rules, err := alertStore.ListRules()
	if err != nil {
		t.Fatalf("list rules: %v", err)
	}
	if len(rules) != len(DefaultRules()) {
		t.Fatalf("expected %d default rules, got %d", len(DefaultRules()), len(rules))
	}

	// Second call should be a no-op
	eval.EnsureDefaults()
	rules2, _ := alertStore.ListRules()
	if len(rules2) != len(rules) {
		t.Errorf("second EnsureDefaults changed count: %d → %d", len(rules), len(rules2))
	}
}

func TestEvaluatorThresholdAlert(t *testing.T) {
	alertStore, metricsStore, nodeStore, serverStore, cleanup := setupTestDB(t)
	defer cleanup()

	// Create a server and node
	srv := &models.Server{
		ID: "srv1", Name: "test-server", Hostname: "localhost",
		IPAddress: "127.0.0.1", Status: "online",
		LastHeartbeat: time.Now().Unix(),
	}
	if err := serverStore.Create(srv); err != nil {
		t.Fatalf("create server: %v", err)
	}

	node := &models.Node{
		ID: "node1", ServerID: "srv1", Name: "validator",
		ContainerName: "klever-node1", RestAPIPort: 8080,
		DataDirectory: "/data/node1", Status: "running",
	}
	if err := nodeStore.Create(node); err != nil {
		t.Fatalf("create node: %v", err)
	}

	// Insert metrics above threshold
	now := time.Now().Unix()
	metrics := map[string]float64{"klv_connectedPeers": 1.0}
	if err := metricsStore.InsertNodeMetrics("node1", "srv1", metrics, now-30); err != nil {
		t.Fatalf("insert metrics: %v", err)
	}
	if err := metricsStore.InsertNodeMetrics("node1", "srv1", metrics, now-15); err != nil {
		t.Fatalf("insert metrics: %v", err)
	}

	// Create a rule with no duration requirement (fires immediately)
	rule := &store.AlertRule{
		ID: "test-peers", Name: "Low Peers", Enabled: true,
		MetricName: "klv_connectedPeers", Condition: "lt", Threshold: 3,
		DurationSec: 0, Severity: "warning", NodeFilter: "*", CooldownMin: 5,
	}
	if err := alertStore.CreateRule(rule); err != nil {
		t.Fatalf("create rule: %v", err)
	}

	notifier := notify.NewManager()
	eval := NewEvaluator(alertStore, metricsStore, nodeStore, serverStore, notifier)

	// Run evaluation manually
	eval.evaluate()

	// Check that an alert was created
	active, err := alertStore.ListActiveAlerts()
	if err != nil {
		t.Fatalf("list active alerts: %v", err)
	}
	if len(active) != 1 {
		t.Fatalf("expected 1 active alert, got %d", len(active))
	}
	if active[0].RuleID != "test-peers" {
		t.Errorf("alert rule_id = %q, want test-peers", active[0].RuleID)
	}
	if active[0].State != "firing" {
		t.Errorf("alert state = %q, want firing", active[0].State)
	}
}

func TestEvaluatorResolvesAlert(t *testing.T) {
	alertStore, metricsStore, nodeStore, serverStore, cleanup := setupTestDB(t)
	defer cleanup()

	srv := &models.Server{
		ID: "srv1", Name: "test-server", Hostname: "localhost",
		IPAddress: "127.0.0.1", Status: "online",
		LastHeartbeat: time.Now().Unix(),
	}
	if err := serverStore.Create(srv); err != nil {
		t.Fatalf("create server: %v", err)
	}

	node := &models.Node{
		ID: "node1", ServerID: "srv1", Name: "validator",
		ContainerName: "klever-node1", RestAPIPort: 8080,
		DataDirectory: "/data/node1", Status: "running",
	}
	if err := nodeStore.Create(node); err != nil {
		t.Fatalf("create node: %v", err)
	}

	// Insert low peer metrics first
	now := time.Now().Unix()
	if err := metricsStore.InsertNodeMetrics("node1", "srv1", map[string]float64{"klv_connectedPeers": 1}, now-30); err != nil {
		t.Fatalf("insert metrics: %v", err)
	}
	if err := metricsStore.InsertNodeMetrics("node1", "srv1", map[string]float64{"klv_connectedPeers": 1}, now-15); err != nil {
		t.Fatalf("insert metrics: %v", err)
	}

	rule := &store.AlertRule{
		ID: "test-peers", Name: "Low Peers", Enabled: true,
		MetricName: "klv_connectedPeers", Condition: "lt", Threshold: 3,
		DurationSec: 0, Severity: "warning", NodeFilter: "*", CooldownMin: 5,
	}
	if err := alertStore.CreateRule(rule); err != nil {
		t.Fatalf("create rule: %v", err)
	}

	notifier := notify.NewManager()
	eval := NewEvaluator(alertStore, metricsStore, nodeStore, serverStore, notifier)

	// First eval: should fire
	eval.evaluate()

	active, _ := alertStore.ListActiveAlerts()
	if len(active) != 1 {
		t.Fatalf("expected 1 active alert after first eval, got %d", len(active))
	}

	// Insert normal metrics (peers recovered)
	if err := metricsStore.InsertNodeMetrics("node1", "srv1", map[string]float64{"klv_connectedPeers": 10}, now); err != nil {
		t.Fatalf("insert normal metrics: %v", err)
	}

	// Second eval: should resolve
	eval.evaluate()

	active2, _ := alertStore.ListActiveAlerts()
	if len(active2) != 0 {
		t.Errorf("expected 0 active alerts after resolve, got %d", len(active2))
	}

	// Check history shows resolved
	history, _ := alertStore.ListAlertHistory(10)
	if len(history) != 1 {
		t.Fatalf("expected 1 alert in history, got %d", len(history))
	}
	if history[0].State != "resolved" {
		t.Errorf("history alert state = %q, want resolved", history[0].State)
	}
}

func TestEvaluatorPendingThenFiring(t *testing.T) {
	alertStore, metricsStore, nodeStore, serverStore, cleanup := setupTestDB(t)
	defer cleanup()

	srv := &models.Server{
		ID: "srv1", Name: "test", Hostname: "localhost",
		IPAddress: "127.0.0.1", Status: "online",
		LastHeartbeat: time.Now().Unix(),
	}
	_ = serverStore.Create(srv)

	node := &models.Node{
		ID: "node1", ServerID: "srv1", Name: "validator",
		ContainerName: "klever-node1", RestAPIPort: 8080,
		DataDirectory: "/data/node1", Status: "running",
	}
	_ = nodeStore.Create(node)

	now := time.Now().Unix()
	_ = metricsStore.InsertNodeMetrics("node1", "srv1", map[string]float64{"klv_connectedPeers": 1}, now-30)
	_ = metricsStore.InsertNodeMetrics("node1", "srv1", map[string]float64{"klv_connectedPeers": 1}, now-15)

	// Rule with 60s duration — should stay pending
	rule := &store.AlertRule{
		ID: "dur-peers", Name: "Duration Low Peers", Enabled: true,
		MetricName: "klv_connectedPeers", Condition: "lt", Threshold: 3,
		DurationSec: 60, Severity: "warning", NodeFilter: "*", CooldownMin: 5,
	}
	_ = alertStore.CreateRule(rule)

	notifier := notify.NewManager()
	eval := NewEvaluator(alertStore, metricsStore, nodeStore, serverStore, notifier)

	eval.evaluate()

	// Should be pending, not yet firing (duration not met)
	active, _ := alertStore.ListActiveAlerts()
	if len(active) != 0 {
		t.Errorf("expected 0 active alerts (still pending in-memory), got %d", len(active))
	}

	// Check state is pending in evaluator memory
	eval.mu.Lock()
	stateCount := len(eval.states)
	var pendingState string
	for _, s := range eval.states {
		pendingState = s.State
	}
	eval.mu.Unlock()

	if stateCount != 1 {
		t.Fatalf("expected 1 state entry, got %d", stateCount)
	}
	if pendingState != "pending" {
		t.Errorf("state = %q, want pending", pendingState)
	}
}

func TestEvaluatorSystemMetrics(t *testing.T) {
	alertStore, metricsStore, nodeStore, serverStore, cleanup := setupTestDB(t)
	defer cleanup()

	srv := &models.Server{
		ID: "srv1", Name: "prod", Hostname: "prod.local",
		IPAddress: "10.0.0.1", Status: "online",
		LastHeartbeat: time.Now().Unix(),
	}
	_ = serverStore.Create(srv)

	now := time.Now().Unix()
	_ = metricsStore.InsertSystemMetrics("srv1", 95, 80, 60, 2.5, now-30)
	_ = metricsStore.InsertSystemMetrics("srv1", 96, 82, 62, 2.8, now-15)

	rule := &store.AlertRule{
		ID: "sys-cpu", Name: "System CPU", Enabled: true,
		MetricName: "cpu_percent", Condition: "gt", Threshold: 90,
		DurationSec: 0, Severity: "warning", NodeFilter: "*", CooldownMin: 5,
	}
	_ = alertStore.CreateRule(rule)

	notifier := notify.NewManager()
	eval := NewEvaluator(alertStore, metricsStore, nodeStore, serverStore, notifier)
	eval.evaluate()

	active, _ := alertStore.ListActiveAlerts()
	if len(active) != 1 {
		t.Fatalf("expected 1 active alert for system CPU, got %d", len(active))
	}
	if active[0].ServerID != "srv1" {
		t.Errorf("alert server_id = %q, want srv1", active[0].ServerID)
	}
}

func TestEvaluatorHeartbeatStale(t *testing.T) {
	alertStore, metricsStore, nodeStore, serverStore, cleanup := setupTestDB(t)
	defer cleanup()

	// Server with stale heartbeat (120s ago, threshold 60s)
	srv := &models.Server{
		ID: "srv1", Name: "stale", Hostname: "stale.local",
		IPAddress: "10.0.0.1", Status: "online",
		LastHeartbeat: time.Now().Unix() - 120,
	}
	_ = serverStore.Create(srv)

	rule := &store.AlertRule{
		ID: "hb-stale", Name: "Heartbeat Stale", Enabled: true,
		MetricName: "agent.heartbeat", Condition: "stall", Threshold: 60,
		DurationSec: 0, Severity: "critical", NodeFilter: "*", CooldownMin: 5,
	}
	_ = alertStore.CreateRule(rule)

	notifier := notify.NewManager()
	eval := NewEvaluator(alertStore, metricsStore, nodeStore, serverStore, notifier)
	eval.evaluate()

	active, _ := alertStore.ListActiveAlerts()
	if len(active) != 1 {
		t.Fatalf("expected 1 active alert for stale heartbeat, got %d", len(active))
	}
	if active[0].Severity != "critical" {
		t.Errorf("alert severity = %q, want critical", active[0].Severity)
	}
}

func TestDefaultRules(t *testing.T) {
	rules := DefaultRules()
	if len(rules) < 5 {
		t.Errorf("expected at least 5 default rules, got %d", len(rules))
	}

	ids := map[string]bool{}
	for _, r := range rules {
		if ids[r.ID] {
			t.Errorf("duplicate default rule ID: %s", r.ID)
		}
		ids[r.ID] = true

		if r.Name == "" || r.MetricName == "" || r.Condition == "" {
			t.Errorf("incomplete default rule: %+v", r)
		}
		if !r.Builtin {
			t.Errorf("default rule %s should have Builtin=true", r.ID)
		}
	}
}

func TestEvaluatorStartStop(t *testing.T) {
	alertStore, metricsStore, nodeStore, serverStore, cleanup := setupTestDB(t)
	defer cleanup()

	notifier := notify.NewManager()
	eval := NewEvaluator(alertStore, metricsStore, nodeStore, serverStore, notifier)

	eval.Start()
	time.Sleep(100 * time.Millisecond)
	eval.Stop()
}
