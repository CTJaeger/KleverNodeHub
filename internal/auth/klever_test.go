package auth

import (
	"crypto/ed25519"
	"encoding/hex"
	"strings"
	"testing"
)

func TestKleverAddressValidation(t *testing.T) {
	tests := []struct {
		addr  string
		valid bool
	}{
		{"klv1qqqqqqqqqqqqqpgq0khua5d0eqte54nyelum6veygkl6unjc64fsu2fxfp", true},
		{"klv1v9mcl3m25tymxzs2tu3zztp7y5ceg3yy9z2glj5wykywanf564fsd5djjt", true},
		{"", false},
		{"klv1short", false},
		{"btc1qqqqqqqqqqqqqpgq0khua5d0eqte54nyelum6veygkl6unjc64fsu2fxfp", false}, // wrong prefix
	}
	for _, tt := range tests {
		if got := isValidKleverAddress(tt.addr); got != tt.valid {
			t.Errorf("isValidKleverAddress(%q) = %v, want %v", tt.addr, got, tt.valid)
		}
	}
}

func TestKleverSetAndGetAddress(t *testing.T) {
	km := NewKleverAuthManager("")
	if km.HasAddress() {
		t.Error("expected no address initially")
	}

	// Invalid address
	if err := km.SetAddress("invalid"); err != ErrInvalidAddress {
		t.Errorf("expected ErrInvalidAddress, got %v", err)
	}

	// Valid address
	addr := "klv1qqqqqqqqqqqqqpgq0khua5d0eqte54nyelum6veygkl6unjc64fsu2fxfp"
	if err := km.SetAddress(addr); err != nil {
		t.Fatalf("SetAddress: %v", err)
	}
	if !km.HasAddress() {
		t.Error("expected address after set")
	}
	if km.Address() != addr {
		t.Errorf("Address() = %q, want %q", km.Address(), addr)
	}

	// Remove
	km.RemoveAddress()
	if km.HasAddress() {
		t.Error("expected no address after remove")
	}
}

func TestKleverFromStored(t *testing.T) {
	addr := "klv1qqqqqqqqqqqqqpgq0khua5d0eqte54nyelum6veygkl6unjc64fsu2fxfp"
	km := NewKleverAuthManager(addr)
	if !km.HasAddress() {
		t.Error("expected address from stored")
	}
	if km.Address() != addr {
		t.Errorf("Address() = %q, want %q", km.Address(), addr)
	}
}

func TestKleverChallengeNoAddress(t *testing.T) {
	km := NewKleverAuthManager("")
	_, err := km.CreateChallenge("klv1qqqqqqqqqqqqqpgq0khua5d0eqte54nyelum6veygkl6unjc64fsu2fxfp")
	if err != ErrNoKleverAddress {
		t.Errorf("expected ErrNoKleverAddress, got %v", err)
	}
}

func TestKleverChallengeMismatch(t *testing.T) {
	km := NewKleverAuthManager("klv1qqqqqqqqqqqqqpgq0khua5d0eqte54nyelum6veygkl6unjc64fsu2fxfp")
	_, err := km.CreateChallenge("klv1v9mcl3m25tymxzs2tu3zztp7y5ceg3yy9z2glj5wykywanf564fsd5djjt")
	if err != ErrAddressMismatch {
		t.Errorf("expected ErrAddressMismatch, got %v", err)
	}
}

func TestKleverChallengeCreation(t *testing.T) {
	addr := "klv1qqqqqqqqqqqqqpgq0khua5d0eqte54nyelum6veygkl6unjc64fsu2fxfp"
	km := NewKleverAuthManager(addr)

	nonce, err := km.CreateChallenge(addr)
	if err != nil {
		t.Fatalf("CreateChallenge: %v", err)
	}
	if len(nonce) != 64 { // 32 bytes = 64 hex chars
		t.Errorf("expected 64 hex chars nonce, got %d", len(nonce))
	}
}

func TestKleverBech32Decode(t *testing.T) {
	// Test that we can decode a known klv1 address
	addr := "klv1qqqqqqqqqqqqqpgq0khua5d0eqte54nyelum6veygkl6unjc64fsu2fxfp"
	pubKey, err := kleverAddressToPublicKey(addr)
	if err != nil {
		t.Fatalf("kleverAddressToPublicKey: %v", err)
	}
	if len(pubKey) != 32 {
		t.Errorf("expected 32-byte public key, got %d", len(pubKey))
	}
}

