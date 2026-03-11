# Changelog

## [Unreleased]

### 2026-03-11
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
