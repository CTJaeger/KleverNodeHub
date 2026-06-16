package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/CTJaeger/KleverNodeHub/internal/models"
)

// ImageInfo describes a local Docker image for the cleanup view.
type ImageInfo struct {
	ID       string   `json:"id"`        // full image ID (e.g. "sha256:abc...")
	RepoTags []string `json:"repo_tags"` // e.g. ["kleverapp/klever-go:v1.2.3"]
	Created  int64    `json:"created"`   // creation time, unix seconds
	Size     int64    `json:"size"`      // size on disk, bytes
	InUse    bool     `json:"in_use"`    // referenced by an existing container
	UsedBy   []string `json:"used_by,omitempty"`
}

// ListKleverImages returns detailed info for every local kleverapp/klever-go
// image, flagging which ones are still referenced by a container so the
// dashboard can block their deletion.
func (d *DockerClient) ListKleverImages(ctx context.Context) ([]ImageInfo, error) {
	filterJSON, _ := json.Marshal(map[string][]string{
		"reference": {kleverImage},
	})
	u := fmt.Sprintf("http://localhost/%s/images/json?filters=%s",
		d.apiVersion, url.QueryEscape(string(filterJSON)))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	resp, err := d.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list images: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("list images: HTTP %d: %s", resp.StatusCode, string(body))
	}

	var raw []struct {
		ID       string   `json:"Id"`
		RepoTags []string `json:"RepoTags"`
		Created  int64    `json:"Created"`
		Size     int64    `json:"Size"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode images: %w", err)
	}

	usage, err := d.imageUsage(ctx)
	if err != nil {
		return nil, err
	}

	images := make([]ImageInfo, 0, len(raw))
	for _, img := range raw {
		users := usage[img.ID]
		images = append(images, ImageInfo{
			ID:       img.ID,
			RepoTags: img.RepoTags,
			Created:  img.Created,
			Size:     img.Size,
			InUse:    len(users) > 0,
			UsedBy:   users,
		})
	}
	return images, nil
}

// imageUsage maps an image ID to the names of containers that reference it.
// It lists every container (running and stopped) so an image kept by a stopped
// node container is still reported as in-use.
func (d *DockerClient) imageUsage(ctx context.Context) (map[string][]string, error) {
	u := fmt.Sprintf("http://localhost/%s/containers/json?all=true", d.apiVersion)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	resp, err := d.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list containers: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("list containers: HTTP %d: %s", resp.StatusCode, string(body))
	}

	var containers []struct {
		Names   []string `json:"Names"`
		ImageID string   `json:"ImageID"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&containers); err != nil {
		return nil, fmt.Errorf("decode containers: %w", err)
	}

	usage := make(map[string][]string)
	for _, c := range containers {
		name := ""
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}
		usage[c.ImageID] = append(usage[c.ImageID], name)
	}
	return usage, nil
}

// RemoveImage deletes a single image by ID. noprune is left at Docker's default
// (false) so untagged parent layers freed by the delete are also reclaimed.
func (d *DockerClient) RemoveImage(ctx context.Context, imageID string) error {
	u := fmt.Sprintf("http://localhost/%s/images/%s?force=false",
		d.apiVersion, url.PathEscape(imageID))
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, u, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	resp, err := d.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("remove image: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("remove image: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

// imageRemoveResult reports the outcome of removing one image.
type imageRemoveResult struct {
	ID      string `json:"id"`
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

// executeRemoveImages removes the requested klever-go images. The agent
// independently re-verifies that each requested image is a klever-go image and
// is not in use before deleting it — the dashboard's checks are a convenience,
// not the security boundary.
func (e *Executor) executeRemoveImages(ctx context.Context, payload any, result *models.CommandResult) error {
	ids := extractStringSlice(payload, "image_ids")
	if len(ids) == 0 {
		return fmt.Errorf("image_ids is required")
	}

	images, err := e.docker.ListKleverImages(ctx)
	if err != nil {
		return err
	}
	deletable := make(map[string]bool, len(images))
	for _, img := range images {
		if !img.InUse {
			deletable[img.ID] = true
		}
	}

	results := make([]imageRemoveResult, 0, len(ids))
	for _, id := range ids {
		entry := imageRemoveResult{ID: id}
		switch {
		case !deletable[id]:
			entry.Error = "image is not a removable klever-go image (unknown or in use)"
		default:
			if err := e.docker.RemoveImage(ctx, id); err != nil {
				entry.Error = err.Error()
			} else {
				entry.Success = true
			}
		}
		results = append(results, entry)
	}

	out, _ := json.Marshal(results)
	result.Output = string(out)
	return nil
}
