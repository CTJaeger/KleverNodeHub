package handlers

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/CTJaeger/KleverNodeHub/internal/auth"
	"github.com/go-webauthn/webauthn/protocol"
)

// AuthHandler handles authentication API requests.
type AuthHandler struct {
	jwt      *auth.JWTManager
	webauthn *auth.WebAuthnManager
	recovery *auth.RecoveryManager
	password *auth.PasswordManager
	limiter  *auth.RateLimiter
	klever   *auth.KleverAuthManager

	// onCredentialsChanged is called when passkey credentials are added or updated.
	onCredentialsChanged func([]auth.PasskeyCredential)
	// onPasswordChanged is called when the password hash changes.
	onPasswordChanged func(string)
	// onKleverAddressChanged is called when the Klever admin address changes.
	onKleverAddressChanged func(string)
	// saveSigningKey persists a new JWT signing key (ending all sessions).
	saveSigningKey func([]byte) error
	// onRecoveryCodesChanged persists the codes after one was used.
	onRecoveryCodesChanged func([]auth.RecoveryCode)
	// setupToken authorises the first-run wizard (see SetSetupToken).
	setupToken string
	// onSessionsEnded runs after all sessions were ended (closes live sockets).
	onSessionsEnded func()
	rotateMu        sync.Mutex
}

// SetOnSessionsEnded sets what else to cut off when all sessions end.
func (h *AuthHandler) SetOnSessionsEnded(fn func()) { h.onSessionsEnded = fn }

// SetOnRecoveryCodesChanged sets the callback that persists recovery codes.
func (h *AuthHandler) SetOnRecoveryCodesChanged(fn func([]auth.RecoveryCode)) {
	h.onRecoveryCodesChanged = fn
}

// NewAuthHandler creates a new AuthHandler.
func NewAuthHandler(jwt *auth.JWTManager, webauthn *auth.WebAuthnManager, recovery *auth.RecoveryManager, password *auth.PasswordManager, limiter *auth.RateLimiter, klever *auth.KleverAuthManager) *AuthHandler {
	return &AuthHandler{
		jwt:      jwt,
		webauthn: webauthn,
		recovery: recovery,
		password: password,
		limiter:  limiter,
		klever:   klever,
	}
}

// SetOnCredentialsChanged sets the callback for credential persistence.
func (h *AuthHandler) SetOnCredentialsChanged(fn func([]auth.PasskeyCredential)) {
	h.onCredentialsChanged = fn
}

// SetOnPasswordChanged sets the callback for password hash persistence.
func (h *AuthHandler) SetOnPasswordChanged(fn func(string)) {
	h.onPasswordChanged = fn
}

// SetOnKleverAddressChanged sets the callback for Klever address persistence.
func (h *AuthHandler) SetOnKleverAddressChanged(fn func(string)) {
	h.onKleverAddressChanged = fn
}

// HandleSetupStatus returns whether initial setup has been completed.
// GET /api/setup/status
func (h *AuthHandler) HandleSetupStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"setup_complete": h.setupComplete(),
		"has_password":   h.password.HasPassword(),
		"passkey_count":  h.webauthn.CredentialCount(),
		"has_klever":     h.klever != nil && h.klever.HasAddress(),
	})
}

// SetSigningKeySaver sets how a new JWT signing key is persisted.
func (h *AuthHandler) SetSigningKeySaver(fn func([]byte) error) { h.saveSigningKey = fn }

// endAllSessions invalidates every issued token by replacing the JWT signing
// key, then logs the caller back in so that only this session survives. Used
// after a credential change, so a stolen session can't outlive it.
func (h *AuthHandler) endAllSessions(w http.ResponseWriter) (*auth.TokenPair, error) {
	// One at a time, so the persisted key is always the one in use.
	h.rotateMu.Lock()
	defer h.rotateMu.Unlock()
	key, err := auth.GenerateSigningKey()
	if err != nil {
		return nil, err
	}
	// Persist first: a key that only lives in memory would bring the old
	// sessions back after a restart.
	if h.saveSigningKey != nil {
		if err := h.saveSigningKey(key); err != nil {
			return nil, fmt.Errorf("save signing key: %w", err)
		}
	}
	if err := h.jwt.SetSigningKey(key); err != nil {
		return nil, err
	}
	tokens, err := h.jwt.IssueTokenPair("admin")
	if err != nil {
		return nil, err
	}
	setAuthCookies(w, tokens)
	if h.onSessionsEnded != nil {
		h.onSessionsEnded()
	}
	log.Println("auth: all sessions ended (JWT signing key rotated)")
	return tokens, nil
}

