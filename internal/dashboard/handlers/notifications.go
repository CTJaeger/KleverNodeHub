package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

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

// channelConfigRequest is the body for adding/updating a notification channel.
type channelConfigRequest struct {
	Name   string               `json:"name"`   // unique name, e.g. "telegram-ops"
	Type   string               `json:"type"`   // telegram, pushover, webhook
	Config map[string]string    `json:"config"`
	Filter notify.ChannelFilter `json:"filter"`
}

// HandleListChannels handles GET /api/notifications/channels
func (h *NotificationHandler) HandleListChannels(w http.ResponseWriter, _ *http.Request) {
	channels := h.manager.ChannelsWithFilters()

	type channelDetail struct {
		Name   string               `json:"name"`
		Type   string               `json:"type"`
		Filter notify.ChannelFilter `json:"filter"`
	}

	details := make([]channelDetail, len(channels))
	for i, ch := range channels {
		details[i] = channelDetail{
			Name:   ch.Name,
			Type:   h.getChannelType(ch.Name),
			Filter: ch.Filter,
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"channels": details})
}

// HandleAddChannel handles POST /api/notifications/channels
func (h *NotificationHandler) HandleAddChannel(w http.ResponseWriter, r *http.Request) {
	var req channelConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	if req.Name == "" {
		req.Name = req.Type + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}

	ch, err := h.createNamedChannel(req.Name, req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	if err := ch.Validate(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	h.manager.AddChannelWithFilter(ch, req.Filter)
	h.saveChannelConfig(req)

	writeJSON(w, http.StatusOK, map[string]any{"success": true, "channel": ch.Name()})
}

// HandleUpdateChannel handles PUT /api/notifications/channels/{name}
func (h *NotificationHandler) HandleUpdateChannel(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "channel name required"})
		return
	}

	var req channelConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	if len(req.Config) == 0 {
		if err := h.manager.UpdateChannelFilter(name, req.Filter); err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		h.updateSavedFilter(name, req.Filter)
		writeJSON(w, http.StatusOK, map[string]any{"success": true})
		return
	}

	h.manager.RemoveChannel(name)
	req.Name = name
	ch, err := h.createNamedChannel(name, req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := ch.Validate(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	h.manager.AddChannelWithFilter(ch, req.Filter)
	h.saveChannelConfig(req)

	writeJSON(w, http.StatusOK, map[string]any{"success": true})
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

func (h *NotificationHandler) createNamedChannel(name string, req channelConfigRequest) (notify.Channel, error) {
	switch req.Type {
	case "telegram":
		ch := notify.NewTelegramChannel(notify.TelegramConfig{
			BotToken: req.Config["bot_token"],
			ChatID:   req.Config["chat_id"],
		})
		return &namedChannel{Channel: ch, name: name}, nil
	case "pushover":
		ch := notify.NewPushoverChannel(notify.PushoverConfig{
			UserKey:  req.Config["user_key"],
			AppToken: req.Config["app_token"],
		})
		return &namedChannel{Channel: ch, name: name}, nil
	case "webhook":
		ch := notify.NewWebhookChannel(notify.WebhookConfig{
			URL: req.Config["url"],
		})
		return &namedChannel{Channel: ch, name: name}, nil
	default:
		return nil, fmt.Errorf("unknown channel type: %s", req.Type)
	}
}

// namedChannel wraps a Channel with a custom name to support multiple instances.
type namedChannel struct {
	notify.Channel
	name string
}

func (n *namedChannel) Name() string {
	return n.name
}

func (h *NotificationHandler) saveChannelConfig(req channelConfigRequest) {
	data, err := json.Marshal(req)
	if err != nil {
		return
	}
	_ = h.settings.Set("notify_ch_"+req.Name, string(data))
}

func (h *NotificationHandler) removeChannelConfig(name string) {
	_ = h.settings.Delete("notify_ch_" + name)
	_ = h.settings.Delete("notify_channel_" + name)
}

func (h *NotificationHandler) updateSavedFilter(name string, filter notify.ChannelFilter) {
	key := "notify_ch_" + name
	data, err := h.settings.Get(key)
	if err != nil || data == "" {
		return
	}
	var req channelConfigRequest
	if err := json.Unmarshal([]byte(data), &req); err != nil {
		return
	}
	req.Filter = filter
	updated, err := json.Marshal(req)
	if err != nil {
		return
	}
	_ = h.settings.Set(key, string(updated))
}

func (h *NotificationHandler) getChannelType(name string) string {
	key := "notify_ch_" + name
	data, err := h.settings.Get(key)
	if err != nil || data == "" {
		return ""
	}
	var req channelConfigRequest
	if err := json.Unmarshal([]byte(data), &req); err != nil {
		return ""
	}
	return req.Type
}

// LoadSavedChannels loads previously saved notification channels from settings.
func LoadSavedChannels(settings *store.SettingsStore, manager *notify.Manager) {
	all, err := settings.GetAll()
	if err != nil {
		return
	}

	// New format: notify_ch_*
	for key, data := range all {
		if len(key) <= 10 || key[:10] != "notify_ch_" {
			continue
		}
		if data == "" {
			continue
		}
		var req channelConfigRequest
		if err := json.Unmarshal([]byte(data), &req); err != nil {
			continue
		}
		h := &NotificationHandler{manager: manager, settings: settings}
		ch, err := h.createNamedChannel(req.Name, req)
		if err != nil {
			continue
		}
		manager.AddChannelWithFilter(ch, req.Filter)
	}

	// Legacy format: notify_channel_* (backward compatibility)
	for _, chType := range []string{"telegram", "pushover", "webhook"} {
		legacyKey := "notify_channel_" + chType
		data := all[legacyKey]
		if data == "" {
			continue
		}
		existing := manager.Channels()
		found := false
		for _, name := range existing {
			if name == chType {
				found = true
				break
			}
		}
		if found {
			continue
		}
		var oldReq channelConfigRequest
		if err := json.Unmarshal([]byte(data), &oldReq); err != nil {
			continue
		}
		oldReq.Name = chType
		h := &NotificationHandler{manager: manager, settings: settings}
		ch, err := h.createNamedChannel(chType, oldReq)
		if err != nil {
			continue
		}
		manager.AddChannel(ch)
	}
}
