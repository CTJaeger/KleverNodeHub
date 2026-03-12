package auth

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
)

const (
	recoveryCodeCount  = 8
	recoveryCodeLength = 12 // 3 groups of 4 hex chars
)

// RecoveryCode holds a hashed recovery code with its usage state.
type RecoveryCode struct {
	Hash string `json:"hash"` // Argon2id hash (hex-encoded: salt + hash)
	Used bool   `json:"used"`
}

// RecoveryManager handles generation, hashing, and verification of recovery codes.
type RecoveryManager struct {
	mu    sync.Mutex
	codes []RecoveryCode
}

// NewRecoveryManager creates a new RecoveryManager with the given stored codes.
func NewRecoveryManager(codes []RecoveryCode) *RecoveryManager {
	return &RecoveryManager{codes: codes}
}

// GenerateCodes generates a new set of recovery codes.
// Returns the plaintext codes (to show the user once) and the hashed codes (to store).
func (rm *RecoveryManager) GenerateCodes() (plaintextCodes []string, hashedCodes []RecoveryCode, err error) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	plaintextCodes = make([]string, recoveryCodeCount)
	hashedCodes = make([]RecoveryCode, recoveryCodeCount)

	for i := 0; i < recoveryCodeCount; i++ {
		code, err := generateRandomCode()
		if err != nil {
			return nil, nil, fmt.Errorf("generate code %d: %w", i, err)
		}
		plaintextCodes[i] = code

		hash, err := HashArgon2id(normalizeCode(code))
		if err != nil {
			return nil, nil, fmt.Errorf("hash code %d: %w", i, err)
		}
		hashedCodes[i] = RecoveryCode{Hash: hash, Used: false}
	}

	rm.codes = hashedCodes
	return plaintextCodes, hashedCodes, nil
}

// Verify checks a recovery code against stored hashes.
// If valid, the code is marked as used and cannot be reused.
// Returns true if the code was valid and unused.
func (rm *RecoveryManager) Verify(code string) bool {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	normalized := normalizeCode(code)

	for i := range rm.codes {
		if rm.codes[i].Used {
			continue
		}
		if VerifyArgon2id(normalized, rm.codes[i].Hash) {
			rm.codes[i].Used = true
			return true
		}
	}
	return false
}

// Remaining returns the number of unused recovery codes.
func (rm *RecoveryManager) Remaining() int {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	count := 0
	for _, c := range rm.codes {
		if !c.Used {
			count++
		}
	}
	return count
}

// Codes returns the current stored codes (for persistence).
func (rm *RecoveryManager) Codes() []RecoveryCode {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	result := make([]RecoveryCode, len(rm.codes))
	copy(result, rm.codes)
	return result
}

// generateRandomCode creates a code in format XXXX-XXXX-XXXX.
func generateRandomCode() (string, error) {
	b := make([]byte, 6) // 6 bytes = 12 hex chars
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	h := hex.EncodeToString(b)
	return fmt.Sprintf("%s-%s-%s",
		strings.ToUpper(h[0:4]),
		strings.ToUpper(h[4:8]),
		strings.ToUpper(h[8:12]),
	), nil
}

// normalizeCode removes dashes and converts to uppercase.
func normalizeCode(code string) string {
	return strings.ToUpper(strings.ReplaceAll(code, "-", ""))
}