// HandleLogoutAll ends every session except the caller's.
// POST /api/auth/logout-all
func (h *AuthHandler) HandleLogoutAll(w http.ResponseWriter, _ *http.Request) {
	tokens, err := h.endAllSessions(w)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "all other sessions ended", "access_token": tokens.AccessToken})
}

// setupComplete reports whether any login method is configured.
func (h *AuthHandler) setupComplete() bool {
	return h.password.HasPassword() || h.webauthn.HasCredentials() || (h.klever != nil && h.klever.HasAddress())
}

// SetSetupToken sets the one-time token the first-run wizard must present
// (printed to the dashboard log at startup). Whoever reaches a fresh install
// first would otherwise own it.
func (h *AuthHandler) SetSetupToken(token string) { h.setupToken = token }

// validSetupToken compares in constant time; with no token configured it fails.
func (h *AuthHandler) validSetupToken(given string) bool {
	return h.setupToken != "" && subtle.ConstantTimeCompare([]byte(given), []byte(h.setupToken)) == 1
}

// HandleSetupPassword sets the initial password during first-time setup.
// POST /api/setup/password
func (h *AuthHandler) HandleSetupPassword(w http.ResponseWriter, r *http.Request) {
	if h.password.HasPassword() {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "password already configured"})
		return
	}
	var req struct {
		Password   string `json:"password"`
		SetupToken string `json:"setup_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}

	// This route is public, so it must not let anyone add a login method:
	// after setup only a logged-in user may (e.g. a passkey-only hub adding a
	// password), before setup only the holder of the setup token.
	// 403, not 401: the browser treats 401 as an expired session and would
	// refresh-and-redirect instead of showing the message.
	if !auth.Authenticated(h.jwt, r) {
		if h.setupComplete() {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "setup already completed: log in to set a password"})
			return
		}
		if !h.validSetupToken(req.SetupToken) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "invalid setup token: use the token printed in the dashboard log at startup"})
			return
		}
	}

	hash, err := h.password.SetPassword(req.Password)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	if h.onPasswordChanged != nil {
		h.onPasswordChanged(hash)
	}

	// Auto-login after setup
	tokens, err := h.jwt.IssueTokenPair("admin")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "token generation failed"})
		return
	}

	setAuthCookies(w, tokens)
	writeJSON(w, http.StatusOK, map[string]any{
		"status":       "password set",
		"access_token": tokens.AccessToken,
	})
}

// HandlePasswordLogin authenticates via password.
// POST /api/auth/password
func (h *AuthHandler) HandlePasswordLogin(w http.ResponseWriter, r *http.Request) {
	ip := extractIP(r)

	if !h.limiter.Allow(ip) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many login attempts, please try again later"})
		return
	}

	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}

	if !h.password.Verify(req.Password) {
		h.limiter.RecordFailure(ip)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid password"})
		return
	}

	h.limiter.Reset(ip)

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

// HandleChangePassword changes the admin password (requires current password).
// PUT /api/auth/password
func (h *AuthHandler) HandleChangePassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}

	if !h.password.Verify(req.CurrentPassword) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "current password is incorrect"})
		return
	}

	hash, err := h.password.SetPassword(req.NewPassword)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	if h.onPasswordChanged != nil {
		h.onPasswordChanged(hash)
	}

	tokens, err := h.endAllSessions(w)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "password changed, but ending other sessions failed: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "password changed", "access_token": tokens.AccessToken})
}

// trustedProxies are extra reverse-proxy networks (--trusted-proxy), e.g. the
// Docker bridge when the hub runs in a container behind a host proxy.
var trustedProxies []*net.IPNet

// SetTrustedProxies sets the networks whose X-Forwarded-For is believed.
func SetTrustedProxies(nets []*net.IPNet) { trustedProxies = nets }

func isTrustedProxy(ip net.IP) bool {
	for _, n := range trustedProxies {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// extractIP returns the client IP used for rate limiting. X-Forwarded-For is
// trusted only when the request comes from a local reverse proxy, and then only
// its rightmost entry (added by the proxy): anything left of it was sent by the
// client and can be forged to dodge the limiter.
func extractIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsLoopback() || isTrustedProxy(ip)) {
		if vals := r.Header.Values("X-Forwarded-For"); len(vals) > 0 {
			parts := strings.Split(vals[len(vals)-1], ",")
			if last := strings.TrimSpace(parts[len(parts)-1]); last != "" {
				return last
			}
		}
	}
	return host
}

// HandlePasskeyBeginRegister starts the WebAuthn registration ceremony.
// POST /api/auth/passkey/register/begin
func (h *AuthHandler) HandlePasskeyBeginRegister(w http.ResponseWriter, r *http.Request) {
	// Public route, so a session is required: the setup wizard registers its
	// passkey after the password step, when it already has one.
	if !auth.Authenticated(h.jwt, r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
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
	// Checked again here, not only at begin: this is the step that stores the
	// credential.
	if !auth.Authenticated(h.jwt, r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
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

	// Rate-limited like password login: each attempt runs up to ten Argon2id
	// hashes, so without a limit it is also a cheap way to load the server.
	ip := extractIP(r)
	if h.limiter != nil && !h.limiter.AllowIP(ip) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many failed attempts, try again later"})
		return
	}
	if !h.recovery.Verify(req.Code) {
		if h.limiter != nil {
			h.limiter.RecordFailure(ip)
		}
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid recovery code"})
		return
	}
	if h.limiter != nil {
		h.limiter.Reset(ip)
	}
	// Persist that the code is used, or it would work again after a restart.
	if h.onRecoveryCodesChanged != nil {
		h.onRecoveryCodesChanged(h.recovery.Codes())
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

	// Never delete the last login method: with none left the hub counts as
	// not set up, and the public setup routes would let anyone enroll.
	if h.webauthn.CredentialCount() <= 1 && !h.password.HasPassword() && (h.klever == nil || !h.klever.HasAddress()) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "cannot delete the last login method: add a password or another passkey first"})
		return
	}

	if err := h.webauthn.DeletePasskey(passkeyID); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	if h.onCredentialsChanged != nil {
		h.onCredentialsChanged(h.webauthn.Credentials())
	}

	// Sessions opened with the removed passkey must not outlive it.
	tokens, err := h.endAllSessions(w)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "passkey deleted, but ending other sessions failed: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "deleted", "access_token": tokens.AccessToken})
}

// HandleKleverChallenge generates a challenge nonce for Klever Extension login.
// GET /api/auth/klever/challenge?address=klv1...
func (h *AuthHandler) HandleKleverChallenge(w http.ResponseWriter, r *http.Request) {
	address := r.URL.Query().Get("address")
	if address == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "address parameter required"})
		return
	}

	nonce, err := h.klever.CreateChallenge(address)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"challenge": nonce,
	})
}

// HandleKleverVerify verifies a Klever Extension signature and issues JWT tokens.
// POST /api/auth/klever/verify
func (h *AuthHandler) HandleKleverVerify(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Address   string `json:"address"`
		Challenge string `json:"challenge"`
		Signature string `json:"signature"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}

	if err := h.klever.VerifySignature(req.Address, req.Challenge, req.Signature); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
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

