package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/CTJaeger/KleverNodeHub/internal/crypto"
	"github.com/CTJaeger/KleverNodeHub/internal/dashboard"
	"github.com/CTJaeger/KleverNodeHub/internal/models"
	"github.com/CTJaeger/KleverNodeHub/internal/store"
)

// RegistrationHandler handles registration token API requests.
type RegistrationHandler struct {
	tokenManager *dashboard.TokenManager
	serverStore  *store.ServerStore
	ca           *crypto.CA
}

// NewRegistrationHandler creates a new RegistrationHandler.
func NewRegistrationHandler(tm *dashboard.TokenManager, serverStore *store.ServerStore, ca *crypto.CA) *RegistrationHandler {
	return &RegistrationHandler{
		tokenManager: tm,
		serverStore:  serverStore,
		ca:           ca,
	}
}

// HandleGenerateToken generates a new one-time registration token.
// POST /api/registration/token
func (h *RegistrationHandler) HandleGenerateToken(w http.ResponseWriter, r *http.Request) {
	token, err := h.tokenManager.Generate()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to generate token"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"token": token})
}

// HandleRegisterAgent processes an incoming agent registration.
// POST /api/agent/register
func (h *RegistrationHandler) HandleRegisterAgent(w http.ResponseWriter, r *http.Request) {
	var req models.RegistrationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	// Validate one-time token
	if !h.tokenManager.Validate(req.Token) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid or expired token"})
		return
	}

	// Generate server ID
	serverID := fmt.Sprintf("srv-%d", time.Now().UnixNano())

	// Determine IP from request if not provided
	ip := req.IP
	if ip == "" {
		ip = r.RemoteAddr
	}

	// Generate agent Ed25519 keypair and issue certificate signed by CA
	agentPub, agentPriv, err := crypto.GenerateEd25519KeyPair()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to generate agent keys"})
		return
	}

	certPEM, err := h.ca.IssueAgentCertificate(agentPub, serverID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to issue agent certificate"})
		return
	}

	keyPEM, err := crypto.EncodePrivateKeyPEM(agentPriv)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to encode agent key"})
		return
	}

	// Create server record in database
	server := &models.Server{
		ID:           serverID,
		Name:         req.Hostname,
		Hostname:     req.Hostname,
		IPAddress:    ip,
		OSInfo:       req.OS,
		Status:       "online",
		Certificate:  certPEM,
		RegisteredAt: time.Now().Unix(),
	}
	if err := h.serverStore.Create(server); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create server"})
		return
	}

	writeJSON(w, http.StatusOK, &models.RegistrationResponse{
		ServerID:  serverID,
		CertPEM:   string(certPEM),
		KeyPEM:    string(keyPEM),
		CACertPEM: string(h.ca.CertPEM),
	})
}
