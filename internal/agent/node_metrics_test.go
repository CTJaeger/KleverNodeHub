package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/CTJaeger/KleverNodeHub/internal/models"
)

// sampleNodeStatusJSON is a realistic /node/status response from a Klever node.
const sampleNodeStatusJSON = `{
  "data": {
    "metrics": {
      "klv_app_version": "v1.7.15-0-gc1de9106/go1.25.3/linux-amd64/adc9ec9168",
      "klv_are_vm_queries_ready": "true",
      "klv_average_block_tx_count": "1",
      "klv_average_tps": "1",
      "klv_body_blocks_size": 40,
      "klv_chain_id": "108",
      "klv_connected_nodes": 224,
      "klv_consensus_group_size": 21,
      "klv_consensus_processed_proposed_block": 0,
      "klv_consensus_received_proposed_block": 1,
      "klv_consensus_slot_state": "signed",
      "klv_consensus_state": "not in consensus group",
      "klv_count_accepted_blocks": 0,
      "klv_count_consensus": 0,
      "klv_count_consensus_accepted_blocks": 0,
      "klv_count_leader": 0,
      "klv_cpu_load_percent": 1,
      "klv_current_block_hash": "3e2094edd2981dbcc7f74b0773e81257cf1bdcacfb3b35a2227d5702a584122c",
      "klv_current_block_tx_count": 0,
      "klv_current_header_block_size": 335,
      "klv_current_slot": 29146851,
      "klv_current_slot_timestamp": 1773267804,
      "klv_dev_rewards": "0",
      "klv_epoch_for_economics_data": 0,
      "klv_epoch_number": 5397,
      "klv_highest_final_nonce": 29091834,
      "klv_inflation": "0",
      "klv_is_syncing": 0,
      "klv_latest_tag_software_version": "v1.7.15",
      "klv_live_validator_nodes": 116,
      "klv_mem_heap_inuse": 1279819776,
      "klv_mem_load_percent": 15,
      "klv_mem_stack_inuse": 14712832,
      "klv_mem_total": 16372256768,
      "klv_mem_used_golang": 2461405184,
      "klv_mem_used_sys": 2310709528,
      "klv_min_transaction_version": 1,
      "klv_network_recv_bps": 256088,
      "klv_network_recv_bps_peak": 1120893,
      "klv_network_recv_bytes_in_epoch_per_host": 1366652730,
      "klv_network_recv_percent": 22,
      "klv_network_sent_bps": 240183,
      "klv_network_sent_bps_peak": 1610049,
      "klv_network_sent_bytes_in_epoch_per_host": 1360626209,
      "klv_network_sent_percent": 14,
      "klv_node_display_name": "klever-intense-hog",
      "klv_node_type": "observer",
      "klv_nonce": 29091835,
      "klv_nonce_at_epoch_start": 29088784,
      "klv_nonce_for_tps": 29091834,
      "klv_nonces_passed_in_current_epoch": 0,
      "klv_num_connected_peers": 221,
      "klv_num_nodes": 21,
      "klv_num_shard_headers_processed": 0,
      "klv_num_transactions_processed": 706359,
      "klv_num_transactions_processed_tps_benchmark": 57922962,
      "klv_num_tx_block": 1,
      "klv_num_validators": 21,
      "klv_peak_tps": 3000,
      "klv_peer_type": "observer",
      "klv_probable_highest_nonce": 29091834,
      "klv_public_key_block_sign": "70094d2e93791772e8795737defe2fa89dbb09e0e30ce84dac3ff44a70a237540",
      "klv_redundancy_level": 0,
      "klv_slot_at_epoch_start": 29143800,
      "klv_slot_duration": 4000,
      "klv_slot_time": 4,
      "klv_slots_passed_in_current_epoch": 0,
      "klv_slots_per_epoch": 5400,
      "klv_start_time": 1656680400,
      "klv_synchronized_slot": 29146851,
      "klv_total_fees": "0",
      "klv_total_supply": "0",
      "klv_tx_pool_load": 1,
      "klv_txs_blocks_size": 382
    }
  },
  "error": "",
  "code": "successful"
}`

func newMockNodeServer(t *testing.T, response string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/node/status" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(response))
	}))
}

