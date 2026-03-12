package dashboard

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io/fs"
	"log"
	"math/big"
	"net/http"
	"time"

	"github.com/CTJaeger/KleverNodeHub/internal/version"
	"github.com/CTJaeger/KleverNodeHub/web"
)

// ServerConfig holds the dashboard HTTP server configuration.
type ServerConfig struct {
	Addr     string // Listen address, e.g. ":9443"
	CertFile string // Path to TLS cert (optional, auto-generates if empty)
	KeyFile  string // Path to TLS key (optional, auto-generates if empty)
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

	// Health endpoint (unauthenticated)
	s.mux.HandleFunc("GET /health", s.handleHealth)

	// Page routes — serve HTML templates
	s.mux.HandleFunc("GET /", s.servePage("templates/login.html"))
	s.mux.HandleFunc("GET /login", s.servePage("templates/login.html"))
	s.mux.HandleFunc("GET /overview", s.servePage("templates/overview.html"))
	s.mux.HandleFunc("GET /node/{id}", s.servePage("templates/node.html"))
	s.mux.HandleFunc("GET /settings", s.servePage("templates/settings.html"))

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
		Status  string       `json:"status"`
		Uptime  string       `json:"uptime"`
		Build   version.Info `json:"build"`
	}{
		Status:  "ok",
		Uptime:  version.Uptime().Round(time.Second).String(),
		Build:   version.Get(),
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
		"default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self' wss: ws:;")
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

	if s.config.CertFile != "" && s.config.KeyFile != "" {
		return srv.ListenAndServeTLS(s.config.CertFile, s.config.KeyFile)
	}

	// Use auto-generated certificate
	return srv.ListenAndServeTLS("", "")
}

// getTLSConfig creates the TLS configuration.
func (s *Server) getTLSConfig() (*tls.Config, error) {
	cfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		CipherSuites: []uint16{
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
			tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
		},
	}

	// Auto-generate self-signed cert if none provided
	if s.config.CertFile == "" || s.config.KeyFile == "" {
		cert, err := generateSelfSignedCert()
		if err != nil {
			return nil, err
		}
		cfg.Certificates = []tls.Certificate{cert}
	}

	return cfg, nil
}

// generateSelfSignedCert creates a self-signed TLS certificate for development/first-run.
func generateSelfSignedCert() (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("generate key: %w", err)
	}

	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))

	template := x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			Organization: []string{"Klever Node Hub"},
			CommonName:   "localhost",
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("create certificate: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("marshal key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	return tls.X509KeyPair(certPEM, keyPEM)
}
