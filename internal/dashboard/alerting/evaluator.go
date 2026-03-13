package alerting

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/CTJaeger/KleverNodeHub/internal/models"
	"github.com/CTJaeger/KleverNodeHub/internal/notify"
	"github.com/CTJaeger/KleverNodeHub/internal/store"
)

// AlertState tracks the in-memory state of a pending alert.
// Key: "ruleID:nodeID" or "ruleID:server:serverID"
type AlertState struct {
	RuleID      string
	NodeID      string
	ServerID    string
	FirstSeen   time.Time
	LastSeen    time.Time
	LastValue   float64
	State       string // "normal", "pending", "firing"
	NotifiedAt  time.Time
	AlertRecord *store.AlertRecord
}

// systemMetrics are metrics evaluated per-server, not per-node.
var systemMetrics = map[string]bool{
	"cpu_percent":  true,
	"mem_percent":  true,
	"disk_percent": true,
}

// Evaluator evaluates alert rules against current metrics.
type Evaluator struct {
	mu           sync.Mutex
	alertStore   *store.AlertStore
	metricsStore *store.MetricsStore
	nodeStore    *store.NodeStore
	serverStore  *store.ServerStore
	notifier     *notify.Manager
	states       map[string]*AlertState
	cancel       context.CancelFunc
	interval     time.Duration
	idCounter    int64
}

// NewEvaluator creates a new alert evaluator.
func NewEvaluator(
	alertStore *store.AlertStore,
	metricsStore *store.MetricsStore,
	nodeStore *store.NodeStore,
	serverStore *store.ServerStore,
	notifier *notify.Manager,
) *Evaluator {
	return &Evaluator{
		alertStore:   alertStore,
		metricsStore: metricsStore,
		nodeStore:    nodeStore,
		serverStore:  serverStore,
		notifier:     notifier,
		states:       make(map[string]*AlertState),
		interval:     15 * time.Second,
	}
}

// Start launches the evaluation loop.
func (e *Evaluator) Start() {
	e.hydrateFromDB()

	ctx, cancel := context.WithCancel(context.Background())
	e.cancel = cancel

	go e.run(ctx)
	log.Printf("alert evaluator started (interval=%s)", e.interval)
}

// hydrateFromDB loads active alerts from the database into in-memory state
// so the evaluator can track and resolve them without creating duplicates.
func (e *Evaluator) hydrateFromDB() {
	alerts, err := e.alertStore.ListActiveAlerts()
	if err != nil {
		log.Printf("alert evaluator: hydrate from DB: %v", err)
		return
	}

	for i := range alerts {
		a := &alerts[i]
		var stateKey string
		if a.NodeID != "" {
			stateKey = fmt.Sprintf("%s:%s", a.RuleID, a.NodeID)
		} else if a.ServerID != "" {
			stateKey = fmt.Sprintf("%s:server:%s", a.RuleID, a.ServerID)
		} else {
			continue
		}

		record := *a // copy
		e.states[stateKey] = &AlertState{
			RuleID:      a.RuleID,
			NodeID:      a.NodeID,
			ServerID:    a.ServerID,
			FirstSeen:   time.Unix(a.FiredAt, 0),
			LastSeen:    time.Unix(a.CreatedAt, 0),
			State:       "firing",
			NotifiedAt:  time.Unix(a.NotifiedAt, 0),
			AlertRecord: &record,
		}
	}

	if len(alerts) > 0 {
		log.Printf("alert evaluator: hydrated %d active alerts from DB", len(alerts))
	}
}

// Stop halts the evaluation loop.
func (e *Evaluator) Stop() {
	if e.cancel != nil {
		e.cancel()
		log.Println("alert evaluator stopped")
	}
}

// EnsureDefaults creates default rules if none exist.
func (e *Evaluator) EnsureDefaults() {
	e.migrateRules()

	count, err := e.alertStore.RuleCount()
	if err != nil {
		log.Printf("alert evaluator: check rule count: %v", err)
		return
	}
	if count > 0 {
		return
	}

	for _, rule := range DefaultRules() {
		if err := e.alertStore.CreateRule(&rule); err != nil {
			log.Printf("alert evaluator: create default rule %q: %v", rule.Name, err)
		}
	}
	log.Printf("alert evaluator: created %d default rules", len(DefaultRules()))
}