func parseHostPort(t *testing.T, url string) (string, int) {
	t.Helper()
	// url looks like http://127.0.0.1:12345
	addr := strings.TrimPrefix(url, "http://")
	parts := strings.Split(addr, ":")
	port, err := strconv.Atoi(parts[1])
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}
	return parts[0], port
}

func TestNewNodeMetricsCollector(t *testing.T) {
	c := NewNodeMetricsCollector()
	if c == nil {
		t.Fatal("expected non-nil collector")
	}
	if c.pollInterval != defaultNodePollInterval {
		t.Errorf("pollInterval = %v, want %v", c.pollInterval, defaultNodePollInterval)
	}
	if c.stallTimeout != defaultNonceStallTimeout {
		t.Errorf("stallTimeout = %v, want %v", c.stallTimeout, defaultNonceStallTimeout)
	}
}

func TestNewNodeMetricsCollector_Options(t *testing.T) {
	c := NewNodeMetricsCollector(
		WithPollInterval(5*time.Second),
		WithStallTimeout(30*time.Second),
	)
	if c.pollInterval != 5*time.Second {
		t.Errorf("pollInterval = %v, want 5s", c.pollInterval)
	}
	if c.stallTimeout != 30*time.Second {
		t.Errorf("stallTimeout = %v, want 30s", c.stallTimeout)
	}
}

func TestUpdateNodes(t *testing.T) {
	c := NewNodeMetricsCollector()

	report := &models.DiscoveryReport{
		Nodes: []models.DiscoveredNode{
			{ContainerID: "abc123", RestAPIPort: 8080},
			{ContainerID: "def456", RestAPIPort: 8081},
			{ContainerID: "ghi789", RestAPIPort: 0}, // should be skipped
		},
	}
	c.UpdateNodes(report)

	if len(c.nodes) != 2 {
		t.Errorf("nodes = %d, want 2", len(c.nodes))
	}

	// Update with fewer nodes
	report2 := &models.DiscoveryReport{
		Nodes: []models.DiscoveredNode{
			{ContainerID: "abc123", RestAPIPort: 8080},
		},
	}
	c.UpdateNodes(report2)
	if len(c.nodes) != 1 {
		t.Errorf("nodes after update = %d, want 1", len(c.nodes))
	}
}

func TestCollectOne_Success(t *testing.T) {
	srv := newMockNodeServer(t, sampleNodeStatusJSON)
	defer srv.Close()

	host, port := parseHostPort(t, srv.URL)

	c := NewNodeMetricsCollector(WithHTTPClient(srv.Client()))
	c.mu.Lock()
	c.nodes["test-node"] = nodeEndpoint{host: host, port: port}
	c.mu.Unlock()

	evt, err := c.CollectOne("test-node", "server-1")
	if err != nil {
		t.Fatalf("CollectOne: %v", err)
	}

	if evt.NodeID != "test-node" {
		t.Errorf("NodeID = %q, want %q", evt.NodeID, "test-node")
	}
	if evt.ServerID != "server-1" {
		t.Errorf("ServerID = %q, want %q", evt.ServerID, "server-1")
	}
	if evt.CollectedAt == 0 {
		t.Error("CollectedAt should be set")
	}

	// Check critical metrics are present
	criticalKeys := []string{
		"klv_nonce", "klv_epoch_number", "klv_is_syncing",
		"klv_connected_nodes", "klv_consensus_state",
		"klv_cpu_load_percent", "klv_mem_load_percent",
		"klv_network_recv_bps", "klv_network_sent_bps",
		"klv_node_type", "klv_chain_id",
		"klv_num_transactions_processed", "klv_tx_pool_load",
		"klv_live_validator_nodes", "klv_num_validators",
	}
	for _, key := range criticalKeys {
		if _, ok := evt.Metrics[key]; !ok {
			t.Errorf("missing metric: %s", key)
		}
	}

	// Verify total metric count matches sample (76 fields)
	if len(evt.Metrics) < 70 {
		t.Errorf("metrics count = %d, want >= 70", len(evt.Metrics))
	}
}

func TestCollectOne_UnknownNode(t *testing.T) {
	c := NewNodeMetricsCollector()
	_, err := c.CollectOne("nonexistent", "server-1")
	if err == nil {
		t.Fatal("expected error for unknown node")
	}
}

