# Klever Node Hub

**Self-hosted management dashboard for Klever validator nodes**

---

## Overview

Klever Node Hub is a lightweight, self-hosted web dashboard that lets Klever validator operators manage and monitor all their nodes across multiple servers — from any device, anywhere in the world.

## Architecture

```
Any Device (Browser)
        │
        │ HTTPS + 2FA
        ▼
┌──────────────────┐
│  Dashboard       │  Docker Container on one of your servers
│  (Klever Node Hub)│
└───────┬──────────┘
        │ mTLS (mutual certificate auth)
   ┌────┼────┐
   ▼    ▼    ▼
 Agent Agent Agent    Lightweight agents on each server
```

### Key Principles

- **Self-hosted** — runs on your own infrastructure, no third-party dependency
- **Zero trust** — mTLS between Dashboard and Agents, no SSH keys stored
- **Minimal attack surface** — Go standard library only, no third-party packages
- **Cross-platform access** — any device with a browser (phone, tablet, laptop)
- **Docker-native** — one-liner installation, fits existing node operator workflows

## Features (Planned)

- **Node Management** — Create, start, stop, restart, update nodes remotely
- **Real-time Monitoring** — Version, sync status, nonce, CPU, disk usage per node
- **Multi-Server** — Manage nodes across unlimited servers from one dashboard
- **Docker Image Tag Selector** — Choose specific Klever Docker image versions
- **Alerts** — Telegram notifications for node issues (optional)
- **Secure by Design** — mTLS, Argon2id, AES-256-GCM, TOTP 2FA

## Security

| Layer | Technology |
|---|---|
| Dashboard Login | Master password (Argon2id) + TOTP 2FA |
| Config Encryption | AES-256-GCM (encrypted at rest) |
| Agent Communication | mTLS with Ed25519 certificates |
| Agent Command Whitelist | Only known commands accepted (no shell access) |
| Dependencies | Go stdlib + golang.org/x/crypto only |

### Why open source is safe

Security follows [Kerckhoffs's principle](https://en.wikipedia.org/wiki/Kerckhoffs%27s_principle) — knowing the source code does not help an attacker without the encryption keys. No security through obscurity.

## Tech Stack

| Component | Technology | Reason |
|---|---|---|
| Dashboard Backend | Go | Single binary, no runtime needed |
| Dashboard Frontend | Embedded Web UI | No separate frontend deployment |
| Agent | Go | Single binary, minimal footprint |
| Communication | WebSocket + mTLS | Encrypted, authenticated, persistent |
| Database | SQLite (encrypted) | No external DB server needed |
| Key Derivation | Argon2id | Brute-force resistant (~1s per attempt) |
| Encryption at Rest | AES-256-GCM | Industry standard |
| Certificates | Ed25519 | Fast, secure, small keys |

## Installation

### Dashboard (on one of your servers)

```bash
docker run -d -p 9443:9443 --name klever-node-hub klever-node-hub
```

### Agent (on each validator server)

```bash
curl -sSL https://raw.githubusercontent.com/CTJaeger/KleverNodeHub/main/install-agent.sh | bash
klever-agent register --token <one-time-token> --dashboard https://<your-server>:9443
```

## Project Structure

```
KleverNodeHub/
├── cmd/
│   ├── dashboard/          # Dashboard binary entry point
│   └── agent/              # Agent binary entry point
├── internal/
│   ├── auth/               # Argon2id, TOTP, session management
│   ├── crypto/             # AES-256-GCM, Ed25519, mTLS setup
│   ├── dashboard/          # HTTP handlers, WebSocket hub
│   ├── agent/              # Agent logic, command whitelist
│   └── models/             # Data structures
├── web/                    # Embedded frontend (HTML/JS/CSS)
├── .github/workflows/      # CI/CD
├── Dockerfile              # Dashboard container
├── go.mod
└── README.md
```

## Development

### Prerequisites

- Go 1.22+
- Docker (for containerized deployment)

### Build

```bash
# Dashboard
go build -o klever-node-hub ./cmd/dashboard

# Agent
go build -o klever-agent ./cmd/agent
```

### Test

```bash
go test ./... -v -race
```

## CI/CD

Automated checks on every push and pull request:

- **Unit Tests** — `go test ./... -race`
- **Linting** — `golangci-lint` (static analysis)
- **Security Scan** — `govulncheck` (known vulnerability detection)
- **Build Verification** — Cross-platform build (Linux, macOS, Windows)

## License

MIT

## Contributing

This project is currently in early development. Contributions welcome once the core architecture is stable.
