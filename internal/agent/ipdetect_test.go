package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDetectPublicIP_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("203.0.113.42\n"))
	}))
	defer server.Close()

	origEndpoints := ipEndpoints
	ipEndpoints = []string{server.URL}
	defer func() { ipEndpoints = origEndpoints }()

	ip := DetectPublicIP(context.Background())
	if ip != "203.0.113.42" {
		t.Errorf("got %q, want %q", ip, "203.0.113.42")
	}
}

func TestDetectPublicIP_Fallback(t *testing.T) {
	failServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer failServer.Close()

	okServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("10.0.0.1"))
	}))
	defer okServer.Close()

	origEndpoints := ipEndpoints
	ipEndpoints = []string{failServer.URL, okServer.URL}
	defer func() { ipEndpoints = origEndpoints }()

	ip := DetectPublicIP(context.Background())
	if ip != "10.0.0.1" {
		t.Errorf("got %q, want %q", ip, "10.0.0.1")
	}
}

func TestDetectPublicIP_AllFail(t *testing.T) {
	origEndpoints := ipEndpoints
	ipEndpoints = []string{"http://127.0.0.1:1"}
	defer func() { ipEndpoints = origEndpoints }()

	ip := DetectPublicIP(context.Background())
	if ip != "" {
		t.Errorf("expected empty string, got %q", ip)
	}
}