func TestCollectOne_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	host, port := parseHostPort(t, srv.URL)

	c := NewNodeMetricsCollector(WithHTTPClient(srv.Client()))
	c.mu.Lock()
	c.nodes["test-node"] = nodeEndpoint{host: host, port: port}
	c.mu.Unlock()

	_, err := c.CollectOne("test-node", "server-1")
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
}

func TestCollectOne_NodeError(t *testing.T) {
	errorResp := `{"data":{"metrics":{}},"error":"node is starting","code":"error"}`
	srv := newMockNodeServer(t, errorResp)
	defer srv.Close()

	host, port := parseHostPort(t, srv.URL)

	c := NewNodeMetricsCollector(WithHTTPClient(srv.Client()))
	c.mu.Lock()
	c.nodes["test-node"] = nodeEndpoint{host: host, port: port}
	c.mu.Unlock()

	_, err := c.CollectOne("test-node", "server-1")
	if err == nil {
		t.Fatal("expected error for error code response")
	}
}

func TestCollectAll_MultipleNodes(t *testing.T) {
	srv := newMockNodeServer(t, sampleNodeStatusJSON)
	defer srv.Close()

	host, port := parseHostPort(t, srv.URL)

	c := NewNodeMetricsCollector(WithHTTPClient(srv.Client()))
	c.UpdateNodes(&models.DiscoveryReport{
		Nodes: []models.DiscoveredNode{
			{ContainerID: "node-a", RestAPIPort: port},
			{ContainerID: "node-b", RestAPIPort: port},
		},
	})
	// Override host (httptest uses 127.0.0.1)
	c.mu.Lock()
	c.nodes["node-a"] = nodeEndpoint{host: host, port: port}
	c.nodes["node-b"] = nodeEndpoint{host: host, port: port}
	c.mu.Unlock()

	events, stalls := c.CollectAll("server-1")
	if len(events) != 2 {
		t.Errorf("events = %d, want 2", len(events))
	}
	// First poll — no stall yet
	if len(stalls) != 0 {
		t.Errorf("stalls = %d, want 0", len(stalls))
	}
}

func TestCollectAll_UnreachableNode(t *testing.T) {
	c := NewNodeMetricsCollector()
	c.mu.Lock()
	c.nodes["dead-node"] = nodeEndpoint{host: "127.0.0.1", port: 1} // unreachable port
	c.mu.Unlock()

	events, _ := c.CollectAll("server-1")
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	if events[0].Error == "" {
		t.Error("expected error in event for unreachable node")
	}
}

func TestNonceStallDetection(t *testing.T) {
	// Use a very short stall timeout for testing
	c := NewNodeMetricsCollector(WithStallTimeout(1 * time.Millisecond))

	nodeID := "stall-node"
	serverID := "server-1"

	// First poll — nonce first seen, no stall
	evt1 := &models.NodeMetricsEvent{
		NodeID:   nodeID,
		ServerID: serverID,
		Metrics:  map[string]any{"klv_nonce": float64(100)},
	}
	stall := c.checkNonceStall(nodeID, serverID, evt1)
	if stall != nil {
		t.Fatal("unexpected stall on first poll")
	}

	// Wait for stall timeout
	time.Sleep(5 * time.Millisecond)

	// Second poll — same nonce, should trigger stall
	evt2 := &models.NodeMetricsEvent{
		NodeID:   nodeID,
		ServerID: serverID,
		Metrics:  map[string]any{"klv_nonce": float64(100)},
	}
	stall = c.checkNonceStall(nodeID, serverID, evt2)
	if stall == nil {
		t.Fatal("expected stall event")
	}
	if stall.StuckNonce != 100 {
		t.Errorf("StuckNonce = %d, want 100", stall.StuckNonce)
	}
	if stall.NodeID != nodeID {
		t.Errorf("NodeID = %q, want %q", stall.NodeID, nodeID)
	}

	// Third poll — same nonce, but already alerted, no duplicate
	evt3 := &models.NodeMetricsEvent{
		NodeID:   nodeID,
		ServerID: serverID,
		Metrics:  map[string]any{"klv_nonce": float64(100)},
	}
	stall = c.checkNonceStall(nodeID, serverID, evt3)
	if stall != nil {
		t.Fatal("should not re-alert for same stall")
	}

	// Fourth poll — nonce changed, stall cleared
	evt4 := &models.NodeMetricsEvent{
		NodeID:   nodeID,
		ServerID: serverID,
		Metrics:  map[string]any{"klv_nonce": float64(101)},
	}
	stall = c.checkNonceStall(nodeID, serverID, evt4)
	if stall != nil {
		t.Fatal("should not alert when nonce advances")
	}
}

