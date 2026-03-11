// Package auth implements authentication for the Klever Node Hub dashboard.
//
// DEPENDENCY NOTE: This package uses github.com/go-webauthn/webauthn as the
// sole exception to the stdlib-only rule. WebAuthn/FIDO2 requires CBOR parsing,
// attestation verification, and compliance with W3C WebAuthn spec — implementing
// this manually would be error-prone and a security risk. The go-webauthn library
// is the standard Go implementation, actively maintained, and widely audited.
// All other auth components (JWT, recovery codes, middleware) use stdlib only.
package auth

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

// PasskeyCredential stores a registered WebAuthn credential.
type PasskeyCredential struct {
	ID           string `json:"id"`            // Credential ID (hex-encoded)
	PublicKey    []byte `json:"public_key"`     // COSE public key
	Name         string `json:"name"`           // User-friendly name (e.g., "MacBook Pro")
	SignCount    uint32 `json:"sign_count"`     // Signature counter (replay detection)
	RegisteredAt int64  `json:"registered_at"`  // Unix timestamp
}

// WebAuthnManager handles passkey registration and authentication.
type WebAuthnManager struct {
	mu          sync.Mutex
	wa          *webauthn.WebAuthn
	credentials []PasskeyCredential
	// Session data for ongoing ceremonies (in-memory, short-lived)
	sessions map[string]*webauthn.SessionData
}

// WebAuthnConfig holds configuration for WebAuthn initialization.
type WebAuthnConfig struct {
	RPDisplayName string // Relying Party display name (e.g., "Klever Node Hub")
	RPID          string // Relying Party ID (e.g., "localhost" or domain)
	RPOrigins     []string // Allowed origins (e.g., "https://localhost:9443")
}

// dashboardUser implements the webauthn.User interface for single-user mode.
type dashboardUser struct {
	id          []byte
	name        string
	credentials []webauthn.Credential
}

func (u *dashboardUser) WebAuthnID() []byte                         { return u.id }
func (u *dashboardUser) WebAuthnName() string                       { return u.name }
func (u *dashboardUser) WebAuthnDisplayName() string                { return u.name }
func (u *dashboardUser) WebAuthnCredentials() []webauthn.Credential { return u.credentials }

// NewWebAuthnManager creates a new WebAuthn manager.
func NewWebAuthnManager(config WebAuthnConfig, credentials []PasskeyCredential) (*WebAuthnManager, error) {
	wa, err := webauthn.New(&webauthn.Config{
		RPDisplayName: config.RPDisplayName,
		RPID:          config.RPID,
		RPOrigins:     config.RPOrigins,
	})
	if err != nil {
		return nil, fmt.Errorf("create webauthn: %w", err)
	}

	return &WebAuthnManager{
		wa:          wa,
		credentials: credentials,
		sessions:    make(map[string]*webauthn.SessionData),
	}, nil
}

// BeginRegistration starts a passkey registration ceremony.
// Returns the options to send to the browser and a session ID.
func (wm *WebAuthnManager) BeginRegistration(passkeyName string) (*protocol.CredentialCreation, string, error) {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	user := wm.buildUser()

	options, session, err := wm.wa.BeginRegistration(user)
	if err != nil {
		return nil, "", fmt.Errorf("begin registration: %w", err)
	}

	sessionID := generateSessionID()
	wm.sessions[sessionID] = session

	return options, sessionID, nil
}

// FinishRegistration completes a passkey registration ceremony.
func (wm *WebAuthnManager) FinishRegistration(sessionID string, response *protocol.ParsedCredentialCreationData, name string) error {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	session, ok := wm.sessions[sessionID]
	if !ok {
		return fmt.Errorf("invalid or expired session")
	}
	delete(wm.sessions, sessionID)

	user := wm.buildUser()

	credential, err := wm.wa.CreateCredential(user, *session, response)
	if err != nil {
		return fmt.Errorf("create credential: %w", err)
	}

	wm.credentials = append(wm.credentials, PasskeyCredential{
		ID:           hex.EncodeToString(credential.ID),
		PublicKey:    credential.PublicKey,
		Name:         name,
		SignCount:    credential.Authenticator.SignCount,
		RegisteredAt: time.Now().Unix(),
	})

	return nil
}

// BeginLogin starts a passkey authentication ceremony.
func (wm *WebAuthnManager) BeginLogin() (*protocol.CredentialAssertion, string, error) {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	if len(wm.credentials) == 0 {
		return nil, "", fmt.Errorf("no passkeys registered")
	}

	user := wm.buildUser()

	options, session, err := wm.wa.BeginLogin(user)
	if err != nil {
		return nil, "", fmt.Errorf("begin login: %w", err)
	}

	sessionID := generateSessionID()
	wm.sessions[sessionID] = session

	return options, sessionID, nil
}

// FinishLogin completes a passkey authentication ceremony.
func (wm *WebAuthnManager) FinishLogin(sessionID string, response *protocol.ParsedCredentialAssertionData) error {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	session, ok := wm.sessions[sessionID]
	if !ok {
		return fmt.Errorf("invalid or expired session")
	}
	delete(wm.sessions, sessionID)

	user := wm.buildUser()

	credential, err := wm.wa.ValidateLogin(user, *session, response)
	if err != nil {
		return fmt.Errorf("validate login: %w", err)
	}

	// Update sign count for the used credential
	credID := hex.EncodeToString(credential.ID)
	for i := range wm.credentials {
		if wm.credentials[i].ID == credID {
			wm.credentials[i].SignCount = credential.Authenticator.SignCount
			break
		}
	}

	return nil
}

// DeletePasskey removes a passkey by ID. Enforces minimum 1 passkey.
func (wm *WebAuthnManager) DeletePasskey(id string) error {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	if len(wm.credentials) <= 1 {
		return fmt.Errorf("cannot delete last passkey")
	}

	for i, cred := range wm.credentials {
		if cred.ID == id {
			wm.credentials = append(wm.credentials[:i], wm.credentials[i+1:]...)
			return nil
		}
	}

	return fmt.Errorf("passkey not found: %s", id)
}

// Credentials returns the current stored credentials (for persistence).
func (wm *WebAuthnManager) Credentials() []PasskeyCredential {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	result := make([]PasskeyCredential, len(wm.credentials))
	copy(result, wm.credentials)
	return result
}

// HasCredentials returns true if at least one passkey is registered.
func (wm *WebAuthnManager) HasCredentials() bool {
	wm.mu.Lock()
	defer wm.mu.Unlock()
	return len(wm.credentials) > 0
}

// CredentialCount returns the number of registered passkeys.
func (wm *WebAuthnManager) CredentialCount() int {
	wm.mu.Lock()
	defer wm.mu.Unlock()
	return len(wm.credentials)
}

func (wm *WebAuthnManager) buildUser() *dashboardUser {
	user := &dashboardUser{
		id:   []byte("klever-node-hub-admin"),
		name: "admin",
	}

	for _, cred := range wm.credentials {
		credID, _ := hex.DecodeString(cred.ID)
		user.credentials = append(user.credentials, webauthn.Credential{
			ID:        credID,
			PublicKey: cred.PublicKey,
			Authenticator: webauthn.Authenticator{
				SignCount: cred.SignCount,
			},
		})
	}

	return user
}

func generateSessionID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	// Add timestamp for uniqueness
	ts := make([]byte, 8)
	binary.BigEndian.PutUint64(ts, uint64(time.Now().UnixNano()))
	b = append(b, ts...)
	return hex.EncodeToString(b)
}
