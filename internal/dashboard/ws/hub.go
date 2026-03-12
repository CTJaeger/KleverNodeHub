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

// pendingCommand tracks a command awaiting a response from an agent.
type pendingCommand struct {
	resultCh chan *models.CommandResult
	timer    *time.Timer
}

// BrowserConn represents an active browser WebSocket connection.
type BrowserConn struct {
	ID     string
	SendCh chan []byte
}

// Hub manages all active agent connections.
type Hub struct {
	mu          sync.RWMutex
	connections map[string]*AgentConn // serverID -> connection
	serverStore *store.ServerStore
	stopCh      chan struct{}

	pendingMu sync.Mutex
	pending   map[string]*pendingCommand // commandID -> pending

	browserMu    sync.RWMutex
	browserConns map[string]*BrowserConn // clientID -> connection
}

// NewHub creates a new connection hub.
func NewHub(serverStore *store.ServerStore) *Hub {
	return &Hub{
		connections:  make(map[string]*AgentConn),
		serverStore:  serverStore,
		stopCh:       make(chan struct{}),
		pending:      make(map[string]*pendingCommand),
		browserConns: make(map[string]*BrowserConn),
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

	_ = h.serverStore.UpdateHeartbeat(serverID, time.Now().Unix())

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

// RegisterBrowser adds a browser client connection.
func (h *Hub) RegisterBrowser(clientID string) *BrowserConn {
	h.browserMu.Lock()
	defer h.browserMu.Unlock()

	if existing, ok := h.browserConns[clientID]; ok {
		close(existing.SendCh)
	}

	conn := &BrowserConn{
		ID:     clientID,
		SendCh: make(chan []byte, 64),
	}
	h.browserConns[clientID] = conn
	log.Printf("browser client connected: %s", clientID)
	return conn
}

// UnregisterBrowser removes a browser client connection.
func (h *Hub) UnregisterBrowser(clientID string) {
	h.browserMu.Lock()
	defer h.browserMu.Unlock()

	if conn, ok := h.browserConns[clientID]; ok {
		close(conn.SendCh)
		delete(h.browserConns, clientID)
		log.Printf("browser client disconnected: %s", clientID)
	}
}

// BroadcastToBrowsers sends a JSON event to all connected browser clients.
func (h *Hub) BroadcastToBrowsers(action string, payload any) {
	msg := map[string]any{
		"action":  action,
		"payload": payload,
	}
	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("browser broadcast marshal error: %v", err)
		return
	}

	h.browserMu.RLock()
	defer h.browserMu.RUnlock()

	for _, conn := range h.browserConns {
		select {
		case conn.SendCh <- data:
		default:
			log.Printf("browser broadcast: buffer full for %s", conn.ID)
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

// SendCommand sends a command to an agent and waits for the result.
// Returns the result or an error if the agent is offline or the command times out.
func (h *Hub) SendCommand(serverID string, msg *models.Message, timeout time.Duration) (*models.CommandResult, error) {
	if !h.IsConnected(serverID) {
		return nil, fmt.Errorf("agent offline: %s", serverID)
	}

	// Create pending entry with timer set before adding to map
	// to avoid a race between SendCommand and HandleResult on pc.timer.
	pc := &pendingCommand{
		resultCh: make(chan *models.CommandResult, 1),
	}
	pc.timer = time.AfterFunc(timeout, func() {
		h.pendingMu.Lock()
		if p, ok := h.pending[msg.ID]; ok {
			delete(h.pending, msg.ID)
			p.resultCh <- &models.CommandResult{
				CommandID: msg.ID,
				Error:     "command timed out",
			}
		}
		h.pendingMu.Unlock()
	})

	h.pendingMu.Lock()
	h.pending[msg.ID] = pc
	h.pendingMu.Unlock()

	// Send the command
	if err := h.Send(serverID, msg); err != nil {
		h.pendingMu.Lock()
		delete(h.pending, msg.ID)
		h.pendingMu.Unlock()
		pc.timer.Stop()
		return nil, err
	}

	// Wait for result
	result := <-pc.resultCh
	pc.timer.Stop()
	return result, nil
}

// HandleResult processes a command result from an agent.
// Matches the result to a pending command by command ID.
func (h *Hub) HandleResult(result *models.CommandResult) {
	h.pendingMu.Lock()
	pc, ok := h.pending[result.CommandID]
	if ok {
		delete(h.pending, result.CommandID)
	}
	h.pendingMu.Unlock()

	if ok {
		pc.timer.Stop()
		pc.resultCh <- result
	} else {
		log.Printf("received result for unknown command: %s", result.CommandID)
	}
}

// PendingCount returns the number of commands awaiting responses.
func (h *Hub) PendingCount() int {
	h.pendingMu.Lock()
	defer h.pendingMu.Unlock()
	return len(h.pending)
}

// PendingCommandIDs returns the IDs of all pending commands (for testing).
func (h *Hub) PendingCommandIDs() []string {
	h.pendingMu.Lock()
	defer h.pendingMu.Unlock()
	ids := make([]string, 0, len(h.pending))
	for id := range h.pending {
		ids = append(ids, id)
	}
	return ids
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
