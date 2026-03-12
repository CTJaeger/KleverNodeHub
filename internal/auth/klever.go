package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/sha3"
)

// KleverAuthManager handles Klever Extension wallet authentication.
// It stores a single admin klv1... address and manages challenge-response sessions.
type KleverAuthManager struct {
	mu         sync.Mutex
	address    string
	challenges map[string]*kleverChallenge
}

type kleverChallenge struct {
	nonce     string
	address   string
	expiresAt time.Time
}

const (
	challengeTTL   = 5 * time.Minute
	challengeBytes = 32
	kleverHRP      = "klv"
	kleverAddrLen  = 32 // 32-byte Ed25519 public key
)

var (
	ErrNoKleverAddress  = errors.New("no klever admin address configured")
	ErrInvalidAddress   = errors.New("invalid klever address format")
	ErrChallengeExpired = errors.New("challenge expired or not found")
	ErrAddressMismatch  = errors.New("address does not match registered admin")
	ErrInvalidSignature = errors.New("invalid signature")
)

// NewKleverAuthManager creates a new KleverAuthManager with an optional stored address.
func NewKleverAuthManager(storedAddress string) *KleverAuthManager {
	return &KleverAuthManager{
		address:    storedAddress,
		challenges: make(map[string]*kleverChallenge),
	}
}

// SetAddress registers a klv1... address as the admin wallet.
func (k *KleverAuthManager) SetAddress(address string) error {
	if !isValidKleverAddress(address) {
		return ErrInvalidAddress
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	k.address = address
	return nil
}

// RemoveAddress removes the registered admin address.
func (k *KleverAuthManager) RemoveAddress() {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.address = ""
}

// HasAddress returns whether an admin address is configured.
func (k *KleverAuthManager) HasAddress() bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.address != ""
}

// Address returns the registered admin address.
func (k *KleverAuthManager) Address() string {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.address
}

// CreateChallenge generates a random nonce for the given address.
// Returns the hex-encoded nonce.
func (k *KleverAuthManager) CreateChallenge(address string) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()

	if k.address == "" {
		return "", ErrNoKleverAddress
	}
	if !strings.EqualFold(address, k.address) {
		return "", ErrAddressMismatch
	}

	// Generate random nonce
	nonceBytes := make([]byte, challengeBytes)
	if _, err := rand.Read(nonceBytes); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}
	nonce := hex.EncodeToString(nonceBytes)

	// Clean expired challenges
	now := time.Now()
	for id, ch := range k.challenges {
		if now.After(ch.expiresAt) {
			delete(k.challenges, id)
		}
	}

	k.challenges[nonce] = &kleverChallenge{
		nonce:     nonce,
		address:   address,
		expiresAt: now.Add(challengeTTL),
	}

	return nonce, nil
}

// CreateSetupChallenge generates a nonce for any valid klv1... address (no registered address required).
// Used during wallet linking in settings to prove ownership before saving.
func (k *KleverAuthManager) CreateSetupChallenge(address string) (string, error) {
	if !isValidKleverAddress(address) {
		return "", ErrInvalidAddress
	}

	k.mu.Lock()
	defer k.mu.Unlock()

	nonceBytes := make([]byte, challengeBytes)
	if _, err := rand.Read(nonceBytes); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}
	nonce := hex.EncodeToString(nonceBytes)

	now := time.Now()
	for id, ch := range k.challenges {
		if now.After(ch.expiresAt) {
			delete(k.challenges, id)
		}
	}

	k.challenges[nonce] = &kleverChallenge{
		nonce:     nonce,
		address:   address,
		expiresAt: now.Add(challengeTTL),
	}

	return nonce, nil
}

// VerifySetupSignature verifies a signature for wallet linking (no registered address check).
// On success, the caller should save the address.
func (k *KleverAuthManager) VerifySetupSignature(address, nonce, signature string) error {
	k.mu.Lock()
	ch, ok := k.challenges[nonce]
	if !ok || time.Now().After(ch.expiresAt) {
		delete(k.challenges, nonce)
		k.mu.Unlock()
		return ErrChallengeExpired
	}
	if !strings.EqualFold(ch.address, address) {
		k.mu.Unlock()
		return ErrAddressMismatch
	}
	delete(k.challenges, nonce)
	k.mu.Unlock()

	return verifyEd25519Signature(address, nonce, signature)
}

