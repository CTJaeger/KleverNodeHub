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

	"github.com/CTJaeger/KleverNodeHub/internal/release"
)

func TestValidReleaseURL(t *testing.T) {
	base := releaseDownloadPrefix
	tests := []struct {
		url  string
		want bool
	}{
		{base + "v0.3.89/klever-agent-linux-amd64", true},
		{"https://github.com/someone-else/KleverNodeHub/releases/download/v0.3.89/klever-agent-linux-amd64", false},
		{base + "../../../evil/repo/releases/download/v1/klever-agent-linux-amd64", false},
		{base + "v1/klever-agent-linux-amd64?x=1", false},
		{base + "v1/klever-agent-linux-amd64#frag", false},
		{base + "klever-agent-linux-amd64", false}, // missing tag segment
		{base + "v1/a/b", false},
		{"http://github.com/CTJaeger/KleverNodeHub/releases/download/v1/x", false},
	}
	for _, tt := range tests {
		if got := validReleaseURL(tt.url); got != tt.want {
			t.Errorf("validReleaseURL(%q) = %v, want %v", tt.url, got, tt.want)
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
