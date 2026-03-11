package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	accessTokenExpiry  = 15 * time.Minute
	refreshTokenExpiry = 7 * 24 * time.Hour
)

// TokenPair holds an access token and refresh token.
type TokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"` // seconds
}

// JWTManager handles JWT token creation and validation.
type JWTManager struct {
	signingKey []byte
}

// NewJWTManager creates a new JWTManager with the given signing key.
// Key must be at least 32 bytes.
func NewJWTManager(signingKey []byte) (*JWTManager, error) {
	if len(signingKey) < 32 {
		return nil, fmt.Errorf("signing key must be at least 32 bytes")
	}
	key := make([]byte, len(signingKey))
	copy(key, signingKey)
	return &JWTManager{signingKey: key}, nil
}

// GenerateSigningKey creates a cryptographically random 32-byte signing key.
func GenerateSigningKey() ([]byte, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate signing key: %w", err)
	}
	return key, nil
}

// jwtHeader is the fixed JWT header for HS256.
type jwtHeader struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
}

// JWTClaims holds the JWT payload.
type JWTClaims struct {
	Subject   string `json:"sub"`
	IssuedAt  int64  `json:"iat"`
	ExpiresAt int64  `json:"exp"`
	TokenType string `json:"type"` // "access" or "refresh"
}

// IssueTokenPair creates a new access + refresh token pair.
func (jm *JWTManager) IssueTokenPair(subject string) (*TokenPair, error) {
	now := time.Now()

	accessToken, err := jm.createToken(JWTClaims{
		Subject:   subject,
		IssuedAt:  now.Unix(),
		ExpiresAt: now.Add(accessTokenExpiry).Unix(),
		TokenType: "access",
	})
	if err != nil {
		return nil, fmt.Errorf("create access token: %w", err)
	}

	refreshToken, err := jm.createToken(JWTClaims{
		Subject:   subject,
		IssuedAt:  now.Unix(),
		ExpiresAt: now.Add(refreshTokenExpiry).Unix(),
		TokenType: "refresh",
	})
	if err != nil {
		return nil, fmt.Errorf("create refresh token: %w", err)
	}

	return &TokenPair{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresIn:    int64(accessTokenExpiry.Seconds()),
	}, nil
}

// ValidateAccessToken validates an access token and returns its claims.
func (jm *JWTManager) ValidateAccessToken(token string) (*JWTClaims, error) {
	claims, err := jm.validateToken(token)
	if err != nil {
		return nil, err
	}
	if claims.TokenType != "access" {
		return nil, fmt.Errorf("not an access token")
	}
	return claims, nil
}

// ValidateRefreshToken validates a refresh token and returns its claims.
func (jm *JWTManager) ValidateRefreshToken(token string) (*JWTClaims, error) {
	claims, err := jm.validateToken(token)
	if err != nil {
		return nil, err
	}
	if claims.TokenType != "refresh" {
		return nil, fmt.Errorf("not a refresh token")
	}
	return claims, nil
}

// RefreshTokenPair validates a refresh token and issues a new token pair.
func (jm *JWTManager) RefreshTokenPair(refreshToken string) (*TokenPair, error) {
	claims, err := jm.ValidateRefreshToken(refreshToken)
	if err != nil {
		return nil, fmt.Errorf("invalid refresh token: %w", err)
	}
	return jm.IssueTokenPair(claims.Subject)
}

func (jm *JWTManager) createToken(claims JWTClaims) (string, error) {
	header := jwtHeader{Alg: "HS256", Typ: "JWT"}

	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", fmt.Errorf("marshal header: %w", err)
	}

	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("marshal claims: %w", err)
	}

	headerB64 := base64.RawURLEncoding.EncodeToString(headerJSON)
	claimsB64 := base64.RawURLEncoding.EncodeToString(claimsJSON)

	signingInput := headerB64 + "." + claimsB64
	signature := jm.sign([]byte(signingInput))
	sigB64 := base64.RawURLEncoding.EncodeToString(signature)

	return signingInput + "." + sigB64, nil
}

func (jm *JWTManager) validateToken(token string) (*JWTClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid token format")
	}

	// Verify signature
	signingInput := parts[0] + "." + parts[1]
	expectedSig := jm.sign([]byte(signingInput))
	actualSig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, fmt.Errorf("decode signature: %w", err)
	}

	if !hmac.Equal(expectedSig, actualSig) {
		return nil, fmt.Errorf("invalid signature")
	}

	// Decode claims
	claimsJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decode claims: %w", err)
	}

	var claims JWTClaims
	if err := json.Unmarshal(claimsJSON, &claims); err != nil {
		return nil, fmt.Errorf("unmarshal claims: %w", err)
	}

	// Check expiry
	if time.Now().Unix() > claims.ExpiresAt {
		return nil, fmt.Errorf("token expired")
	}

	return &claims, nil
}

func (jm *JWTManager) sign(data []byte) []byte {
	mac := hmac.New(sha256.New, jm.signingKey)
	mac.Write(data)
	return mac.Sum(nil)
}
