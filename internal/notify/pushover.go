package notify

import (
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// PushoverConfig holds Pushover configuration.
type PushoverConfig struct {
	UserKey  string `json:"user_key"`
	AppToken string `json:"app_token"`
}

// PushoverChannel sends notifications via Pushover API.
type PushoverChannel struct {
	config     PushoverConfig
	httpClient *http.Client
}

// NewPushoverChannel creates a new Pushover notification channel.
func NewPushoverChannel(cfg PushoverConfig) *PushoverChannel {
	return &PushoverChannel{
		config: cfg,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (p *PushoverChannel) Name() string { return "pushover" }

func (p *PushoverChannel) Validate() error {
	if p.config.UserKey == "" {
		return fmt.Errorf("user_key is required")
	}
	if p.config.AppToken == "" {
		return fmt.Errorf("app_token is required")
	}
	return nil
}

func (p *PushoverChannel) Send(alert *Alert) error {
	if err := p.Validate(); err != nil {
		return err
	}

	priority := "0" // normal
	switch alert.Severity {
	case SeverityWarning:
		priority = "1" // high
	case SeverityCritical:
		priority = "2" // emergency
	}

	data := url.Values{
		"token":    {p.config.AppToken},
		"user":     {p.config.UserKey},
		"title":    {alert.Title},
		"message":  {alert.Message},
		"priority": {priority},
	}

	// Emergency priority requires retry/expire params
	if priority == "2" {
		data.Set("retry", "60")
		data.Set("expire", "3600")
	}

	resp, err := p.httpClient.PostForm("https://api.pushover.net/1/messages.json", data)
	if err != nil {
		return fmt.Errorf("pushover send: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("pushover API error: HTTP %d", resp.StatusCode)
	}

	return nil
}
