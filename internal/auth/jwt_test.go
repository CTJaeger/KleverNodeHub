package auth

import (
	"testing"
	"time"
)

func newTestJWTManager(t *testing.T) *JWTManager {
	t.Helper()
	key, err := GenerateSigningKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	jm, err := NewJWTManager(key)
	if err != nil {
		t.Fatalf("create manager: %v", err)
	}
	return jm
}

func TestNewJWTManager_ShortKey(t *testing.T) {
	_, err := NewJWTManager([]byte("short"))
	if err == nil {
		t.Error("expected error for short key")
	}
}

func TestIssueTokenPair(t *testing.T) {
	jm := newTestJWTManager(t)

	pair, err := jm.IssueTokenPair("dashboard-user")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	if pair.AccessToken == "" {
		t.Error("access token is empty")
	}
	if pair.RefreshToken == "" {
		t.Error("refresh token is empty")
	}
	if pair.ExpiresIn != int64(accessTokenExpiry.Seconds()) {
		t.Errorf("expires_in = %d, want %d", pair.ExpiresIn, int64(accessTokenExpiry.Seconds()))
	}
}

func TestValidateAccessToken(t *testing.T) {
	jm := newTestJWTManager(t)

	pair, err := jm.IssueTokenPair("user1")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	claims, err := jm.ValidateAccessToken(pair.AccessToken)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}

	if claims.Subject != "user1" {
		t.Errorf("subject = %q, want %q", claims.Subject, "user1")
	}
	if claims.TokenType != "access" {
		t.Errorf("type = %q, want %q", claims.TokenType, "access")
	}
}

func TestValidateAccessToken_WrongType(t *testing.T) {
	jm := newTestJWTManager(t)

	pair, err := jm.IssueTokenPair("user1")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	// Refresh token should not validate as access token
	_, err = jm.ValidateAccessToken(pair.RefreshToken)
	if err == nil {
		t.Error("refresh token should not validate as access token")
	}
}

func TestValidateRefreshToken(t *testing.T) {
	jm := newTestJWTManager(t)

	pair, err := jm.IssueTokenPair("user1")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	claims, err := jm.ValidateRefreshToken(pair.RefreshToken)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}

	if claims.Subject != "user1" {
		t.Errorf("subject = %q, want %q", claims.Subject, "user1")
	}
	if claims.TokenType != "refresh" {
		t.Errorf("type = %q, want %q", claims.TokenType, "refresh")
	}
}

func TestValidateToken_InvalidSignature(t *testing.T) {
	jm1 := newTestJWTManager(t)
	jm2 := newTestJWTManager(t)

	pair, err := jm1.IssueTokenPair("user1")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	// Token from jm1 should not validate with jm2
	_, err = jm2.ValidateAccessToken(pair.AccessToken)
	if err == nil {
		t.Error("expected error for token signed with different key")
	}
}

func TestValidateToken_Expired(t *testing.T) {
	key, _ := GenerateSigningKey()
	jm, _ := NewJWTManager(key)

	// Create a token that's already expired
	token, err := jm.createToken(JWTClaims{
		Subject:   "user1",
		IssuedAt:  time.Now().Add(-1 * time.Hour).Unix(),
		ExpiresAt: time.Now().Add(-1 * time.Minute).Unix(),
		TokenType: "access",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	_, err = jm.ValidateAccessToken(token)
	if err == nil {
		t.Error("expected error for expired token")
	}
}

func TestValidateToken_InvalidFormat(t *testing.T) {
	jm := newTestJWTManager(t)

	_, err := jm.ValidateAccessToken("not.a.valid.token")
	if err == nil {
		t.Error("expected error for invalid format")
	}

	_, err = jm.ValidateAccessToken("garbage")
	if err == nil {
		t.Error("expected error for garbage input")
	}
}

func TestRefreshTokenPair(t *testing.T) {
	jm := newTestJWTManager(t)

	pair1, err := jm.IssueTokenPair("user1")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	pair2, err := jm.RefreshTokenPair(pair1.RefreshToken)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}

	// New access token should be valid
	claims, err := jm.ValidateAccessToken(pair2.AccessToken)
	if err != nil {
		t.Fatalf("validate new access: %v", err)
	}
	if claims.Subject != "user1" {
		t.Errorf("subject = %q, want %q", claims.Subject, "user1")
	}

	// New refresh token should also be valid
	_, err = jm.ValidateRefreshToken(pair2.RefreshToken)
	if err != nil {
		t.Fatalf("validate new refresh: %v", err)
	}
}

func TestRefreshTokenPair_WithAccessToken(t *testing.T) {
	jm := newTestJWTManager(t)

	pair, err := jm.IssueTokenPair("user1")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	// Access token should not work as refresh token
	_, err = jm.RefreshTokenPair(pair.AccessToken)
	if err == nil {
		t.Error("access token should not work as refresh token")
	}
}

func TestGenerateSigningKey(t *testing.T) {
	key1, err := GenerateSigningKey()
	if err != nil {
		t.Fatalf("generate 1: %v", err)
	}

	key2, err := GenerateSigningKey()
	if err != nil {
		t.Fatalf("generate 2: %v", err)
	}

	if len(key1) != 32 {
		t.Errorf("key length = %d, want 32", len(key1))
	}

	// Two keys should be different
	equal := true
	for i := range key1 {
		if key1[i] != key2[i] {
			equal = false
			break
		}
	}
	if equal {
		t.Error("two generated keys should differ")
	}
}
