package dashboard

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"sync"
	"time"
)

const (
	tokenLength = 32            // 32 bytes = 256 bits
	tokenExpiry = 1 * time.Hour // Tokens expire after 1 hour
)

// RegistrationToken is a one-time token for agent registration.
type RegistrationToken struct {
	Token     string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// TokenManager manages one-time registration tokens.
type TokenManager struct {
	mu     sync.Mutex
	tokens map[string]*RegistrationToken
}

// NewTokenManager creates a new TokenManager.
func NewTokenManager() *TokenManager {
	return &TokenManager{
		tokens: make(map[string]*RegistrationToken),
	}
}

// Generate creates a new one-time registration token.
func (tm *TokenManager) Generate() (string, error) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	b := make([]byte, tokenLength)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}

	token := base64.RawURLEncoding.EncodeToString(b)
	now := time.Now()

	tm.tokens[token] = &RegistrationToken{
		Token:     token,
		CreatedAt: now,
		ExpiresAt: now.Add(tokenExpiry),
	}

	// Clean expired tokens while we're here
	tm.cleanExpired()

	return token, nil
}

// Validate checks if a token is valid (exists and not expired).
// If valid, the token is consumed (single-use).
func (tm *TokenManager) Validate(token string) bool {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	rt, ok := tm.tokens[token]
	if !ok {
		return false
	}

	// Check expiry
	if time.Now().After(rt.ExpiresAt) {
		delete(tm.tokens, token)
		return false
	}

	// Consume token (single-use)
	delete(tm.tokens, token)
	return true
}

// PendingCount returns the number of valid pending tokens.
func (tm *TokenManager) PendingCount() int {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	tm.cleanExpired()
	return len(tm.tokens)
}

// cleanExpired removes expired tokens. Must be called with lock held.
func (tm *TokenManager) cleanExpired() {
	now := time.Now()
	for token, rt := range tm.tokens {
		if now.After(rt.ExpiresAt) {
			delete(tm.tokens, token)
		}
	}
}
