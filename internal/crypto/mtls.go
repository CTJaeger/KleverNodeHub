package crypto

import (
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"fmt"
)

// DashboardTLSConfig creates a TLS config for the dashboard server.
// It requires client certificates signed by the CA (mTLS).
func DashboardTLSConfig(ca *CA) (*tls.Config, error) {
	// Create server certificate (self-signed by CA for TLS)
	serverPub, serverPriv, err := GenerateEd25519KeyPair()
	if err != nil {
		return nil, fmt.Errorf("generate server key: %w", err)
	}

	serverCertDER, err := createServerCertificate(serverPub, ca.Certificate, ca.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("create server cert: %w", err)
	}

	serverKeyDER, err := x509.MarshalPKCS8PrivateKey(serverPriv)
	if err != nil {
		return nil, fmt.Errorf("marshal server key: %w", err)
	}

	serverCert := tls.Certificate{
		Certificate: [][]byte{serverCertDER},
		PrivateKey:  serverPriv,
		Leaf:        nil,
	}
	// Set the raw DER for the certificate chain
	_ = serverKeyDER // key is already in serverCert.PrivateKey

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
	serverPub ed25519.PublicKey,
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
	}

	certDER, err := x509.CreateCertificate(nil, template, caCert, serverPub, caPriv)
	if err != nil {
		return nil, fmt.Errorf("create server certificate: %w", err)
	}

	return certDER, nil
}
