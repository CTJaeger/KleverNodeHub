package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/CTJaeger/KleverNodeHub/internal/crypto"
	"github.com/CTJaeger/KleverNodeHub/internal/dashboard"
	"github.com/CTJaeger/KleverNodeHub/internal/models"
	"github.com/CTJaeger/KleverNodeHub/internal/store"
)

func setupRegistrationHandler(t *testing.T) (*RegistrationHandler, *dashboard.TokenManager) {
	t.Helper()

	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	serverStore := store.NewServerStore(db)
	ca, err := crypto.NewCA()
	if err != nil {
		t.Fatalf("new CA: %v", err)
	}

	tm := dashboard.NewTokenManager()
	handler := NewRegistrationHandler(tm, serverStore, ca)
	return handler, tm
}

func TestHandleRegisterAgent_Success(t *testing.T) {
	handler, tm := setupRegistrationHandler(t)

	token, err := tm.Generate()
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}

	reqBody, _ := json.Marshal(models.RegistrationRequest{
		Token:    token,
		Hostname: "test-agent",
		OS:       "linux/amd64",
		IP:       "10.0.0.5",
	})

	req := httptest.NewRequest(http.MethodPost, "/api/agent/register", bytes.NewReader(reqBody))
	w := httptest.NewRecorder()

	handler.HandleRegisterAgent(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body: %s)", w.Code, http.StatusOK, w.Body.String())
	}

	var resp models.RegistrationResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if resp.ServerID == "" {
		t.Error("expected server_id in response")
	}
	if resp.CertPEM == "" {
		t.Error("expected cert_pem in response")
	}
	if resp.KeyPEM == "" {
		t.Error("expected key_pem in response")
	}
	if resp.CACertPEM == "" {
		t.Error("expected ca_cert_pem in response")
	}
}

func TestHandleRegisterAgent_InvalidToken(t *testing.T) {
	handler, _ := setupRegistrationHandler(t)

	reqBody, _ := json.Marshal(models.RegistrationRequest{
		Token:    "invalid-token",
		Hostname: "test-agent",
	})

	req := httptest.NewRequest(http.MethodPost, "/api/agent/register", bytes.NewReader(reqBody))
	w := httptest.NewRecorder()

	handler.HandleRegisterAgent(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestHandleRegisterAgent_TokenSingleUse(t *testing.T) {
	handler, tm := setupRegistrationHandler(t)

	token, _ := tm.Generate()

	reqBody, _ := json.Marshal(models.RegistrationRequest{
		Token:    token,
		Hostname: "test-agent",
	})

	// First call should succeed
	req := httptest.NewRequest(http.MethodPost, "/api/agent/register", bytes.NewReader(reqBody))
	w := httptest.NewRecorder()
	handler.HandleRegisterAgent(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("first call: status = %d, want %d", w.Code, http.StatusOK)
	}

	// Second call with same token should fail
	reqBody, _ = json.Marshal(models.RegistrationRequest{
		Token:    token,
		Hostname: "test-agent-2",
	})
	req = httptest.NewRequest(http.MethodPost, "/api/agent/register", bytes.NewReader(reqBody))
	w = httptest.NewRecorder()
	handler.HandleRegisterAgent(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("second call: status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestHandleRegisterAgent_InvalidBody(t *testing.T) {
	handler, _ := setupRegistrationHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/api/agent/register", bytes.NewReader([]byte("not json")))
	w := httptest.NewRecorder()

	handler.HandleRegisterAgent(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleGenerateToken(t *testing.T) {
	handler, _ := setupRegistrationHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/api/registration/token", nil)
	w := httptest.NewRecorder()

	handler.HandleGenerateToken(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["token"] == "" {
		t.Error("expected token in response")
	}
}
