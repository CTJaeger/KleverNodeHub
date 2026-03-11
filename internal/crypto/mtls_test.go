package crypto

import (
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestDashboardTLSConfig(t *testing.T) {
	ca, err := NewCA()
	if err != nil {
		t.Fatalf("create CA: %v", err)
	}

	config, err := DashboardTLSConfig(ca)
	if err != nil {
		t.Fatalf("create TLS config: %v", err)
	}

	if config.MinVersion != tls.VersionTLS13 {
		t.Error("min version should be TLS 1.3")
	}
	if config.ClientAuth != tls.RequireAndVerifyClientCert {
		t.Error("should require client certs")
	}
	if len(config.Certificates) != 1 {
		t.Errorf("expected 1 server cert, got %d", len(config.Certificates))
	}
}

func TestAgentTLSConfig(t *testing.T) {
	ca, err := NewCA()
	if err != nil {
		t.Fatalf("create CA: %v", err)
	}

	agentPub, agentPriv, err := GenerateEd25519KeyPair()
	if err != nil {
		t.Fatalf("generate agent key: %v", err)
	}

	agentCertPEM, err := ca.IssueAgentCertificate(agentPub, "test-agent")
	if err != nil {
		t.Fatalf("issue agent cert: %v", err)
	}

	agentKeyPEM, err := EncodePrivateKeyPEM(agentPriv)
	if err != nil {
		t.Fatalf("encode agent key: %v", err)
	}

	config, err := AgentTLSConfig(agentCertPEM, agentKeyPEM, ca.CertPEM)
	if err != nil {
		t.Fatalf("create agent TLS config: %v", err)
	}

	if config.MinVersion != tls.VersionTLS13 {
		t.Error("min version should be TLS 1.3")
	}
	if len(config.Certificates) != 1 {
		t.Errorf("expected 1 client cert, got %d", len(config.Certificates))
	}
}

func TestMTLSHandshake(t *testing.T) {
	// Create CA
	ca, err := NewCA()
	if err != nil {
		t.Fatalf("create CA: %v", err)
	}

	// Create server TLS config
	serverConfig, err := DashboardTLSConfig(ca)
	if err != nil {
		t.Fatalf("create server TLS: %v", err)
	}

	// Create agent key and cert
	agentPub, agentPriv, err := GenerateEd25519KeyPair()
	if err != nil {
		t.Fatalf("generate agent key: %v", err)
	}

	agentCertPEM, err := ca.IssueAgentCertificate(agentPub, "test-agent")
	if err != nil {
		t.Fatalf("issue agent cert: %v", err)
	}

	agentKeyPEM, err := EncodePrivateKeyPEM(agentPriv)
	if err != nil {
		t.Fatalf("encode agent key: %v", err)
	}

	clientConfig, err := AgentTLSConfig(agentCertPEM, agentKeyPEM, ca.CertPEM)
	if err != nil {
		t.Fatalf("create agent TLS: %v", err)
	}
	// Skip server name verification for localhost test
	clientConfig.InsecureSkipVerify = true

	// Start TLS server
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	tlsListener := tls.NewListener(listener, serverConfig)

	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Verify client cert was presented
			if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
				http.Error(w, "no client cert", http.StatusUnauthorized)
				return
			}
			fmt.Fprintf(w, "hello %s", r.TLS.PeerCertificates[0].Subject.CommonName)
		}),
	}

	go server.Serve(tlsListener)
	defer server.Close()

	// Give server time to start
	time.Sleep(50 * time.Millisecond)

	// Connect as agent
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: clientConfig,
		},
	}

	resp, err := client.Get(fmt.Sprintf("https://%s/", listener.Addr().String()))
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "hello test-agent" {
		t.Errorf("body = %q, want %q", string(body), "hello test-agent")
	}
}

func TestMTLSHandshake_NoClientCert(t *testing.T) {
	ca, err := NewCA()
	if err != nil {
		t.Fatalf("create CA: %v", err)
	}

	serverConfig, err := DashboardTLSConfig(ca)
	if err != nil {
		t.Fatalf("create server TLS: %v", err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	tlsListener := tls.NewListener(listener, serverConfig)
	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("should not reach here"))
		}),
	}
	go server.Serve(tlsListener)
	defer server.Close()

	time.Sleep(50 * time.Millisecond)

	// Client without any cert
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		},
	}

	_, err = client.Get(fmt.Sprintf("https://%s/", listener.Addr().String()))
	if err == nil {
		t.Error("expected TLS handshake to fail without client cert")
	}
}

func TestMTLSHandshake_WrongCA(t *testing.T) {
	ca1, err := NewCA()
	if err != nil {
		t.Fatalf("create CA1: %v", err)
	}

	ca2, err := NewCA()
	if err != nil {
		t.Fatalf("create CA2: %v", err)
	}

	serverConfig, err := DashboardTLSConfig(ca1)
	if err != nil {
		t.Fatalf("create server TLS: %v", err)
	}

	// Agent cert signed by different CA
	agentPub, agentPriv, err := GenerateEd25519KeyPair()
	if err != nil {
		t.Fatalf("generate agent key: %v", err)
	}

	agentCertPEM, err := ca2.IssueAgentCertificate(agentPub, "rogue-agent")
	if err != nil {
		t.Fatalf("issue rogue cert: %v", err)
	}

	agentKeyPEM, err := EncodePrivateKeyPEM(agentPriv)
	if err != nil {
		t.Fatalf("encode agent key: %v", err)
	}

	clientConfig, err := AgentTLSConfig(agentCertPEM, agentKeyPEM, ca2.CertPEM)
	if err != nil {
		t.Fatalf("create agent TLS: %v", err)
	}
	clientConfig.InsecureSkipVerify = true

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	tlsListener := tls.NewListener(listener, serverConfig)
	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("should not reach here"))
		}),
	}
	go server.Serve(tlsListener)
	defer server.Close()

	time.Sleep(50 * time.Millisecond)

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: clientConfig,
		},
	}

	_, err = client.Get(fmt.Sprintf("https://%s/", listener.Addr().String()))
	if err == nil {
		t.Error("expected TLS handshake to fail with cert from wrong CA")
	}
}