// HandleKleverSetupChallenge generates a challenge for wallet linking (protected).
// POST /api/setup/klever/challenge
func (h *AuthHandler) HandleKleverSetupChallenge(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Address string `json:"address"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}

	nonce, err := h.klever.CreateSetupChallenge(req.Address)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"challenge": nonce})
}

// HandleKleverSetup verifies ownership and registers a Klever admin address (protected).
// POST /api/setup/klever
func (h *AuthHandler) HandleKleverSetup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Address   string `json:"address"`
		Challenge string `json:"challenge"`
		Signature string `json:"signature"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}

	if err := h.klever.VerifySetupSignature(req.Address, req.Challenge, req.Signature); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "signature verification failed: " + err.Error()})
		return
	}

	if err := h.klever.SetAddress(req.Address); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	if h.onKleverAddressChanged != nil {
		h.onKleverAddressChanged(req.Address)
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "address registered"})
}

// HandleKleverRemove removes the registered Klever admin address (protected).
// DELETE /api/auth/klever
func (h *AuthHandler) HandleKleverRemove(w http.ResponseWriter, r *http.Request) {
	// Same rule as HandleDeletePasskey: never remove the last login method.
	if !h.password.HasPassword() && !h.webauthn.HasCredentials() {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "cannot remove the last login method: add a password or passkey first"})
		return
	}
	h.klever.RemoveAddress()

	if h.onKleverAddressChanged != nil {
		h.onKleverAddressChanged("")
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "address removed"})
}

// HandleKleverStatus returns the current Klever auth configuration (protected).
// GET /api/auth/klever
func (h *AuthHandler) HandleKleverStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"has_address": h.klever.HasAddress(),
		"address":     h.klever.Address(),
	})
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
