package agent

import (
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/CTJaeger/KleverNodeHub/internal/crypto"
)

// TestTLSConfig_VerifiesDashboardCert proves Agent.TLSConfig actually
// verifies the dashboard's server cert against the stored CA — i.e. an
// imposter dashboard not signed by that CA gets rejected, even when it
// presents a valid-looking cert with the right SAN.
func TestTLSConfig_VerifiesDashboardCert(t *testing.T) {
	// Real CA + real dashboard, agent should connect.
	caGood, err := crypto.NewCA()
	if err != nil {
		t.Fatalf("ca: %v", err)
	}
	serverCfg, err := crypto.DashboardTLSConfig(caGood)
	if err != nil {
		t.Fatalf("dashboard tls: %v", err)
	}

	agentPub, agentPriv, err := crypto.GenerateEd25519KeyPair()
	if err != nil {
		t.Fatalf("agent keys: %v", err)
	}
	agentCertPEM, err := caGood.IssueAgentCertificate(agentPub, "test-agent")
	if err != nil {
		t.Fatalf("issue agent cert: %v", err)
	}
	agentKeyPEM, err := crypto.EncodePrivateKeyPEM(agentPriv)
	if err != nil {
		t.Fatalf("encode agent key: %v", err)
	}

	addr := startMTLSEcho(t, serverCfg)

	// Build the agent config from the same CA — should connect cleanly.
	a := New(t.TempDir())
	a.config = &Config{
		ServerID:  "srv-1",
		CertPEM:   string(agentCertPEM),
		KeyPEM:    string(agentKeyPEM),
		CACertPEM: string(caGood.CertPEM),
	}
	cfg, err := a.TLSConfig()
	if err != nil {
		t.Fatalf("agent TLSConfig: %v", err)
	}

	if err := dialAndGet(addr, cfg); err != nil {
		t.Fatalf("expected handshake to succeed with trusted CA, got: %v", err)
	}

	// Now stand up a SECOND dashboard signed by a DIFFERENT CA, and try to
	// connect with the ORIGINAL agent config — agent should reject.
	caEvil, err := crypto.NewCA()
	if err != nil {
		t.Fatalf("evil ca: %v", err)
	}
	evilServerCfg, err := crypto.DashboardTLSConfig(caEvil)
	if err != nil {
		t.Fatalf("evil dashboard tls: %v", err)
	}
	evilAddr := startMTLSEcho(t, evilServerCfg)

	// Even though evilServerCfg's cert has the right SAN ("localhost"),
	// it's signed by a different CA. Verification must fail.
	if err := dialAndGet(evilAddr, cfg); err == nil {
		t.Fatal("expected handshake to FAIL against imposter CA, but it succeeded")
	} else if !strings.Contains(err.Error(), "signed by unknown authority") &&
		!strings.Contains(err.Error(), "unknown certificate authority") {
		t.Logf("(got expected failure, message: %v)", err)
	}
}

func startMTLSEcho(t *testing.T, cfg *tls.Config) string {
	t.Helper()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", cfg)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "ok")
		}),
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return ln.Addr().String()
}

func dialAndGet(addr string, cfg *tls.Config) error {
	d := &net.Dialer{Timeout: 3 * time.Second}
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: cfg,
			DialContext:     d.DialContext,
		},
		Timeout: 3 * time.Second,
	}
	resp, err := client.Get("https://" + addr + "/")
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	return nil
}
