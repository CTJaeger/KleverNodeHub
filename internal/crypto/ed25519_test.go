package crypto

import (
	"crypto/ed25519"
	"crypto/x509"
	"testing"
)

func TestGenerateEd25519KeyPair(t *testing.T) {
	pub, priv, err := GenerateEd25519KeyPair()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pub) != ed25519.PublicKeySize {
		t.Errorf("public key size = %d, want %d", len(pub), ed25519.PublicKeySize)
	}
	if len(priv) != ed25519.PrivateKeySize {
		t.Errorf("private key size = %d, want %d", len(priv), ed25519.PrivateKeySize)
	}
}

func TestKeyPairPEMRoundTrip(t *testing.T) {
	_, priv, err := GenerateEd25519KeyPair()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	pemData, err := EncodePrivateKeyPEM(priv)
	if err != nil {
		t.Fatalf("encode PEM: %v", err)
	}

	decoded, err := DecodePrivateKeyPEM(pemData)
	if err != nil {
		t.Fatalf("decode PEM: %v", err)
	}

	if !priv.Equal(decoded) {
		t.Error("decoded key does not match original")
	}
}

func TestCreateCACertificate(t *testing.T) {
	pub, priv, err := GenerateEd25519KeyPair()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	certDER, err := CreateCACertificate(pub, priv)
	if err != nil {
		t.Fatalf("create CA cert: %v", err)
	}

	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		t.Fatalf("parse cert: %v", err)
	}

	if !cert.IsCA {
		t.Error("certificate is not marked as CA")
	}
	if cert.Subject.CommonName != "Klever Node Hub CA" {
		t.Errorf("CN = %q, want %q", cert.Subject.CommonName, "Klever Node Hub CA")
	}
}

func TestCertPEMRoundTrip(t *testing.T) {
	pub, priv, err := GenerateEd25519KeyPair()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	certDER, err := CreateCACertificate(pub, priv)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}

	pemData := EncodeCertPEM(certDER)
	decoded, err := DecodeCertPEM(pemData)
	if err != nil {
		t.Fatalf("decode PEM: %v", err)
	}

	if decoded.Subject.CommonName != "Klever Node Hub CA" {
		t.Errorf("CN = %q, want %q", decoded.Subject.CommonName, "Klever Node Hub CA")
	}
}

func TestCreateSignedCertificate(t *testing.T) {
	// Generate CA
	caPub, caPriv, err := GenerateEd25519KeyPair()
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}

	caCertDER, err := CreateCACertificate(caPub, caPriv)
	if err != nil {
		t.Fatalf("create CA cert: %v", err)
	}

	caCert, err := x509.ParseCertificate(caCertDER)
	if err != nil {
		t.Fatalf("parse CA cert: %v", err)
	}

	// Generate agent key
	agentPub, _, err := GenerateEd25519KeyPair()
	if err != nil {
		t.Fatalf("generate agent key: %v", err)
	}

	// Sign agent cert
	agentCertDER, err := CreateSignedCertificate(agentPub, caCert, caPriv, "agent-server1")
	if err != nil {
		t.Fatalf("create signed cert: %v", err)
	}

	agentCert, err := x509.ParseCertificate(agentCertDER)
	if err != nil {
		t.Fatalf("parse agent cert: %v", err)
	}

	if agentCert.Subject.CommonName != "agent-server1" {
		t.Errorf("CN = %q, want %q", agentCert.Subject.CommonName, "agent-server1")
	}

	// Verify signature chain
	roots := x509.NewCertPool()
	roots.AddCert(caCert)

	opts := x509.VerifyOptions{
		Roots:     roots,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}

	if _, err := agentCert.Verify(opts); err != nil {
		t.Errorf("certificate verification failed: %v", err)
	}
}

func TestDecodeCertPEM_Invalid(t *testing.T) {
	_, err := DecodeCertPEM([]byte("not a cert"))
	if err == nil {
		t.Error("expected error for invalid PEM")
	}
}

func TestDecodePrivateKeyPEM_Invalid(t *testing.T) {
	_, err := DecodePrivateKeyPEM([]byte("not a key"))
	if err == nil {
		t.Error("expected error for invalid PEM")
	}
}
