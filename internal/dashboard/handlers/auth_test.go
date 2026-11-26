package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/CTJaeger/KleverNodeHub/internal/auth"
)

const testSetupToken = "test-setup-token"

func setupAuthHandler(t *testing.T) (*AuthHandler, []string) {
	t.Helper()
	jwt, err := auth.NewJWTManager([]byte("test-secret-key-that-is-long-enough-32"))
	if err != nil {
		t.Fatalf("NewJWTManager: %v", err)
	}

	webauthn, err := auth.NewWebAuthnManager(auth.WebAuthnConfig{
		RPDisplayName: "Test",
		RPID:          "localhost",
		RPOrigins:     []string{"https://localhost:9443"},
	}, nil)
	if err != nil {
		t.Fatalf("NewWebAuthnManager: %v", err)
	}

	recovery := auth.NewRecoveryManager(nil)
	codes, _, err := recovery.GenerateCodes()
	if err != nil {
		t.Fatalf("GenerateCodes: %v", err)
	}

	password := auth.NewPasswordManager("")
	limiter := auth.NewRateLimiter(15*time.Minute, 5)
	klever := auth.NewKleverAuthManager("")

	handler := NewAuthHandler(jwt, webauthn, recovery, password, limiter, klever)
	handler.SetSetupToken(testSetupToken)
	return handler, codes
}

func TestHandleSetupStatus_NoPasskeysNoPassword(t *testing.T) {
	handler, _ := setupAuthHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/setup/status", nil)
	w := httptest.NewRecorder()

	handler.HandleSetupStatus(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp map[string]any
	_ = json.NewDecoder(w.Body).Decode(&resp)

	if resp["setup_complete"] != false {
		t.Error("setup_complete should be false without passkeys or password")
	}
	if resp["has_password"] != false {
		t.Error("has_password should be false")
	}
}

func TestHandleSetupPassword(t *testing.T) {
	handler, _ := setupAuthHandler(t)

	body, _ := json.Marshal(map[string]string{"password": "testpassword123", "setup_token": testSetupToken})
	req := httptest.NewRequest(http.MethodPost, "/api/setup/password", bytes.NewReader(body))
	w := httptest.NewRecorder()

	handler.HandleSetupPassword(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d (body: %s)", w.Code, http.StatusOK, w.Body.String())
	}

	var resp map[string]any
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp["access_token"] == nil || resp["access_token"] == "" {
		t.Error("expected access_token after setup")
	}

	// Setup complete should now be true
	req2 := httptest.NewRequest(http.MethodGet, "/api/setup/status", nil)
	w2 := httptest.NewRecorder()
	handler.HandleSetupStatus(w2, req2)

	var status map[string]any
	_ = json.NewDecoder(w2.Body).Decode(&status)
	if status["setup_complete"] != true {
		t.Error("setup_complete should be true after password set")
	}
}

func TestHandleSetupPassword_AlreadySet(t *testing.T) {
	handler, _ := setupAuthHandler(t)

	body, _ := json.Marshal(map[string]string{"password": "testpassword123", "setup_token": testSetupToken})
	req := httptest.NewRequest(http.MethodPost, "/api/setup/password", bytes.NewReader(body))
	w := httptest.NewRecorder()
	handler.HandleSetupPassword(w, req)

	// Try again
	req2 := httptest.NewRequest(http.MethodPost, "/api/setup/password", bytes.NewReader(body))
	w2 := httptest.NewRecorder()
	handler.HandleSetupPassword(w2, req2)

	if w2.Code != http.StatusConflict {
		t.Errorf("status = %d, want %d", w2.Code, http.StatusConflict)
	}
}

func TestHandlePasswordLogin_Valid(t *testing.T) {
	handler, _ := setupAuthHandler(t)

	// Set password first
	body, _ := json.Marshal(map[string]string{"password": "mypassword123", "setup_token": testSetupToken})
	req := httptest.NewRequest(http.MethodPost, "/api/setup/password", bytes.NewReader(body))
	w := httptest.NewRecorder()
	handler.HandleSetupPassword(w, req)

	// Login
	body2, _ := json.Marshal(map[string]string{"password": "mypassword123", "setup_token": testSetupToken})
	req2 := httptest.NewRequest(http.MethodPost, "/api/auth/password", bytes.NewReader(body2))
	req2.RemoteAddr = "127.0.0.1:12345"
	w2 := httptest.NewRecorder()
	handler.HandlePasswordLogin(w2, req2)

	if w2.Code != http.StatusOK {
		t.Errorf("status = %d, want %d (body: %s)", w2.Code, http.StatusOK, w2.Body.String())
	}
}

func TestHandlePasswordLogin_Invalid(t *testing.T) {
	handler, _ := setupAuthHandler(t)

	// Set password
	body, _ := json.Marshal(map[string]string{"password": "mypassword123", "setup_token": testSetupToken})
	req := httptest.NewRequest(http.MethodPost, "/api/setup/password", bytes.NewReader(body))
	w := httptest.NewRecorder()
	handler.HandleSetupPassword(w, req)

	// Wrong password
	body2, _ := json.Marshal(map[string]string{"password": "wrongpassword", "setup_token": testSetupToken})
	req2 := httptest.NewRequest(http.MethodPost, "/api/auth/password", bytes.NewReader(body2))
	req2.RemoteAddr = "127.0.0.1:12345"
	w2 := httptest.NewRecorder()
	handler.HandlePasswordLogin(w2, req2)

	if w2.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w2.Code, http.StatusUnauthorized)
	}
}

