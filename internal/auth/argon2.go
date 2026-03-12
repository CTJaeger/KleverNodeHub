package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"

	"golang.org/x/crypto/argon2"
)

const (
	argon2Time    = 3
	argon2Memory  = 64 * 1024 // 64 MB
	argon2Threads = 4
	argon2KeyLen  = 32
	saltLen       = 16
)

// HashArgon2id hashes the input with Argon2id and returns hex(salt + hash).
func HashArgon2id(input string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}

	hash := argon2.IDKey([]byte(input), salt, argon2Time, argon2Memory, argon2Threads, argon2KeyLen)

	result := make([]byte, saltLen+argon2KeyLen)
	copy(result[:saltLen], salt)
	copy(result[saltLen:], hash)

	return hex.EncodeToString(result), nil
}

// VerifyArgon2id checks an input against a stored Argon2id hash.
// Uses constant-time comparison to prevent timing attacks.
func VerifyArgon2id(input, storedHash string) bool {
	decoded, err := hex.DecodeString(storedHash)
	if err != nil || len(decoded) != saltLen+argon2KeyLen {
		return false
	}

	salt := decoded[:saltLen]
	expectedHash := decoded[saltLen:]

	actualHash := argon2.IDKey([]byte(input), salt, argon2Time, argon2Memory, argon2Threads, argon2KeyLen)

	return subtle.ConstantTimeCompare(expectedHash, actualHash) == 1
}
