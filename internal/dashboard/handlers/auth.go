package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/CTJaeger/KleverNodeHub/internal/auth"
)

// AuthHandler handles authentication API requests.
type AuthHandler struct {
	jwt      *auth.JWTManager
	webauthn *auth.WebAuthnManager
	recovery *auth.RecoveryManager
}

// NewAuthHandler creates a new AuthHandler.
func NewAuthHandler(jwt *auth.JWTManager, webauthn *auth.WebAuthnManager, recovery *auth.RecoveryManager) *AuthHandler {
	return &AuthHandler{
		jwt:      jwt,
		webauthn: webauthn,
		recovery: recovery,
	}
}

// HandleSetupStatus returns whether initial setup has been completed.
// GET /api/setup/status
func (h *AuthHandler) HandleSetupStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"setup_complete": h.webauthn.HasCredentials(),
		"passkey_count":  h.webauthn.CredentialCount(),
	})
}

// HandlePasskeyBeginRegister starts the WebAuthn registration ceremony.
// POST /api/auth/passkey/register/begin
func (h *AuthHandler) HandlePasskeyBeginRegister(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	if req.Name == "" {
		req.Name = "default"
	}

	options, sessionID, err := h.webauthn.BeginRegistration(req.Name)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"options":    options,
		"session_id": sessionID,
	})
}

// HandlePasskeyBeginLogin starts the WebAuthn login ceremony.
// POST /api/auth/passkey/login/begin
func (h *AuthHandler) HandlePasskeyBeginLogin(w http.ResponseWriter, r *http.Request) {
	options, sessionID, err := h.webauthn.BeginLogin()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"options":    options,
		"session_id": sessionID,
	})
}

// HandleRecoveryLogin authenticates via recovery code.
// POST /api/auth/recovery
func (h *AuthHandler) HandleRecoveryLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}

	if !h.recovery.Verify(req.Code) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid recovery code"})
		return
	}

	// Issue JWT tokens
	tokens, err := h.jwt.IssueTokenPair("admin")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "token generation failed"})
		return
	}

	setAuthCookies(w, tokens)
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":  tokens.AccessToken,
		"refresh_token": tokens.RefreshToken,
		"remaining":     h.recovery.Remaining(),
	})
}

// HandleRefresh refreshes the JWT token pair.
// POST /api/auth/refresh
func (h *AuthHandler) HandleRefresh(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RefreshToken string `json:"refresh_token"`
	}

	// Try request body first, then cookie
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.RefreshToken == "" {
		if cookie, err := r.Cookie("refresh_token"); err == nil {
			req.RefreshToken = cookie.Value
		}
	}

	if req.RefreshToken == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "refresh token required"})
		return
	}

	tokens, err := h.jwt.RefreshTokenPair(req.RefreshToken)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid refresh token"})
		return
	}

	setAuthCookies(w, tokens)
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":  tokens.AccessToken,
		"refresh_token": tokens.RefreshToken,
	})
}

// HandleLogout clears auth cookies.
// POST /api/auth/logout
func (h *AuthHandler) HandleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     "access_token",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     "refresh_token",
		Value:    "",
		Path:     "/api/auth/refresh",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "logged out"})
}

// setAuthCookies sets the JWT cookies in the response.
func setAuthCookies(w http.ResponseWriter, tokens *auth.TokenPair) {
	http.SetCookie(w, &http.Cookie{
		Name:     "access_token",
		Value:    tokens.AccessToken,
		Path:     "/",
		MaxAge:   15 * 60, // 15 minutes
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     "refresh_token",
		Value:    tokens.RefreshToken,
		Path:     "/api/auth/refresh",
		MaxAge:   7 * 24 * int(time.Hour/time.Second),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
}
