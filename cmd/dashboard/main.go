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
	"github.com/CTJaeger/KleverNodeHub/internal/crypto"
	"github.com/CTJaeger/KleverNodeHub/internal/dashboard"
	"github.com/CTJaeger/KleverNodeHub/internal/dashboard/alerting"
	"github.com/CTJaeger/KleverNodeHub/internal/dashboard/handlers"
	"github.com/CTJaeger/KleverNodeHub/internal/dashboard/scheduler"
	"github.com/CTJaeger/KleverNodeHub/internal/dashboard/ws"
	"github.com/CTJaeger/KleverNodeHub/internal/notify"
	"github.com/CTJaeger/KleverNodeHub/internal/store"
	"github.com/CTJaeger/KleverNodeHub/internal/version"
)

func main() {
	info := version.Get()
	fmt.Printf("Klever Node Hub - Dashboard %s (%s)\n", info.Version, info.GitCommit)

	// CLI flags
	addr := flag.String("addr", ":9443", "Listen address (host:port)")
	domain := flag.String("domain", "localhost", "Domain for WebAuthn RP ID and TLS (e.g. localhost, myserver.local, node.example.com)")
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
	metricsStore := store.NewMetricsStore(db)

	// --- Certificate Authority ---
	caDir := filepath.Join(*dataDir, "ca")
	encKey, err := loadOrCreateEncryptionKey(settingsStore)
	if err != nil {
		log.Fatalf("encryption key: %v", err)
	}
	ca, err := loadOrCreateCA(caDir, encKey)
	if err != nil {
		log.Fatalf("CA: %v", err)
	}

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
	rpOrigins := []string{fmt.Sprintf("https://%s%s", *domain, *addr)}
	if *domain != "localhost" {
		rpOrigins = append(rpOrigins, fmt.Sprintf("https://localhost%s", *addr))
	}
	waCredentials := loadPasskeyCredentials(settingsStore)
	webauthnMgr, err := auth.NewWebAuthnManager(auth.WebAuthnConfig{
		RPDisplayName: "Klever Node Hub",
		RPID:          *domain,
		RPOrigins:     rpOrigins,
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

	// --- Metrics Scheduler ---
	metricsScheduler := scheduler.New(metricsStore)
	metricsScheduler.Start()

	// --- WebSocket Hub ---
	hub := ws.NewHub(serverStore)
	hub.StartHealthCheck(60 * time.Second)

	// --- Handlers ---
	authHandler := handlers.NewAuthHandler(jwtMgr, webauthnMgr, recoveryMgr)
	nodeHandler := handlers.NewNodeHandler(hub, nodeStore)
	serverHandler := handlers.NewServerHandler(serverStore, nodeStore)
	metricsHandler := handlers.NewMetricsHandler(metricsStore)
	tagCache := dashboard.NewTagCache()
	dockerHandler := handlers.NewDockerHandler(hub, nodeStore, tagCache)
	configHandler := handlers.NewConfigHandler(hub, nodeStore)
	logHandler := handlers.NewLogHandler(hub, nodeStore)
	keyHandler := handlers.NewKeyHandler(hub, nodeStore)
	provisionHandler := handlers.NewProvisionHandler(hub)
	notifyManager := notify.NewManager()
	handlers.LoadSavedChannels(settingsStore, notifyManager)
	notifyHandler := handlers.NewNotificationHandler(notifyManager, settingsStore)
	alertStore := store.NewAlertStore(db)
	alertHandler := handlers.NewAlertHandler(alertStore)
	alertEvaluator := alerting.NewEvaluator(alertStore, metricsStore, nodeStore, serverStore, notifyManager)
	alertEvaluator.EnsureDefaults()
	alertEvaluator.Start()
	updateStore := dashboard.NewUpdateStore(*dataDir)
	updateHandler := handlers.NewUpdateHandler(hub, updateStore, serverStore)
	settingsHandler := handlers.NewSettingsHandler(settingsStore)
	tokenManager := dashboard.NewTokenManager()
	regHandler := handlers.NewRegistrationHandler(tokenManager, serverStore, ca)

	// Persist passkey credentials when they change
	authHandler.SetOnCredentialsChanged(func(creds []auth.PasskeyCredential) {
		savePasskeyCredentials(settingsStore, creds)
	})

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
	mux.HandleFunc("POST /api/auth/passkey/register/finish", authHandler.HandlePasskeyFinishRegister)
	mux.HandleFunc("POST /api/auth/passkey/login/begin", authHandler.HandlePasskeyBeginLogin)
	mux.HandleFunc("POST /api/auth/passkey/login/finish", authHandler.HandlePasskeyFinishLogin)
	mux.HandleFunc("POST /api/auth/recovery", authHandler.HandleRecoveryLogin)
	mux.HandleFunc("POST /api/auth/refresh", authHandler.HandleRefresh)
	mux.HandleFunc("POST /api/auth/logout", authHandler.HandleLogout)

	// Agent registration (token-based, no JWT required)
	mux.HandleFunc("POST /api/agent/register", regHandler.HandleRegisterAgent)

	// GeoIP resolver for server region detection
	geoResolver := dashboard.NewGeoIPResolver()

	// WebSocket endpoint for agents (authenticated via mTLS cert, not JWT)
	wsHandler := ws.NewAgentHandler(hub, serverStore, nodeStore, metricsStore, geoResolver)
	mux.HandleFunc("GET /ws/agent", wsHandler.HandleUpgrade)

	// WebSocket endpoint for browser clients (authenticated via JWT cookie)
	browserWsHandler := ws.NewBrowserHandler(hub)
	mux.Handle("GET /ws", authMw(http.HandlerFunc(browserWsHandler.HandleUpgrade)))

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
	mux.Handle("POST /api/nodes/batch/upgrade", authMw(http.HandlerFunc(dockerHandler.HandleBatchUpgrade)))
	mux.Handle("GET /api/docker/tags", authMw(http.HandlerFunc(dockerHandler.HandleListTags)))
	mux.Handle("GET /api/nodes/{id}/metrics", authMw(http.HandlerFunc(metricsHandler.HandleNodeMetrics)))
	mux.Handle("GET /api/servers/{id}/metrics", authMw(http.HandlerFunc(metricsHandler.HandleServerMetrics)))
	mux.Handle("POST /api/nodes/provision", authMw(http.HandlerFunc(provisionHandler.HandleProvision)))
	mux.Handle("GET /api/nodes/{id}/config", authMw(http.HandlerFunc(configHandler.HandleListFiles)))
	mux.Handle("GET /api/nodes/{id}/config/{filename}", authMw(http.HandlerFunc(configHandler.HandleReadFile)))
	mux.Handle("PUT /api/nodes/{id}/config/{filename}", authMw(http.HandlerFunc(configHandler.HandleWriteFile)))
	mux.Handle("GET /api/nodes/{id}/config/{filename}/backups", authMw(http.HandlerFunc(configHandler.HandleListBackups)))
	mux.Handle("POST /api/nodes/{id}/config/restore", authMw(http.HandlerFunc(configHandler.HandleRestore)))
	mux.Handle("POST /api/config/push", authMw(http.HandlerFunc(configHandler.HandleMultiPush)))
	mux.Handle("GET /api/nodes/{id}/logs", authMw(http.HandlerFunc(logHandler.HandleFetchLogs)))
	mux.Handle("GET /api/nodes/{id}/keys", authMw(http.HandlerFunc(keyHandler.HandleGetKeyInfo)))
	mux.Handle("POST /api/nodes/{id}/keys/generate", authMw(http.HandlerFunc(keyHandler.HandleGenerateKey)))
	mux.Handle("POST /api/nodes/{id}/keys/import", authMw(http.HandlerFunc(keyHandler.HandleImportKey)))
	mux.Handle("GET /api/nodes/{id}/keys/export", authMw(http.HandlerFunc(keyHandler.HandleExportKey)))
	mux.Handle("GET /api/nodes/{id}/keys/backups", authMw(http.HandlerFunc(keyHandler.HandleListKeyBackups)))
	mux.Handle("GET /api/notifications/channels", authMw(http.HandlerFunc(notifyHandler.HandleListChannels)))
	mux.Handle("POST /api/notifications/channels", authMw(http.HandlerFunc(notifyHandler.HandleAddChannel)))
	mux.Handle("PUT /api/notifications/channels/{name}", authMw(http.HandlerFunc(notifyHandler.HandleUpdateChannel)))
	mux.Handle("DELETE /api/notifications/channels/{name}", authMw(http.HandlerFunc(notifyHandler.HandleRemoveChannel)))
	mux.Handle("POST /api/notifications/channels/{name}/test", authMw(http.HandlerFunc(notifyHandler.HandleTestChannel)))
	mux.Handle("GET /api/notifications/history", authMw(http.HandlerFunc(notifyHandler.HandleHistory)))
	mux.Handle("GET /api/alerts", authMw(http.HandlerFunc(alertHandler.HandleListActiveAlerts)))
	mux.Handle("GET /api/alerts/history", authMw(http.HandlerFunc(alertHandler.HandleAlertHistory)))
	mux.Handle("GET /api/alerts/rules", authMw(http.HandlerFunc(alertHandler.HandleListRules)))
	mux.Handle("POST /api/alerts/rules", authMw(http.HandlerFunc(alertHandler.HandleCreateOrUpdateRule)))
	mux.Handle("DELETE /api/alerts/rules/{id}", authMw(http.HandlerFunc(alertHandler.HandleDeleteRule)))
	mux.Handle("POST /api/alerts/{id}/acknowledge", authMw(http.HandlerFunc(alertHandler.HandleAcknowledgeAlert)))
	mux.Handle("POST /api/agent/upload", authMw(http.HandlerFunc(updateHandler.HandleUploadBinary)))
	mux.Handle("GET /api/agent/binaries", authMw(http.HandlerFunc(updateHandler.HandleListBinaries)))
	mux.Handle("GET /api/agent/version", authMw(http.HandlerFunc(updateHandler.HandleLatestVersion)))
	mux.Handle("POST /api/agent/update/{server_id}", authMw(http.HandlerFunc(updateHandler.HandleUpdateAgent)))
	mux.Handle("POST /api/agent/update/all", authMw(http.HandlerFunc(updateHandler.HandleUpdateAll)))
	mux.Handle("GET /api/settings", authMw(http.HandlerFunc(settingsHandler.HandleGetAll)))
	mux.Handle("PUT /api/settings", authMw(http.HandlerFunc(settingsHandler.HandleUpdate)))
	mux.Handle("GET /api/settings/{key}", authMw(http.HandlerFunc(settingsHandler.HandleGetSingle)))
	mux.Handle("PUT /api/settings/{key}", authMw(http.HandlerFunc(settingsHandler.HandleUpdateSingle)))
	mux.Handle("POST /api/settings/reset", authMw(http.HandlerFunc(settingsHandler.HandleResetDefaults)))

	// --- Graceful shutdown ---
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigCh
		log.Printf("received %s, shutting down...", sig)
		alertEvaluator.Stop()
		metricsScheduler.Stop()
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

// savePasskeyCredentials persists passkey credentials to the settings store.
func savePasskeyCredentials(settings *store.SettingsStore, creds []auth.PasskeyCredential) {
	data, err := json.Marshal(creds)
	if err != nil {
		log.Printf("WARNING: failed to marshal passkey credentials: %v", err)
		return
	}
	if err := settings.Set("passkey_credentials", string(data)); err != nil {
		log.Printf("WARNING: failed to save passkey credentials: %v", err)
	}
}

// loadOrCreateEncryptionKey loads or generates the master encryption key for CA private key storage.
func loadOrCreateEncryptionKey(settings *store.SettingsStore) ([]byte, error) {
	keyHex, err := settings.Get("encryption_key")
	if err != nil {
		return nil, fmt.Errorf("read encryption key: %w", err)
	}

	if keyHex != "" {
		return hex.DecodeString(keyHex)
	}

	// Generate new 32-byte key (reuses the JWT key generation which produces 32 bytes)
	key, err := auth.GenerateSigningKey()
	if err != nil {
		return nil, err
	}
	if err := settings.Set("encryption_key", hex.EncodeToString(key)); err != nil {
		return nil, fmt.Errorf("save encryption key: %w", err)
	}
	log.Println("generated new encryption key")
	return key, nil
}

// loadOrCreateCA loads the CA from disk or creates a new one.
func loadOrCreateCA(caDir string, encKey []byte) (*crypto.CA, error) {
	ca, err := crypto.LoadCAFromDir(caDir, encKey)
	if err == nil {
		log.Println("loaded existing CA")
		return ca, nil
	}

	// Create new CA
	ca, err = crypto.NewCA()
	if err != nil {
		return nil, fmt.Errorf("create CA: %w", err)
	}

	if err := ca.SaveToDir(caDir, encKey); err != nil {
		return nil, fmt.Errorf("save CA: %w", err)
	}

	log.Println("created new certificate authority")
	return ca, nil
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
