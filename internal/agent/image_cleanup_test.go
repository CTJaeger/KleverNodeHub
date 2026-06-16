package agent

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"sync"
	"testing"

	"github.com/CTJaeger/KleverNodeHub/internal/models"
)

// newMockDockerForImages stands up a unix-socket Docker mock that serves two
// klever-go images (one in use, one unused) and records image deletes.
func newMockDockerForImages(t *testing.T) (*DockerClient, *[]string, func()) {
	t.Helper()
	socketPath, sockCleanup := shortSocketPath(t)

	var mu sync.Mutex
	deleted := []string{}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && contains(r.URL.Path, "/images/json"):
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"Id": "sha256:aaa", "RepoTags": []string{"kleverapp/klever-go:v0.59.0"}, "Created": 1700000000, "Size": 100},
				{"Id": "sha256:bbb", "RepoTags": []string{"kleverapp/klever-go:v0.60.0"}, "Created": 1710000000, "Size": 200},
			})
		case r.Method == http.MethodGet && contains(r.URL.Path, "/containers/json"):
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"Names": []string{"/klever-node1"}, "ImageID": "sha256:bbb"},
			})
		case r.Method == http.MethodDelete && contains(r.URL.Path, "/images/"):
			mu.Lock()
			deleted = append(deleted, r.URL.Path)
			mu.Unlock()
			_ = json.NewEncoder(w).Encode([]map[string]any{{"Deleted": "sha256:aaa"}})
		default:
			http.NotFound(w, r)
		}
	})

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := &http.Server{Handler: mux}
	go func() { _ = server.Serve(listener) }()

	client := NewDockerClient(socketPath)
	return client, &deleted, func() {
		_ = server.Close()
		_ = listener.Close()
		sockCleanup()
	}
}

func TestListKleverImages_FlagsInUse(t *testing.T) {
	client, _, cleanup := newMockDockerForImages(t)
	defer cleanup()

	images, err := client.ListKleverImages(context.Background())
	if err != nil {
		t.Fatalf("ListKleverImages: %v", err)
	}
	if len(images) != 2 {
		t.Fatalf("expected 2 images, got %d", len(images))
	}

	byID := map[string]ImageInfo{}
	for _, img := range images {
		byID[img.ID] = img
	}
	if byID["sha256:aaa"].InUse {
		t.Errorf("image aaa should be unused")
	}
	bbb := byID["sha256:bbb"]
	if !bbb.InUse {
		t.Errorf("image bbb should be in use")
	}
	if len(bbb.UsedBy) != 1 || bbb.UsedBy[0] != "klever-node1" {
		t.Errorf("image bbb UsedBy = %v, want [klever-node1]", bbb.UsedBy)
	}
}

func TestExecuteRemoveImages_RefusesInUse(t *testing.T) {
	client, deleted, cleanup := newMockDockerForImages(t)
	defer cleanup()

	exec := NewExecutorWithClient(client)
	msg := &models.Message{
		ID:     "cmd-rm",
		Type:   "command",
		Action: "docker.images.remove",
		Payload: map[string]any{
			"image_ids": []any{"sha256:aaa", "sha256:bbb"},
		},
	}

	result := exec.Execute(msg, nil)
	if !result.Success {
		t.Fatalf("expected success, got error: %s", result.Error)
	}

	var results []imageRemoveResult
	if err := json.Unmarshal([]byte(result.Output), &results); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	byID := map[string]imageRemoveResult{}
	for _, r := range results {
		byID[r.ID] = r
	}
	if !byID["sha256:aaa"].Success {
		t.Errorf("aaa should be removed: %s", byID["sha256:aaa"].Error)
	}
	if byID["sha256:bbb"].Success {
		t.Errorf("bbb is in use and must not be removed")
	}

	// Only the unused image should have hit the Docker delete endpoint.
	if len(*deleted) != 1 {
		t.Fatalf("expected 1 delete call, got %d: %v", len(*deleted), *deleted)
	}
	if !contains((*deleted)[0], "aaa") {
		t.Errorf("delete call should target aaa, got %s", (*deleted)[0])
	}
}

func TestExtractStringSlice(t *testing.T) {
	got := extractStringSlice(map[string]any{"image_ids": []any{"a", "", "b"}}, "image_ids")
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("extractStringSlice = %v, want [a b]", got)
	}
	if extractStringSlice(map[string]any{}, "image_ids") != nil {
		t.Errorf("missing field should return nil")
	}
}
