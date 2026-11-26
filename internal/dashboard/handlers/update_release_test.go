package handlers

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/CTJaeger/KleverNodeHub/internal/dashboard"
	"github.com/CTJaeger/KleverNodeHub/internal/release"
)

func TestValidReleaseURL(t *testing.T) {
	up, next := upstreamDownloadPrefix, successorDownloadPrefix
	tests := []struct {
		url  string
		want bool
	}{
		{up + "v0.3.93/klever-agent-linux-amd64", true},
		{next + "v0.3.93-knh.1/klever-agent-linux-amd64", true},
		{up + "v0.3.93-knh.1/klever-agent-linux-amd64", false}, // knh tags only from the new repository
		{next + "v0.3.93/klever-agent-linux-amd64", false},     // plain tags only from the old one
		{"https://github.com/someone-else/KleverNodeHub/releases/download/v0.3.89/klever-agent-linux-amd64", false},
		{up + "../../../evil/repo/releases/download/v1/klever-agent-linux-amd64", false},
		{up + "v1/klever-agent-linux-amd64?x=1", false},
		{up + "v1/klever-agent-linux-amd64#frag", false},
		{up + "klever-agent-linux-amd64", false}, // missing tag segment
		{up + "v1/a/b", false},
		{"http://github.com/CTJaeger/KleverNodeHub/releases/download/v1/x", false},
	}
	for _, tt := range tests {
		if got := validReleaseURL(tt.url); got != tt.want {
			t.Errorf("validReleaseURL(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

func TestNeedsBridge(t *testing.T) {
	tests := []struct {
		agent, target string
		want          bool
	}{
		{"v0.3.92", "v0.3.93-knh.1", true},
		{"v0.3.88", "v0.3.93-knh.2", true},
		{"", "v0.3.93-knh.1", true},
		{"v0.3.93", "v0.3.93-knh.1", false},
		{"v0.3.93-knh.1", "v0.3.93-knh.2", false},
		{"v0.3.92", "v0.3.93", false}, // the bridge itself
		{"v0.3.90", "v0.3.92", false},
	}
	for _, tt := range tests {
		if got := needsBridge(tt.agent, tt.target); got != tt.want {
			t.Errorf("needsBridge(%q, %q) = %v, want %v", tt.agent, tt.target, got, tt.want)
		}
	}
}

func TestBinaryForRoutesOldAgentsThroughTheBridge(t *testing.T) {
	store := dashboard.NewUpdateStore(t.TempDir())
	for _, v := range []string{agentBridgeVersion, "v0.3.93-knh.1"} {
		if _, err := store.StoreRelease(v, "linux", "amd64", []byte(v), "klever-agent-linux-amd64", []byte("sums"), []byte("sig")); err != nil {
			t.Fatal(err)
		}
	}
	h := &UpdateHandler{updateStore: store}

	for agent, want := range map[string]string{"v0.3.90": agentBridgeVersion, "v0.3.93": "v0.3.93-knh.1"} {
		_, info, err := h.binaryFor(agent, "v0.3.93-knh.1", "linux", "amd64")
		if err != nil || info.Version != want {
			t.Errorf("agent %s: got %v / %v, want %s", agent, info, err, want)
		}
	}
	// Without an explicit version the newest stored one is the target.
	_, info, err := h.binaryFor("v0.3.91", "", "linux", "amd64")
	if err != nil || info.Version != agentBridgeVersion {
		t.Errorf("latest for an old agent: got %v / %v, want the bridge", info, err)
	}
}

func TestImageForRelease(t *testing.T) {
	tests := []struct {
		current, tag, want string
	}{
		{"ctjaeger/klever-node-hub:v0.3.93", "v0.3.93-knh.1", "ghcr.io/test0rmaik/klever-node-hub:v0.3.93-knh.1"},
		{"docker.io/ctjaeger/klever-node-hub:latest", "v0.3.93-knh.1", "ghcr.io/test0rmaik/klever-node-hub:v0.3.93-knh.1"},
		{"ctjaeger/klever-node-hub", "v0.3.93-knh.2", "ghcr.io/test0rmaik/klever-node-hub:v0.3.93-knh.2"},
		{"ghcr.io/test0rmaik/klever-node-hub:v0.3.93-knh.1", "v0.3.93-knh.2", "ghcr.io/test0rmaik/klever-node-hub:v0.3.93-knh.2"},
		{"registry.local:5000/mirror/klever-node-hub:v0.3.93", "v0.3.93-knh.1", "registry.local:5000/mirror/klever-node-hub:v0.3.93-knh.1"},
		{"ctjaeger/klever-node-hub:v0.3.92", "v0.3.93", "ctjaeger/klever-node-hub:v0.3.93"},
	}
	for _, tt := range tests {
		got, err := imageForRelease(tt.current, tt.tag)
		if err != nil || got != tt.want {
			t.Errorf("imageForRelease(%q, %q) = %q, %v; want %q", tt.current, tt.tag, got, err, tt.want)
		}
	}
}

// rewriteTransport sends every request to the test server, keeping the path.
type rewriteTransport struct{ target string }

func (rt rewriteTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	u, _ := url.Parse(rt.target)
	r2 := r.Clone(r.Context())
	r2.URL.Scheme, r2.URL.Host = u.Scheme, u.Host
	return http.DefaultTransport.RoundTrip(r2)
}

func TestFetchSignedChecksums(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	defer release.SwapPublicKeyForTest(pub)()

	sums := []byte("abc123  klever-agent-linux-amd64\n# release good\n")
	goodSig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, sums))
	files := map[string]string{
		"/good/checksums.txt":     string(sums),
		"/good/release.sig":       goodSig,
		"/unsigned/checksums.txt": string(sums),
		"/forged/checksums.txt":   "evil999  klever-agent-linux-amd64\n",
		"/forged/release.sig":     goodSig,
		"/renamed/checksums.txt":  string(sums), // validly signed, but for release "good"
		"/renamed/release.sig":    goodSig,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for name, body := range files {
			if strings.HasSuffix(r.URL.Path, name) {
				_, _ = w.Write([]byte(body))
				return
			}
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	client := &http.Client{Transport: rewriteTransport{srv.URL}}

	got, _, err := fetchSignedChecksums(client, "good")
	if err != nil || string(got) != string(sums) {
		t.Fatalf("signed release rejected: %v", err)
	}
	if _, _, err := fetchSignedChecksums(client, "unsigned"); err == nil {
		t.Error("release without signature accepted")
	}
	if _, _, err := fetchSignedChecksums(client, "forged"); err == nil {
		t.Error("checksums not matching the signature accepted")
	}
	if _, _, err := fetchSignedChecksums(client, "renamed"); err == nil {
		t.Error("signed checksums of another release accepted under a new tag")
	}
	if _, _, err := fetchSignedChecksums(client, "../x"); err == nil {
		t.Error("path-like tag accepted")
	}
}

func TestUploadDisabledByDefault(t *testing.T) {
	h := &UpdateHandler{}
	w := httptest.NewRecorder()
	h.HandleUploadBinary(w, httptest.NewRequest(http.MethodPost, "/", nil))
	if w.Code != http.StatusForbidden {
		t.Errorf("upload: status = %d, want %d", w.Code, http.StatusForbidden)
	}
}
