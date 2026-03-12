# Changelog

## [Unreleased]

### 2026-03-12
- **Issue #24**: Alert rules engine with configurable thresholds
  - Migration 3: `alert_rules` and `alerts` tables with indexes
  - `AlertStore` with full CRUD for rules and alert records (create, update, delete, list, query)
  - `Evaluator`: periodic rule evaluation engine (15s interval, configurable)
  - Alert state machine: Normal → Pending → Firing → Resolved
  - Duration-based rules: threshold must breach for configured seconds before firing
  - Cooldown period prevents notification spam (per-rule configurable)
  - Recovery notifications when alerts resolve
  - 7 built-in default rules: nonce stall, node offline, high CPU, high memory, disk full, low peers, sync lag
  - System metrics evaluation (CPU/memory/disk per server) and node metrics evaluation
  - Heartbeat stale detection for agent offline alerts
  - Stall detection for nonce and heartbeat metrics
  - Integration with notification manager from Issue #23
  - Dashboard API: GET active alerts, GET history, GET/POST/DELETE rules, POST acknowledge
  - Alert banner on overview page (active alerts with severity colors)
  - Alert history panel with acknowledge button
  - Alert rules configuration UI (add/edit/delete, built-in rules editable)
  - 22 unit tests (evaluator: threshold, resolve, pending→firing, system metrics, heartbeat stale, defaults, start/stop; store: CRUD, enabled filter, ack, count, active queries)

- **Issue #23**: Notification system — Telegram, Pushover, and webhook channels
  - `Channel` interface with `Send`, `Validate`, `Name` methods
  - `TelegramChannel`: Bot API, Markdown formatting, rate limiting (20 msg/min)
  - `PushoverChannel`: Priority mapping (critical=emergency, warning=high, info=normal)
  - `WebhookChannel`: Configurable URL/headers, retry with exponential backoff (3 attempts)
  - `Manager`: Fan-out to all enabled channels, test endpoint, in-memory history (500 entries)
  - Dashboard API: CRUD channels, test send, history
  - Channel config persisted in settings store, auto-loaded on startup
  - 15 unit tests (manager ops, fan-out, partial failure, history, validation, webhook send/retry)

- **Issue #22**: Validator key management — generate, import, export
  - Key generation via klever-go keygenerator Docker entrypoint
  - Import/export with PEM format validation (BLS public key extraction)
  - Auto-backup before key operations, timestamped backups in `config/key-backups/`
  - 6 executor commands: `key.info`, `key.generate`, `key.import`, `key.export`, `key.backup`, `key.backups`
  - Dashboard API: GET key info, POST generate, POST import, GET export, GET backups
  - Key management UI: generate/import/export buttons, key info display on node detail page
  - 10 unit tests (get info, import, invalid PEM, backup on import, export, backup/list)

- **Issue #21**: Real-time log streaming from node containers
  - `FetchLogs`: Docker API log reader with multiplexed stream parsing (stdout/stderr)
  - `StreamLogs`: Live log follow with context cancellation and 30-min timeout
  - `LogStreamManager`: Manages active streams (one per container, auto-cleanup)
  - `node.logs` executor command with tail and since parameters
  - Dashboard API: `GET /api/nodes/{id}/logs?tail=100&since=<timestamp>`
  - Log viewer UI: terminal-style display, log level highlighting (ERROR/WARN/INFO/DEBUG)
  - Text search filter, timestamp toggle, line count selector, auto-scroll
  - Download logs as text file
  - 5 unit tests (timestamp parsing, Docker stream parsing, empty stream, manager lifecycle)

- **Issue #20**: Remote node configuration management (read/write/diff)
  - Agent-side config ops: `ListConfigFiles`, `ReadConfigFile`, `WriteConfigFile`, `BackupConfigFile`, `RestoreConfigBackup`
  - Path traversal prevention, allowed extension whitelist (.toml, .json, .pem, .yaml, .yml, .cfg)
  - Auto-backup before every write, timestamped backup files in `config/backups/`
  - 6 new executor commands: `config.list`, `config.read`, `config.write`, `config.backup`, `config.backups`, `config.restore`
  - Dashboard API: GET/PUT config files, GET backups, POST restore, POST multi-push
  - Config editor UI on node detail page: file selector, textarea editor, Save & Restart, backup/restore
  - Multi-node config push: `POST /api/config/push` with optional container restart
  - 12 unit tests (list, read, write with backup, traversal prevention, extension validation, restore)

- **Issue #19**: Complete upgrade/downgrade flow with progress tracking
  - `UpgradeContainerWithRollback`: 6-step upgrade with health verification and automatic rollback
  - Progress callback (`UpgradeProgress`) reports each step: snapshot, pulling, stopping, removing, creating, verifying
  - Executor uses rollback-aware upgrade (replaces plain `UpgradeContainer`)
  - Batch upgrade: `POST /api/nodes/batch/upgrade` — sequential upgrade to maintain quorum
  - Node detail UI: version selector dropdown, upgrade/downgrade buttons, progress bar
  - Added `node.provision` to command whitelist
  - 5 new tests: success with progress, create-fail rollback, no-progress, total steps, rollback helper

