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

	body, _ := json.Marshal(map[string]string{"password": "testpassword123"})
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

	body, _ := json.Marshal(map[string]string{"password": "testpassword123"})
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
	body, _ := json.Marshal(map[string]string{"password": "mypassword123"})
	req := httptest.NewRequest(http.MethodPost, "/api/setup/password", bytes.NewReader(body))
	w := httptest.NewRecorder()
	handler.HandleSetupPassword(w, req)

	// Login
	body2, _ := json.Marshal(map[string]string{"password": "mypassword123"})
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
	body, _ := json.Marshal(map[string]string{"password": "mypassword123"})
	req := httptest.NewRequest(http.MethodPost, "/api/setup/password", bytes.NewReader(body))
	w := httptest.NewRecorder()
	handler.HandleSetupPassword(w, req)

	// Wrong password
	body2, _ := json.Marshal(map[string]string{"password": "wrongpassword"})
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
