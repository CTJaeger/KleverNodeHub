package agent

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ipEndpoints are queried in order until one succeeds.
var ipEndpoints = []string{
	"https://api.ipify.org",
	"https://ifconfig.me/ip",
	"https://icanhazip.com",
}

// DetectPublicIP queries external services to determine the agent's public IP.
// Returns empty string on failure (graceful degradation).
func DetectPublicIP(ctx context.Context) string {
	client := &http.Client{Timeout: 5 * time.Second}

	for _, endpoint := range ipEndpoints {
		ip, err := fetchIP(ctx, client, endpoint)
		if err == nil && ip != "" {
			return ip
		}
	}
	return ""
}

func fetchIP(ctx context.Context, client *http.Client, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "KleverNodeHub-Agent")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64))
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(body)), nil
}