// migrateRules renames legacy rules and ensures new builtins exist.
func (e *Evaluator) migrateRules() {
	// Migrate old "Node Offline" (agent.heartbeat) → "Agent Offline"
	old, err := e.alertStore.GetRule("builtin-node-offline")
	if err == nil && old.MetricName == "agent.heartbeat" {
		old.ID = "builtin-agent-offline"
		old.Name = "Agent Offline"
		if err := e.alertStore.CreateRule(old); err != nil {
			log.Printf("alert evaluator: migrate rule: %v", err)
		} else {
			_ = e.alertStore.DeleteRule("builtin-node-offline")
			log.Println("alert evaluator: migrated 'Node Offline' → 'Agent Offline'")
		}
	}

	// Ensure any missing builtin rules are created
	for _, rule := range DefaultRules() {
		if !rule.Builtin {
			continue
		}
		if _, err := e.alertStore.GetRule(rule.ID); err != nil {
			if err := e.alertStore.CreateRule(&rule); err != nil {
				log.Printf("alert evaluator: create missing builtin %q: %v", rule.Name, err)
			} else {
				log.Printf("alert evaluator: created missing builtin rule %q", rule.Name)
			}
		}
	}
}

func (e *Evaluator) run(ctx context.Context) {
	// Initial evaluation after short delay
	select {
	case <-ctx.Done():
		return
	case <-time.After(5 * time.Second):
	}

	e.evaluate()

	ticker := time.NewTicker(e.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.evaluate()
		}
	}
}

func (e *Evaluator) evaluate() {
	rules, err := e.alertStore.ListEnabledRules()
	if err != nil {
		log.Printf("alert evaluator: list rules: %v", err)
		return
	}

	if len(rules) == 0 {
		return
	}

	now := time.Now()
	lookback := now.Add(-2 * time.Minute).Unix()
	nowUnix := now.Unix()

	// Fetch nodes and servers
	nodes, err := e.nodeStore.ListAll("")
	if err != nil {
		log.Printf("alert evaluator: list nodes: %v", err)
		return
	}

	servers, err := e.serverStore.List()
	if err != nil {
		log.Printf("alert evaluator: list servers: %v", err)
		return
	}

	for i := range rules {
		rule := &rules[i]

		if systemMetrics[rule.MetricName] {
			e.evaluateSystemRule(rule, servers, lookback, nowUnix, now)
		} else if rule.MetricName == "agent.heartbeat" {
			e.evaluateHeartbeatRule(rule, servers, now)
		} else if rule.MetricName == "node.status" {
			e.evaluateNodeStatusRule(rule, nodes, now)
		} else {
			e.evaluateNodeRule(rule, nodes, lookback, nowUnix, now)
		}
	}

	// Resolve stale alerts (states that haven't been seen recently)
	e.resolveStaleAlerts(now)
}

func (e *Evaluator) evaluateNodeRule(rule *store.AlertRule, nodes []models.Node, from, to int64, now time.Time) {
	for i := range nodes {
		node := &nodes[i]

		if rule.NodeFilter != "*" && rule.NodeFilter != node.ID {
			continue
		}

		stateKey := fmt.Sprintf("%s:%s", rule.ID, node.ID)

		if rule.Condition == "stall" {
			e.evaluateStall(rule, stateKey, node.ID, node.ServerID, node.ContainerName, from, to, now)
		} else {
			e.evaluateThreshold(rule, stateKey, node.ID, node.ServerID, node.ContainerName, from, to, now)
		}
	}
}

func (e *Evaluator) evaluateSystemRule(rule *store.AlertRule, servers []models.Server, from, to int64, now time.Time) {
	for i := range servers {
		srv := &servers[i]
		stateKey := fmt.Sprintf("%s:server:%s", rule.ID, srv.ID)

		metrics, err := e.metricsStore.QuerySystemMetrics(srv.ID, from, to)
		if err != nil || len(metrics) == 0 {
			e.markNormal(stateKey, now)
			continue
		}

		latest := metrics[len(metrics)-1]
		var value float64
		switch rule.MetricName {
		case "cpu_percent":
			value = latest.CPUPercent
		case "mem_percent":
			value = latest.MemPercent
		case "disk_percent":
			value = latest.DiskPercent
		default:
			continue
		}

		breached := checkCondition(rule.Condition, value, rule.Threshold)
		source := fmt.Sprintf("server:%s (%s)", srv.Name, srv.Hostname)
		e.processResult(rule, stateKey, "", srv.ID, source, value, breached, now)
	}
}

func (e *Evaluator) evaluateHeartbeatRule(rule *store.AlertRule, servers []models.Server, now time.Time) {
	for i := range servers {
		srv := &servers[i]

		// Skip servers that have never sent a heartbeat
		if srv.LastHeartbeat == 0 {
			continue
		}

		stateKey := fmt.Sprintf("%s:server:%s", rule.ID, srv.ID)

		staleSec := float64(now.Unix() - srv.LastHeartbeat)
		breached := staleSec > rule.Threshold
		source := fmt.Sprintf("server:%s (%s)", srv.Name, srv.Hostname)
		e.processResult(rule, stateKey, "", srv.ID, source, staleSec, breached, now)
	}
}

