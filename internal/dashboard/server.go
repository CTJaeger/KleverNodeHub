package dashboard

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"time"

	kvcrypto "github.com/CTJaeger/KleverNodeHub/internal/crypto"
	"github.com/CTJaeger/KleverNodeHub/internal/version"
	"github.com/CTJaeger/KleverNodeHub/web"
)

// ServerConfig holds the dashboard HTTP server configuration.
type ServerConfig struct {
	Addr string       // Listen address, e.g. ":9443"
	CA   *kvcrypto.CA // CA for mTLS (if nil, uses self-signed cert)
}

// Server is the main dashboard HTTP server.
type Server struct {
	config *ServerConfig
	mux    *http.ServeMux
}

// NewServer creates a new dashboard server.
func NewServer(config *ServerConfig) *Server {
	if config.Addr == "" {
		config.Addr = ":9443"
	}
	return &Server{
		config: config,
		mux:    http.NewServeMux(),
	}
}

// Mux returns the underlying ServeMux for registering additional routes.
func (s *Server) Mux() *http.ServeMux {
	return s.mux
}

// SetupRoutes configures all HTTP routes including static assets and templates.
func (s *Server) SetupRoutes() error {
	// Static assets
	staticFS, err := fs.Sub(web.StaticFS, "static")
	if err != nil {
		return fmt.Errorf("load static assets: %w", err)
	}
	s.mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))

	// PWA: serve manifest and service worker from root scope
	s.mux.HandleFunc("GET /manifest.json", func(w http.ResponseWriter, _ *http.Request) {
		data, err := web.StaticFS.ReadFile("static/manifest.json")
		if err != nil {
			http.NotFound(w, nil)
			return
		}
		w.Header().Set("Content-Type", "application/manifest+json")
		_, _ = w.Write(data)
	})
	s.mux.HandleFunc("GET /sw.js", func(w http.ResponseWriter, _ *http.Request) {
		data, err := web.StaticFS.ReadFile("static/sw.js")
		if err != nil {
			http.NotFound(w, nil)
			return
		}
		w.Header().Set("Content-Type", "application/javascript")
		w.Header().Set("Service-Worker-Allowed", "/")
		_, _ = w.Write(data)
	})

	// Health endpoint (unauthenticated)
	s.mux.HandleFunc("GET /health", s.handleHealth)

	// Page routes — serve HTML templates
	s.mux.HandleFunc("GET /", s.servePage("templates/login.html"))
	s.mux.HandleFunc("GET /login", s.servePage("templates/login.html"))
	s.mux.HandleFunc("GET /overview", s.servePage("templates/overview.html"))
	s.mux.HandleFunc("GET /node/{id}", s.servePage("templates/node.html"))
	s.mux.HandleFunc("GET /servers/{id}", s.servePage("templates/server.html"))
	s.mux.HandleFunc("GET /settings", s.servePage("templates/settings.html"))
	s.mux.HandleFunc("GET /alerts", s.servePage("templates/alerts.html"))

	return nil
}

// servePage returns a handler that serves an embedded HTML template.
func (s *Server) servePage(templatePath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.setSecurityHeaders(w)

		tmpl, err := web.StaticFS.ReadFile(templatePath)
		if err != nil {
			http.Error(w, "page not found", http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(tmpl)
	}
}

// handleHealth returns build info and uptime. Unauthenticated, used for monitoring.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	resp := struct {
		Status string       `json:"status"`
		Uptime string       `json:"uptime"`
		Build  version.Info `json:"build"`
	}{
		Status: "ok",
		Uptime: version.Uptime().Round(time.Second).String(),
		Build:  version.Get(),
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// setSecurityHeaders adds security headers to the response.
func (s *Server) setSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("X-XSS-Protection", "1; mode=block")
	w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
	w.Header().Set("Content-Security-Policy",
		"default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; font-src 'self' https://fonts.gstatic.com; img-src 'self' data:; connect-src 'self' wss: ws:;")
}

// SecurityHeadersMiddleware wraps a handler with security headers.
func (s *Server) SecurityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.setSecurityHeaders(w)
		next.ServeHTTP(w, r)
	})
}

// Start starts the HTTPS server.
func (s *Server) Start() error {
	tlsConfig, err := s.getTLSConfig()
	if err != nil {
		return fmt.Errorf("TLS setup: %w", err)
	}

	srv := &http.Server{
		Addr:         s.config.Addr,
		Handler:      s.SecurityHeadersMiddleware(s.mux),
		TLSConfig:    tlsConfig,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	log.Printf("Dashboard starting on https://localhost%s", s.config.Addr)

	// Certs are already in the TLS config
	return srv.ListenAndServeTLS("", "")
}

// getTLSConfig creates the TLS configuration.
// When a CA is provided, the server cert is signed by the CA so agents can verify it.
// Browser connections skip mTLS client auth (agents use mTLS on the /ws/agent path).
func (s *Server) getTLSConfig() (*tls.Config, error) {
	if s.config.CA != nil {
		tlsCfg, err := kvcrypto.DashboardTLSConfig(s.config.CA)
		if err != nil {
			return nil, fmt.Errorf("dashboard TLS from CA: %w", err)
		}
		// Allow browsers without client certs (agents still present theirs)
		tlsCfg.ClientAuth = tls.RequestClientCert
		return tlsCfg, nil
	}

	// No CA available — this shouldn't happen in normal operation
	return &tls.Config{
		MinVersion: tls.VersionTLS13,
	}, nil
}
