package ws

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/coder/websocket"

	"github.com/CTJaeger/KleverNodeHub/internal/dashboard"
	"github.com/CTJaeger/KleverNodeHub/internal/models"
	"github.com/CTJaeger/KleverNodeHub/internal/store"
)

// AgentHandler handles WebSocket connections from agents.
type AgentHandler struct {
	hub          *Hub
	serverStore  *store.ServerStore
	nodeStore    *store.NodeStore
	metricsStore *store.MetricsStore
	versionStore *store.VersionHistoryStore
	geoResolver  *dashboard.GeoIPResolver
}

// NewAgentHandler creates a new WebSocket handler for agent connections.
func NewAgentHandler(hub *Hub, serverStore *store.ServerStore, nodeStore *store.NodeStore, metricsStore *store.MetricsStore, versionStore *store.VersionHistoryStore, geoResolver *dashboard.GeoIPResolver) *AgentHandler {
	return &AgentHandler{
		hub:          hub,
		serverStore:  serverStore,
		nodeStore:    nodeStore,
		metricsStore: metricsStore,
		versionStore: versionStore,
		geoResolver:  geoResolver,
	}
}

// HandleUpgrade upgrades an HTTP connection to WebSocket and runs the agent message loop.
func (h *AgentHandler) HandleUpgrade(w http.ResponseWriter, r *http.Request) {
	// Extract server ID from query parameter
	serverID := r.URL.Query().Get("server_id")
	if serverID == "" {
		http.Error(w, "missing server_id parameter", http.StatusBadRequest)
		return
	}

	// Verify server exists
	if _, err := h.serverStore.GetByID(serverID); err != nil {
		http.Error(w, "unknown server", http.StatusUnauthorized)
		return
	}

	// Upgrade to WebSocket
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true, // We validate via server_id; mTLS will be added later
	})
	if err != nil {
		log.Printf("websocket upgrade failed for %s: %v", serverID, err)
		return
	}

	conn.SetReadLimit(50 << 20) // 50 MB — agent update responses can include large payloads

	log.Printf("agent WebSocket connected: %s", serverID)

	// Register in hub
	agentConn := h.hub.Register(serverID)
	h.hub.BroadcastToBrowsers("agent.connected", map[string]string{"server_id": serverID})

	// Run read and write loops
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Write loop: send messages from hub to agent
	go func() {
		defer cancel()
		for {
			select {
			case <-ctx.Done():
				return
			case data, ok := <-agentConn.SendCh:
				if !ok {
					return
				}
				writeCtx, writeCancel := context.WithTimeout(ctx, 10*time.Second)
				err := conn.Write(writeCtx, websocket.MessageText, data)
				writeCancel()
				if err != nil {
					log.Printf("websocket write error for %s: %v", serverID, err)
					return
				}
			}
		}
	}()

	// Read loop: receive messages from agent
	h.readLoop(ctx, conn, serverID, agentConn)

	// Cleanup
	h.hub.Unregister(serverID)
	h.hub.BroadcastToBrowsers("agent.disconnected", map[string]string{"server_id": serverID})
	_ = conn.Close(websocket.StatusNormalClosure, "closing")
	log.Printf("agent WebSocket disconnected: %s", serverID)
}

