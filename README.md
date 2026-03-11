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
        │ HTTPS + Passkey Auth
        ▼
┌──────────────────────┐
│  Dashboard           │  Docker container or binary on one of your servers
│  (Klever Node Hub)   │
└──────────┬───────────┘
           │ mTLS (mutual certificate auth)
      ┌────┼────┐
      ▼    ▼    ▼
    Agent Agent Agent    Lightweight agents on each server
      │    │    │
    Nodes Nodes Nodes    Klever validator/observer Docker containers
```

### Key Principles

- **Self-hosted** — runs on your own infrastructure, no third-party dependency
- **Zero trust** — mTLS between Dashboard and Agents, no SSH keys stored
- **Passwordless** — WebAuthn Passkey authentication (Face ID, Touch ID, hardware keys)
- **Minimal attack surface** — Go standard library + golang.org/x/crypto only
- **Cross-platform access** — any device with a browser (phone, tablet, laptop)
- **Docker-native** — one-liner installation, fits existing node operator workflows

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

### Monitoring
- **Real-time metrics** — CPU, memory, disk, network per server
- **Klever node metrics** — Nonce, sync status, epoch, peers, consensus state (76 metrics from `/node/status`)
- **Historical data** — 7-day high-resolution + long-term averaged archives
- **Nonce stall detection** — Alerts when a node stops producing blocks

### Notifications
- **Telegram bot** — Alerts + interactive commands (`/nodes`, `/status <name>`)
- **Pushover** — Push notifications to any device
- **Webhook** — Extensible to any notification service
- **Per-channel rules** — Choose which alerts go to which channels

### Dashboard
- **Mobile-first** — Responsive UI that works on phone, tablet, and desktop
- **Overview grid** — All servers and nodes at a glance
- **Live log streaming** — Docker container logs in the browser
- **Agent auto-update** — Push agent updates from the dashboard

## Security

| Layer | Technology |
|---|---|
| Dashboard Login | WebAuthn Passkey (passwordless, phishing-resistant) |
| Account Recovery | Single-use recovery codes (Argon2id hashed) |
| Config Encryption | AES-256-GCM (encrypted at rest) |
| Agent Communication | mTLS with Ed25519 certificates |
| Agent Command Whitelist | Only known commands accepted (no shell access) |
| Sessions | JWT with short expiry + refresh rotation |
| Dependencies | Go stdlib + golang.org/x/crypto only |

### Why open source is safe

Security follows [Kerckhoffs's principle](https://en.wikipedia.org/wiki/Kerckhoffs%27s_principle) — knowing the source code does not help an attacker without the encryption keys. No security through obscurity.

## Tech Stack

| Component | Technology | Reason |
|---|---|---|
| Dashboard Backend | Go 1.26 | Single binary, no runtime needed |
| Dashboard Frontend | Embedded Web UI | No separate frontend deployment |
| Agent | Go | Single binary, minimal footprint |
| Authentication | WebAuthn/Passkey | Passwordless, phishing-resistant |
| Communication | WebSocket + mTLS | Encrypted, authenticated, persistent |
| Database | SQLite (encrypted) | No external DB server needed |
| Encryption at Rest | AES-256-GCM | Industry standard |
| Certificates | Ed25519 | Fast, secure, small keys |

## Installation

### Dashboard (on one of your servers)

```bash
docker run -d -p 9443:9443 --name klever-node-hub klever-node-hub
```

On first access, a setup wizard will guide you through registering your Passkey and generating recovery codes.

### Agent (on each validator server)

```bash
curl -sSL https://raw.githubusercontent.com/CTJaeger/KleverNodeHub/main/scripts/install-agent.sh | bash
klever-agent register --token <one-time-token> --dashboard https://<your-server>:9443
```

The agent will auto-discover any existing Klever nodes running on the server.

## Project Structure

```
KleverNodeHub/
├── cmd/
│   ├── dashboard/              # Dashboard binary entry point
│   └── agent/                  # Agent binary entry point
├── internal/
│   ├── auth/                   # WebAuthn, recovery codes, JWT, middleware
│   ├── crypto/                 # AES-256-GCM, Ed25519, mTLS, CA
│   ├── dashboard/              # HTTP server, handlers, WebSocket hub, scheduler
│   ├── agent/                  # Agent logic, Docker ops, executor, whitelist
│   ├── models/                 # Shared data structures
│   ├── store/                  # SQLite database layer
│   └── notify/                 # Telegram, Pushover, webhook dispatchers
├── web/                        # Embedded frontend (HTML/JS/CSS)
├── scripts/                    # Agent install script
├── docs/                       # PRD and documentation
├── .github/workflows/          # CI/CD
├── Dockerfile                  # Dashboard container
├── Dockerfile.agent            # Agent container
├── Makefile
├── go.mod
└── README.md
```

## Development

### Prerequisites

- Go 1.26+
- Docker (for containerized deployment)

### Build

```bash
# Using Make (outputs to bin/)
make build-dashboard
make build-agent

# Or directly
go build -o bin/klever-node-hub ./cmd/dashboard
go build -o bin/klever-agent ./cmd/agent
```

### Test

```bash
go test ./... -v -race
```

## CI/CD

Automated checks on every push and pull request:

- **Unit Tests** — `go test ./... -race`
- **Static Analysis** — `go vet ./...`
- **Security Scan** — `govulncheck` (known vulnerability detection)
- **Build Verification** — Cross-platform build (Linux, macOS, Windows × amd64, arm64)

## Documentation

- **[Product Requirements Document](docs/PRD.md)** — Full specification with architecture, data models, API endpoints, workflows, and implementation phases

## License

MIT

## Contributing

This project is currently in early development. Contributions welcome once the core architecture is stable.
