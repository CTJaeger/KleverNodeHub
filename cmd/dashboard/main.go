package main

import (
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/CTJaeger/KleverNodeHub/internal/auth"
	"github.com/CTJaeger/KleverNodeHub/internal/dashboard"
	"github.com/CTJaeger/KleverNodeHub/internal/dashboard/handlers"
	"github.com/CTJaeger/KleverNodeHub/internal/dashboard/ws"
	"github.com/CTJaeger/KleverNodeHub/internal/store"
	"github.com/CTJaeger/KleverNodeHub/internal/version"
)

func main() {
	info := version.Get()
	fmt.Printf("Klever Node Hub - Dashboard %s (%s)\n", info.Version, info.GitCommit)

	// CLI flags
	addr := flag.String("addr", ":9443", "Listen address (host:port)")
	dataDir := flag.String("data-dir", defaultDataDir(), "Data directory for DB, certs, config")
	flag.Parse()

	// Ensure data directory exists
	if err := os.MkdirAll(*dataDir, 0700); err != nil {
		log.Fatalf("create data dir: %v", err)
	}
	log.Printf("data directory: %s", *dataDir)

	// --- Database ---
	dbPath := filepath.Join(*dataDir, "dashboard.db")
	db, err := store.Open(dbPath)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer func() { _ = db.Close() }()

	serverStore := store.NewServerStore(db)
	nodeStore := store.NewNodeStore(db)
	settingsStore := store.NewSettingsStore(db)

	// --- Auth: JWT ---
	jwtKey, err := loadOrCreateJWTKey(settingsStore)
	if err != nil {
		log.Fatalf("JWT key: %v", err)
	}
	jwtMgr, err := auth.NewJWTManager(jwtKey)
	if err != nil {
		log.Fatalf("JWT manager: %v", err)
	}

	// --- Auth: WebAuthn ---
	hostname := "localhost"
	if h, _ := os.Hostname(); h != "" {
		hostname = h
	}
	waCredentials := loadPasskeyCredentials(settingsStore)
	webauthnMgr, err := auth.NewWebAuthnManager(auth.WebAuthnConfig{
		RPDisplayName: "Klever Node Hub",
		RPID:          hostname,
		RPOrigins:     []string{fmt.Sprintf("https://%s%s", hostname, *addr)},
	}, waCredentials)
	if err != nil {
		// Non-fatal: WebAuthn may fail on some systems, passkey login won't work
		log.Printf("WARNING: WebAuthn init failed (passkey login disabled): %v", err)
		webauthnMgr, _ = auth.NewWebAuthnManager(auth.WebAuthnConfig{
			RPDisplayName: "Klever Node Hub",
			RPID:          "localhost",
			RPOrigins:     []string{fmt.Sprintf("https://localhost%s", *addr)},
		}, waCredentials)
	}

	// --- Auth: Recovery codes ---
	recoveryCodes := loadRecoveryCodes(settingsStore)
	recoveryMgr := auth.NewRecoveryManager(recoveryCodes)

	// Generate initial recovery codes on first run
	if len(recoveryCodes) == 0 {
		plaintextCodes, _, err := recoveryMgr.GenerateCodes()
		if err != nil {
			log.Fatalf("generate recovery codes: %v", err)
		}
		saveRecoveryCodes(settingsStore, recoveryMgr.Codes())
		log.Println("=== INITIAL RECOVERY CODES (save these!) ===")
		for i, code := range plaintextCodes {
			log.Printf("  %d: %s", i+1, code)
		}
		log.Println("=============================================")
	}

	// --- WebSocket Hub ---
	hub := ws.NewHub(serverStore)
	hub.StartHealthCheck(60 * time.Second)

	// --- Handlers ---
	authHandler := handlers.NewAuthHandler(jwtMgr, webauthnMgr, recoveryMgr)
	nodeHandler := handlers.NewNodeHandler(hub, nodeStore)
	serverHandler := handlers.NewServerHandler(serverStore, nodeStore)
	tagCache := dashboard.NewTagCache()
	dockerHandler := handlers.NewDockerHandler(hub, nodeStore, tagCache)
	tokenManager := dashboard.NewTokenManager()
	regHandler := handlers.NewRegistrationHandler(tokenManager)

	// --- Server + Routes ---
	srv := dashboard.NewServer(&dashboard.ServerConfig{Addr: *addr})
	if err := srv.SetupRoutes(); err != nil {
		log.Fatalf("setup routes: %v", err)
	}

	mux := srv.Mux()
	authMw := auth.Middleware(jwtMgr)

	// Public routes (no auth required)
	mux.HandleFunc("GET /api/setup/status", authHandler.HandleSetupStatus)
	mux.HandleFunc("POST /api/auth/passkey/register/begin", authHandler.HandlePasskeyBeginRegister)
	mux.HandleFunc("POST /api/auth/passkey/login/begin", authHandler.HandlePasskeyBeginLogin)
	mux.HandleFunc("POST /api/auth/recovery", authHandler.HandleRecoveryLogin)
	mux.HandleFunc("POST /api/auth/refresh", authHandler.HandleRefresh)
	mux.HandleFunc("POST /api/auth/logout", authHandler.HandleLogout)

	// WebSocket endpoint for agents (authenticated via mTLS cert, not JWT)
	wsHandler := ws.NewAgentHandler(hub, serverStore, nodeStore)
	mux.HandleFunc("GET /ws/agent", wsHandler.HandleUpgrade)

	// Protected routes (JWT required)
	mux.Handle("POST /api/registration/token", authMw(http.HandlerFunc(regHandler.HandleGenerateToken)))
	mux.Handle("GET /api/servers", authMw(http.HandlerFunc(serverHandler.HandleList)))
	mux.Handle("GET /api/servers/{id}", authMw(http.HandlerFunc(serverHandler.HandleGet)))
	mux.Handle("GET /api/nodes", authMw(http.HandlerFunc(serverHandler.HandleListNodes)))
	mux.Handle("GET /api/nodes/{id}", authMw(http.HandlerFunc(serverHandler.HandleGetNode)))
	mux.Handle("POST /api/nodes/{id}/start", authMw(http.HandlerFunc(nodeHandler.HandleStart)))
	mux.Handle("POST /api/nodes/{id}/stop", authMw(http.HandlerFunc(nodeHandler.HandleStop)))
	mux.Handle("POST /api/nodes/{id}/restart", authMw(http.HandlerFunc(nodeHandler.HandleRestart)))
	mux.Handle("POST /api/nodes/batch", authMw(http.HandlerFunc(nodeHandler.HandleBatch)))
	mux.Handle("POST /api/nodes/{id}/upgrade", authMw(http.HandlerFunc(dockerHandler.HandleUpgrade)))
	mux.Handle("POST /api/nodes/{id}/downgrade", authMw(http.HandlerFunc(dockerHandler.HandleDowngrade)))
	mux.Handle("GET /api/docker/tags", authMw(http.HandlerFunc(dockerHandler.HandleListTags)))

	// --- Graceful shutdown ---
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigCh
		log.Printf("received %s, shutting down...", sig)
		hub.Stop()
		_ = db.Close()
		os.Exit(0)
	}()

	// --- Start ---
	if err := srv.Start(); err != nil {
		log.Fatalf("server: %v", err)
	}
}