func (e *Evaluator) evaluateNodeStatusRule(rule *store.AlertRule, nodes []models.Node, now time.Time) {
	for i := range nodes {
		node := &nodes[i]

		if rule.NodeFilter != "*" && rule.NodeFilter != node.ID {
			continue
		}

		stateKey := fmt.Sprintf("%s:%s", rule.ID, node.ID)

		// Node is "offline" if status is not "running"
		breached := node.Status != "running" && node.Status != ""
		source := fmt.Sprintf("node:%s", node.ContainerName)
		var value float64
		if breached {
			value = 1
		}
		e.processResult(rule, stateKey, node.ID, node.ServerID, source, value, breached, now)
	}
}

func (e *Evaluator) evaluateThreshold(rule *store.AlertRule, stateKey, nodeID, serverID, nodeName string, from, to int64, now time.Time) {
	points, err := e.metricsStore.QueryRecent(nodeID, rule.MetricName, from, to)
	if err != nil || len(points) == 0 {
		e.markNormal(stateKey, now)
		return
	}

	latest := points[len(points)-1]
	breached := checkCondition(rule.Condition, latest.Value, rule.Threshold)
	source := fmt.Sprintf("node:%s", nodeName)
	e.processResult(rule, stateKey, nodeID, serverID, source, latest.Value, breached, now)
}

func (e *Evaluator) evaluateStall(rule *store.AlertRule, stateKey, nodeID, serverID, nodeName string, from, to int64, now time.Time) {
	points, err := e.metricsStore.QueryRecent(nodeID, rule.MetricName, from, to)
	source := fmt.Sprintf("node:%s", nodeName)

	// No data at all — if threshold > 0, treat as stalled (no metrics arriving)
	if err != nil || len(points) == 0 {
		if rule.Threshold > 0 {
			e.processResult(rule, stateKey, nodeID, serverID, source, 0, true, now)
		} else {
			e.markNormal(stateKey, now)
		}
		return
	}

	if len(points) < 2 {
		e.markNormal(stateKey, now)
		return
	}

	// Find the last time the value changed
	lastValue := points[len(points)-1].Value
	lastChangeAt := points[len(points)-1].CollectedAt
	for i := len(points) - 2; i >= 0; i-- {
		if points[i].Value != lastValue {
			break
		}
		lastChangeAt = points[i].CollectedAt
	}

	// Use threshold as seconds of stall required
	stalledSec := float64(to - lastChangeAt)
	stalled := stalledSec >= rule.Threshold

	e.processResult(rule, stateKey, nodeID, serverID, source, lastValue, stalled, now)
}

func (e *Evaluator) processResult(rule *store.AlertRule, stateKey, nodeID, serverID, source string, value float64, breached bool, now time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()

	state, exists := e.states[stateKey]

	if !breached {
		if exists && (state.State == "pending" || state.State == "firing") {
			e.resolveAlert(state, source, now)
		}
		delete(e.states, stateKey)
		return
	}

	if !exists {
		state = &AlertState{
			RuleID:    rule.ID,
			NodeID:    nodeID,
			ServerID:  serverID,
			FirstSeen: now,
			State:     "pending",
		}
		e.states[stateKey] = state
	}

	state.LastSeen = now
	state.LastValue = value

	switch state.State {
	case "pending":
		elapsed := now.Sub(state.FirstSeen)
		if elapsed >= time.Duration(rule.DurationSec)*time.Second {
			state.State = "firing"
			e.fireAlert(rule, state, source, value, now)
		}

	case "firing":
		// Check cooldown for re-notification
		if !state.NotifiedAt.IsZero() {
			cooldown := time.Duration(rule.CooldownMin) * time.Minute
			if now.Sub(state.NotifiedAt) >= cooldown {
				e.sendNotification(rule, state, source, value, false)
				state.NotifiedAt = now
				if state.AlertRecord != nil {
					state.AlertRecord.NotifiedAt = now.Unix()
					_ = e.alertStore.UpdateAlert(state.AlertRecord)
				}
			}
		}
	}
}

