package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/CTJaeger/KleverNodeHub/internal/models"
)

const (
	defaultNodePollInterval  = 15 * time.Second
	defaultNodePollTimeout   = 5 * time.Second
	defaultNonceStallTimeout = 15 * time.Second
)

// NodeMetricsCollector polls /node/status for each discovered Klever node.
type NodeMetricsCollector struct {
	httpClient   *http.Client
	pollInterval time.Duration
	stallTimeout time.Duration

	mu         sync.RWMutex
	nodes      map[string]nodeEndpoint // nodeID → endpoint
	lastNonces map[string]nonceState   // nodeID → last seen nonce + time
}

type nodeEndpoint struct {
	host string
	port int
}

type nonceState struct {
	nonce   uint64
	firstAt time.Time // when this nonce was first seen
	alerted bool      // whether we already sent a stall alert
}

// nodeStatusResponse is the raw JSON from /node/status.
type nodeStatusResponse struct {
	Data struct {
		Metrics map[string]json.RawMessage `json:"metrics"`
	} `json:"data"`
	Error string `json:"error"`
	Code  string `json:"code"`
}

// NodeMetricsCollectorOption configures the NodeMetricsCollector.
type NodeMetricsCollectorOption func(*NodeMetricsCollector)

// WithPollInterval sets the polling interval.
func WithPollInterval(d time.Duration) NodeMetricsCollectorOption {
	return func(c *NodeMetricsCollector) { c.pollInterval = d }
}

// WithStallTimeout sets the nonce stall detection timeout.
func WithStallTimeout(d time.Duration) NodeMetricsCollectorOption {
	return func(c *NodeMetricsCollector) { c.stallTimeout = d }
}

// WithHTTPClient sets a custom HTTP client (useful for testing).
func WithHTTPClient(client *http.Client) NodeMetricsCollectorOption {
	return func(c *NodeMetricsCollector) { c.httpClient = client }
}

// NewNodeMetricsCollector creates a new collector for Klever node metrics.
func NewNodeMetricsCollector(opts ...NodeMetricsCollectorOption) *NodeMetricsCollector {
	c := &NodeMetricsCollector{
		httpClient: &http.Client{
			Timeout: defaultNodePollTimeout,
		},
		pollInterval: defaultNodePollInterval,
		stallTimeout: defaultNonceStallTimeout,
		nodes:        make(map[string]nodeEndpoint),
		lastNonces:   make(map[string]nonceState),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// UpdateNodes updates the set of nodes to poll from a discovery report.
func (c *NodeMetricsCollector) UpdateNodes(report *models.DiscoveryReport) {
	c.mu.Lock()
	defer c.mu.Unlock()

	current := make(map[string]bool)
	for _, n := range report.Nodes {
		if n.RestAPIPort <= 0 {
			continue
		}
		nodeID := n.ContainerID
		current[nodeID] = true
		c.nodes[nodeID] = nodeEndpoint{host: "127.0.0.1", port: n.RestAPIPort}
	}

	// Remove nodes that are no longer present
	for id := range c.nodes {
		if !current[id] {
			delete(c.nodes, id)
			delete(c.lastNonces, id)
		}
	}
}

// CollectOne polls a single node and returns its metrics event.
func (c *NodeMetricsCollector) CollectOne(nodeID, serverID string) (*models.NodeMetricsEvent, error) {
	c.mu.RLock()
	ep, ok := c.nodes[nodeID]
	c.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown node: %s", nodeID)
	}

	return c.pollNode(nodeID, serverID, ep)
}

// CollectAll polls all registered nodes and returns metrics events plus any stall alerts.
func (c *NodeMetricsCollector) CollectAll(serverID string) ([]*models.NodeMetricsEvent, []*models.NodeNonceStallEvent) {
	c.mu.RLock()
	snapshot := make(map[string]nodeEndpoint, len(c.nodes))
	for id, ep := range c.nodes {
		snapshot[id] = ep
	}
	c.mu.RUnlock()

	var events []*models.NodeMetricsEvent
	var stalls []*models.NodeNonceStallEvent

	for nodeID, ep := range snapshot {
		evt, err := c.pollNode(nodeID, serverID, ep)
		if err != nil {
			log.Printf("poll node %s: %v", nodeID, err)
			events = append(events, &models.NodeMetricsEvent{
				NodeID:      nodeID,
				ServerID:    serverID,
				Error:       err.Error(),
				CollectedAt: time.Now().Unix(),
			})
			continue
		}
		events = append(events, evt)

		// Check nonce stall
		if stall := c.checkNonceStall(nodeID, serverID, evt); stall != nil {
			stalls = append(stalls, stall)
		}
	}

	return events, stalls
}

// pollNode fetches /node/status from a single node endpoint.
func (c *NodeMetricsCollector) pollNode(nodeID, serverID string, ep nodeEndpoint) (*models.NodeMetricsEvent, error) {
	url := fmt.Sprintf("http://%s:%d/node/status", ep.host, ep.port)

	resp, err := c.httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
	}

	var raw nodeStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if raw.Code != "successful" {
		return nil, fmt.Errorf("node error: %s", raw.Error)
	}

	// Parse raw metrics into typed map
	metrics := make(map[string]any, len(raw.Data.Metrics))
	for k, v := range raw.Data.Metrics {
		var parsed any
		if err := json.Unmarshal(v, &parsed); err != nil {
			metrics[k] = string(v)
		} else {
			metrics[k] = parsed
		}
	}

	return &models.NodeMetricsEvent{
		NodeID:      nodeID,
		ServerID:    serverID,
		Metrics:     metrics,
		CollectedAt: time.Now().Unix(),
	}, nil
}

