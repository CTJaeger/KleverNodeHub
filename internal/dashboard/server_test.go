package dashboard

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestServerSetupRoutes(t *testing.T) {
	srv := NewServer(&ServerConfig{Addr: ":9443"})
	if err := srv.SetupRoutes(); err != nil {
		t.Fatalf("SetupRoutes: %v", err)
	}

	// Test login page
	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	w := httptest.NewRecorder()
	srv.Mux().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("GET /login = %d, want %d", w.Code, http.StatusOK)
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
}

func TestServerSecurityHeaders(t *testing.T) {
	srv := NewServer(&ServerConfig{Addr: ":9443"})
	srv.SetupRoutes()

	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	w := httptest.NewRecorder()
	srv.Mux().ServeHTTP(w, req)

	headers := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":       "DENY",
		"X-XSS-Protection":      "1; mode=block",
		"Referrer-Policy":       "strict-origin-when-cross-origin",
	}

	for name, expected := range headers {
		got := w.Header().Get(name)
		if got != expected {
			t.Errorf("Header %s = %q, want %q", name, got, expected)
		}
	}

	csp := w.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Error("missing Content-Security-Policy header")
	}
}

func TestServerOverviewPage(t *testing.T) {
	srv := NewServer(&ServerConfig{Addr: ":9443"})
	srv.SetupRoutes()

	req := httptest.NewRequest(http.MethodGet, "/overview", nil)
	w := httptest.NewRecorder()
	srv.Mux().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("GET /overview = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestServerNodePage(t *testing.T) {
	srv := NewServer(&ServerConfig{Addr: ":9443"})
	srv.SetupRoutes()

	req := httptest.NewRequest(http.MethodGet, "/node/test-id", nil)
	w := httptest.NewRecorder()
	srv.Mux().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("GET /node/test-id = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestServerStaticAssets(t *testing.T) {
	srv := NewServer(&ServerConfig{Addr: ":9443"})
	srv.SetupRoutes()

	req := httptest.NewRequest(http.MethodGet, "/static/css/style.css", nil)
	w := httptest.NewRecorder()
	srv.Mux().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("GET /static/css/style.css = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestGenerateSelfSignedCert(t *testing.T) {
	cert, err := generateSelfSignedCert()
	if err != nil {
		t.Fatalf("generateSelfSignedCert: %v", err)
	}

	if len(cert.Certificate) == 0 {
		t.Error("certificate is empty")
	}
	if cert.PrivateKey == nil {
		t.Error("private key is nil")
	}
}