// VerifySignature verifies an Ed25519 signature from the Klever Extension.
// The signature should be hex-encoded (128 hex chars = 64 bytes).
func (k *KleverAuthManager) VerifySignature(address, nonce, signature string) error {
	k.mu.Lock()
	ch, ok := k.challenges[nonce]
	if !ok || time.Now().After(ch.expiresAt) {
		delete(k.challenges, nonce)
		k.mu.Unlock()
		return ErrChallengeExpired
	}
	if !strings.EqualFold(ch.address, address) || !strings.EqualFold(k.address, address) {
		k.mu.Unlock()
		return ErrAddressMismatch
	}
	// Consume challenge (single-use)
	delete(k.challenges, nonce)
	k.mu.Unlock()

	return verifyEd25519Signature(address, nonce, signature)
}

// kleverSignedMessageHash builds the Klever Extension signed message hash.
// Format: 0x17 + "Klever Signed Message:\n" + len(message) + message → Keccak-256
func kleverSignedMessageHash(message string) []byte {
	prefix := append([]byte{0x17}, []byte("Klever Signed Message:\n")...)
	lenStr := fmt.Sprintf("%d", len(message))
	full := append(prefix, []byte(lenStr)...)
	full = append(full, []byte(message)...)

	h := sha3.NewLegacyKeccak256()
	h.Write(full)
	return h.Sum(nil)
}

// verifyEd25519Signature verifies an Ed25519 signature against a klv1... address.
// The Klever Extension prepends a prefix and hashes with Keccak-256 before signing.
func verifyEd25519Signature(address, message, signature string) error {
	pubKey, err := kleverAddressToPublicKey(address)
	if err != nil {
		return fmt.Errorf("decode address: %w", err)
	}

	sigBytes, err := hex.DecodeString(signature)
	if err != nil || len(sigBytes) != ed25519.SignatureSize {
		return ErrInvalidSignature
	}

	hash := kleverSignedMessageHash(message)
	if !ed25519.Verify(pubKey, hash, sigBytes) {
		return ErrInvalidSignature
	}

	return nil
}

// isValidKleverAddress checks basic format of a klv1... address.
func isValidKleverAddress(address string) bool {
	if !strings.HasPrefix(address, "klv1") {
		return false
	}
	if len(address) != 62 {
		return false
	}
	// Check bech32 charset
	for _, c := range address[4:] {
		if !isBech32Char(c) {
			return false
		}
	}
	return true
}

// kleverAddressToPublicKey decodes a klv1... bech32 address to Ed25519 public key bytes.
func kleverAddressToPublicKey(address string) (ed25519.PublicKey, error) {
	if !strings.HasPrefix(address, kleverHRP+"1") {
		return nil, ErrInvalidAddress
	}

	data := address[len(kleverHRP)+1:] // skip "klv1"
	decoded, err := bech32Decode5to8(data)
	if err != nil {
		return nil, err
	}

	if len(decoded) != kleverAddrLen {
		return nil, fmt.Errorf("expected %d bytes, got %d", kleverAddrLen, len(decoded))
	}

	return ed25519.PublicKey(decoded), nil
}

// bech32Decode5to8 converts bech32 characters (5-bit groups) to 8-bit bytes.
func bech32Decode5to8(data string) ([]byte, error) {
	// Convert characters to 5-bit values
	values := make([]byte, len(data))
	for i, c := range data {
		v, ok := bech32CharToValue(c)
		if !ok {
			return nil, fmt.Errorf("invalid bech32 character at position %d", i)
		}
		values[i] = v
	}

	// Strip the 6-byte checksum
	if len(values) < 7 {
		return nil, fmt.Errorf("bech32 data too short")
	}
	values = values[:len(values)-6]

	// Convert from 5-bit groups to 8-bit bytes
	return convertBits(values, 5, 8, false)
}

// convertBits converts between bit groupings (e.g., 5-bit to 8-bit).
func convertBits(data []byte, fromBits, toBits uint, pad bool) ([]byte, error) {
	acc := uint32(0)
	bits := uint(0)
	maxv := uint32((1 << toBits) - 1)
	var result []byte

	for _, b := range data {
		acc = (acc << fromBits) | uint32(b)
		bits += fromBits
		for bits >= toBits {
			bits -= toBits
			result = append(result, byte((acc>>bits)&maxv))
		}
	}

	if pad {
		if bits > 0 {
			result = append(result, byte((acc<<(toBits-bits))&maxv))
		}
	} else if bits >= fromBits {
		return nil, fmt.Errorf("invalid padding")
	} else if (acc<<(toBits-bits))&maxv != 0 {
		return nil, fmt.Errorf("non-zero padding")
	}

	return result, nil
}

const bech32Charset = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"

func bech32CharToValue(c rune) (byte, bool) {
	idx := strings.IndexRune(bech32Charset, c)
	if idx < 0 {
		return 0, false
	}
	return byte(idx), true
}

func isBech32Char(c rune) bool {
	return strings.ContainsRune(bech32Charset, c)
}
