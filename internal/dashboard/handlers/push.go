package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/CTJaeger/KleverNodeHub/internal/notify"
	"github.com/CTJaeger/KleverNodeHub/internal/store"
)

// PushHandler manages Web Push subscription endpoints.
type PushHandler struct {
	webpush  *notify.WebPushChannel
	settings *store.SettingsStore
}

// NewPushHandler creates a new push notification handler.
func NewPushHandler(webpush *notify.WebPushChannel, settings *store.SettingsStore) *PushHandler {
	return &PushHandler{webpush: webpush, settings: settings}
}

// HandleGetVAPIDKey returns the VAPID public key for the frontend.
// GET /api/push/vapid-key
func (h *PushHandler) HandleGetVAPIDKey(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"public_key": h.webpush.PublicKey(),
	})
}

// HandleSubscribe registers a push subscription.
// POST /api/push/subscribe
func (h *PushHandler) HandleSubscribe(w http.ResponseWriter, r *http.Request) {
	var sub notify.PushSubscription
	if err := json.NewDecoder(r.Body).Decode(&sub); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	if sub.Endpoint == "" || sub.Keys.P256dh == "" || sub.Keys.Auth == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "endpoint, p256dh and auth are required"})
		return
	}

	h.webpush.AddSubscription(sub)
	h.saveSubscriptions()

	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// HandleUnsubscribe removes a push subscription.
// POST /api/push/unsubscribe
func (h *PushHandler) HandleUnsubscribe(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Endpoint string `json:"endpoint"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	h.webpush.RemoveSubscription(req.Endpoint)
	h.saveSubscriptions()

	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// HandleTestPush sends a test push notification to all subscribers.
// POST /api/push/test
func (h *PushHandler) HandleTestPush(w http.ResponseWriter, _ *http.Request) {
	if h.webpush.SubscriptionCount() == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no push subscriptions registered"})
		return
	}

	testAlert := &notify.Alert{
		Title:    "Test Push Notification",
		Message:  "If you see this, Web Push notifications are working!",
		Severity: notify.SeverityInfo,
		Source:   "settings/push-test",
	}

	if err := h.webpush.Send(testAlert); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// HandleStatus returns push notification status.
// GET /api/push/status
func (h *PushHandler) HandleStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":       h.webpush.SubscriptionCount() > 0,
		"subscriptions": h.webpush.SubscriptionCount(),
	})
}

func (h *PushHandler) saveSubscriptions() {
	subs := h.webpush.Subscriptions()
	data, err := json.Marshal(subs)
	if err != nil {
		return
	}
	_ = h.settings.Set("push_subscriptions", string(data))
}

// LoadSavedSubscriptions loads push subscriptions from settings into the channel.
func LoadSavedSubscriptions(settings *store.SettingsStore, webpush *notify.WebPushChannel) {
	data, err := settings.Get("push_subscriptions")
	if err != nil || data == "" {
		return
	}
	var subs []notify.PushSubscription
	if err := json.Unmarshal([]byte(data), &subs); err != nil {
		return
	}
	webpush.SetSubscriptions(subs)
}
