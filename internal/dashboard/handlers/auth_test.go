package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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

	handler := NewAuthHandler(jwt, webauthn, recovery)
	return handler, codes
}

func TestHandleSetupStatus_NoPasskeys(t *testing.T) {
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
		t.Error("setup_complete should be false without passkeys")
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
