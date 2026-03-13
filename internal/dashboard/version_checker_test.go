package dashboard

import "testing"

func TestIsNewer(t *testing.T) {
	tests := []struct {
		remote string
		local  string
		want   bool
	}{
		{"v0.2.0", "v0.1.0", true},
		{"v0.1.0", "v0.1.0", false},
		{"v0.1.0", "v0.2.0", false},
		{"v1.0.0", "v0.9.9", true},
		{"v0.1.1", "v0.1.0", true},
		{"v0.1.0", "dev", true},
		{"", "dev", false},
		{"v0.1.0", "", true},
	}
	for _, tt := range tests {
		got := isNewer(tt.remote, tt.local)
		if got != tt.want {
			t.Errorf("isNewer(%q, %q) = %v, want %v", tt.remote, tt.local, got, tt.want)
		}
	}
}

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "0.9.9", 1},
		{"0.1.0", "0.1.0", 0},
		{"0.1.0", "0.2.0", -1},
		{"1.0.0", "0.0.1", 1},
		{"0.0.2", "0.0.1", 1},
	}
	for _, tt := range tests {
		got := compareVersions(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestFindAsset(t *testing.T) {
	r := &ReleaseInfo{
		Assets: []ReleaseAsset{
			{Name: "klever-node-hub-linux-amd64", BrowserDownloadURL: "https://example.com/hub-linux-amd64"},
			{Name: "klever-agent-linux-amd64", BrowserDownloadURL: "https://example.com/agent-linux-amd64"},
			{Name: "checksums.txt", BrowserDownloadURL: "https://example.com/checksums.txt"},
		},
	}

	url := r.FindAsset("klever-node-hub", "linux", "amd64")
	if url != "https://example.com/hub-linux-amd64" {
		t.Errorf("FindAsset dashboard = %q", url)
	}

	url = r.FindAsset("klever-agent", "linux", "amd64")
	if url != "https://example.com/agent-linux-amd64" {
		t.Errorf("FindAsset agent = %q", url)
	}

	url = r.FindAsset("klever-node-hub", "darwin", "arm64")
	if url != "" {
		t.Errorf("FindAsset missing should be empty, got %q", url)
	}
}
