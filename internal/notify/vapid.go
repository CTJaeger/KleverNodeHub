package notify

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"math/big"
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

	return vapidFromPrivate(privKey), nil
}

// LoadVAPIDKeys reconstructs VAPID keys from stored base64url-encoded strings.
func LoadVAPIDKeys(publicB64, privateB64 string) (*VAPIDKeys, error) {
	privBytes, err := base64.RawURLEncoding.DecodeString(privateB64)
	if err != nil {
		return nil, fmt.Errorf("decode VAPID private key: %w", err)
	}

	privKey := new(ecdsa.PrivateKey)
	privKey.Curve = elliptic.P256()
	privKey.D = new(big.Int).SetBytes(privBytes)
	privKey.PublicKey.X, privKey.PublicKey.Y = privKey.Curve.ScalarBaseMult(privBytes)

	return vapidFromPrivate(privKey), nil
}

func vapidFromPrivate(privKey *ecdsa.PrivateKey) *VAPIDKeys {
	pubBytes := elliptic.Marshal(privKey.Curve, privKey.PublicKey.X, privKey.PublicKey.Y)
	return &VAPIDKeys{
		PrivateKey: privKey,
		PublicKey:  base64.RawURLEncoding.EncodeToString(pubBytes),
		PrivateB64: base64.RawURLEncoding.EncodeToString(privKey.D.Bytes()),
	}
}
