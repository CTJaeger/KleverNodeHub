# Klever Node Hub

**Self-hosted management dashboard for Klever validator nodes**

---

## Overview

Klever Node Hub is a lightweight, self-hosted web dashboard that lets Klever validator operators manage and monitor all their nodes across multiple servers — from any device, anywhere in the world.

It replaces manual SSH sessions and bash scripts with a secure, centralized web interface that communicates with lightweight agents deployed on each server.

## Architecture

```
Any Device (Browser)
        │
        │ HTTPS + Password/Passkey/Klever Auth (port 9443)
        ▼
┌──────────────────────┐
│  Dashboard           │  Docker container or binary on one of your servers
│  (Klever Node Hub)   │
└──────────┬───────────┘
           │ WebSocket + mTLS (mutual certificate auth)
      ┌────┼────┐
      ▼    ▼    ▼
    Agent Agent Agent    Lightweight agents on each server
      │    │    │
    Nodes Nodes Nodes    Klever validator/observer Docker containers
```

### Key Principles

- **Self-hosted** — runs on your own infrastructure, no third-party dependency
- **Zero trust** — mTLS between Dashboard and Agents, no SSH keys stored
- **Flexible auth** — Password (works via IP), WebAuthn Passkeys, Klever Extension wallet login
- **Minimal dependencies** — Go standard library + battle-tested open-source packages only
- **Cross-platform access** — any device with a browser (phone, tablet, laptop)
- **Docker-native** — fits existing node operator workflows

## Features

### Node Management
- **Install from scratch** — Provision new nodes remotely (Docker, config, keys)
- **Full lifecycle** — Start, stop, restart, upgrade, downgrade nodes
- **Docker image tags** — Select specific Klever Docker image versions
- **Batch operations** — Apply actions to multiple nodes at once
- **Auto-discovery** — Agent detects existing Klever nodes on registration

### Configuration
- **Remote config editing** — View and edit node YAML config files from the dashboard
- **Centralized push** — Push a config to multiple nodes at once
- **Validator key management** — Generate, import, export BLS validator keys
- **Auto-backup** — Config files backed up before every change

### Monitoring & Alerting
- **Real-time metrics** — CPU, memory, disk, network per server
- **Klever node metrics** — Nonce, sync status, epoch, peers, consensus state (76 metrics from `/node/status`)
- **Historical data** — 7-day high-resolution + long-term averaged archives
- **Nonce stall detection** — Alerts when a node stops producing blocks
- **Alert rules** — Configurable alert rules with acknowledgement
- **GeoIP detection** — Automatic server region detection

### Notifications
- **Telegram bot** — Alerts + interactive commands (`/nodes`, `/status <name>`)
- **Pushover** — Push notifications to any device
- **Webhook** — Extensible to any notification service
- **Per-channel rules** — Choose which alerts go to which channels

### Dashboard
- **Mobile-first** — Responsive UI that works on phone, tablet, and desktop
- **Overview grid** — All servers and nodes at a glance with live status
- **Live log streaming** — Docker container logs in the browser
- **Agent auto-update** — Push agent updates from the dashboard
- **Data tables** — Pagination, search, and column filtering

## Security

| Layer | Technology |
|---|---|
| Dashboard Login | Password (Argon2id) + WebAuthn Passkey + Klever Extension (Ed25519 challenge-response) |
| Rate Limiting | 5 attempts per 15 min per IP, then HTTP 429 |
| Account Recovery | Single-use recovery codes (Argon2id hashed) |
| Config Encryption | AES-256-GCM (encrypted at rest) |
| Agent Communication | mTLS with Ed25519 certificates |
| Agent Command Whitelist | Only known commands accepted (no shell access) |
| Sessions | JWT with short expiry + refresh rotation |

### Why open source is safe