func (e *Evaluator) fireAlert(rule *store.AlertRule, state *AlertState, source string, value float64, now time.Time) {
	// Dedup: check if an active alert already exists for this rule+node/server
	var existing *store.AlertRecord
	if state.NodeID != "" {
		existing, _ = e.alertStore.GetActiveAlertByRuleAndNode(rule.ID, state.NodeID)
	} else if state.ServerID != "" {
		existing, _ = e.alertStore.GetActiveAlertByRuleAndServer(rule.ID, state.ServerID)
	}
	if existing != nil {
		// Reattach existing alert instead of creating a duplicate
		state.AlertRecord = existing
		state.NotifiedAt = time.Unix(existing.NotifiedAt, 0)
		return
	}

	alertID := fmt.Sprintf("alert-%d-%d", now.UnixNano(), e.idCounter)
	e.idCounter++

	msg := formatAlertMessage(rule, source, value)

	record := &store.AlertRecord{
		ID:         alertID,
		RuleID:     rule.ID,
		RuleName:   rule.Name,
		NodeID:     state.NodeID,
		ServerID:   state.ServerID,
		Severity:   rule.Severity,
		State:      "firing",
		Message:    msg,
		FiredAt:    now.Unix(),
		NotifiedAt: now.Unix(),
		CreatedAt:  now.Unix(),
	}

	if err := e.alertStore.CreateAlert(record); err != nil {
		log.Printf("alert evaluator: create alert: %v", err)
	}

	state.AlertRecord = record
	state.NotifiedAt = now

	e.sendNotification(rule, state, source, value, false)
}

func (e *Evaluator) resolveAlert(state *AlertState, source string, now time.Time) {
	state.State = "resolved"

	if state.AlertRecord != nil {
		state.AlertRecord.State = "resolved"
		state.AlertRecord.ResolvedAt = now.Unix()
		if err := e.alertStore.UpdateAlert(state.AlertRecord); err != nil {
			log.Printf("alert evaluator: resolve alert: %v", err)
		}

		// Send recovery notification
		e.notifier.Send(&notify.Alert{
			Title:     fmt.Sprintf("Resolved: %s", state.AlertRecord.RuleName),
			Message:   fmt.Sprintf("%s has recovered", source),
			Severity:  notify.SeverityInfo,
			Source:    source,
			AlertType: "resolved",
			Time:      now.Unix(),
		})
	}
}

func (e *Evaluator) markNormal(stateKey string, now time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()

	state, exists := e.states[stateKey]
	if exists && (state.State == "pending" || state.State == "firing") {
		source := ""
		if state.NodeID != "" {
			source = fmt.Sprintf("node:%s", state.NodeID)
		} else {
			source = fmt.Sprintf("server:%s", state.ServerID)
		}
		e.resolveAlert(state, source, now)
	}
	delete(e.states, stateKey)
}

func (e *Evaluator) resolveStaleAlerts(now time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()

	staleThreshold := now.Add(-5 * time.Minute)
	for key, state := range e.states {
		if state.LastSeen.Before(staleThreshold) && state.State != "resolved" {
			// Not seen for 5 minutes, consider resolved
			if state.AlertRecord != nil {
				state.AlertRecord.State = "resolved"
				state.AlertRecord.ResolvedAt = now.Unix()
				_ = e.alertStore.UpdateAlert(state.AlertRecord)
			}
			delete(e.states, key)
		}
	}
}

func (e *Evaluator) sendNotification(rule *store.AlertRule, state *AlertState, source string, value float64, _ bool) {
	msg := formatAlertMessage(rule, source, value)
	e.notifier.Send(&notify.Alert{
		Title:     fmt.Sprintf("%s: %s", rule.Severity, rule.Name),
		Message:   msg,
		Severity:  rule.Severity,
		Source:    source,
		AlertType: alertTypeFromRule(rule),
		Time:      time.Now().Unix(),
	})
}

// alertTypeFromRule derives the alert type category from a rule's metric name.
func alertTypeFromRule(rule *store.AlertRule) string {
	switch rule.MetricName {
	case "agent.heartbeat":
		return "node_down"
	case "klv_nonce":
		return "nonce_stall"
	case "cpu_percent", "mem_percent", "disk_percent":
		return "resource"
	default:
		return "metric"
	}
}

func formatAlertMessage(rule *store.AlertRule, source string, value float64) string {
	switch rule.Condition {
	case "gt":
		return fmt.Sprintf("%s: %s is %.1f (threshold: >%.1f)", source, rule.MetricName, value, rule.Threshold)
	case "lt":
		return fmt.Sprintf("%s: %s is %.1f (threshold: <%.1f)", source, rule.MetricName, value, rule.Threshold)
	case "eq":
		return fmt.Sprintf("%s: %s equals %.0f", source, rule.MetricName, value)
	case "stall":
		return fmt.Sprintf("%s: %s stalled at %.0f", source, rule.MetricName, value)
	default:
		return fmt.Sprintf("%s: %s = %.1f", source, rule.MetricName, value)
	}
}

func checkCondition(condition string, value, threshold float64) bool {
	switch condition {
	case "gt":
		return value > threshold
	case "lt":
		return value < threshold
	case "eq":
		return value == threshold
	default:
		return false
	}
}
