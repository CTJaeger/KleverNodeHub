package notify

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// mockChannel implements the Channel interface for testing.
type mockChannel struct {
	name      string
	sendErr   error
	sendCount int32
}

func (m *mockChannel) Name() string         { return m.name }
func (m *mockChannel) Validate() error       { return nil }
func (m *mockChannel) Send(_ *Alert) error {
	atomic.AddInt32(&m.sendCount, 1)
	return m.sendErr
}

func TestManagerAddRemoveChannels(t *testing.T) {
	mgr := NewManager()

	ch1 := &mockChannel{name: "test1"}
	ch2 := &mockChannel{name: "test2"}

	mgr.AddChannel(ch1)
	mgr.AddChannel(ch2)

	names := mgr.Channels()
	if len(names) != 2 {
		t.Fatalf("expected 2 channels, got %d", len(names))
	}

	mgr.RemoveChannel("test1")
	names = mgr.Channels()
	if len(names) != 1 {
		t.Fatalf("expected 1 channel after remove, got %d", len(names))
	}
	if names[0] != "test2" {
		t.Errorf("remaining channel = %q, want test2", names[0])
	}
}

func TestManagerSend_FanOut(t *testing.T) {
	mgr := NewManager()

	ch1 := &mockChannel{name: "ch1"}
	ch2 := &mockChannel{name: "ch2"}
	mgr.AddChannel(ch1)
	mgr.AddChannel(ch2)

	alert := &Alert{
		Title:    "Test",
		Message:  "Hello",
		Severity: SeverityInfo,
	}

	mgr.Send(alert)

	if atomic.LoadInt32(&ch1.sendCount) != 1 {
		t.Errorf("ch1 send count = %d, want 1", ch1.sendCount)
	}
	if atomic.LoadInt32(&ch2.sendCount) != 1 {
		t.Errorf("ch2 send count = %d, want 1", ch2.sendCount)
	}
}

func TestManagerSend_PartialFailure(t *testing.T) {
	mgr := NewManager()

	ch1 := &mockChannel{name: "ok"}
	ch2 := &mockChannel{name: "fail", sendErr: fmt.Errorf("send error")}
	mgr.AddChannel(ch1)
	mgr.AddChannel(ch2)

	alert := &Alert{Title: "Test", Severity: SeverityWarning}
	mgr.Send(alert)

	// Both should have been called
	if atomic.LoadInt32(&ch1.sendCount) != 1 {
		t.Error("expected ok channel to receive alert")
	}
	if atomic.LoadInt32(&ch2.sendCount) != 1 {
		t.Error("expected fail channel to receive alert")
	}

	// History should show both
	hist := mgr.History(10)
	if len(hist) != 2 {
		t.Fatalf("expected 2 history entries, got %d", len(hist))
	}

	var successCount, failCount int
	for _, h := range hist {
		if h.Success {
			successCount++
		} else {
			failCount++
		}
	}
	if successCount != 1 || failCount != 1 {
		t.Errorf("success=%d fail=%d, want 1 each", successCount, failCount)
	}
}

func TestManagerHistory(t *testing.T) {
	mgr := NewManager()
	ch := &mockChannel{name: "test"}
	mgr.AddChannel(ch)

	for i := 0; i < 5; i++ {
		mgr.Send(&Alert{Title: fmt.Sprintf("Alert %d", i), Severity: SeverityInfo})
	}

	hist := mgr.History(3)
	if len(hist) != 3 {
		t.Fatalf("expected 3 history entries, got %d", len(hist))
	}
}

func TestManagerSendTest(t *testing.T) {
	mgr := NewManager()
	ch := &mockChannel{name: "test"}
	mgr.AddChannel(ch)

	if err := mgr.SendTest("test"); err != nil {
		t.Errorf("SendTest: %v", err)
	}
	if atomic.LoadInt32(&ch.sendCount) != 1 {
		t.Error("expected test channel to receive test alert")
	}
}

func TestManagerSendTest_NotFound(t *testing.T) {
	mgr := NewManager()
	err := mgr.SendTest("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent channel")
	}
}

func TestTelegramValidate(t *testing.T) {
	ch := NewTelegramChannel(TelegramConfig{})
	if err := ch.Validate(); err == nil {
		t.Error("expected validation error for empty config")
	}

	ch = NewTelegramChannel(TelegramConfig{BotToken: "abc", ChatID: "123"})
	if err := ch.Validate(); err != nil {
		t.Errorf("unexpected validation error: %v", err)
	}
}

func TestPushoverValidate(t *testing.T) {
	ch := NewPushoverChannel(PushoverConfig{})
	if err := ch.Validate(); err == nil {
		t.Error("expected validation error for empty config")
	}

	ch = NewPushoverChannel(PushoverConfig{UserKey: "u", AppToken: "a"})
	if err := ch.Validate(); err != nil {
		t.Errorf("unexpected validation error: %v", err)
	}
}

func TestWebhookValidate(t *testing.T) {
	ch := NewWebhookChannel(WebhookConfig{})
	if err := ch.Validate(); err == nil {
		t.Error("expected validation error for empty URL")
	}
}

func TestWebhookSend(t *testing.T) {
	var received *Alert

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var alert Alert
		if err := json.NewDecoder(r.Body).Decode(&alert); err == nil {
			received = &alert
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ch := NewWebhookChannel(WebhookConfig{URL: server.URL})
	alert := &Alert{
		Title:    "Test Alert",
		Message:  "Hello webhook",
		Severity: SeverityCritical,
	}

	if err := ch.Send(alert); err != nil {
		t.Fatalf("webhook send: %v", err)
	}

	if received == nil {
		t.Fatal("webhook did not receive alert")
	}
	if received.Title != "Test Alert" {
		t.Errorf("received title = %q, want Test Alert", received.Title)
	}
	if received.Severity != SeverityCritical {
		t.Errorf("received severity = %q, want critical", received.Severity)
	}
}

func TestWebhookSend_Retry(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		count := atomic.AddInt32(&attempts, 1)
		if count < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ch := NewWebhookChannel(WebhookConfig{URL: server.URL})
	if err := ch.Send(&Alert{Title: "Retry Test"}); err != nil {
		t.Errorf("expected success after retries, got: %v", err)
	}
	if atomic.LoadInt32(&attempts) != 3 {
		t.Errorf("expected 3 attempts, got %d", attempts)
	}
}

func TestFormatTelegramMessage(t *testing.T) {
	alert := &Alert{
		Title:    "Node Down",
		Message:  "klever-node1 is not responding",
		Severity: SeverityCritical,
		Source:   "node:klever-node1",
	}

	msg := formatTelegramMessage(alert)
	if msg == "" {
		t.Error("expected non-empty message")
	}
	// Should contain the emoji for critical
	if len(msg) < 5 {
		t.Errorf("message too short: %q", msg)
	}
}
