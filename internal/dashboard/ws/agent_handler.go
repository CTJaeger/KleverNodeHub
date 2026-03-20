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
	geoResolver  *dashboard.GeoIPResolver
}

// NewAgentHandler creates a new WebSocket handler for agent connections.
func NewAgentHandler(hub *Hub, serverStore *store.ServerStore, nodeStore *store.NodeStore, metricsStore *store.MetricsStore, geoResolver *dashboard.GeoIPResolver) *AgentHandler {
	return &AgentHandler{
		hub:          hub,
		serverStore:  serverStore,
		nodeStore:    nodeStore,
		metricsStore: metricsStore,
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
func (h *AgentHandler) readLoop(ctx context.Context, conn *websocket.Conn, serverID string, agentConn *AgentConn) {
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

		switch msg.Action {
		case "agent.info":
			log.Printf("agent info from %s: %s", serverID, string(data))
			h.handleAgentInfo(ctx, serverID, &msg)

		case "agent.heartbeat":
			_ = h.serverStore.UpdateHeartbeat(serverID, time.Now().Unix())
			h.handleHeartbeatMetrics(serverID, &msg)
			h.handleHeartbeatIP(ctx, serverID, &msg)

		case "agent.discovery":
			h.handleDiscovery(serverID, &msg)
			h.hub.BroadcastToBrowsers("agent.discovery", map[string]any{
				"server_id": serverID,
			})
			h.hub.BroadcastToBrowsers("node.update", map[string]any{
				"server_id": serverID,
			})

		case "node.metrics":
			h.handleNodeMetrics(&msg)
			h.hub.BroadcastToBrowsers("node.metrics", msg.Payload)

		case "node.nonce_stall":
			h.handleNonceStall(serverID, &msg)
			h.hub.BroadcastToBrowsers("node.nonce_stall", map[string]any{
				"server_id": serverID,
				"payload":   msg.Payload,
			})

		case "command.result":
			h.handleCommandResult(&msg)

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

		found := false
		for i := range existing {
			if existing[i].ContainerName == discovered.ContainerName {
				found = true
				existing[i].Status = discovered.Status
				existing[i].DockerImageTag = discovered.DockerImageTag
				existing[i].RestAPIPort = discovered.RestAPIPort
				existing[i].DataDirectory = discovered.DataDirectory
				existing[i].BLSPublicKey = discovered.BLSPublicKey
				existing[i].Metadata = meta
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
			log.Printf("discovery: new node %q (container: %s) on server %s", discovered.DisplayName, discovered.ContainerName, serverID)
			if err := h.nodeStore.Create(&models.Node{
				ID:              fmt.Sprintf("node-%s-%d", discovered.ContainerName, time.Now().UnixNano()),
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

	// Resolve container name to dashboard node ID
	nodeID := evt.NodeID
	if node, err := h.nodeStore.GetByContainerID(evt.NodeID); err == nil {
		nodeID = node.ID
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
