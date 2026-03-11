package dashboard

import (
	"testing"
	"time"
)

func TestGenerateToken(t *testing.T) {
	tm := NewTokenManager()

	token, err := tm.Generate()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	if token == "" {
		t.Error("token should not be empty")
	}
	if tm.PendingCount() != 1 {
		t.Errorf("pending = %d, want 1", tm.PendingCount())
	}
}

func TestGenerateUniqueTokens(t *testing.T) {
	tm := NewTokenManager()

	t1, _ := tm.Generate()
	t2, _ := tm.Generate()

	if t1 == t2 {
		t.Error("tokens should be unique")
	}
	if tm.PendingCount() != 2 {
		t.Errorf("pending = %d, want 2", tm.PendingCount())
	}
}

func TestValidateToken(t *testing.T) {
	tm := NewTokenManager()

	token, _ := tm.Generate()

	if !tm.Validate(token) {
		t.Error("valid token should validate")
	}

	// Single-use: should not validate again
	if tm.Validate(token) {
		t.Error("token should not validate twice")
	}

	if tm.PendingCount() != 0 {
		t.Errorf("pending = %d, want 0", tm.PendingCount())
	}
}

func TestValidateInvalidToken(t *testing.T) {
	tm := NewTokenManager()

	if tm.Validate("nonexistent") {
		t.Error("invalid token should not validate")
	}
}

func TestValidateExpiredToken(t *testing.T) {
	tm := NewTokenManager()

	token, _ := tm.Generate()

	// Manually expire the token
	tm.mu.Lock()
	tm.tokens[token].ExpiresAt = time.Now().Add(-1 * time.Minute)
	tm.mu.Unlock()

	if tm.Validate(token) {
		t.Error("expired token should not validate")
	}
}

func TestPendingCountCleansExpired(t *testing.T) {
	tm := NewTokenManager()

	token, _ := tm.Generate()

	// Manually expire
	tm.mu.Lock()
	tm.tokens[token].ExpiresAt = time.Now().Add(-1 * time.Minute)
	tm.mu.Unlock()

	if tm.PendingCount() != 0 {
		t.Errorf("pending = %d, want 0 (expired should be cleaned)", tm.PendingCount())
	}
}
