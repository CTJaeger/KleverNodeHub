package notify

import (
	"fmt"
	"log"
	"sync"
	"time"
)

// Severity levels for alerts.
const (
	SeverityInfo     = "info"
	SeverityWarning  = "warning"
	SeverityCritical = "critical"
)

// Alert represents a notification to be sent.
type Alert struct {
	Title    string `json:"title"`
	Message  string `json:"message"`
	Severity string `json:"severity"` // info, warning, critical
	Source   string `json:"source"`   // e.g., "node:klever-node1", "system:server1"
	Time     int64  `json:"time"`
}

// Channel is the interface for notification delivery channels.
type Channel interface {
	Name() string
	Send(alert *Alert) error
	Validate() error
}

// HistoryEntry records a sent notification.
type HistoryEntry struct {
	ID        int64  `json:"id"`
	Channel   string `json:"channel"`
	Title     string `json:"title"`
	Message   string `json:"message"`
	Severity  string `json:"severity"`
	Source    string `json:"source"`
	Success   bool   `json:"success"`
	Error     string `json:"error,omitempty"`
	SentAt    int64  `json:"sent_at"`
}

// Manager sends alerts to all registered channels.
type Manager struct {
	mu       sync.RWMutex
	channels []Channel
	history  []HistoryEntry
	maxHist  int
}

// NewManager creates a new notification manager.
func NewManager() *Manager {
	return &Manager{
		maxHist: 500,
	}
}

// AddChannel registers a notification channel.
func (m *Manager) AddChannel(ch Channel) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.channels = append(m.channels, ch)
}

// RemoveChannel removes a channel by name.
func (m *Manager) RemoveChannel(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, ch := range m.channels {
		if ch.Name() == name {
			m.channels = append(m.channels[:i], m.channels[i+1:]...)
			return
		}
	}
}

// Channels returns the list of registered channel names.
func (m *Manager) Channels() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	names := make([]string, len(m.channels))
	for i, ch := range m.channels {
		names[i] = ch.Name()
	}
	return names
}

// Send dispatches an alert to all registered channels (fan-out).
func (m *Manager) Send(alert *Alert) {
	if alert.Time == 0 {
		alert.Time = time.Now().Unix()
	}

	m.mu.RLock()
	channels := make([]Channel, len(m.channels))
	copy(channels, m.channels)
	m.mu.RUnlock()

	for _, ch := range channels {
		entry := HistoryEntry{
			Channel:  ch.Name(),
			Title:    alert.Title,
			Message:  alert.Message,
			Severity: alert.Severity,
			Source:   alert.Source,
			SentAt:   time.Now().Unix(),
		}

		if err := ch.Send(alert); err != nil {
			entry.Success = false
			entry.Error = err.Error()
			log.Printf("notify: %s failed: %v", ch.Name(), err)
		} else {
			entry.Success = true
		}

		m.addHistory(entry)
	}
}

// SendTest sends a test alert to a specific channel.
func (m *Manager) SendTest(channelName string) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, ch := range m.channels {
		if ch.Name() == channelName {
			return ch.Send(&Alert{
				Title:    "Test Notification",
				Message:  "This is a test notification from Klever Node Hub.",
				Severity: SeverityInfo,
				Source:   "system:test",
				Time:     time.Now().Unix(),
			})
		}
	}
	return fmt.Errorf("channel not found: %s", channelName)
}

// History returns recent notification history.
func (m *Manager) History(limit int) []HistoryEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if limit <= 0 || limit > len(m.history) {
		limit = len(m.history)
	}

	// Return most recent entries
	start := len(m.history) - limit
	if start < 0 {
		start = 0
	}

	result := make([]HistoryEntry, limit)
	copy(result, m.history[start:])
	return result
}

func (m *Manager) addHistory(entry HistoryEntry) {
	m.mu.Lock()
	defer m.mu.Unlock()

	entry.ID = int64(len(m.history) + 1)
	m.history = append(m.history, entry)

	// Trim old entries
	if len(m.history) > m.maxHist {
		m.history = m.history[len(m.history)-m.maxHist:]
	}
}
