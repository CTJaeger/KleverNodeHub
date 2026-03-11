package ws

import (
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/CTJaeger/KleverNodeHub/internal/models"
	"github.com/CTJaeger/KleverNodeHub/internal/store"
)

// AgentConn represents an active agent WebSocket connection.
type AgentConn struct {
	ServerID      string
	SendCh        chan []byte
	lastHeartbeat time.Time
	mu            sync.Mutex
}

// UpdateHeartbeat records the latest heartbeat time.
func (ac *AgentConn) UpdateHeartbeat() {
	ac.mu.Lock()
	defer ac.mu.Unlock()
	ac.lastHeartbeat = time.Now()
}

// LastHeartbeat returns the time of the last heartbeat.
func (ac *AgentConn) LastHeartbeat() time.Time {
	ac.mu.Lock()
	defer ac.mu.Unlock()
	return ac.lastHeartbeat
}

// Hub manages all active agent connections.
type Hub struct {
	mu          sync.RWMutex
	connections map[string]*AgentConn // serverID -> connection
	serverStore *store.ServerStore
	stopCh      chan struct{}
}

// NewHub creates a new connection hub.
func NewHub(serverStore *store.ServerStore) *Hub {
	return &Hub{
		connections: make(map[string]*AgentConn),
		serverStore: serverStore,
		stopCh:      make(chan struct{}),
	}
}

// Register adds a new agent connection to the hub.
func (h *Hub) Register(serverID string) *AgentConn {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Close existing connection if any
	if existing, ok := h.connections[serverID]; ok {
		close(existing.SendCh)
	}

	conn := &AgentConn{
		ServerID:      serverID,
		SendCh:        make(chan []byte, 64),
		lastHeartbeat: time.Now(),
	}
	h.connections[serverID] = conn

	h.serverStore.UpdateHeartbeat(serverID, time.Now().Unix())

	log.Printf("agent connected: %s", serverID)
	return conn
}

// Unregister removes an agent connection from the hub.
func (h *Hub) Unregister(serverID string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if conn, ok := h.connections[serverID]; ok {
		close(conn.SendCh)
		delete(h.connections, serverID)
		log.Printf("agent disconnected: %s", serverID)
	}
}

// Send sends a message to a specific agent.
func (h *Hub) Send(serverID string, msg *models.Message) error {
	h.mu.RLock()
	conn, ok := h.connections[serverID]
	h.mu.RUnlock()

	if !ok {
		return fmt.Errorf("agent not connected: %s", serverID)
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}

	select {
	case conn.SendCh <- data:
		return nil
	default:
		return fmt.Errorf("agent send buffer full: %s", serverID)
	}
}

// Broadcast sends a message to all connected agents.
func (h *Hub) Broadcast(msg *models.Message) {
	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("broadcast marshal error: %v", err)
		return
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	for _, conn := range h.connections {
		select {
		case conn.SendCh <- data:
		default:
			log.Printf("broadcast: buffer full for %s", conn.ServerID)
		}
	}
}

// IsConnected checks if an agent is currently connected.
func (h *Hub) IsConnected(serverID string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	_, ok := h.connections[serverID]
	return ok
}

// ConnectedCount returns the number of connected agents.
func (h *Hub) ConnectedCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.connections)
}

// StartHealthCheck starts the background heartbeat monitor.
// Marks agents as offline if no heartbeat received within timeout.
func (h *Hub) StartHealthCheck(timeout time.Duration) {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				h.checkHeartbeats(timeout)
			case <-h.stopCh:
				return
			}
		}
	}()
}

// Stop stops the health check goroutine.
func (h *Hub) Stop() {
	close(h.stopCh)
}

func (h *Hub) checkHeartbeats(timeout time.Duration) {
	h.mu.RLock()
	var stale []string
	for id, conn := range h.connections {
		if time.Since(conn.LastHeartbeat()) > timeout {
			stale = append(stale, id)
		}
	}
	h.mu.RUnlock()

	for _, id := range stale {
		log.Printf("agent heartbeat timeout: %s", id)
		h.Unregister(id)
	}
}
