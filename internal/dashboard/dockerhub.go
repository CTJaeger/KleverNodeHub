package dashboard

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	dockerHubURL    = "https://hub.docker.com/v2/repositories/kleverapp/klever-go/tags/"
	tagCacheTTL     = 15 * time.Minute
	tagQueryLimit   = 100
)

// filteredPrefixes are tag prefixes to exclude from the list.
var filteredPrefixes = []string{
	"dev", "testnet", "devnet", "alpine", "val-only",
}

// DockerTag represents a Docker Hub image tag.
type DockerTag struct {
	Name        string `json:"name"`
	LastUpdated string `json:"last_updated"`
	FullSize    int64  `json:"full_size"`
	IsLatest    bool   `json:"is_latest,omitempty"`
}

// TagCache caches Docker Hub tag listings.
type TagCache struct {
	mu        sync.RWMutex
	tags      []DockerTag
	fetchedAt time.Time
}

// NewTagCache creates a new tag cache.
func NewTagCache() *TagCache {
	return &TagCache{}
}

// GetTags returns cached tags or fetches fresh ones if expired.
func (tc *TagCache) GetTags() ([]DockerTag, error) {
	tc.mu.RLock()
	if time.Since(tc.fetchedAt) < tagCacheTTL && len(tc.tags) > 0 {
		tags := make([]DockerTag, len(tc.tags))
		copy(tags, tc.tags)
		tc.mu.RUnlock()
		return tags, nil
	}
	tc.mu.RUnlock()

	// Fetch fresh tags
	tags, err := fetchDockerHubTags()
	if err != nil {
		return nil, err
	}

	tc.mu.Lock()
	tc.tags = tags
	tc.fetchedAt = time.Now()
	tc.mu.Unlock()

	return tags, nil
}

// fetchDockerHubTags queries Docker Hub for available tags.
func fetchDockerHubTags() ([]DockerTag, error) {
	url := fmt.Sprintf("%s?page_size=%d&ordering=last_updated", dockerHubURL, tagQueryLimit)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("fetch tags: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch tags: HTTP %d", resp.StatusCode)
	}

	var result struct {
		Results []struct {
			Name        string `json:"name"`
			LastUpdated string `json:"last_updated"`
			FullSize    int64  `json:"full_size"`
		} `json:"results"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode tags: %w", err)
	}

	var tags []DockerTag
	for _, r := range result.Results {
		if shouldFilterTag(r.Name) {
			continue
		}
		tag := DockerTag{
			Name:        r.Name,
			LastUpdated: r.LastUpdated,
			FullSize:    r.FullSize,
			IsLatest:    r.Name == "latest",
		}
		tags = append(tags, tag)
	}

	// Sort: latest first, then by name descending (newest versions first)
	sort.Slice(tags, func(i, j int) bool {
		if tags[i].IsLatest {
			return true
		}
		if tags[j].IsLatest {
			return false
		}
		return tags[i].Name > tags[j].Name
	})

	return tags, nil
}

// shouldFilterTag returns true if a tag should be excluded.
func shouldFilterTag(name string) bool {
	lower := strings.ToLower(name)
	for _, prefix := range filteredPrefixes {
		if strings.HasPrefix(lower, prefix) || strings.Contains(lower, "-"+prefix) {
			return true
		}
	}
	return false
}
