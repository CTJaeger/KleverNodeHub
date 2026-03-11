package crypto

import (
	"crypto/ed25519"
	"crypto/x509"
	"fmt"
	"os"
	"path/filepath"
)

// CA represents the certificate authority for the dashboard.
type CA struct {
	Certificate *x509.Certificate
	CertPEM     []byte
	PrivateKey  ed25519.PrivateKey
}

// NewCA generates a new certificate authority with a fresh Ed25519 key pair.
func NewCA() (*CA, error) {
	pub, priv, err := GenerateEd25519KeyPair()
	if err != nil {
		return nil, fmt.Errorf("generate CA keys: %w", err)
	}

	certDER, err := CreateCACertificate(pub, priv)
	if err != nil {
		return nil, fmt.Errorf("create CA cert: %w", err)
	}

	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return nil, fmt.Errorf("parse CA cert: %w", err)
	}

	certPEM := EncodeCertPEM(certDER)

	return &CA{
		Certificate: cert,
		CertPEM:     certPEM,
		PrivateKey:  priv,
	}, nil
}

// IssueAgentCertificate creates and signs a certificate for an agent.
func (ca *CA) IssueAgentCertificate(agentPub ed25519.PublicKey, agentName string) (certPEM []byte, err error) {
	certDER, err := CreateSignedCertificate(agentPub, ca.Certificate, ca.PrivateKey, agentName)
	if err != nil {
		return nil, fmt.Errorf("issue agent cert: %w", err)
	}

	return EncodeCertPEM(certDER), nil
}

// VerifyAgentCertificate verifies that an agent certificate was signed by this CA.
func (ca *CA) VerifyAgentCertificate(certPEM []byte) (*x509.Certificate, error) {
	cert, err := DecodeCertPEM(certPEM)
	if err != nil {
		return nil, fmt.Errorf("decode agent cert: %w", err)
	}

	roots := x509.NewCertPool()
	roots.AddCert(ca.Certificate)

	opts := x509.VerifyOptions{
		Roots:     roots,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}

	if _, err := cert.Verify(opts); err != nil {
		return nil, fmt.Errorf("verify agent cert: %w", err)
	}

	return cert, nil
}

// SaveToDir saves the CA certificate and encrypted private key to a directory.
func (ca *CA) SaveToDir(dir string, encryptionKey []byte) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create CA dir: %w", err)
	}

	// Save certificate (not secret)
	certPath := filepath.Join(dir, "ca.crt")
	if err := os.WriteFile(certPath, ca.CertPEM, 0644); err != nil {
		return fmt.Errorf("write CA cert: %w", err)
	}

	// Encrypt and save private key
	keyPEM, err := EncodePrivateKeyPEM(ca.PrivateKey)
	if err != nil {
		return fmt.Errorf("encode CA key: %w", err)
	}

	encryptedKey, err := Encrypt(keyPEM, encryptionKey)
	if err != nil {
		return fmt.Errorf("encrypt CA key: %w", err)
	}

	keyPath := filepath.Join(dir, "ca.key.enc")
	if err := os.WriteFile(keyPath, encryptedKey, 0600); err != nil {
		return fmt.Errorf("write CA key: %w", err)
	}

	return nil
}

// LoadCAFromDir loads a CA from a directory with an encrypted private key.
func LoadCAFromDir(dir string, encryptionKey []byte) (*CA, error) {
	// Read certificate
	certPath := filepath.Join(dir, "ca.crt")
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, fmt.Errorf("read CA cert: %w", err)
	}

	cert, err := DecodeCertPEM(certPEM)
	if err != nil {
		return nil, fmt.Errorf("decode CA cert: %w", err)
	}

	// Read and decrypt private key
	keyPath := filepath.Join(dir, "ca.key.enc")
	encryptedKey, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("read CA key: %w", err)
	}

	keyPEM, err := Decrypt(encryptedKey, encryptionKey)
	if err != nil {
		return nil, fmt.Errorf("decrypt CA key: %w", err)
	}

	priv, err := DecodePrivateKeyPEM(keyPEM)
	if err != nil {
		return nil, fmt.Errorf("decode CA key: %w", err)
	}

	return &CA{
		Certificate: cert,
		CertPEM:     certPEM,
		PrivateKey:  priv,
	}, nil
}
