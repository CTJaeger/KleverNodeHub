package crypto

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
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

	serverCert := tls.Certificate{
		Certificate: [][]byte{serverCertDER},
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