- **Issue #18**: Node provisioning wizard — install Klever node from scratch
  - Multi-step `Provisioner` (7 steps): preflight, pull, dirs, config, container, start, verify
  - Progress reporting, cleanup on failure, `node.provision` executor command
  - Dashboard handler `POST /api/nodes/provision`, UI wizard with live progress bar
  - Config download from official Klever backup endpoints (mainnet/testnet)

- **Issue #17**: Metrics dashboard UI — charts, gauges, and historical graphs
  - Custom lightweight charting module (`charts.js`) — SVG ring gauges, Canvas time-series, sparklines
  - Overview page: CPU/Memory/Disk gauges per server, node status breakdown (running/stopped/syncing)
  - Node detail page: status header (nonce, epoch, peers, consensus), sync progress bar
  - 6 time-series charts: block nonce, peers, transactions, network I/O, CPU, memory
  - Time range selector (1h, 6h, 24h, 7d, 30d), charts auto-resize on window resize
  - Auto-refresh every 15s, WebSocket push for real-time updates
  - Responsive layout: charts stack vertically on mobile, 2-column grid on desktop
  - No external dependencies, all embedded via Go `embed.FS`

- **Issue #16**: Metrics storage — hot/cold tables with retention and decimation
  - Migration 2: `metrics_recent`, `metrics_archive`, `system_metrics` tables with indexes
  - `MetricsStore` with batch insert, query (recent/archive/auto-resolution), decimation, purge
  - `Scheduler` with 3 background jobs: decimation (1h), archive purge (24h), system cleanup (6h)
  - WebSocket agent handler persists `node.metrics` and heartbeat system metrics to DB
  - Metrics query API: `GET /api/nodes/{id}/metrics`, `GET /api/servers/{id}/metrics`
  - Auto-resolution: recent queries use hot table, older use archive, spans merge both
  - 10 unit tests for store operations

### 2026-03-11
- **Issue #15**: Klever node metrics polling from `/node/status` endpoint
  - New `NodeMetricsCollector` polls each discovered node's REST API
  - Parses all 76+ metrics from `/node/status` JSON response into `map[string]any`
  - Configurable poll interval (default 15s) and HTTP timeout (5s)
  - Nonce stall detection: alerts when `klv_nonce` stops incrementing (configurable threshold)
  - `node.metrics` and `node.nonce_stall` WebSocket events
  - Auto-updates node list from discovery reports
  - `RunPoller()` background goroutine for continuous polling
  - 15 unit tests with mock HTTP server (success, errors, stall detection, serialization)
  - `NodeMetricsEvent` and `NodeNonceStallEvent` models
  - Integrated into agent main loop with dedicated channels
  - Fixed pre-existing lint issue in `webauthn.go` (unchecked `rand.Read`)

- **Issue #14**: Agent system metrics collection (CPU, memory, disk, load average)
  - New `MetricsCollector` with `/proc` parsing for Linux, graceful fallback for macOS/Windows
  - CPU% via delta between two `/proc/stat` samples
  - Memory from `/proc/meminfo` (MemTotal, MemAvailable)
  - Disk via `syscall.Statfs` (build tags: unix vs windows)
  - Load average from `/proc/loadavg`
  - Metrics attached to heartbeat payload (`HeartbeatPayload.Metrics`)
  - `SystemMetrics` model with all fields
  - 8 unit tests including mock `/proc` data tests (skip on non-Linux)

- **Issue #12/#13 completion**: Implement missing acceptance criteria
  - Added `HandlePasskeyFinishRegister` and `HandlePasskeyFinishLogin` to complete WebAuthn ceremony
  - Added `POST /api/agent/register` endpoint — validates token, creates server, issues mTLS certificate
  - Implemented `registerWithDashboard()` HTTP client in agent (replaces placeholder)
  - Agent now saves `KeyPEM` from registration response
  - CA initialization in dashboard main (load or create, encrypted private key storage)
  - Passkey credential persistence via `onCredentialsChanged` callback
  - Added `RegistrationResponse.KeyPEM` field for agent private key delivery
  - 5 new registration handler tests (success, invalid token, single-use, invalid body, generate token)

- **Lint fixes**: Resolve all 15 staticcheck issues
  - Migrated `nhooyr.io/websocket` → `github.com/coder/websocket` (SA1019 deprecated)
  - Merged `if ctx.Err() != nil { break }` into `for ctx.Err() == nil` loop condition (QF1006)
  - Removed ineffective `break` in `select` case (SA4011)
  - `docker_test.go` nolint directive already had `staticcheck` (QF1002 — no change needed)

- **Issue #13**: Wire agent main() — lifecycle, WebSocket, and command execution
  - CLI flags: `--config-dir`, `--dashboard-url`, `--register-token`, `--docker-socket`
  - Config load/save with registration flow
  - WebSocket connection to dashboard with auto-reconnect (exponential backoff)
  - Message pump: read commands, execute via Executor, send results back
  - Heartbeat every 30s, auto-discovery every 5 min
  - Graceful shutdown on SIGINT/SIGTERM
  - Added `Agent.Config()` getter

