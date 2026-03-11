package crypto

import (
	"bytes"
	"crypto/rand"
	"testing"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("generate key: %v", err)
	}

	plaintext := []byte("Klever Node Hub secret data")

	ciphertext, err := Encrypt(plaintext, key)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	if bytes.Equal(plaintext, ciphertext) {
		t.Error("ciphertext should differ from plaintext")
	}

	decrypted, err := Decrypt(ciphertext, key)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}

	if !bytes.Equal(plaintext, decrypted) {
		t.Errorf("decrypted = %q, want %q", decrypted, plaintext)
	}
}

func TestEncryptDecryptEmptyData(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("generate key: %v", err)
	}

	ciphertext, err := Encrypt([]byte{}, key)
	if err != nil {
		t.Fatalf("encrypt empty: %v", err)
	}

	decrypted, err := Decrypt(ciphertext, key)
	if err != nil {
		t.Fatalf("decrypt empty: %v", err)
	}

	if len(decrypted) != 0 {
		t.Errorf("expected empty, got %d bytes", len(decrypted))
	}
}

func TestEncryptDecryptLargeData(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("generate key: %v", err)
	}

	plaintext := make([]byte, 1024*1024) // 1 MB
	if _, err := rand.Read(plaintext); err != nil {
		t.Fatalf("generate data: %v", err)
	}

	ciphertext, err := Encrypt(plaintext, key)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	decrypted, err := Decrypt(ciphertext, key)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}

	if !bytes.Equal(plaintext, decrypted) {
		t.Error("round-trip failed for large data")
	}
}

func TestDecryptWrongKey(t *testing.T) {
	key1 := make([]byte, 32)
	key2 := make([]byte, 32)
	if _, err := rand.Read(key1); err != nil {
		t.Fatalf("generate key1: %v", err)
	}
	if _, err := rand.Read(key2); err != nil {
		t.Fatalf("generate key2: %v", err)
	}

	ciphertext, err := Encrypt([]byte("secret"), key1)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	_, err = Decrypt(ciphertext, key2)
	if err == nil {
		t.Error("expected error when decrypting with wrong key")
	}
}

func TestDecryptTamperedData(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("generate key: %v", err)
	}

	ciphertext, err := Encrypt([]byte("secret"), key)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	// Tamper with the ciphertext
	ciphertext[len(ciphertext)-1] ^= 0xFF

	_, err = Decrypt(ciphertext, key)
	if err == nil {
		t.Error("expected error for tampered ciphertext")
	}
}

func TestEncryptInvalidKeySize(t *testing.T) {
	_, err := Encrypt([]byte("data"), []byte("short"))
	if err == nil {
		t.Error("expected error for invalid key size")
	}
}

func TestDecryptInvalidKeySize(t *testing.T) {
	_, err := Decrypt([]byte("data"), []byte("short"))
	if err == nil {
		t.Error("expected error for invalid key size")
	}
}

func TestDecryptTooShort(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("generate key: %v", err)
	}

	_, err := Decrypt([]byte{1, 2, 3}, key)
	if err == nil {
		t.Error("expected error for too-short ciphertext")
	}
}

func TestEncryptProducesDifferentCiphertexts(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("generate key: %v", err)
	}

	plaintext := []byte("same data")

	ct1, err := Encrypt(plaintext, key)
	if err != nil {
		t.Fatalf("encrypt 1: %v", err)
	}

	ct2, err := Encrypt(plaintext, key)
	if err != nil {
		t.Fatalf("encrypt 2: %v", err)
	}

	if bytes.Equal(ct1, ct2) {
		t.Error("two encryptions of same data should produce different ciphertexts (random nonce)")
	}
}

func TestDeriveKey(t *testing.T) {
	secret := []byte("my-master-secret")
	salt := []byte("klever-node-hub")
	info := []byte("ca-key-encryption")

	key, err := DeriveKey(secret, salt, info)
	if err != nil {
		t.Fatalf("derive key: %v", err)
	}

	if len(key) != 32 {
		t.Errorf("key length = %d, want 32", len(key))
	}

	// Same inputs produce same key
	key2, err := DeriveKey(secret, salt, info)
	if err != nil {
		t.Fatalf("derive key 2: %v", err)
	}

	if !bytes.Equal(key, key2) {
		t.Error("same inputs should produce same key")
	}

	// Different info produces different key
	key3, err := DeriveKey(secret, salt, []byte("different-purpose"))
	if err != nil {
		t.Fatalf("derive key 3: %v", err)
	}

	if bytes.Equal(key, key3) {
		t.Error("different info should produce different key")
	}
}

func TestDeriveKeyEmptySecret(t *testing.T) {
	_, err := DeriveKey([]byte{}, nil, nil)
	if err == nil {
		t.Error("expected error for empty secret")
	}
}
