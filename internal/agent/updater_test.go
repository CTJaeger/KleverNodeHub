package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestSHA256Hex(t *testing.T) {
	data := []byte("hello world")
	got := SHA256Hex(data)

	h := sha256.Sum256(data)
	want := hex.EncodeToString(h[:])

	if got != want {
		t.Errorf("SHA256Hex = %q, want %q", got, want)
	}
}

func TestSHA256Hex_Empty(t *testing.T) {
	got := SHA256Hex([]byte{})
	// SHA-256 of empty input is well-known
	want := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if got != want {
		t.Errorf("SHA256Hex(empty) = %q, want %q", got, want)
	}
}

func TestSHA256Hex_Deterministic(t *testing.T) {
	data := []byte("test binary data for agent update")
	a := SHA256Hex(data)
	b := SHA256Hex(data)
	if a != b {
		t.Error("SHA256Hex not deterministic")
	}
}
