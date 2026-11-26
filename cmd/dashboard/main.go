package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
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
	resetRecoveryCodes := flag.Bool("reset-recovery-codes", false, "Generate new recovery codes and exit")
	trustedProxy := flag.String("trusted-proxy", "", "Comma-separated CIDRs of reverse proxies whose X-Forwarded-For is trusted for rate limiting, besides loopback (e.g. 172.17.0.0/16 when running in Docker behind a host proxy)")
	allowUnsignedAgents := flag.Bool("allow-unsigned-agent-binaries", false, "Enable agent binary upload (unsigned builds; agents still refuse them unless their own agent.json allows unsigned updates)")
	// Internal flags used by the Docker self-update flow. The orchestrator container
	// spawns a sidecar that re-execs this binary with these flags to finish the
	// container swap after the orchestrator has stopped itself.
	finalizeNewID := flag.String("self-update-finalize", "", "Internal: ID of the new container to start once --replaces has stopped")
	replacesOldID := flag.String("replaces", "", "Internal: ID of the old container to remove during --self-update-finalize")
	flag.Parse()

	// --- Self-update finalize sidecar mode ---
	// Runs in a short-lived helper container, finishes the swap, then exits.
	// Must short-circuit before any data-dir / DB / network init.
	if *finalizeNewID != "" {
		if err := handlers.RunSelfUpdateFinalize(*finalizeNewID, *replacesOldID); err != nil {
			log.Fatalf("self-update finalize: %v", err)
		}
		return
	}

	// Clean up stopped finalize-helper containers left behind by past failed
	// self-update attempts. Best-effort; never blocks startup.
	handlers.SweepStaleFinalizeHelpers()

	// Ensure data directory exists
	if err := os.MkdirAll(*dataDir, 0700); err != nil {
		log.Fatalf("create data dir: %v", err)
	}

	// --- Handle --reset-recovery-codes ---
	if *resetRecoveryCodes {
		resetRecoveryCodesAndExit(*dataDir)
		return
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
	versionHistoryStore := store.NewVersionHistoryStore(db)

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
	instanceID, err := loadOrCreateInstanceID(settingsStore)
	if err != nil {
		log.Fatalf("instance ID: %v", err)
	}
	waCredentials := loadPasskeyCredentials(settingsStore)
	webauthnMgr, err := auth.NewWebAuthnManager(auth.WebAuthnConfig{
		RPDisplayName: "Klever Node Hub",
		RPID:          *domain,
		RPOrigins:     rpOrigins,
		InstanceID:    instanceID,
	}, waCredentials)
	if err != nil {
		// Non-fatal: WebAuthn may fail on some systems, passkey login won't work
		log.Printf("WARNING: WebAuthn init failed (passkey login disabled): %v", err)
		webauthnMgr, _ = auth.NewWebAuthnManager(auth.WebAuthnConfig{
			RPDisplayName: "Klever Node Hub",
			RPID:          "localhost",
			RPOrigins:     []string{fmt.Sprintf("https://localhost%s", *addr)},
			InstanceID:    instanceID,
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
		// Not into the log: the journal is readable by more users than the
		// data dir, and kept long after the codes were saved elsewhere.
		codesFile := filepath.Join(*dataDir, "recovery-codes.txt")
		content := "Klever Node Hub recovery codes — store them safely, then delete this file.\n"
		for i, code := range plaintextCodes {
			content += fmt.Sprintf("%d: %s\n", i+1, code)
		}
		_ = os.Remove(codesFile) // WriteFile keeps an existing file's mode
		if err := os.WriteFile(codesFile, []byte(content), 0o600); err != nil {
			log.Fatalf("write recovery codes: %v", err)
		}
		log.Printf("initial recovery codes written to %s — save them and delete the file", codesFile)
	}

	// --- Auth: Password ---
	passwordHash, _ := settingsStore.Get("password_hash")
	passwordMgr := auth.NewPasswordManager(passwordHash)

	// --- Auth: Rate Limiter ---
	loginLimiter := auth.NewRateLimiter(15*time.Minute, 5)
	loginLimiter.SetGlobalLimit(50) // per 15 min across all IPs; passkey/wallet login unaffected

	// --- Auth: Klever Extension ---
	kleverAddress, _ := settingsStore.Get("klever_admin_address")
	kleverMgr := auth.NewKleverAuthManager(kleverAddress)

	// --- Metrics Scheduler ---
	metricsScheduler := scheduler.New(metricsStore)
	metricsScheduler.Start()

	// --- WebSocket Hub ---
	// Reset all servers to offline on startup; agents will set online when they connect
	if err := serverStore.ResetAllStatus("offline"); err != nil {
		log.Printf("reset server status: %v", err)
	}
	hub := ws.NewHub(serverStore, nodeStore)
	// Heartbeat timeout is read live from settings on every health-check tick,
	// so changing it on the Settings page takes effect without a restart.
	hub.StartHealthCheck(settingsStore.HeartbeatTimeout)

	// --- Handlers ---
	authHandler := handlers.NewAuthHandler(jwtMgr, webauthnMgr, recoveryMgr, passwordMgr, loginLimiter, kleverMgr)
	nodeHandler := handlers.NewNodeHandler(hub, nodeStore)
	serverHandler := handlers.NewServerHandler(serverStore, nodeStore, metricsStore)
	benchmarkHandler := handlers.NewBenchmarkHandler(hub, serverStore)
	metricsHandler := handlers.NewMetricsHandler(metricsStore)
	tagCache := dashboard.NewTagCache()
	dockerHandler := handlers.NewDockerHandler(hub, nodeStore, tagCache)
	dockerCleanupHandler := handlers.NewDockerCleanupHandler(hub, serverStore)
	configHandler := handlers.NewConfigHandler(hub, nodeStore)
	batchConfigHandler := handlers.NewBatchConfigHandler(hub, nodeStore)
	slotInspectorHandler := handlers.NewSlotInspectorHandler(hub, nodeStore)
	performanceHandler := handlers.NewPerformanceHandler(versionHistoryStore, metricsStore, nodeStore)
	logHandler := handlers.NewLogHandler(hub, nodeStore)
	keyHandler := handlers.NewKeyHandler(hub, nodeStore)
	provisionHandler := handlers.NewProvisionHandler(hub)
	notifyManager := notify.NewManager()
	handlers.LoadSavedChannels(settingsStore, notifyManager)
	notifyHandler := handlers.NewNotificationHandler(notifyManager, settingsStore)

	// --- Web Push (VAPID + subscriptions) ---
	vapidKeys, err := loadOrCreateVAPIDKeys(settingsStore)
	if err != nil {
		log.Fatalf("VAPID keys: %v", err)
	}
	webpushChannel := notify.NewWebPushChannel(vapidKeys, "mailto:admin@klever-node-hub.local")
	handlers.LoadSavedSubscriptions(settingsStore, webpushChannel)
	notifyManager.AddChannel(webpushChannel)
	pushHandler := handlers.NewPushHandler(webpushChannel, settingsStore)
	log.Printf("Web Push ready (%d saved subscriptions)", webpushChannel.SubscriptionCount())
	alertStore := store.NewAlertStore(db)
	alertHandler := handlers.NewAlertHandler(alertStore)
	// Note: we do NOT resolve all firing alerts on startup.
	// The evaluator will naturally resolve them if conditions clear,
	// and dedup prevents duplicate alerts from being created.
	alertEvaluator := alerting.NewEvaluator(alertStore, metricsStore, nodeStore, serverStore, settingsStore, notifyManager, hub.IsConnected)
	alertEvaluator.EnsureDefaults()
	alertEvaluator.Start()
	regressionDetector := alerting.NewRegressionDetector(versionHistoryStore, metricsStore, nodeStore, alertStore, notifyManager)
	regressionDetector.Start()
	updateStore := dashboard.NewUpdateStore(*dataDir)
	versionChecker := dashboard.NewVersionChecker("CTJaeger", "KleverNodeHub")
	versionChecker.Start()
	updateHandler := handlers.NewUpdateHandler(hub, updateStore, serverStore, versionChecker)
	updateHandler.SetAllowUnsignedBinaries(*allowUnsignedAgents)
	settingsHandler := handlers.NewSettingsHandler(settingsStore)
	systemHandler := handlers.NewSystemHandler(versionChecker)
	tokenManager := dashboard.NewTokenManager()
	regHandler := handlers.NewRegistrationHandler(tokenManager, serverStore, ca)

	securityHandler := handlers.NewSecurityHandler(authHandler, serverStore, updateStore, hub)

	// First-run wizard token: only someone who can read this log may claim
	// a fresh install. A new one is made on every start until setup is done.
	if setupToken, err := crypto.RandomHex(16); err != nil {
		log.Fatalf("setup token: %v", err)
	} else {
		authHandler.SetSetupToken(setupToken)
		if !passwordMgr.HasPassword() && !webauthnMgr.HasCredentials() && !kleverMgr.HasAddress() {
			log.Printf("setup not completed — setup token for the first-run wizard: %s", setupToken)
		}
	}
	authHandler.SetOnRecoveryCodesChanged(func(codes []auth.RecoveryCode) {
		saveRecoveryCodes(settingsStore, codes)
	})
	authHandler.SetOnSessionsEnded(hub.DisconnectBrowsers)
	if *trustedProxy != "" {
		var nets []*net.IPNet
		for _, c := range strings.Split(*trustedProxy, ",") {
			_, n, err := net.ParseCIDR(strings.TrimSpace(c))
			if err != nil {
				log.Fatalf("--trusted-proxy: %v", err)
			}
			nets = append(nets, n)
		}
		handlers.SetTrustedProxies(nets)
	}
	// Persist passkey credentials when they change
	authHandler.SetSigningKeySaver(func(key []byte) error {
		return settingsStore.Set("jwt_signing_key", hex.EncodeToString(key))
	})
	authHandler.SetOnCredentialsChanged(func(creds []auth.PasskeyCredential) {
		savePasskeyCredentials(settingsStore, creds)
	})

	// Persist password hash when it changes
	authHandler.SetOnPasswordChanged(func(hash string) {
		if err := settingsStore.Set("password_hash", hash); err != nil {
			log.Printf("WARNING: failed to save password hash: %v", err)
		}
	})

	// Persist Klever admin address when it changes
	authHandler.SetOnKleverAddressChanged(func(address string) {
		if err := settingsStore.Set("klever_admin_address", address); err != nil {
			log.Printf("WARNING: failed to save klever address: %v", err)
		}
	})

	// --- Server + Routes ---
	srv := dashboard.NewServer(&dashboard.ServerConfig{Addr: *addr, CA: ca})
	if err := srv.SetupRoutes(); err != nil {
		log.Fatalf("setup routes: %v", err)
	}

	mux := srv.Mux()
	authMw := auth.Middleware(jwtMgr)

	// Public routes (no auth required)
	mux.HandleFunc("GET /api/setup/status", authHandler.HandleSetupStatus)
	mux.HandleFunc("POST /api/auth/passkey/register/begin", limitBody(authHandler.HandlePasskeyBeginRegister))
	mux.HandleFunc("POST /api/auth/passkey/register/finish", limitBody(authHandler.HandlePasskeyFinishRegister))
	mux.HandleFunc("POST /api/auth/passkey/login/begin", limitBody(authHandler.HandlePasskeyBeginLogin))
	mux.HandleFunc("POST /api/auth/passkey/login/finish", limitBody(authHandler.HandlePasskeyFinishLogin))
	mux.HandleFunc("POST /api/auth/password", limitBody(authHandler.HandlePasswordLogin))
	mux.HandleFunc("POST /api/setup/password", limitBody(authHandler.HandleSetupPassword))
	mux.HandleFunc("POST /api/auth/recovery", limitBody(authHandler.HandleRecoveryLogin))
	mux.HandleFunc("GET /api/auth/klever/challenge", authHandler.HandleKleverChallenge)
	mux.HandleFunc("POST /api/auth/klever/verify", limitBody(authHandler.HandleKleverVerify))
	mux.HandleFunc("POST /api/auth/refresh", limitBody(authHandler.HandleRefresh))
	mux.HandleFunc("POST /api/auth/logout", limitBody(authHandler.HandleLogout))

	// Agent registration (token-based, no JWT required)
	mux.HandleFunc("POST /api/agent/register", limitBody(regHandler.HandleRegisterAgent))
	mux.HandleFunc("GET /api/agent/ca", regHandler.HandleCACert)

	// GeoIP resolver for server region detection
	geoResolver := dashboard.NewGeoIPResolver()

	// WebSocket endpoint for agents (authenticated via mTLS cert, not JWT)
	wsHandler := ws.NewAgentHandler(hub, serverStore, nodeStore, metricsStore, versionHistoryStore, geoResolver)
	mux.HandleFunc("GET /ws/agent", wsHandler.HandleUpgrade)

	// WebSocket endpoint for browser clients (authenticated via JWT cookie)
	browserWsHandler := ws.NewBrowserHandler(hub)
	browserWsHandler.SetOriginPatterns([]string{*domain, *domain + ":*"})
	mux.Handle("GET /ws", authMw(http.HandlerFunc(browserWsHandler.HandleUpgrade)))

	// Protected routes (JWT required)
	mux.Handle("GET /api/auth/passkeys", authMw(http.HandlerFunc(authHandler.HandleListPasskeys)))
	mux.Handle("DELETE /api/auth/passkeys/{id}", authMw(http.HandlerFunc(authHandler.HandleDeletePasskey)))
	mux.Handle("PUT /api/auth/password", authMw(http.HandlerFunc(authHandler.HandleChangePassword)))
	mux.Handle("POST /api/auth/logout-all", authMw(http.HandlerFunc(authHandler.HandleLogoutAll)))
	mux.Handle("GET /api/security/check", authMw(http.HandlerFunc(securityHandler.HandleCheck)))
	mux.Handle("POST /api/security/check/servers", authMw(http.HandlerFunc(securityHandler.HandleCheckServers)))
	mux.Handle("GET /api/auth/klever", authMw(http.HandlerFunc(authHandler.HandleKleverStatus)))
	mux.Handle("POST /api/setup/klever/challenge", authMw(http.HandlerFunc(authHandler.HandleKleverSetupChallenge)))
	mux.Handle("POST /api/setup/klever", authMw(http.HandlerFunc(authHandler.HandleKleverSetup)))
	mux.Handle("DELETE /api/auth/klever", authMw(http.HandlerFunc(authHandler.HandleKleverRemove)))
	mux.Handle("POST /api/registration/token", authMw(http.HandlerFunc(regHandler.HandleGenerateToken)))
	mux.Handle("GET /api/servers", authMw(http.HandlerFunc(serverHandler.HandleList)))
	mux.Handle("GET /api/servers/{id}", authMw(http.HandlerFunc(serverHandler.HandleGet)))
	mux.Handle("PATCH /api/servers/{id}", authMw(http.HandlerFunc(serverHandler.HandleUpdateServer)))
	mux.Handle("DELETE /api/servers/{id}", authMw(http.HandlerFunc(serverHandler.HandleDelete)))
	mux.Handle("GET /api/nodes", authMw(http.HandlerFunc(serverHandler.HandleListNodes)))
	mux.Handle("GET /api/nodes/{id}", authMw(http.HandlerFunc(serverHandler.HandleGetNode)))
	mux.Handle("PATCH /api/nodes/{id}", authMw(http.HandlerFunc(serverHandler.HandleUpdateNode)))
	mux.Handle("DELETE /api/nodes/{id}", authMw(http.HandlerFunc(nodeHandler.HandleDelete)))
	mux.Handle("POST /api/nodes/{id}/start", authMw(http.HandlerFunc(nodeHandler.HandleStart)))
	mux.Handle("POST /api/nodes/{id}/stop", authMw(http.HandlerFunc(nodeHandler.HandleStop)))
	mux.Handle("POST /api/nodes/{id}/restart", authMw(http.HandlerFunc(nodeHandler.HandleRestart)))
	mux.Handle("POST /api/nodes/batch", authMw(http.HandlerFunc(nodeHandler.HandleBatch)))
	mux.Handle("POST /api/nodes/{id}/upgrade", authMw(http.HandlerFunc(dockerHandler.HandleUpgrade)))
	mux.Handle("POST /api/nodes/{id}/downgrade", authMw(http.HandlerFunc(dockerHandler.HandleDowngrade)))
	mux.Handle("POST /api/nodes/batch/upgrade", authMw(http.HandlerFunc(dockerHandler.HandleBatchUpgrade)))
	mux.Handle("GET /api/docker/tags", authMw(http.HandlerFunc(dockerHandler.HandleListTags)))
	mux.Handle("POST /api/nodes/{id}/restore-db", authMw(http.HandlerFunc(dockerHandler.HandleRestoreDB)))
	mux.Handle("POST /api/nodes/{id}/reset-db", authMw(http.HandlerFunc(dockerHandler.HandleResetDB)))
	mux.Handle("POST /api/nodes/{id}/config/preview", authMw(http.HandlerFunc(dockerHandler.HandleConfigPreview)))
	mux.Handle("POST /api/nodes/{id}/config/upgrade", authMw(http.HandlerFunc(dockerHandler.HandleConfigUpgrade)))
	mux.Handle("GET /api/nodes/{id}/config/version-backups", authMw(http.HandlerFunc(dockerHandler.HandleConfigVersionBackups)))
	mux.Handle("POST /api/nodes/{id}/config/version-restore", authMw(http.HandlerFunc(dockerHandler.HandleConfigVersionRestore)))
	mux.Handle("GET /api/nodes/{id}/metrics", authMw(http.HandlerFunc(metricsHandler.HandleNodeMetrics)))
	mux.Handle("GET /api/nodes/{id}/performance", authMw(http.HandlerFunc(performanceHandler.HandleNodePerformance)))
	mux.Handle("GET /api/servers/{id}/metrics", authMw(http.HandlerFunc(metricsHandler.HandleServerMetrics)))
	mux.Handle("GET /api/servers/{id}/images", authMw(http.HandlerFunc(dockerCleanupHandler.HandleListImages)))
	mux.Handle("POST /api/servers/{id}/images/remove", authMw(http.HandlerFunc(dockerCleanupHandler.HandleRemoveImages)))
	mux.Handle("POST /api/servers/{id}/benchmark", authMw(http.HandlerFunc(benchmarkHandler.HandleRunBenchmark)))
	mux.Handle("GET /api/batch-config/parameters", authMw(http.HandlerFunc(batchConfigHandler.HandleListParameters)))
	mux.Handle("POST /api/batch-config/apply", authMw(http.HandlerFunc(batchConfigHandler.HandleApply)))
	mux.Handle("POST /api/slot-inspect", authMw(http.HandlerFunc(slotInspectorHandler.HandleInspect)))
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
	mux.Handle("POST /api/notifications/test-inline", authMw(http.HandlerFunc(notifyHandler.HandleTestInline)))
	mux.Handle("GET /api/notifications/history", authMw(http.HandlerFunc(notifyHandler.HandleHistory)))
	mux.Handle("GET /api/push/vapid-key", authMw(http.HandlerFunc(pushHandler.HandleGetVAPIDKey)))
	mux.Handle("POST /api/push/subscribe", authMw(http.HandlerFunc(pushHandler.HandleSubscribe)))
	mux.Handle("POST /api/push/unsubscribe", authMw(http.HandlerFunc(pushHandler.HandleUnsubscribe)))
	mux.Handle("POST /api/push/test", authMw(http.HandlerFunc(pushHandler.HandleTestPush)))
	mux.Handle("GET /api/push/status", authMw(http.HandlerFunc(pushHandler.HandleStatus)))
	mux.Handle("GET /api/alerts", authMw(http.HandlerFunc(alertHandler.HandleListActiveAlerts)))
	mux.Handle("GET /api/alerts/history", authMw(http.HandlerFunc(alertHandler.HandleAlertHistory)))
	mux.Handle("GET /api/alerts/rules", authMw(http.HandlerFunc(alertHandler.HandleListRules)))
	mux.Handle("POST /api/alerts/rules", authMw(http.HandlerFunc(alertHandler.HandleCreateOrUpdateRule)))
	mux.Handle("DELETE /api/alerts/rules/{id}", authMw(http.HandlerFunc(alertHandler.HandleDeleteRule)))
	mux.Handle("POST /api/alerts/{id}/acknowledge", authMw(http.HandlerFunc(alertHandler.HandleAcknowledgeAlert)))
	mux.Handle("POST /api/agent/upload", authMw(http.HandlerFunc(updateHandler.HandleUploadBinary)))
	mux.Handle("GET /api/agent/binaries", authMw(http.HandlerFunc(updateHandler.HandleListBinaries)))
	mux.Handle("GET /api/agent/version", authMw(http.HandlerFunc(updateHandler.HandleLatestVersion)))
	// Agent-facing binary download (no browser-session auth — the agent has none;
	// validated by server_id like the WS upgrade). Lets capable agents pull the
	// update over HTTP instead of base64-over-WebSocket.
	mux.HandleFunc("GET /api/agent/binary/{server_id}", updateHandler.HandleAgentBinary)
	mux.Handle("POST /api/agent/update/{server_id}", authMw(http.HandlerFunc(updateHandler.HandleUpdateAgent)))
	mux.Handle("POST /api/agent/update/all", authMw(http.HandlerFunc(updateHandler.HandleUpdateAll)))
	mux.Handle("POST /api/agent/restart/{server_id}", authMw(http.HandlerFunc(updateHandler.HandleRestartAgent)))
	mux.Handle("GET /api/agent/releases", authMw(http.HandlerFunc(updateHandler.HandleGitHubReleases)))
	mux.Handle("POST /api/agent/download-release", authMw(http.HandlerFunc(updateHandler.HandleDownloadFromRelease)))
	mux.Handle("POST /api/agent/download-release-auto", authMw(http.HandlerFunc(updateHandler.HandleDownloadReleaseAuto)))
	mux.Handle("GET /api/settings", authMw(http.HandlerFunc(settingsHandler.HandleGetAll)))
	mux.Handle("PUT /api/settings", authMw(http.HandlerFunc(settingsHandler.HandleUpdate)))
	mux.Handle("GET /api/settings/{key}", authMw(http.HandlerFunc(settingsHandler.HandleGetSingle)))
	mux.Handle("PUT /api/settings/{key}", authMw(http.HandlerFunc(settingsHandler.HandleUpdateSingle)))
	mux.Handle("POST /api/settings/reset", authMw(http.HandlerFunc(settingsHandler.HandleResetDefaults)))
	mux.Handle("GET /api/system/version", authMw(http.HandlerFunc(systemHandler.HandleVersionInfo)))
	mux.Handle("POST /api/system/check-update", authMw(http.HandlerFunc(systemHandler.HandleCheckUpdate)))
	mux.Handle("POST /api/system/update", authMw(http.HandlerFunc(systemHandler.HandleSelfUpdate)))

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

// publicBodyLimit caps request bodies on the unauthenticated routes: none of
// them needs more, and anything larger is only good for exhausting memory.
const publicBodyLimit = 64 << 10

func limitBody(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, publicBodyLimit)
		h(w, r)
	}
}

// loadOrCreateJWTKey loads the JWT signing key from the settings store,
// or generates a new one on first run.
func loadOrCreateJWTKey(settings *store.SettingsStore) ([]byte, error) {
	keyHex, err := settings.Get("jwt_signing_key")
	if err != nil {
		return nil, fmt.Errorf("read JWT key: %w", err)
	}

	// Keys created by versions up to v0.3.88 are replaced once on the first
	// start of a newer version, which ends all sessions. Everyone has to log
	// in again.
	rotated, err := settings.Get(jwtKeyRotatedSetting)
	if err != nil {
		return nil, fmt.Errorf("read JWT key state: %w", err)
	}

	if keyHex != "" && rotated != "" {
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
	if err := settings.Set(jwtKeyRotatedSetting, "1"); err != nil {
		return nil, fmt.Errorf("save JWT key state: %w", err)
	}
	if keyHex != "" {
		log.Println("JWT signing key replaced after the security update: all sessions were ended, please log in again")
	} else {
		log.Println("generated new JWT signing key")
	}
	return key, nil
}

// jwtKeyRotatedSetting marks that the signing key was created or replaced by
// v0.3.89 or newer (see loadOrCreateJWTKey).
const jwtKeyRotatedSetting = "jwt_signing_key_rotated"

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

// loadOrCreateInstanceID loads or generates a unique instance ID for this dashboard.
// This ID is used as part of the WebAuthn user ID so that multiple dashboard instances
// sharing the same RP ID (e.g., "localhost") don't overwrite each other's passkeys.
func loadOrCreateInstanceID(settings *store.SettingsStore) (string, error) {
	id, err := settings.Get("instance_id")
	if err != nil {
		return "", fmt.Errorf("read instance ID: %w", err)
	}
	if id != "" {
		return id, nil
	}

	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate instance ID: %w", err)
	}
	id = hex.EncodeToString(b)
	if err := settings.Set("instance_id", id); err != nil {
		return "", fmt.Errorf("save instance ID: %w", err)
	}
	log.Printf("generated new dashboard instance ID: %s", id)
	return id, nil
}

// loadOrCreateVAPIDKeys loads VAPID keys from the settings store, or generates new ones.
func loadOrCreateVAPIDKeys(settings *store.SettingsStore) (*notify.VAPIDKeys, error) {
	pubB64, _ := settings.Get("vapid_public_key")
	privB64, _ := settings.Get("vapid_private_key")

	if pubB64 != "" && privB64 != "" {
		keys, err := notify.LoadVAPIDKeys(pubB64, privB64)
		if err != nil {
			return nil, fmt.Errorf("load VAPID keys: %w", err)
		}
		return keys, nil
	}

	// Generate new VAPID key pair
	keys, err := notify.GenerateVAPIDKeys()
	if err != nil {
		return nil, err
	}
	if err := settings.Set("vapid_public_key", keys.PublicKey); err != nil {
		return nil, fmt.Errorf("save VAPID public key: %w", err)
	}
	if err := settings.Set("vapid_private_key", keys.PrivateB64); err != nil {
		return nil, fmt.Errorf("save VAPID private key: %w", err)
	}
	log.Println("generated new VAPID key pair for Web Push")
	return keys, nil
}

// resetRecoveryCodesAndExit opens the DB, generates new recovery codes, saves them, and exits.
func resetRecoveryCodesAndExit(dataDir string) {
	dbPath := filepath.Join(dataDir, "dashboard.db")
	db, err := store.Open(dbPath)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer func() { _ = db.Close() }()

	settingsStore := store.NewSettingsStore(db)

	// Load existing codes to preserve count context
	existingCodes := loadRecoveryCodes(settingsStore)
	recoveryMgr := auth.NewRecoveryManager(existingCodes)

	plaintextCodes, _, err := recoveryMgr.GenerateCodes()
	if err != nil {
		log.Fatalf("generate recovery codes: %v", err)
	}
	saveRecoveryCodes(settingsStore, recoveryMgr.Codes())

	fmt.Println("=== NEW RECOVERY CODES ===")
	for i, code := range plaintextCodes {
		fmt.Printf("  %d: %s\n", i+1, code)
	}
	fmt.Println("==========================")
	fmt.Println("Previous codes have been invalidated. Save these codes securely.")
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
