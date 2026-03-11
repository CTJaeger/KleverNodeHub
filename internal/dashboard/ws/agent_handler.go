package ws

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"nhooyr.io/websocket"

	"github.com/CTJaeger/KleverNodeHub/internal/models"
	"github.com/CTJaeger/KleverNodeHub/internal/store"
)

// AgentHandler handles WebSocket connections from agents.
type AgentHandler struct {
	hub         *Hub
	serverStore *store.ServerStore
	nodeStore   *store.NodeStore
}

// NewAgentHandler creates a new WebSocket handler for agent connections.
func NewAgentHandler(hub *Hub, serverStore *store.ServerStore, nodeStore *store.NodeStore) *AgentHandler {
	return &AgentHandler{
		hub:         hub,
		serverStore: serverStore,
		nodeStore:   nodeStore,
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

	log.Printf("agent WebSocket connected: %s", serverID)

	// Register in hub
	agentConn := h.hub.Register(serverID)

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

		case "agent.heartbeat":
			h.serverStore.UpdateHeartbeat(serverID, time.Now().Unix())

		case "agent.discovery":
			h.handleDiscovery(serverID, &msg)

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

	for _, discovered := range report.Nodes {
		// Check if node already exists
		existing, _ := h.nodeStore.ListByServer(serverID)
		found := false
		for i := range existing {
			if existing[i].ContainerName == discovered.ContainerName {
				found = true
				existing[i].Status = discovered.Status
				existing[i].DockerImageTag = discovered.DockerImageTag
				existing[i].RestAPIPort = discovered.RestAPIPort
				existing[i].DataDirectory = discovered.DataDirectory
				existing[i].BLSPublicKey = discovered.BLSPublicKey
				_ = h.nodeStore.Update(&existing[i])
				break
			}
		}

		if !found {
			nodeType := "validator"
			if discovered.RedundancyLevel > 0 {
				nodeType = "observer"
			}
			_ = h.nodeStore.Create(&models.Node{
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
				CreatedAt:       time.Now().Unix(),
			})
		}
	}
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
