package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// GeoIPResolver resolves IP addresses to geographic regions.
type GeoIPResolver struct {
	client *http.Client
	cache  map[string]string
	mu     sync.RWMutex
}

// NewGeoIPResolver creates a new resolver with an in-memory cache.
func NewGeoIPResolver() *GeoIPResolver {
	return &GeoIPResolver{
		client: &http.Client{Timeout: 5 * time.Second},
		cache:  make(map[string]string),
	}
}

// ipAPIResponse is the response from ip-api.com.
type ipAPIResponse struct {
	Status  string `json:"status"`
	Country string `json:"country"`
	City    string `json:"city"`
}

// Resolve returns a human-readable region string for the given IP.
// Results are cached in memory. Returns empty string on failure.
func (g *GeoIPResolver) Resolve(ctx context.Context, ip string) string {
	if ip == "" {
		return ""
	}

	g.mu.RLock()
	if region, ok := g.cache[ip]; ok {
		g.mu.RUnlock()
		return region
	}
	g.mu.RUnlock()

	region := g.lookup(ctx, ip)

	g.mu.Lock()
	g.cache[ip] = region
	g.mu.Unlock()

	return region
}

func (g *GeoIPResolver) lookup(ctx context.Context, ip string) string {
	url := fmt.Sprintf("http://ip-api.com/json/%s?fields=status,country,city", ip)
	return g.lookupURL(ctx, url)
}

// lookupURL fetches and parses a GeoIP JSON response from the given URL.
func (g *GeoIPResolver) lookupURL(ctx context.Context, url string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", "KleverNodeHub-Dashboard")

	resp, err := g.client.Do(req)
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return ""
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if err != nil {
		return ""
	}

	var result ipAPIResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return ""
	}

	if result.Status != "success" {
		return ""
	}

	if result.City != "" {
		return result.City + ", " + result.Country
	}
	return result.Country
}