func TestHandleRecoveryLogin_Valid(t *testing.T) {
	handler, codes := setupAuthHandler(t)

	body, _ := json.Marshal(map[string]string{"code": codes[0]})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/recovery", bytes.NewReader(body))
	w := httptest.NewRecorder()

	handler.HandleRecoveryLogin(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d (body: %s)", w.Code, http.StatusOK, w.Body.String())
	}

	var resp map[string]any
	_ = json.NewDecoder(w.Body).Decode(&resp)

	if resp["access_token"] == nil || resp["access_token"] == "" {
		t.Error("expected access_token in response")
	}
}

func TestHandleRecoveryLogin_Invalid(t *testing.T) {
	handler, _ := setupAuthHandler(t)

	body, _ := json.Marshal(map[string]string{"code": "XXXX-XXXX-XXXX"})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/recovery", bytes.NewReader(body))
	w := httptest.NewRecorder()

	handler.HandleRecoveryLogin(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestHandleLogout(t *testing.T) {
	handler, _ := setupAuthHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	w := httptest.NewRecorder()

	handler.HandleLogout(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	// Check cookies are cleared
	cookies := w.Result().Cookies()
	for _, c := range cookies {
		if c.Name == "access_token" && c.MaxAge != -1 {
			t.Error("access_token cookie should have MaxAge -1")
		}
	}
}

func TestHandleRefresh_MissingToken(t *testing.T) {
	handler, _ := setupAuthHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/api/auth/refresh", bytes.NewReader([]byte("{}")))
	w := httptest.NewRecorder()

	handler.HandleRefresh(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandlePasskeyBeginLogin_NoCredentials(t *testing.T) {
	handler, _ := setupAuthHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/api/auth/passkey/login/begin", bytes.NewReader([]byte("{}")))
	w := httptest.NewRecorder()

	handler.HandlePasskeyBeginLogin(w, req)

	// Should fail because no credentials are registered
	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

// completeSetup sets the initial password and returns the session token it issues.
func completeSetup(t *testing.T, handler *AuthHandler) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"password": "testpassword123", "setup_token": testSetupToken})
	w := httptest.NewRecorder()
	handler.HandleSetupPassword(w, httptest.NewRequest(http.MethodPost, "/api/setup/password", bytes.NewReader(body)))
	if w.Code != http.StatusOK {
		t.Fatalf("setup password: status = %d (body: %s)", w.Code, w.Body.String())
	}
	var resp map[string]any
	_ = json.NewDecoder(w.Body).Decode(&resp)
	tok, _ := resp["access_token"].(string)
	if tok == "" {
		t.Fatal("setup password returned no access_token")
	}
	return tok
}

func TestPasskeyRegister_NeedsSessionEvenBeforeSetup(t *testing.T) {
	handler, _ := setupAuthHandler(t)

	body, _ := json.Marshal(map[string]string{"name": "first"})
	w := httptest.NewRecorder()
	handler.HandlePasskeyBeginRegister(w, httptest.NewRequest(http.MethodPost, "/api/auth/passkey/register/begin", bytes.NewReader(body)))

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d: the wizard registers passkeys only after the password step", w.Code, http.StatusUnauthorized)
	}
}

func TestHandleSetupPassword_RequiresSetupToken(t *testing.T) {
	for _, tok := range []string{"", "wrong-token"} {
		handler, _ := setupAuthHandler(t)
		body, _ := json.Marshal(map[string]string{"password": "otherpassword123", "setup_token": tok})
		w := httptest.NewRecorder()
		handler.HandleSetupPassword(w, httptest.NewRequest(http.MethodPost, "/api/setup/password", bytes.NewReader(body)))
		if w.Code != http.StatusForbidden {
			t.Errorf("token %q: status = %d, want %d", tok, w.Code, http.StatusForbidden)
		}
		if handler.password.HasPassword() {
			t.Errorf("token %q: password was set", tok)
		}
	}

	handler, _ := setupAuthHandler(t)
	handler.SetSetupToken("") // no token configured: fail closed
	body, _ := json.Marshal(map[string]string{"password": "otherpassword123", "setup_token": ""})
	w := httptest.NewRecorder()
	handler.HandleSetupPassword(w, httptest.NewRequest(http.MethodPost, "/api/setup/password", bytes.NewReader(body)))
	if w.Code != http.StatusForbidden {
		t.Errorf("no configured token: status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestPasskeyRegister_AfterSetupRequiresSession(t *testing.T) {
	handler, _ := setupAuthHandler(t)
	token := completeSetup(t, handler)

	body, _ := json.Marshal(map[string]string{"name": "second"})
	w := httptest.NewRecorder()
	handler.HandlePasskeyBeginRegister(w, httptest.NewRequest(http.MethodPost, "/api/auth/passkey/register/begin", bytes.NewReader(body)))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("begin without session: status = %d, want %d", w.Code, http.StatusUnauthorized)
	}

	w = httptest.NewRecorder()
	handler.HandlePasskeyFinishRegister(w, httptest.NewRequest(http.MethodPost, "/api/auth/passkey/register/finish?session_id=x&name=second", bytes.NewReader([]byte("{}"))))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("finish without session: status = %d, want %d", w.Code, http.StatusUnauthorized)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/auth/passkey/register/begin", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer forged.token.value")
	w = httptest.NewRecorder()
	handler.HandlePasskeyBeginRegister(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("begin with invalid token: status = %d, want %d", w.Code, http.StatusUnauthorized)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/auth/passkey/register/begin", bytes.NewReader(body))
	req.AddCookie(&http.Cookie{Name: "access_token", Value: token})
	w = httptest.NewRecorder()
	handler.HandlePasskeyBeginRegister(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("begin with session: status = %d, want %d (body: %s)", w.Code, http.StatusOK, w.Body.String())
	}
}

func TestHandleSetupPassword_RefusedWhenOtherLoginExists(t *testing.T) {
	handler, _ := setupAuthHandler(t)
	// A hub that logs in by Klever wallet only has no password, but setup is done.
	handler.klever = auth.NewKleverAuthManager("klv1admin")

	body, _ := json.Marshal(map[string]string{"password": "otherpassword123", "setup_token": testSetupToken})
	w := httptest.NewRecorder()
	handler.HandleSetupPassword(w, httptest.NewRequest(http.MethodPost, "/api/setup/password", bytes.NewReader(body)))

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d (body: %s)", w.Code, http.StatusForbidden, w.Body.String())
	}
	if handler.password.HasPassword() {
		t.Error("password must not be set by an unauthenticated caller after setup")
	}
}

func TestPasskeyRegister_RefreshTokenRejected(t *testing.T) {
	handler, _ := setupAuthHandler(t)
	completeSetup(t, handler)
	pair, err := handler.jwt.IssueTokenPair("admin")
	if err != nil {
		t.Fatalf("IssueTokenPair: %v", err)
	}

	body, _ := json.Marshal(map[string]string{"name": "x"})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/passkey/register/begin", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+pair.RefreshToken)
	w := httptest.NewRecorder()
	handler.HandlePasskeyBeginRegister(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("refresh token: status = %d, want %d", w.Code, http.StatusUnauthorized)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/auth/passkey/register/begin", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+pair.AccessToken)
	w = httptest.NewRecorder()
	handler.HandlePasskeyBeginRegister(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("bearer access token: status = %d, want %d (body: %s)", w.Code, http.StatusOK, w.Body.String())
	}
}

func TestHandleSetupStatus_KleverOnlyIsComplete(t *testing.T) {
	handler, _ := setupAuthHandler(t)
	handler.klever = auth.NewKleverAuthManager("klv1admin")

	w := httptest.NewRecorder()
	handler.HandleSetupStatus(w, httptest.NewRequest(http.MethodGet, "/api/setup/status", nil))
	var resp map[string]any
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp["setup_complete"] != true {
		t.Errorf("setup_complete = %v, want true for a Klever-only hub", resp["setup_complete"])
	}
}

func TestLastLoginMethodCannotBeRemoved(t *testing.T) {
	handler, _ := setupAuthHandler(t)
	handler.klever = auth.NewKleverAuthManager("klv1admin")

	w := httptest.NewRecorder()
	handler.HandleKleverRemove(w, httptest.NewRequest(http.MethodDelete, "/api/setup/klever", nil))
	if w.Code != http.StatusConflict {
		t.Errorf("remove last Klever login: status = %d, want %d", w.Code, http.StatusConflict)
	}
	if !handler.klever.HasAddress() {
		t.Error("Klever address was removed although it was the last login method")
	}

	if _, err := handler.password.SetPassword("ownerpassword123"); err != nil { // a second login method exists, so removal is allowed
		t.Fatalf("SetPassword: %v", err)
	}
	w = httptest.NewRecorder()
	handler.HandleKleverRemove(w, httptest.NewRequest(http.MethodDelete, "/api/setup/klever", nil))
	if w.Code != http.StatusOK {
		t.Errorf("remove Klever with password present: status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestLogoutAll_InvalidatesOldTokens(t *testing.T) {
	handler, _ := setupAuthHandler(t)
	old := completeSetup(t, handler)
	var saved []byte
	handler.SetSigningKeySaver(func(k []byte) error { saved = k; return nil })

	w := httptest.NewRecorder()
	handler.HandleLogoutAll(w, httptest.NewRequest(http.MethodPost, "/api/auth/logout-all", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d (body: %s)", w.Code, w.Body.String())
	}
	if len(saved) < 32 {
		t.Error("new signing key was not persisted")
	}
	if _, err := handler.jwt.ValidateAccessToken(old); err == nil {
		t.Error("token issued before logout-all is still valid")
	}
	var resp map[string]any
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if tok, _ := resp["access_token"].(string); tok == "" {
		t.Error("caller got no new token")
	} else if _, err := handler.jwt.ValidateAccessToken(tok); err != nil {
		t.Errorf("caller's new token invalid: %v", err)
	}
}

func TestExtractIP_ForwardedOnlyFromLocalProxy(t *testing.T) {
	cases := []struct{ remote, xff, want string }{
		{"203.0.113.5:4000", "1.2.3.4", "203.0.113.5"},              // direct client can't spoof
		{"127.0.0.1:4000", "6.6.6.6, 198.51.100.7", "198.51.100.7"}, // proxy: rightmost entry
		{"127.0.0.1:4000", "", "127.0.0.1"},
		{"[::1]:4000", "198.51.100.8", "198.51.100.8"},
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodPost, "/", nil)
		r.RemoteAddr = c.remote
		if c.xff != "" {
			r.Header.Set("X-Forwarded-For", c.xff)
		}
		if got := extractIP(r); got != c.want {
			t.Errorf("remote %s xff %q: got %s, want %s", c.remote, c.xff, got, c.want)
		}
	}
}

func TestHandleRecoveryLogin_PersistsAndRateLimits(t *testing.T) {
	handler, codes := setupAuthHandler(t)
	var persisted []auth.RecoveryCode
	handler.SetOnRecoveryCodesChanged(func(c []auth.RecoveryCode) { persisted = c })

	login := func(code string) int {
		body, _ := json.Marshal(map[string]string{"code": code})
		r := httptest.NewRequest(http.MethodPost, "/api/auth/recovery", bytes.NewReader(body))
		r.RemoteAddr = "198.51.100.1:5000"
		w := httptest.NewRecorder()
		handler.HandleRecoveryLogin(w, r)
		return w.Code
	}

	if got := login(codes[0]); got != http.StatusOK {
		t.Fatalf("valid code: status %d", got)
	}
	used := 0
	for _, c := range persisted {
		if c.Used {
			used++
		}
	}
	if used != 1 {
		t.Errorf("persisted codes mark %d as used, want 1", used)
	}

	for i := 0; i < 5; i++ {
		login("WRONG-CODE")
	}
	if got := login(codes[1]); got != http.StatusTooManyRequests {
		t.Errorf("after 5 failures: status %d, want %d", got, http.StatusTooManyRequests)
	}
}
