package handlers

import (
	"net/http"

	"github.com/CTJaeger/KleverNodeHub/internal/dashboard"
)

// RegistrationHandler handles registration token API requests.
type RegistrationHandler struct {
	tokenManager *dashboard.TokenManager
}

// NewRegistrationHandler creates a new RegistrationHandler.
func NewRegistrationHandler(tm *dashboard.TokenManager) *RegistrationHandler {
	return &RegistrationHandler{tokenManager: tm}
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