func TestNonceStallDetection_NoNonceField(t *testing.T) {
	c := NewNodeMetricsCollector()
	evt := &models.NodeMetricsEvent{
		NodeID:   "node-1",
		ServerID: "server-1",
		Metrics:  map[string]any{"klv_epoch_number": float64(5397)},
	}
	stall := c.checkNonceStall("node-1", "server-1", evt)
	if stall != nil {
		t.Fatal("should not stall when nonce field is absent")
	}
}

func TestMetricsValueTypes(t *testing.T) {
	srv := newMockNodeServer(t, sampleNodeStatusJSON)
	defer srv.Close()

	host, port := parseHostPort(t, srv.URL)

	c := NewNodeMetricsCollector(WithHTTPClient(srv.Client()))
	c.mu.Lock()
	c.nodes["test-node"] = nodeEndpoint{host: host, port: port}
	c.mu.Unlock()

	evt, err := c.CollectOne("test-node", "server-1")
	if err != nil {
		t.Fatalf("CollectOne: %v", err)
	}

	// String metrics
	if v, ok := evt.Metrics["klv_app_version"].(string); !ok || v == "" {
		t.Errorf("klv_app_version should be non-empty string, got %v", evt.Metrics["klv_app_version"])
	}
	if v, ok := evt.Metrics["klv_consensus_state"].(string); !ok || v == "" {
		t.Errorf("klv_consensus_state should be non-empty string, got %v", evt.Metrics["klv_consensus_state"])
	}

	// Numeric metrics (JSON numbers are float64)
	if v, ok := evt.Metrics["klv_nonce"].(float64); !ok || v == 0 {
		t.Errorf("klv_nonce should be non-zero number, got %v", evt.Metrics["klv_nonce"])
	}
	if v, ok := evt.Metrics["klv_epoch_number"].(float64); !ok || v == 0 {
		t.Errorf("klv_epoch_number should be non-zero number, got %v", evt.Metrics["klv_epoch_number"])
	}
}

func TestMetricsEventSerialization(t *testing.T) {
	evt := &models.NodeMetricsEvent{
		NodeID:   "node-1",
		ServerID: "server-1",
		Metrics: map[string]any{
			"klv_nonce":         float64(29091835),
			"klv_consensus_state": "not in consensus group",
			"klv_is_syncing":    float64(0),
		},
		CollectedAt: 1773267804,
	}

	data, err := json.Marshal(evt)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded models.NodeMetricsEvent
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.NodeID != "node-1" {
		t.Errorf("NodeID = %q, want %q", decoded.NodeID, "node-1")
	}
	if len(decoded.Metrics) != 3 {
		t.Errorf("metrics count = %d, want 3", len(decoded.Metrics))
	}
}

func TestRunPoller_SendsMetrics(t *testing.T) {
	srv := newMockNodeServer(t, sampleNodeStatusJSON)
	defer srv.Close()

	host, port := parseHostPort(t, srv.URL)

	c := NewNodeMetricsCollector(
		WithPollInterval(10*time.Millisecond),
		WithHTTPClient(srv.Client()),
	)
	c.mu.Lock()
	c.nodes["test-node"] = nodeEndpoint{host: host, port: port}
	c.mu.Unlock()

	metricsCh := make(chan *models.Message, 10)
	stallCh := make(chan *models.Message, 10)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	go c.RunPoller(ctx, "server-1", metricsCh, stallCh)

	// Wait for at least one metric message
	select {
	case msg := <-metricsCh:
		if msg.Action != "node.metrics" {
			t.Errorf("action = %q, want %q", msg.Action, "node.metrics")
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for metrics message")
	}
}

func TestPollInterval(t *testing.T) {
	c := NewNodeMetricsCollector(WithPollInterval(42 * time.Second))
	if c.PollInterval() != 42*time.Second {
		t.Errorf("PollInterval = %v, want 42s", c.PollInterval())
	}
}
