package handlers

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/CTJaeger/KleverNodeHub/internal/auth"
	"github.com/go-webauthn/webauthn/protocol"
)

// AuthHandler handles authentication API requests.
type AuthHandler struct {
	jwt      *auth.JWTManager
	webauthn *auth.WebAuthnManager
	recovery *auth.RecoveryManager

	// onCredentialsChanged is called when passkey credentials are added or updated.
	// Used to persist credentials to the settings store.
	onCredentialsChanged func([]auth.PasskeyCredential)
}

// NewAuthHandler creates a new AuthHandler.
func NewAuthHandler(jwt *auth.JWTManager, webauthn *auth.WebAuthnManager, recovery *auth.RecoveryManager) *AuthHandler {
	return &AuthHandler{
		jwt:      jwt,
		webauthn: webauthn,
		recovery: recovery,
	}
}

// SetOnCredentialsChanged sets the callback for credential persistence.
func (h *AuthHandler) SetOnCredentialsChanged(fn func([]auth.PasskeyCredential)) {
	h.onCredentialsChanged = fn
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

// HandlePasskeyFinishRegister completes the WebAuthn registration ceremony.
// POST /api/auth/passkey/register/finish
func (h *AuthHandler) HandlePasskeyFinishRegister(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session_id")
	name := r.URL.Query().Get("name")
	if sessionID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session_id query parameter required"})
		return
	}
	if name == "" {
		name = "default"
	}

	parsedResponse, err := protocol.ParseCredentialCreationResponseBody(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid credential: " + err.Error()})
		return
	}

	if err := h.webauthn.FinishRegistration(sessionID, parsedResponse, name); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	if h.onCredentialsChanged != nil {
		h.onCredentialsChanged(h.webauthn.Credentials())
	}

	// Issue JWT tokens so the user is authenticated after registration
	tokens, err := h.jwt.IssueTokenPair("admin")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "token generation failed"})
		return
	}

	setAuthCookies(w, tokens)
	writeJSON(w, http.StatusOK, map[string]any{
		"status":       "registered",
		"access_token": tokens.AccessToken,
	})
}

// HandlePasskeyFinishLogin completes the WebAuthn login ceremony and issues JWT tokens.
// POST /api/auth/passkey/login/finish
func (h *AuthHandler) HandlePasskeyFinishLogin(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session_id")
	if sessionID == "" {
		log.Println("login finish: missing session_id")
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session_id query parameter required"})
		return
	}

	parsedResponse, err := protocol.ParseCredentialRequestResponseBody(r.Body)
	if err != nil {
		log.Printf("login finish: parse assertion error: %v", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid assertion: " + err.Error()})
		return
	}

	if err := h.webauthn.FinishLogin(sessionID, parsedResponse); err != nil {
		log.Printf("login finish: webauthn verify error: %v", err)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}

	log.Println("login finish: success")

	if h.onCredentialsChanged != nil {
		h.onCredentialsChanged(h.webauthn.Credentials())
	}

	tokens, err := h.jwt.IssueTokenPair("admin")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "token generation failed"})
		return
	}

	setAuthCookies(w, tokens)
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":  tokens.AccessToken,
		"refresh_token": tokens.RefreshToken,
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

// HandleListPasskeys returns the list of registered passkeys.
// GET /api/auth/passkeys
func (h *AuthHandler) HandleListPasskeys(w http.ResponseWriter, r *http.Request) {
	creds := h.webauthn.Credentials()

	// Return safe subset (no public keys)
	type passkeyInfo struct {
		ID           string `json:"id"`
		Name         string `json:"name"`
		RegisteredAt int64  `json:"registered_at"`
		SignCount    uint32 `json:"sign_count"`
	}

	list := make([]passkeyInfo, len(creds))
	for i, c := range creds {
		list[i] = passkeyInfo{
			ID:           c.ID,
			Name:         c.Name,
			RegisteredAt: c.RegisteredAt,
			SignCount:    c.SignCount,
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"passkeys": list})
}

// HandleDeletePasskey removes a passkey by ID.
// DELETE /api/auth/passkeys/{id}
func (h *AuthHandler) HandleDeletePasskey(w http.ResponseWriter, r *http.Request) {
	passkeyID := r.PathValue("id")
	if passkeyID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing passkey id"})
		return
	}

	if err := h.webauthn.DeletePasskey(passkeyID); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	if h.onCredentialsChanged != nil {
		h.onCredentialsChanged(h.webauthn.Credentials())
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
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