func TestKleverFullSignVerify(t *testing.T) {
	// Generate a real Ed25519 keypair
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}

	// Encode public key as klv1... bech32 address
	addr := publicKeyToKleverAddress(pub)
	if !isValidKleverAddress(addr) {
		t.Fatalf("generated invalid address: %s", addr)
	}

	km := NewKleverAuthManager(addr)

	// Create challenge
	nonce, err := km.CreateChallenge(addr)
	if err != nil {
		t.Fatalf("CreateChallenge: %v", err)
	}

	// Sign with private key (Klever Extension pre-hashes with SHA-256)
	hash := kleverSignedMessageHash(nonce)
	sig := ed25519.Sign(priv, hash)
	sigHex := hex.EncodeToString(sig)

	// Verify
	if err := km.VerifySignature(addr, nonce, sigHex); err != nil {
		t.Fatalf("VerifySignature: %v", err)
	}
}

func TestKleverVerifyWrongSignature(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}

	addr := publicKeyToKleverAddress(pub)
	km := NewKleverAuthManager(addr)

	nonce, err := km.CreateChallenge(addr)
	if err != nil {
		t.Fatalf("CreateChallenge: %v", err)
	}

	// Wrong signature (random bytes)
	wrongSig := make([]byte, 64)
	if err := km.VerifySignature(addr, nonce, hex.EncodeToString(wrongSig)); err != ErrInvalidSignature {
		t.Errorf("expected ErrInvalidSignature, got %v", err)
	}
}

func TestKleverChallengeConsumed(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}

	addr := publicKeyToKleverAddress(pub)
	km := NewKleverAuthManager(addr)

	nonce, err := km.CreateChallenge(addr)
	if err != nil {
		t.Fatal(err)
	}

	hash := kleverSignedMessageHash(nonce)
	sig := ed25519.Sign(priv, hash)
	sigHex := hex.EncodeToString(sig)

	// First verify succeeds
	if err := km.VerifySignature(addr, nonce, sigHex); err != nil {
		t.Fatalf("first verify: %v", err)
	}

	// Second verify fails (challenge consumed)
	if err := km.VerifySignature(addr, nonce, sigHex); err != ErrChallengeExpired {
		t.Errorf("expected ErrChallengeExpired for reused nonce, got %v", err)
	}
}

// publicKeyToKleverAddress encodes an Ed25519 public key as a klv1... bech32 address.
// Used only in tests.
func publicKeyToKleverAddress(pub ed25519.PublicKey) string {
	// Convert 8-bit bytes to 5-bit groups
	converted, err := convertBits(pub, 8, 5, true)
	if err != nil {
		panic(err)
	}

	// Compute bech32 checksum
	values := append(converted, 0, 0, 0, 0, 0, 0) // placeholder for checksum
	polymod := bech32Polymod(expandHRP(kleverHRP), values)
	for i := 0; i < 6; i++ {
		values[len(converted)+i] = byte((polymod >> uint(5*(5-i))) & 31)
	}

	// Encode as bech32 string
	var sb strings.Builder
	sb.WriteString(kleverHRP + "1")
	for _, v := range values {
		sb.WriteByte(bech32Charset[v])
	}
	return sb.String()
}

func expandHRP(hrp string) []byte {
	result := make([]byte, len(hrp)*2+1)
	for i, c := range hrp {
		result[i] = byte(c >> 5)
		result[i+len(hrp)+1] = byte(c & 31)
	}
	result[len(hrp)] = 0
	return result
}

func bech32Polymod(hrp, values []byte) uint32 {
	gen := [5]uint32{0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3}
	chk := uint32(1)
	for _, v := range hrp {
		b := chk >> 25
		chk = (chk&0x1ffffff)<<5 ^ uint32(v)
		for i := 0; i < 5; i++ {
			if (b>>uint(i))&1 == 1 {
				chk ^= gen[i]
			}
		}
	}
	for _, v := range values {
		b := chk >> 25
		chk = (chk&0x1ffffff)<<5 ^ uint32(v)
		for i := 0; i < 5; i++ {
			if (b>>uint(i))&1 == 1 {
				chk ^= gen[i]
			}
		}
	}
	return chk ^ 1
}
