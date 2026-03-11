package auth

import (
	"testing"
)

func newTestWebAuthnManager(t *testing.T) *WebAuthnManager {
	t.Helper()
	wm, err := NewWebAuthnManager(WebAuthnConfig{
		RPDisplayName: "Test Hub",
		RPID:          "localhost",
		RPOrigins:     []string{"https://localhost:9443"},
	}, nil)
	if err != nil {
		t.Fatalf("create manager: %v", err)
	}
	return wm
}

func TestNewWebAuthnManager(t *testing.T) {
	wm := newTestWebAuthnManager(t)

	if wm.HasCredentials() {
		t.Error("new manager should have no credentials")
	}
	if wm.CredentialCount() != 0 {
		t.Errorf("count = %d, want 0", wm.CredentialCount())
	}
}

func TestNewWebAuthnManager_WithExistingCredentials(t *testing.T) {
	creds := []PasskeyCredential{
		{ID: "abc123", Name: "MacBook", SignCount: 5, RegisteredAt: 1000},
	}
	wm, err := NewWebAuthnManager(WebAuthnConfig{
		RPDisplayName: "Test Hub",
		RPID:          "localhost",
		RPOrigins:     []string{"https://localhost:9443"},
	}, creds)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if !wm.HasCredentials() {
		t.Error("should have credentials")
	}
	if wm.CredentialCount() != 1 {
		t.Errorf("count = %d, want 1", wm.CredentialCount())
	}
}

func TestBeginRegistration(t *testing.T) {
	wm := newTestWebAuthnManager(t)

	options, sessionID, err := wm.BeginRegistration("Test Key")
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	if options == nil {
		t.Fatal("options should not be nil")
	}
	if sessionID == "" {
		t.Error("session ID should not be empty")
	}
	if options.Response.RelyingParty.Name != "Test Hub" {
		t.Errorf("RP name = %q, want %q", options.Response.RelyingParty.Name, "Test Hub")
	}
}

func TestBeginLogin_NoCredentials(t *testing.T) {
	wm := newTestWebAuthnManager(t)

	_, _, err := wm.BeginLogin()
	if err == nil {
		t.Error("expected error when no credentials registered")
	}
}

func TestDeletePasskey_LastOne(t *testing.T) {
	creds := []PasskeyCredential{
		{ID: "abc123", Name: "Only Key"},
	}
	wm, _ := NewWebAuthnManager(WebAuthnConfig{
		RPDisplayName: "Test",
		RPID:          "localhost",
		RPOrigins:     []string{"https://localhost:9443"},
	}, creds)

	err := wm.DeletePasskey("abc123")
	if err == nil {
		t.Error("should not allow deleting last passkey")
	}
}

func TestDeletePasskey_OneOfMany(t *testing.T) {
	creds := []PasskeyCredential{
		{ID: "key1", Name: "Key 1"},
		{ID: "key2", Name: "Key 2"},
	}
	wm, _ := NewWebAuthnManager(WebAuthnConfig{
		RPDisplayName: "Test",
		RPID:          "localhost",
		RPOrigins:     []string{"https://localhost:9443"},
	}, creds)

	err := wm.DeletePasskey("key1")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}

	if wm.CredentialCount() != 1 {
		t.Errorf("count = %d, want 1", wm.CredentialCount())
	}

	remaining := wm.Credentials()
	if remaining[0].ID != "key2" {
		t.Errorf("remaining key = %q, want %q", remaining[0].ID, "key2")
	}
}

func TestDeletePasskey_NotFound(t *testing.T) {
	creds := []PasskeyCredential{
		{ID: "key1", Name: "Key 1"},
		{ID: "key2", Name: "Key 2"},
	}
	wm, _ := NewWebAuthnManager(WebAuthnConfig{
		RPDisplayName: "Test",
		RPID:          "localhost",
		RPOrigins:     []string{"https://localhost:9443"},
	}, creds)

	err := wm.DeletePasskey("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent key")
	}
}

func TestCredentialsReturnsCopy(t *testing.T) {
	creds := []PasskeyCredential{
		{ID: "key1", Name: "Key 1"},
	}
	wm, _ := NewWebAuthnManager(WebAuthnConfig{
		RPDisplayName: "Test",
		RPID:          "localhost",
		RPOrigins:     []string{"https://localhost:9443"},
	}, creds)

	c1 := wm.Credentials()
	c1[0].Name = "Modified"

	c2 := wm.Credentials()
	if c2[0].Name != "Key 1" {
		t.Error("modifying returned credentials should not affect manager")
	}
}

func TestFinishRegistration_InvalidSession(t *testing.T) {
	wm := newTestWebAuthnManager(t)

	err := wm.FinishRegistration("nonexistent-session", nil, "Test")
	if err == nil {
		t.Error("expected error for invalid session")
	}
}

func TestFinishLogin_InvalidSession(t *testing.T) {
	wm := newTestWebAuthnManager(t)

	err := wm.FinishLogin("nonexistent-session", nil)
	if err == nil {
		t.Error("expected error for invalid session")
	}
}
