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
	Title     string `json:"title"`
	Message   string `json:"message"`
	Severity  string `json:"severity"`   // info, warning, critical
	Source    string `json:"source"`     // e.g., "node:klever-node1", "system:server1"
	AlertType string `json:"alert_type"` // e.g., "node_down", "nonce_stall", "resource", "version"
	Time      int64  `json:"time"`
}

// Channel is the interface for notification delivery channels.
type Channel interface {
	Name() string
	Send(alert *Alert) error
	Validate() error
}

// ChannelFilter defines which alerts a channel should receive.
type ChannelFilter struct {
	Severities []string `json:"severities,omitempty"` // empty = all
	AlertTypes []string `json:"alert_types,omitempty"` // empty = all
}

// MatchesFilter checks if an alert passes the filter.
func (f *ChannelFilter) MatchesFilter(severity, alertType string) bool {
	if len(f.Severities) > 0 && !containsStr(f.Severities, severity) {
		return false
	}
	if len(f.AlertTypes) > 0 && alertType != "" && !containsStr(f.AlertTypes, alertType) {
		return false
	}
	return true
}

func containsStr(slice []string, val string) bool {
	for _, s := range slice {
		if s == val {
			return true
		}
	}
	return false
}

// HistoryEntry records a sent notification.
type HistoryEntry struct {
	ID       int64  `json:"id"`
	Channel  string `json:"channel"`
	Title    string `json:"title"`
	Message  string `json:"message"`
	Severity string `json:"severity"`
	Source   string `json:"source"`
	Success  bool   `json:"success"`
	Error    string `json:"error,omitempty"`
	SentAt   int64  `json:"sent_at"`
}

// channelEntry pairs a channel with its filter.
type channelEntry struct {
	channel Channel
	filter  ChannelFilter
}

// Manager sends alerts to registered channels with optional filtering.
type Manager struct {
	mu       sync.RWMutex
	channels []channelEntry
	history  []HistoryEntry
	maxHist  int
}

// NewManager creates a new notification manager.
func NewManager() *Manager {
	return &Manager{
		maxHist: 500,
	}
}

// AddChannel registers a notification channel with no filter (receives all alerts).
func (m *Manager) AddChannel(ch Channel) {
	m.AddChannelWithFilter(ch, ChannelFilter{})
}

// AddChannelWithFilter registers a notification channel with a filter.
func (m *Manager) AddChannelWithFilter(ch Channel, filter ChannelFilter) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.channels = append(m.channels, channelEntry{channel: ch, filter: filter})
}

// RemoveChannel removes a channel by name.
func (m *Manager) RemoveChannel(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, entry := range m.channels {
		if entry.channel.Name() == name {
			m.channels = append(m.channels[:i], m.channels[i+1:]...)
			return
		}
	}
}

// UpdateChannelFilter updates the filter for a channel by name.
func (m *Manager) UpdateChannelFilter(name string, filter ChannelFilter) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, entry := range m.channels {
		if entry.channel.Name() == name {
			m.channels[i].filter = filter
			return nil
		}
	}
	return fmt.Errorf("channel not found: %s", name)
}

// ChannelInfo contains channel name and its filter for API responses.
type ChannelInfo struct {
	Name   string        `json:"name"`
	Filter ChannelFilter `json:"filter"`
}

// ChannelsWithFilters returns channel names and their filters.
func (m *Manager) ChannelsWithFilters() []ChannelInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]ChannelInfo, len(m.channels))
	for i, entry := range m.channels {
		result[i] = ChannelInfo{
			Name:   entry.channel.Name(),
			Filter: entry.filter,
		}
	}
	return result
}

// Channels returns the list of registered channel names.
func (m *Manager) Channels() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	names := make([]string, len(m.channels))
	for i, entry := range m.channels {
		names[i] = entry.channel.Name()
	}
	return names
}

// Send dispatches an alert to all matching channels (respects filters).
func (m *Manager) Send(alert *Alert) {
	if alert.Time == 0 {
		alert.Time = time.Now().Unix()
	}

	m.mu.RLock()
	entries := make([]channelEntry, len(m.channels))
	copy(entries, m.channels)
	m.mu.RUnlock()

	for _, entry := range entries {
		if !entry.filter.MatchesFilter(alert.Severity, alert.AlertType) {
			continue
		}

		histEntry := HistoryEntry{
			Channel:  entry.channel.Name(),
			Title:    alert.Title,
			Message:  alert.Message,
			Severity: alert.Severity,
			Source:   alert.Source,
			SentAt:   time.Now().Unix(),
		}

		if err := entry.channel.Send(alert); err != nil {
			histEntry.Success = false
			histEntry.Error = err.Error()
			log.Printf("notify: %s failed: %v", entry.channel.Name(), err)
		} else {
			histEntry.Success = true
		}

		m.addHistory(histEntry)
	}
}

// SendTest sends a test alert to a specific channel (bypasses filters).
func (m *Manager) SendTest(channelName string) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, entry := range m.channels {
		if entry.channel.Name() == channelName {
			return entry.channel.Send(&Alert{
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

	if len(m.history) > m.maxHist {
		m.history = m.history[len(m.history)-m.maxHist:]
	}
}
