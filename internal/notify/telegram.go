package notify

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// TelegramConfig holds Telegram bot configuration.
type TelegramConfig struct {
	BotToken string `json:"bot_token"`
	ChatID   string `json:"chat_id"`
}

// TelegramChannel sends notifications via Telegram Bot API.
type TelegramChannel struct {
	config     TelegramConfig
	httpClient *http.Client
	mu         sync.Mutex
	lastSent   time.Time
	sentCount  int
}

// NewTelegramChannel creates a new Telegram notification channel.
func NewTelegramChannel(cfg TelegramConfig) *TelegramChannel {
	return &TelegramChannel{
		config: cfg,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (t *TelegramChannel) Name() string { return "telegram" }

func (t *TelegramChannel) Validate() error {
	if t.config.BotToken == "" {
		return fmt.Errorf("bot_token is required")
	}
	if t.config.ChatID == "" {
		return fmt.Errorf("chat_id is required")
	}
	return nil
}

func (t *TelegramChannel) Send(alert *Alert) error {
	if err := t.Validate(); err != nil {
		return err
	}

	// Rate limit: 20 messages per minute
	t.mu.Lock()
	now := time.Now()
	if now.Sub(t.lastSent) > time.Minute {
		t.sentCount = 0
		t.lastSent = now
	}
	if t.sentCount >= 20 {
		t.mu.Unlock()
		return fmt.Errorf("rate limit exceeded (20 msg/min)")
	}
	t.sentCount++
	t.mu.Unlock()

	text := formatTelegramMessage(alert)

	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", t.config.BotToken)
	data := url.Values{
		"chat_id":    {t.config.ChatID},
		"text":       {text},
		"parse_mode": {"Markdown"},
	}

	resp, err := t.httpClient.PostForm(apiURL, data)
	if err != nil {
		return fmt.Errorf("telegram send: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		var result struct {
			Description string `json:"description"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&result)
		return fmt.Errorf("telegram API error %d: %s", resp.StatusCode, result.Description)
	}

	return nil
}

func formatTelegramMessage(alert *Alert) string {
	icon := "ℹ️"
	switch alert.Severity {
	case SeverityWarning:
		icon = "⚠️"
	case SeverityCritical:
		icon = "🚨"
	}

	var b strings.Builder
	b.WriteString(icon)
	b.WriteString(" *")
	b.WriteString(escapeTelegramMarkdown(alert.Title))
	b.WriteString("*\n\n")
	b.WriteString(escapeTelegramMarkdown(alert.Message))
	if alert.Source != "" {
		b.WriteString("\n\n_Source: ")
		b.WriteString(escapeTelegramMarkdown(alert.Source))
		b.WriteString("_")
	}
	return b.String()
}

func escapeTelegramMarkdown(s string) string {
	replacer := strings.NewReplacer(
		"_", "\\_",
		"*", "\\*",
		"[", "\\[",
		"`", "\\`",
	)
	return replacer.Replace(s)
}
