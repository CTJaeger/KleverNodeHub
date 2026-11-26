package crypto

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
)

// DashboardTLSConfig creates a TLS config for the dashboard server.
// It requires client certificates signed by the CA (mTLS).
// The server cert uses ECDSA P-256 for broad browser compatibility,
// while still being signed by the Ed25519 CA.
func DashboardTLSConfig(ca *CA) (*tls.Config, error) {
	// Use ECDSA P-256 for server cert (browsers don't all support Ed25519 in TLS)
	serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate server key: %w", err)
	}

	serverCertDER, err := createServerCertificate(serverKey.Public(), ca.Certificate, ca.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("create server cert: %w", err)
	}

	// The CA goes into the chain so a new agent can check it against the
	// fingerprint from its install command before sending its token.
	serverCert := tls.Certificate{
		Certificate: [][]byte{serverCertDER, ca.Certificate.Raw},
		PrivateKey:  serverKey,
	}

	// CA cert pool for verifying client certs
	clientCAs := x509.NewCertPool()
	clientCAs.AddCert(ca.Certificate)

	return &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    clientCAs,
		MinVersion:   tls.VersionTLS13,
	}, nil
}

// Fingerprint returns the SHA-256 hex digest of a certificate (DER), the form
// used for --ca-fingerprint.
func Fingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(sum[:])
}

// AgentTLSConfig creates a TLS config for the agent client.
// It presents the agent's client certificate and verifies the dashboard's server cert.
func AgentTLSConfig(agentCertPEM, agentKeyPEM, caCertPEM []byte) (*tls.Config, error) {
	// Parse agent certificate and key
	agentCert, err := DecodeCertPEM(agentCertPEM)
	if err != nil {
		return nil, fmt.Errorf("decode agent cert: %w", err)
	}

	agentKey, err := DecodePrivateKeyPEM(agentKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("decode agent key: %w", err)
	}

	tlsCert := tls.Certificate{
		Certificate: [][]byte{agentCert.Raw},
		PrivateKey:  agentKey,
	}

	// CA cert pool for verifying server cert
	rootCAs := x509.NewCertPool()
	if !rootCAs.AppendCertsFromPEM(caCertPEM) {
		return nil, fmt.Errorf("failed to add CA cert to pool")
	}

	return &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
		RootCAs:      rootCAs,
		MinVersion:   tls.VersionTLS13,
	}, nil
}

func createServerCertificate(
	serverPub interface{},
	caCert *x509.Certificate,
	caPriv ed25519.PrivateKey,
) ([]byte, error) {
	serialNumber, err := generateSerialNumber()
	if err != nil {
		return nil, err
	}

	template := &x509.Certificate{
		SerialNumber:          serialNumber,
		Subject:               caCert.Subject,
		NotBefore:             caCert.NotBefore,
		NotAfter:              caCert.NotAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, caCert, serverPub, caPriv)
	if err != nil {
		return nil, fmt.Errorf("create server certificate: %w", err)
	}

	return certDER, nil
}

// RandomHex returns n random bytes as hex.
func RandomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