- **Issue #12**: Wire dashboard main() — connect all Phase 1 components
  - CLI flags: `--addr`, `--data-dir` (default `~/.klever-node-hub/`)
  - Full dependency chain: DB → stores → auth → hub → handlers → routes
  - JWT signing key persisted in settings store (auto-generated on first run)
  - WebAuthn + recovery codes loaded from settings store
  - Initial recovery codes printed to console on first run
  - Auth middleware on all protected API routes
  - WebSocket agent handler (`/ws/agent`) with message dispatch
  - Discovery report processing: creates/updates nodes in DB
  - Graceful shutdown on SIGINT/SIGTERM
  - Added `nhooyr.io/websocket` dependency for WebSocket support

- **Lint Fix**: Fixed all 53 golangci-lint issues (50 errcheck, 2 staticcheck, 1 unused)
  - `internal/store/`: Checked rows.Close, tx.Rollback, json.Unmarshal, db.Close returns
  - `internal/agent/`: Checked resp.Body.Close, io.Copy, StopContainer; replaced loop with append spread
  - `internal/crypto/mtls_test.go`: Checked all deferred Close, Serve, Write, Fprintf calls
  - `internal/dashboard/`: Checked SetupRoutes, w.Write returns
  - `internal/dashboard/handlers/nodes.go`: Removed unused `nodeActionRequest` type

- **CI Fix**: Updated Go version from 1.22 to 1.26 in CI workflow (matching project go.mod 1.25+)
  - Removed `-race` flag (requires CGO, we use CGO_ENABLED=0)
  - Added explicit `CGO_ENABLED=0` for cross-compilation builds
  - Fixes lint, security scan, and test job failures

- **Issue #10**: Docker operations — pull image, create/remove containers, upgrade/downgrade
  - Docker image pull via Engine API with progress streaming
  - Container creation with validated params (matching KleverNodeManagement script)
  - Container removal with graceful stop
  - Upgrade/downgrade flow: inspect → pull → remove → create → start
  - Docker Hub tag listing with 15-min cache (filters dev/testnet/alpine tags)
  - Dashboard API: POST upgrade/downgrade, GET /api/docker/tags
  - Port auto-assignment, data directory management
  - Extended command whitelist: create, remove, upgrade, pull, discovery
  - 25+ new tests (container ops, validation, upgrade, config parsing)

- **Issue #9**: Web UI shell — login, overview, and basic node list
  - Embedded HTTPS server with auto-generated self-signed cert
  - Security headers (CSP, X-Frame-Options, HSTS, XSS-Protection)
  - Mobile-first responsive CSS framework (dark theme, 768px/1200px breakpoints)
  - Login page: Passkey authentication, recovery code fallback, first-run setup
  - Overview page: server/node cards, status badges, add-server flow
  - Node detail page: status, actions (start/stop/restart), info display
  - Frontend JS: API client with JWT auto-refresh, WebSocket client, Passkey helpers
  - Auth API handlers: setup status, passkey begin/finish, recovery login, refresh, logout
  - Server/node API handlers: list, get, filter by server
  - Registration token API handler
  - 18 new tests (server, auth handler, server handler)

- **Issue #8**: Basic node operations — start, stop, restart via dashboard
  - Command whitelist with container name validation (injection prevention)
  - Docker operations: start, stop (graceful 30s), restart via Engine API
  - Command executor/dispatcher with timeout handling (60s default)
  - WebSocket hub: SendCommand with pending tracking, timeout, result matching
  - Dashboard API handlers: POST start/stop/restart + batch operations
  - End-to-end flow: API → WebSocket → Agent → Docker → result back
  - 30+ new tests (whitelist, executor, hub commands, handler HTTP tests)

- **Issue #7**: Agent auto-discovery — scan existing Klever nodes on server
  - Docker Engine API client via Unix socket (no CLI dependency)
  - Node discovery: list containers, extract params (port, display name, redundancy, image tag, data dir)
  - BLS public key extraction from `validatorKey.pem`
  - Discovery report message type for WebSocket communication
  - 19 tests (mock Docker socket, parsing, BLS extraction, edge cases)

- **Issue #6**: Agent registration — one-time token, certificate issuance, WebSocket connection
  - WebSocket message envelope and payload types
  - Connection hub for tracking active agent connections
  - One-time token manager for secure registration
  - Agent config persistence and registration flow
  - Install script for automated Linux deployment (systemd)

### 2026-03-10
- **Issue #5**: SQLite store with models and migrations
- **Issue #4**: Auth module — JWT, recovery codes, WebAuthn, middleware
- **Issue #3**: Crypto module — Ed25519, AES-256-GCM, mTLS, CA management
- **Issue #2**: Project scaffolding — Go module, directory structure, Makefile, Dockerfiles