// checkNonceStall detects if a node's nonce has stopped incrementing.
func (c *NodeMetricsCollector) checkNonceStall(nodeID, serverID string, evt *models.NodeMetricsEvent) *models.NodeNonceStallEvent {
	nonceVal, ok := evt.Metrics["klv_nonce"]
	if !ok {
		return nil
	}

	// JSON numbers are float64
	nonce := uint64(0)
	switch v := nonceVal.(type) {
	case float64:
		nonce = uint64(v)
	case json.Number:
		n, err := v.Int64()
		if err != nil {
			return nil
		}
		nonce = uint64(n)
	default:
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	prev, exists := c.lastNonces[nodeID]
	now := time.Now()

	if !exists || nonce != prev.nonce {
		// Nonce changed — reset tracker
		c.lastNonces[nodeID] = nonceState{
			nonce:   nonce,
			firstAt: now,
		}
		return nil
	}

	// Nonce unchanged — check duration
	stallDuration := now.Sub(prev.firstAt)
	if stallDuration >= c.stallTimeout && !prev.alerted {
		prev.alerted = true
		c.lastNonces[nodeID] = prev
		return &models.NodeNonceStallEvent{
			NodeID:        nodeID,
			ServerID:      serverID,
			StuckNonce:    nonce,
			StallDuration: stallDuration.Seconds(),
			DetectedAt:    now.Unix(),
		}
	}

	return nil
}

// PollInterval returns the configured polling interval.
func (c *NodeMetricsCollector) PollInterval() time.Duration {
	return c.pollInterval
}

// RunPoller starts a background goroutine that polls all nodes at the configured interval.
// It sends metrics and stall events to the provided channels.
// The goroutine stops when ctx is canceled.
func (c *NodeMetricsCollector) RunPoller(ctx context.Context, serverID string, metricsCh chan<- *models.Message, stallCh chan<- *models.Message) {
	ticker := time.NewTicker(c.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			events, stalls := c.CollectAll(serverID)

			for _, evt := range events {
				msg := &models.Message{
					ID:        fmt.Sprintf("nm-%s-%d", evt.NodeID[:min(8, len(evt.NodeID))], time.Now().UnixNano()),
					Type:      "event",
					Action:    "node.metrics",
					Payload:   evt,
					Timestamp: time.Now().Unix(),
				}
				select {
				case metricsCh <- msg:
				case <-ctx.Done():
					return
				}
			}

			for _, stall := range stalls {
				msg := &models.Message{
					ID:        fmt.Sprintf("stall-%s-%d", stall.NodeID[:min(8, len(stall.NodeID))], time.Now().UnixNano()),
					Type:      "event",
					Action:    "node.nonce_stall",
					Payload:   stall,
					Timestamp: time.Now().Unix(),
				}
				select {
				case stallCh <- msg:
				case <-ctx.Done():
					return
				}
			}
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
