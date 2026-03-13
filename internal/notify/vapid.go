package notify

import (
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"fmt"
)

// VAPIDKeys holds a VAPID key pair for Web Push.
type VAPIDKeys struct {
	PrivateKey *ecdsa.PrivateKey
	PublicKey  string // base64url-encoded uncompressed public key (65 bytes)
	PrivateB64 string // base64url-encoded private key (32 bytes)
}

// GenerateVAPIDKeys creates a new P-256 ECDSA key pair for VAPID.
func GenerateVAPIDKeys() (*VAPIDKeys, error) {
	privKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate VAPID key: %w", err)
	}

	return vapidFromPrivate(privKey)
}

// LoadVAPIDKeys reconstructs VAPID keys from stored base64url-encoded strings.
func LoadVAPIDKeys(publicB64, privateB64 string) (*VAPIDKeys, error) {
	privBytes, err := base64.RawURLEncoding.DecodeString(privateB64)
	if err != nil {
		return nil, fmt.Errorf("decode VAPID private key: %w", err)
	}

	// Use crypto/ecdh to reconstruct the key (avoids deprecated big.Int fields)
	ecdhPriv, err := ecdh.P256().NewPrivateKey(privBytes)
	if err != nil {
		return nil, fmt.Errorf("parse VAPID private key: %w", err)
	}

	// Convert ecdh → ecdsa via PKIX/PKCS8 round-trip
	ecdsaPriv, err := ecdhToECDSA(ecdhPriv)
	if err != nil {
		return nil, fmt.Errorf("convert VAPID key: %w", err)
	}

	return vapidFromPrivate(ecdsaPriv)
}

func vapidFromPrivate(privKey *ecdsa.PrivateKey) (*VAPIDKeys, error) {
	// Use crypto/ecdh for encoding (avoids deprecated elliptic.Marshal and D field)
	ecdhPriv, err := privKey.ECDH()
	if err != nil {
		return nil, fmt.Errorf("convert to ecdh: %w", err)
	}

	pubBytes := ecdhPriv.PublicKey().Bytes()
	privBytes := ecdhPriv.Bytes()

	return &VAPIDKeys{
		PrivateKey: privKey,
		PublicKey:  base64.RawURLEncoding.EncodeToString(pubBytes),
		PrivateB64: base64.RawURLEncoding.EncodeToString(privBytes),
	}, nil
}

// ecdhToECDSA converts an ecdh.PrivateKey to ecdsa.PrivateKey via PKCS8 round-trip.
func ecdhToECDSA(ecdhKey *ecdh.PrivateKey) (*ecdsa.PrivateKey, error) {
	der, err := x509.MarshalPKCS8PrivateKey(ecdhKey)
	if err != nil {
		return nil, err
	}
	parsed, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, err
	}
	ecdsaKey, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("unexpected key type after PKCS8 round-trip")
	}
	return ecdsaKey, nil
}
