package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// WebhookConfig holds webhook configuration.
type WebhookConfig struct {
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
}

// WebhookChannel sends notifications via HTTP webhook.
type WebhookChannel struct {
	config     WebhookConfig
	httpClient *http.Client
}

// NewWebhookChannel creates a new webhook notification channel.
func NewWebhookChannel(cfg WebhookConfig) *WebhookChannel {
	return &WebhookChannel{
		config: cfg,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (w *WebhookChannel) Name() string { return "webhook" }

func (w *WebhookChannel) Validate() error {
	if w.config.URL == "" {
		return fmt.Errorf("url is required")
	}
	return nil
}

func (w *WebhookChannel) Send(alert *Alert) error {
	if err := w.Validate(); err != nil {
		return err
	}

	body, err := json.Marshal(alert)
	if err != nil {
		return fmt.Errorf("marshal alert: %w", err)
	}

	// Retry with exponential backoff (3 attempts)
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt*2) * time.Second)
		}

		req, err := http.NewRequest(http.MethodPost, w.config.URL, bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		for k, v := range w.config.Headers {
			req.Header.Set(k, v)
		}

		resp, err := w.httpClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return nil
		}
		lastErr = fmt.Errorf("webhook HTTP %d", resp.StatusCode)
	}

	return fmt.Errorf("webhook failed after 3 attempts: %w", lastErr)
}