// readLoop reads messages from the WebSocket and dispatches them.
//
// The high-frequency, DB-writing handlers (heartbeat + node metrics) run on a
// background worker, not inline. The agent pings every 25s and expects a pong
// within 10s; coder/websocket only emits pongs while conn.Read is being called.
// If the read loop blocked on a slow DB write (e.g. during hourly metrics
// decimation, which holds the metrics lock), pongs would stall and the agent
// would disconnect. Persisting asynchronously keeps the loop — and pongs —
// responsive regardless of DB contention. Liveness is already captured by the
// in-memory UpdateHeartbeat below, so dropping a queued metrics sample under
// backpressure never marks the agent offline.
func (h *AgentHandler) readLoop(ctx context.Context, conn *websocket.Conn, serverID string, agentConn *AgentConn) {
	work := make(chan func(), 512)
	go func() {
		for fn := range work {
			fn()
		}
	}()
	defer close(work)

	// enqueueLossy runs DB work on the worker; if it's backed up (DB stalled),
	// the sample is dropped rather than blocking the read loop / pongs.
	enqueueLossy := func(action string, fn func()) {
		select {
		case work <- fn:
		default:
			log.Printf("agent %s: dropping %s (DB worker backed up)", serverID, action)
		}
	}

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("websocket read error for %s: %v", serverID, err)
			}
			return
		}

		var msg models.Message
		if err := json.Unmarshal(data, &msg); err != nil {
			log.Printf("invalid message from %s: %v", serverID, err)
			continue
		}

		agentConn.UpdateHeartbeat()
		m := msg                    // per-iteration copy captured by async closures
		recvAt := time.Now().Unix() // receipt time, recorded now (not when the worker runs)

		switch msg.Action {
		case "agent.info":
			// handleAgentInfo logs the individual fields (version, IP, region)
			// it cares about. Dumping the raw message risks leaking any future
			// field (config snapshot, cert, etc.) into logs as soon as it ships.
			h.handleAgentInfo(ctx, serverID, &msg)

		case "agent.heartbeat":
			enqueueLossy("agent.heartbeat", func() {
				_ = h.serverStore.UpdateHeartbeat(serverID, recvAt)
				h.handleHeartbeatMetrics(serverID, &m)
				h.handleHeartbeatIP(ctx, serverID, &m)
			})

		case "agent.discovery":
			h.handleDiscovery(serverID, &msg)
			h.hub.BroadcastToBrowsers("agent.discovery", map[string]any{
				"server_id": serverID,
			})
			h.hub.BroadcastToBrowsers("node.update", map[string]any{
				"server_id": serverID,
			})

		case "node.metrics":
			enqueueLossy("node.metrics", func() {
				h.handleNodeMetrics(&m)
			})
			h.hub.BroadcastToBrowsers("node.metrics", msg.Payload)

		case "node.nonce_stall":
			h.handleNonceStall(serverID, &msg)
			h.hub.BroadcastToBrowsers("node.nonce_stall", map[string]any{
				"server_id": serverID,
				"payload":   msg.Payload,
			})

		case "command.result":
			h.handleCommandResult(&msg)

		case "benchmark.progress":
			h.hub.BroadcastToBrowsers("benchmark.progress", map[string]any{
				"server_id": serverID,
				"payload":   msg.Payload,
			})

		case "node.restore-db.progress":
			h.hub.BroadcastToBrowsers("node.restore-db.progress", map[string]any{
				"server_id": serverID,
				"payload":   msg.Payload,
			})

		default:
			log.Printf("unknown action from %s: %s", serverID, msg.Action)
		}
	}
}

