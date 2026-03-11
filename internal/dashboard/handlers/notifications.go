package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/CTJaeger/KleverNodeHub/internal/notify"
	"github.com/CTJaeger/KleverNodeHub/internal/store"
)

// NotificationHandler handles notification configuration API requests.
type NotificationHandler struct {
	manager  *notify.Manager
	settings *store.SettingsStore
}

// NewNotificationHandler creates a new NotificationHandler.
func NewNotificationHandler(manager *notify.Manager, settings *store.SettingsStore) *NotificationHandler {
	return &NotificationHandler{
		manager:  manager,
		settings: settings,
	}
}

// HandleListChannels handles GET /api/notifications/channels
func (h *NotificationHandler) HandleListChannels(w http.ResponseWriter, _ *http.Request) {
	channels := h.manager.Channels()
	writeJSON(w, http.StatusOK, map[string]any{"channels": channels})
}

// channelConfigRequest is the body for adding/updating a notification channel.
type channelConfigRequest struct {
	Type    string            `json:"type"`    // telegram, pushover, webhook
	Config  map[string]string `json:"config"`
}

// HandleAddChannel handles POST /api/notifications/channels
func (h *NotificationHandler) HandleAddChannel(w http.ResponseWriter, r *http.Request) {
	var req channelConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	ch, err := h.createChannel(req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	if err := ch.Validate(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	h.manager.AddChannel(ch)
	h.saveChannelConfig(req)

	writeJSON(w, http.StatusOK, map[string]any{"success": true, "channel": ch.Name()})
}

// HandleRemoveChannel handles DELETE /api/notifications/channels/{name}
func (h *NotificationHandler) HandleRemoveChannel(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "channel name required"})
		return
	}

	h.manager.RemoveChannel(name)
	h.removeChannelConfig(name)

	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// HandleTestChannel handles POST /api/notifications/channels/{name}/test
func (h *NotificationHandler) HandleTestChannel(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "channel name required"})
		return
	}

	if err := h.manager.SendTest(name); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// HandleHistory handles GET /api/notifications/history
func (h *NotificationHandler) HandleHistory(w http.ResponseWriter, _ *http.Request) {
	history := h.manager.History(100)
	writeJSON(w, http.StatusOK, map[string]any{"history": history})
}

func (h *NotificationHandler) createChannel(req channelConfigRequest) (notify.Channel, error) {
	switch req.Type {
	case "telegram":
		return notify.NewTelegramChannel(notify.TelegramConfig{
			BotToken: req.Config["bot_token"],
			ChatID:   req.Config["chat_id"],
		}), nil
	case "pushover":
		return notify.NewPushoverChannel(notify.PushoverConfig{
			UserKey:  req.Config["user_key"],
			AppToken: req.Config["app_token"],
		}), nil
	case "webhook":
		return notify.NewWebhookChannel(notify.WebhookConfig{
			URL: req.Config["url"],
		}), nil
	default:
		return nil, fmt.Errorf("unknown channel type: %s", req.Type)
	}
}

func (h *NotificationHandler) saveChannelConfig(req channelConfigRequest) {
	data, err := json.Marshal(req)
	if err != nil {
		return
	}
	key := "notify_channel_" + req.Type
	_ = h.settings.Set(key, string(data))
}

func (h *NotificationHandler) removeChannelConfig(name string) {
	key := "notify_channel_" + name
	_ = h.settings.Set(key, "")
}

// LoadSavedChannels loads previously saved notification channels from settings.
func LoadSavedChannels(settings *store.SettingsStore, manager *notify.Manager) {
	for _, chType := range []string{"telegram", "pushover", "webhook"} {
		key := "notify_channel_" + chType
		data, err := settings.Get(key)
		if err != nil || data == "" {
			continue
		}
		var req channelConfigRequest
		if err := json.Unmarshal([]byte(data), &req); err != nil {
			continue
		}
		h := &NotificationHandler{manager: manager, settings: settings}
		ch, err := h.createChannel(req)
		if err != nil {
			continue
		}
		manager.AddChannel(ch)
	}
}
