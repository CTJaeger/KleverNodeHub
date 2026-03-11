package crypto

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewCA(t *testing.T) {
	ca, err := NewCA()
	if err != nil {
		t.Fatalf("create CA: %v", err)
	}

	if ca.Certificate == nil {
		t.Fatal("certificate is nil")
	}
	if !ca.Certificate.IsCA {
		t.Error("certificate is not marked as CA")
	}
	if ca.PrivateKey == nil {
		t.Fatal("private key is nil")
	}
	if len(ca.CertPEM) == 0 {
		t.Error("cert PEM is empty")
	}
}

func TestIssueAndVerifyAgentCertificate(t *testing.T) {
	ca, err := NewCA()
	if err != nil {
		t.Fatalf("create CA: %v", err)
	}

	agentPub, _, err := GenerateEd25519KeyPair()
	if err != nil {
		t.Fatalf("generate agent key: %v", err)
	}

	certPEM, err := ca.IssueAgentCertificate(agentPub, "test-agent")
	if err != nil {
		t.Fatalf("issue cert: %v", err)
	}

	cert, err := ca.VerifyAgentCertificate(certPEM)
	if err != nil {
		t.Fatalf("verify cert: %v", err)
	}

	if cert.Subject.CommonName != "test-agent" {
		t.Errorf("CN = %q, want %q", cert.Subject.CommonName, "test-agent")
	}
}

func TestVerifyAgentCertificate_WrongCA(t *testing.T) {
	ca1, err := NewCA()
	if err != nil {
		t.Fatalf("create CA1: %v", err)
	}

	ca2, err := NewCA()
	if err != nil {
		t.Fatalf("create CA2: %v", err)
	}

	agentPub, _, err := GenerateEd25519KeyPair()
	if err != nil {
		t.Fatalf("generate agent key: %v", err)
	}

	// Cert issued by CA1
	certPEM, err := ca1.IssueAgentCertificate(agentPub, "agent")
	if err != nil {
		t.Fatalf("issue cert: %v", err)
	}

	// Verify with CA2 should fail
	_, err = ca2.VerifyAgentCertificate(certPEM)
	if err == nil {
		t.Error("expected error when verifying cert from different CA")
	}
}

func TestSaveAndLoadCA(t *testing.T) {
	dir := t.TempDir()
	caDir := filepath.Join(dir, "ca")

	encKey := make([]byte, 32)
	for i := range encKey {
		encKey[i] = byte(i)
	}

	// Create and save CA
	ca, err := NewCA()
	if err != nil {
		t.Fatalf("create CA: %v", err)
	}

	if err := ca.SaveToDir(caDir, encKey); err != nil {
		t.Fatalf("save CA: %v", err)
	}

	// Verify files exist
	if _, err := os.Stat(filepath.Join(caDir, "ca.crt")); err != nil {
		t.Errorf("ca.crt not found: %v", err)
	}
	if _, err := os.Stat(filepath.Join(caDir, "ca.key.enc")); err != nil {
		t.Errorf("ca.key.enc not found: %v", err)
	}

	// Load CA
	loaded, err := LoadCAFromDir(caDir, encKey)
	if err != nil {
		t.Fatalf("load CA: %v", err)
	}

	if loaded.Certificate.Subject.CommonName != ca.Certificate.Subject.CommonName {
		t.Errorf("loaded CN = %q, want %q", loaded.Certificate.Subject.CommonName, ca.Certificate.Subject.CommonName)
	}

	if !ca.PrivateKey.Equal(loaded.PrivateKey) {
		t.Error("loaded private key does not match original")
	}

	// Issue and verify cert with loaded CA
	agentPub, _, err := GenerateEd25519KeyPair()
	if err != nil {
		t.Fatalf("generate agent key: %v", err)
	}

	certPEM, err := loaded.IssueAgentCertificate(agentPub, "reload-agent")
	if err != nil {
		t.Fatalf("issue cert with loaded CA: %v", err)
	}

	_, err = loaded.VerifyAgentCertificate(certPEM)
	if err != nil {
		t.Fatalf("verify cert with loaded CA: %v", err)
	}
}

func TestLoadCA_WrongKey(t *testing.T) {
	dir := t.TempDir()
	caDir := filepath.Join(dir, "ca")

	encKey := make([]byte, 32)
	wrongKey := make([]byte, 32)
	wrongKey[0] = 0xFF

	ca, err := NewCA()
	if err != nil {
		t.Fatalf("create CA: %v", err)
	}

	if err := ca.SaveToDir(caDir, encKey); err != nil {
		t.Fatalf("save CA: %v", err)
	}

	_, err = LoadCAFromDir(caDir, wrongKey)
	if err == nil {
		t.Error("expected error when loading CA with wrong encryption key")
	}
}