// handleDiscovery processes a discovery report from an agent.
func (h *AgentHandler) handleDiscovery(serverID string, msg *models.Message) {
	data, err := json.Marshal(msg.Payload)
	if err != nil {
		log.Printf("marshal discovery payload: %v", err)
		return
	}

	var report models.DiscoveryReport
	if err := json.Unmarshal(data, &report); err != nil {
		log.Printf("unmarshal discovery report: %v", err)
		return
	}

	log.Printf("discovery from %s: %d nodes found", serverID, len(report.Nodes))
	for _, d := range report.Nodes {
		log.Printf("  discovered: container=%q display=%q status=%s", d.ContainerName, d.DisplayName, d.Status)
	}

	existing, _ := h.nodeStore.ListByServer(serverID)
	log.Printf("discovery: %d existing nodes in DB for server %s", len(existing), serverID)
	for _, e := range existing {
		log.Printf("  existing: id=%s container=%q display=%q", e.ID, e.ContainerName, e.DisplayName)
	}

	for _, discovered := range report.Nodes {
		meta := map[string]any{
			"cpu_percent": discovered.CPUPercent,
			"mem_used":    discovered.MemUsed,
			"mem_limit":   discovered.MemLimit,
			"mem_percent": discovered.MemPercent,
		}

		var nodeID string
		found := false
		for i := range existing {
			if existing[i].ContainerName == discovered.ContainerName {
				found = true
				nodeID = existing[i].ID
				existing[i].Status = discovered.Status
				existing[i].DockerImageTag = discovered.DockerImageTag
				existing[i].RestAPIPort = discovered.RestAPIPort
				existing[i].DataDirectory = discovered.DataDirectory
				existing[i].BLSPublicKey = discovered.BLSPublicKey
				// Merge Docker stats into existing metadata (preserve Klever metrics)
				if existing[i].Metadata == nil {
					existing[i].Metadata = meta
				} else {
					for k, v := range meta {
						existing[i].Metadata[k] = v
					}
				}
				// Self-heal the maintenance flag: once the node is seen running
				// again (however it was started), clear the alert suppression.
				if discovered.Status == "running" {
					delete(existing[i].Metadata, "maintenance")
				}
				if err := h.nodeStore.Update(&existing[i]); err != nil {
					log.Printf("discovery: failed to update node %q: %v", discovered.ContainerName, err)
				}
				break
			}
		}

		if !found {
			nodeType := "validator"
			if discovered.RedundancyLevel > 0 {
				nodeType = "observer"
			}
			nodeID = fmt.Sprintf("node-%s-%d", discovered.ContainerName, time.Now().UnixNano())
			log.Printf("discovery: new node %q (container: %s) on server %s", discovered.DisplayName, discovered.ContainerName, serverID)
			if err := h.nodeStore.Create(&models.Node{
				ID:              nodeID,
				ServerID:        serverID,
				Name:            discovered.DisplayName,
				ContainerName:   discovered.ContainerName,
				NodeType:        nodeType,
				RedundancyLevel: discovered.RedundancyLevel,
				RestAPIPort:     discovered.RestAPIPort,
				DisplayName:     discovered.DisplayName,
				DockerImageTag:  discovered.DockerImageTag,
				DataDirectory:   discovered.DataDirectory,
				BLSPublicKey:    discovered.BLSPublicKey,
				Status:          discovered.Status,
				Metadata:        meta,
				CreatedAt:       time.Now().Unix(),
			}); err != nil {
				log.Printf("discovery: failed to create node %q: %v", discovered.ContainerName, err)
				nodeID = ""
			}
		}

		// Track version changes for the performance-regression detector.
		if nodeID != "" && discovered.DockerImageTag != "" && h.versionStore != nil {
			changed, err := h.versionStore.RecordVersion(nodeID, serverID, discovered.DockerImageTag, time.Now().Unix())
			if err != nil {
				log.Printf("discovery: record version history for %q: %v", discovered.ContainerName, err)
			} else if changed {
				log.Printf("discovery: node %q version is now %s", discovered.ContainerName, discovered.DockerImageTag)
			}
		}
	}
}

// handleHeartbeatMetrics extracts system metrics from heartbeat and persists them.
func (h *AgentHandler) handleHeartbeatMetrics(serverID string, msg *models.Message) {
	if h.metricsStore == nil {
		return
	}

	data, err := json.Marshal(msg.Payload)
	if err != nil {
		return
	}

	var hb models.HeartbeatPayload
	if err := json.Unmarshal(data, &hb); err != nil || hb.Metrics == nil {
		return
	}

	m := hb.Metrics
	row := &store.SystemMetricsRow{
		CPUPercent:  m.CPUPercent,
		MemPercent:  m.MemPercent,
		MemTotal:    m.MemTotal,
		MemUsed:     m.MemUsed,
		DiskPercent: m.DiskPercent,
		DiskTotal:   m.DiskTotal,
		DiskUsed:    m.DiskUsed,
		LoadAvg1:    m.LoadAvg1,
		CollectedAt: m.CollectedAt,
	}
	if err := h.metricsStore.InsertSystemMetrics(serverID, row); err != nil {
		log.Printf("store system metrics for %s: %v", serverID, err)
	}
}