Security follows [Kerckhoffs's principle](https://en.wikipedia.org/wiki/Kerckhoffs%27s_principle) — knowing the source code does not help an attacker without the encryption keys. No security through obscurity.

## Tech Stack

| Component | Technology |
|---|---|
| Backend | Go 1.26, single binary, no runtime needed |
| Frontend | Embedded HTML/JS/CSS (no build step, no Node.js) |
| Agent | Go, single binary, minimal footprint |
| Authentication | Password (Argon2id), WebAuthn/Passkey ([go-webauthn](https://github.com/go-webauthn/webauthn)), Klever Extension (Ed25519) |
| Communication | WebSocket ([coder/websocket](https://github.com/coder/websocket)) + mTLS |
| Database | SQLite via [modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite) (pure Go, no CGO) |
| Encryption at Rest | AES-256-GCM |
| Certificates | Ed25519 |

## Installation

### Quick Start (Dashboard)

Build from source and run on one of your servers:

```bash
# Clone and build
git clone https://github.com/CTJaeger/KleverNodeHub.git
cd KleverNodeHub
make build-linux

# Copy to server
scp bin/klever-node-hub-linux user@your-server:/opt/klever/klever-node-hub

# Run on server
./klever-node-hub --domain your-server.example.com
```

Or use Docker:

```bash
# Build the image
docker build -t klever-node-hub .

# Run
docker run -d \
  -p 9443:9443 \
  -v klever-data:/root/.klever-node-hub \
  --name klever-node-hub \
  klever-node-hub \
  --domain your-server.example.com
```

On first access (`https://your-server:9443`), a setup wizard will guide you through setting a password and optionally registering a Passkey. Recovery codes are printed to the log on first run.

> **Note:** Password login works via IP address — no domain required. Passkeys require a valid domain name. Klever Extension login requires the browser extension and a linked wallet address.

### Dashboard CLI Flags

| Flag | Default | Description |
|---|---|---|
| `--addr` | `:9443` | Listen address (host:port) |
| `--domain` | `localhost` | Domain for WebAuthn RP ID and TLS (optional, only needed for Passkey login) |
| `--data-dir` | `~/.klever-node-hub` | Data directory for DB, certs, config |
| `--reset-recovery-codes` | — | Generate new recovery codes and exit |

### Agent (on each validator server)

Use the install script to set up the agent as a systemd service:

```bash
curl -sSL https://raw.githubusercontent.com/CTJaeger/KleverNodeHub/main/scripts/install-agent.sh \
  | sudo bash -s -- --token YOUR_TOKEN --dashboard https://your-server:9443
```

The script will:
1. Install Docker if not present
2. Download the latest agent binary
3. Create a `klever-agent` systemd service
4. Register with your dashboard
5. Auto-discover existing Klever nodes

You can generate a one-time registration token from the dashboard UI.

### Agent CLI Flags

| Flag | Default | Description |
|---|---|---|
| `--config-dir` | `~/.klever-agent` | Config directory |
| `--dashboard-url` | — | Dashboard URL for registration |
| `--register-token` | — | One-time registration token |
| `--docker-socket` | `/var/run/docker.sock` | Docker socket path |

## Project Structure

```
KleverNodeHub/
├── cmd/
│   ├── dashboard/                 # Dashboard entry point
│   ├── agent/                     # Agent entry point
│   └── seed/                      # Test data seeder
├── internal/
│   ├── auth/                      # Password, WebAuthn, Klever Extension, recovery codes, JWT, rate limiter
│   ├── crypto/                    # AES-256-GCM, Ed25519, mTLS, CA
│   ├── dashboard/                 # HTTP server, tag cache, GeoIP, token manager
│   │   ├── alerting/              # Alert evaluator, default rules
│   │   ├── handlers/              # HTTP handlers (nodes, servers, docker, config, keys, alerts, ...)
│   │   ├── scheduler/             # Metrics retention scheduler
│   │   └── ws/                    # WebSocket hub, agent handler, browser handler
│   ├── agent/                     # Agent logic, Docker ops, executor, metrics collector
│   ├── models/                    # Shared data structures and message types
│   ├── store/                     # SQLite database layer (servers, nodes, metrics, alerts, settings)
│   ├── notify/                    # Telegram, Pushover, webhook dispatchers
│   └── version/                   # Build version info
├── web/
│   ├── templates/                 # HTML templates (overview, server, node, alerts, settings, login)
│   └── static/                    # JS (api, app, charts, datatable, login, passkey, klever, ws) + CSS
├── scripts/                       # Agent install script
├── docs/                          # PRD and documentation
├── .github/workflows/             # CI + Release pipelines
├── Dockerfile                     # Dashboard container
├── Dockerfile.agent               # Agent container
├── Makefile                       # Build, test, deploy targets
├── go.mod
└── README.md
```

## Development

### Prerequisites

- Go 1.26+
- Docker (for containerized deployment)

### Build

```bash
# Build both (outputs to bin/)
make build

# Cross-compile for Linux
make build-linux

# Build individually
make build-dashboard
make build-agent
```

### Run locally

```bash
# Direct
make run

# With hot-reload (requires air)
make run-live
```

### Test

```bash
make test          # go test ./... -v -race
make lint          # golangci-lint + go vet
make security      # govulncheck
make coverage      # coverage report
```

### Deploy to remote server

```bash
# Deploy both dashboard + agent
make deploy REMOTE_HOST=your-server

# Deploy individually
make deploy-dashboard REMOTE_HOST=your-server
make deploy-agent REMOTE_HOST=your-server

# Custom SSH key and remote path
make deploy REMOTE_HOST=your-server SSH_KEY=~/.ssh/id_ed25519 REMOTE_PATH=/opt/klever
```

## CI/CD

Automated checks on every push and pull request:

- **Unit Tests** — `go test ./... -race`
- **Lint & Format** — `golangci-lint` + `goimports` + `go vet`
- **Security Scan** — `govulncheck` (known vulnerability detection)
- **Build Verification** — Cross-platform build (Linux, macOS, Windows × amd64, arm64)

### Releases

Tag a version to trigger automatic release builds:

```bash
git tag v0.1.0
git push --tags
```

This creates a GitHub Release with pre-built binaries for all platforms and SHA256 checksums.

## Documentation

- **[Product Requirements Document](docs/PRD.md)** — Full specification with architecture, data models, API endpoints, workflows, and implementation phases

## License

MIT

## Contributing

This project is currently in early development. Contributions welcome once the core architecture is stable.
