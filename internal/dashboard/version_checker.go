package dashboard

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/CTJaeger/KleverNodeHub/internal/version"
)

// ReleaseInfo holds information about a GitHub release.
type ReleaseInfo struct {
	TagName    string         `json:"tag_name"`
	Name       string         `json:"name"`
	Body       string         `json:"body"`
	HTMLURL    string         `json:"html_url"`
	PublishAt  string         `json:"published_at"`
	Assets     []ReleaseAsset `json:"assets"`
	CheckedAt  time.Time      `json:"-"`
	HasUpdate  bool           `json:"has_update"`
	CurrentVer string         `json:"current_version"`
}

// ReleaseAsset holds info about a release download asset.
type ReleaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

// VersionChecker periodically checks GitHub for new releases.
type VersionChecker struct {
	mu       sync.RWMutex
	owner    string
	repo     string
	latest   *ReleaseInfo
	interval time.Duration
	client   *http.Client
}

// NewVersionChecker creates a new version checker.
func NewVersionChecker(owner, repo string) *VersionChecker {
	return &VersionChecker{
		owner:    owner,
		repo:     repo,
		interval: 30 * time.Minute,
		client:   &http.Client{Timeout: 10 * time.Second},
	}
}

// Start begins periodic version checking.
func (vc *VersionChecker) Start() {
	// Check immediately on start
	vc.check()

	go func() {
		ticker := time.NewTicker(vc.interval)
		defer ticker.Stop()
		for range ticker.C {
			vc.check()
		}
	}()
}

// Latest returns the most recently fetched release info.
func (vc *VersionChecker) Latest() *ReleaseInfo {
	vc.mu.RLock()
	defer vc.mu.RUnlock()
	return vc.latest
}

func (vc *VersionChecker) check() {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", vc.owner, vc.repo)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		log.Printf("version check: create request: %v", err)
		return
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "KleverNodeHub/"+version.Version)

	resp, err := vc.client.Do(req)
	if err != nil {
		log.Printf("version check: request failed: %v", err)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		// Rate limited or not found — silently skip
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound {
			return
		}
		log.Printf("version check: unexpected status %d", resp.StatusCode)
		return
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MB limit
	if err != nil {
		log.Printf("version check: read body: %v", err)
		return
	}

	var release ReleaseInfo
	if err := json.Unmarshal(body, &release); err != nil {
		log.Printf("version check: parse: %v", err)
		return
	}

	release.CheckedAt = time.Now()
	release.CurrentVer = version.Version
	release.HasUpdate = isNewer(release.TagName, version.Version)

	vc.mu.Lock()
	vc.latest = &release
	vc.mu.Unlock()

	if release.HasUpdate {
		log.Printf("version check: update available %s → %s", version.Version, release.TagName)
	}
}

// isNewer returns true if remote version is newer than local.
// Handles "dev" as always outdated.
func isNewer(remote, local string) bool {
	if local == "dev" || local == "" {
		return remote != "" && remote != "dev"
	}
	remote = strings.TrimPrefix(remote, "v")
	local = strings.TrimPrefix(local, "v")
	if remote == local {
		return false
	}
	return compareVersions(remote, local) > 0
}

// compareVersions compares two semver strings (without 'v' prefix).
// Returns 1 if a > b, -1 if a < b, 0 if equal.
func compareVersions(a, b string) int {
	aParts := strings.Split(a, ".")
	bParts := strings.Split(b, ".")

	for i := 0; i < 3; i++ {
		var aNum, bNum int
		if i < len(aParts) {
			_, _ = fmt.Sscanf(aParts[i], "%d", &aNum)
		}
		if i < len(bParts) {
			_, _ = fmt.Sscanf(bParts[i], "%d", &bNum)
		}
		if aNum > bNum {
			return 1
		}
		if aNum < bNum {
			return -1
		}
	}
	return 0
}

// FindAsset returns the download URL for a specific binary type.
func (r *ReleaseInfo) FindAsset(prefix, goos, goarch string) string {
	if r == nil {
		return ""
	}
	target := fmt.Sprintf("%s-%s-%s", prefix, goos, goarch)
	for _, a := range r.Assets {
		if strings.HasPrefix(a.Name, target) {
			return a.BrowserDownloadURL
		}
	}
	return ""
}