// handleNodeMetrics persists node metrics from agent polling.
func (h *AgentHandler) handleNodeMetrics(msg *models.Message) {
	if h.metricsStore == nil {
		return
	}

	data, err := json.Marshal(msg.Payload)
	if err != nil {
		log.Printf("marshal node metrics: %v", err)
		return
	}

	var evt models.NodeMetricsEvent
	if err := json.Unmarshal(data, &evt); err != nil {
		log.Printf("unmarshal node metrics: %v", err)
		return
	}

	if evt.Error != "" {
		return // Node was unreachable, nothing to store
	}

	numeric := store.ExtractNumericMetrics(evt.Metrics)
	if len(numeric) == 0 {
		return
	}

	// Resolve container name to dashboard node ID and merge metrics into node metadata
	nodeID := evt.NodeID
	node, err := h.nodeStore.GetByContainerAndServer(evt.NodeID, evt.ServerID)
	if err != nil {
		log.Printf("node metrics: container %q on server %q not found in DB: %v", evt.NodeID, evt.ServerID, err)
	} else {
		nodeID = node.ID

		// Merge Klever metrics into node metadata so the overview can display them
		if node.Metadata == nil {
			node.Metadata = make(map[string]any)
		}
		for k, v := range numeric {
			node.Metadata[k] = v
		}
		_ = h.nodeStore.Update(node)
	}

	if err := h.metricsStore.InsertNodeMetrics(nodeID, evt.ServerID, numeric, evt.CollectedAt); err != nil {
		log.Printf("store node metrics for %s: %v", nodeID, err)
	}
}

// handleNonceStall logs nonce stall events (future: trigger notifications).
func (h *AgentHandler) handleNonceStall(serverID string, msg *models.Message) {
	data, err := json.Marshal(msg.Payload)
	if err != nil {
		return
	}

	var evt models.NodeNonceStallEvent
	if err := json.Unmarshal(data, &evt); err != nil {
		return
	}

	log.Printf("ALERT: nonce stall on node %s (server %s) — stuck at nonce %d for %.0fs",
		evt.NodeID, serverID, evt.StuckNonce, evt.StallDuration)
}

// handleCommandResult processes a command result from an agent.
func (h *AgentHandler) handleCommandResult(msg *models.Message) {
	data, err := json.Marshal(msg.Payload)
	if err != nil {
		log.Printf("marshal command result: %v", err)
		return
	}

	var result models.CommandResult
	if err := json.Unmarshal(data, &result); err != nil {
		log.Printf("unmarshal command result: %v", err)
		return
	}

	h.hub.HandleResult(&result)
}

// handleAgentInfo processes agent.info and updates public IP + region.
func (h *AgentHandler) handleAgentInfo(ctx context.Context, serverID string, msg *models.Message) {
	data, err := json.Marshal(msg.Payload)
	if err != nil {
		return
	}

	var info models.AgentInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return
	}

	// Update agent version if provided
	if info.Version != "" {
		if srv, err := h.serverStore.GetByID(serverID); err == nil && srv.AgentVersion != info.Version {
			srv.AgentVersion = info.Version
			_ = h.serverStore.Update(srv)
			log.Printf("agent %s version updated to %s", serverID, info.Version)
		}
	}

	if info.PublicIP == "" {
		return
	}

	h.updateServerIPAndRegion(ctx, serverID, info.PublicIP)
}

// handleHeartbeatIP updates public IP from heartbeat if changed.
func (h *AgentHandler) handleHeartbeatIP(ctx context.Context, serverID string, msg *models.Message) {
	data, err := json.Marshal(msg.Payload)
	if err != nil {
		return
	}

	var hb models.HeartbeatPayload
	if err := json.Unmarshal(data, &hb); err != nil {
		return
	}

	if hb.PublicIP == "" {
		return
	}

	// Only update if IP changed
	srv, err := h.serverStore.GetByID(serverID)
	if err != nil || srv.PublicIP == hb.PublicIP {
		return
	}

	h.updateServerIPAndRegion(ctx, serverID, hb.PublicIP)
}

// updateServerIPAndRegion resolves the region and persists IP + region.
func (h *AgentHandler) updateServerIPAndRegion(ctx context.Context, serverID, publicIP string) {
	region := ""
	if h.geoResolver != nil {
		region = h.geoResolver.Resolve(ctx, publicIP)
	}

	if err := h.serverStore.UpdatePublicIP(serverID, publicIP, region); err != nil {
		log.Printf("update public IP for %s: %v", serverID, err)
	} else {
		log.Printf("server %s: public IP %s, region %s", serverID, publicIP, region)
	}
}