// defaultDataDir returns the default data directory path.
func defaultDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".klever-node-hub"
	}
	return filepath.Join(home, ".klever-node-hub")
}

// loadOrCreateJWTKey loads the JWT signing key from the settings store,
// or generates a new one on first run.
func loadOrCreateJWTKey(settings *store.SettingsStore) ([]byte, error) {
	keyHex, err := settings.Get("jwt_signing_key")
	if err != nil {
		return nil, fmt.Errorf("read JWT key: %w", err)
	}

	if keyHex != "" {
		key, err := hex.DecodeString(keyHex)
		if err != nil {
			return nil, fmt.Errorf("decode JWT key: %w", err)
		}
		return key, nil
	}

	// Generate new key
	key, err := auth.GenerateSigningKey()
	if err != nil {
		return nil, err
	}
	if err := settings.Set("jwt_signing_key", hex.EncodeToString(key)); err != nil {
		return nil, fmt.Errorf("save JWT key: %w", err)
	}
	log.Println("generated new JWT signing key")
	return key, nil
}

// loadPasskeyCredentials loads stored passkey credentials from settings.
func loadPasskeyCredentials(settings *store.SettingsStore) []auth.PasskeyCredential {
	data, err := settings.Get("passkey_credentials")
	if err != nil || data == "" {
		return nil
	}
	var creds []auth.PasskeyCredential
	if err := json.Unmarshal([]byte(data), &creds); err != nil {
		log.Printf("WARNING: failed to load passkey credentials: %v", err)
		return nil
	}
	return creds
}

// loadRecoveryCodes loads stored recovery codes from settings.
func loadRecoveryCodes(settings *store.SettingsStore) []auth.RecoveryCode {
	data, err := settings.Get("recovery_codes")
	if err != nil || data == "" {
		return nil
	}
	var codes []auth.RecoveryCode
	if err := json.Unmarshal([]byte(data), &codes); err != nil {
		log.Printf("WARNING: failed to load recovery codes: %v", err)
		return nil
	}
	return codes
}

// saveRecoveryCodes persists recovery codes to the settings store.
func saveRecoveryCodes(settings *store.SettingsStore, codes []auth.RecoveryCode) {
	data, err := json.Marshal(codes)
	if err != nil {
		log.Printf("WARNING: failed to marshal recovery codes: %v", err)
		return
	}
	if err := settings.Set("recovery_codes", string(data)); err != nil {
		log.Printf("WARNING: failed to save recovery codes: %v", err)
	}
}
