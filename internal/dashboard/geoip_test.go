package dashboard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGeoIPResolver_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","country":"Germany","city":"Frankfurt"}`))
	}))
	defer server.Close()

	resolver := NewGeoIPResolver()
	// Override the lookup to use our test server
	region := resolver.lookupURL(context.Background(), server.URL)
	if region != "Frankfurt, Germany" {
		t.Errorf("got %q, want %q", region, "Frankfurt, Germany")
	}
}

func TestGeoIPResolver_CityEmpty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","country":"Germany","city":""}`))
	}))
	defer server.Close()

	resolver := NewGeoIPResolver()
	region := resolver.lookupURL(context.Background(), server.URL)
	if region != "Germany" {
		t.Errorf("got %q, want %q", region, "Germany")
	}
}

func TestGeoIPResolver_Fail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"fail","message":"private range"}`))
	}))
	defer server.Close()

	resolver := NewGeoIPResolver()
	region := resolver.lookupURL(context.Background(), server.URL)
	if region != "" {
		t.Errorf("got %q, want empty", region)
	}
}

func TestGeoIPResolver_Cache(t *testing.T) {
	resolver := NewGeoIPResolver()

	// Pre-populate cache
	resolver.mu.Lock()
	resolver.cache["1.2.3.4"] = "Cached Region"
	resolver.mu.Unlock()

	region := resolver.Resolve(context.Background(), "1.2.3.4")
	if region != "Cached Region" {
		t.Errorf("got %q, want %q", region, "Cached Region")
	}
}

func TestGeoIPResolver_EmptyIP(t *testing.T) {
	resolver := NewGeoIPResolver()
	region := resolver.Resolve(context.Background(), "")
	if region != "" {
		t.Errorf("got %q, want empty", region)
	}
}
