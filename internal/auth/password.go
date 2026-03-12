package auth

import (
	"errors"
	"sync"
)

const minPasswordLength = 8

// ErrPasswordTooShort is returned when the password is shorter than the minimum length.
var ErrPasswordTooShort = errors.New("password must be at least 8 characters")

// PasswordManager handles password-based authentication with Argon2id hashing.
type PasswordManager struct {
	mu   sync.Mutex
	hash string // hex-encoded: salt(16) + argon2id_hash(32)
}

// NewPasswordManager creates a new PasswordManager with an optional stored hash.
func NewPasswordManager(storedHash string) *PasswordManager {
	return &PasswordManager{hash: storedHash}
}

// SetPassword hashes and stores a new password. Returns the hash for persistence.
func (pm *PasswordManager) SetPassword(password string) (string, error) {
	if len(password) < minPasswordLength {
		return "", ErrPasswordTooShort
	}

	pm.mu.Lock()
	defer pm.mu.Unlock()

	hash, err := HashArgon2id(password)
	if err != nil {
		return "", err
	}

	pm.hash = hash
	return hash, nil
}

// Verify checks a password against the stored hash.
func (pm *PasswordManager) Verify(password string) bool {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if pm.hash == "" {
		return false
	}

	return VerifyArgon2id(password, pm.hash)
}

// HasPassword returns true if a password has been set.
func (pm *PasswordManager) HasPassword() bool {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	return pm.hash != ""
}

// Hash returns the current hash for persistence.
func (pm *PasswordManager) Hash() string {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	return pm.hash
}
