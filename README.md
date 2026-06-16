# Klever Node Hub

[![CI](https://github.com/CTJaeger/KleverNodeHub/actions/workflows/ci.yml/badge.svg)](https://github.com/CTJaeger/KleverNodeHub/actions/workflows/ci.yml)
[![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![Docker Hub](https://img.shields.io/docker/v/ctjaeger/klever-node-hub?label=Docker&logo=docker)](https://hub.docker.com/r/ctjaeger/klever-node-hub)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

**Self-hosted management dashboard for Klever validator nodes**

![Dashboard Overview](docs/dash.png)

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
- **Provision from scratch** — Deploy new nodes remotely (Docker, config, keys). Create **multiple nodes in one run** with auto-incrementing REST API ports, pick **Main or Fallback** role per node, and choose a **sync mode**: fast bootstrap (latest epoch), full-DB snapshot (archival node in one step), or full sync from genesis. Live node-name validation as you type.
- **Full lifecycle** — Start, stop, restart, upgrade, downgrade nodes
- **Chain DB restore** — Replace a node's chain database with the official Klever FullNode snapshot, streamed straight to disk with a preflight disk check and rollback of the previous DB. For turning a fast-bootstrapped node into a full archival node (e.g. for indexers).
- **Version-aware upgrades** — An outdated-nodes indicator shows which nodes aren't on the latest version; Klever-aware version ordering (stable outranks RC) keeps upgrade suggestions correct. Batch-upgrade selected nodes sequentially to maintain quorum, with optional config refresh.
- **Docker image tags** — Select specific Klever Docker image versions
- **Batch operations** — Apply actions to multiple nodes at once
- **Auto-discovery** — Agent detects existing Klever nodes on registration

### Configuration
- **Remote config editing** — View and edit node config files from the dashboard
- **Centralized push** — Push a config to multiple nodes at once
- **Config version upgrade** — Download fresh configs when upgrading, with versioned backups
- **Validator key management** — Generate, import, export BLS validator keys
- **Auto-backup** — Config files backed up before every change, one-click restore

### Monitoring & Alerting
- **Real-time metrics** — CPU, memory, disk, network per server
- **Klever node metrics** — Nonce, sync status, epoch, peers, consensus state (76 metrics from `/node/status`)
- **Validator overview** — A dedicated Validators page tracking your managed validators (matched by BLS key) on-chain: state (elected/jailed/waiting), commission, self-stake, allowance, blocks produced/missed, plus a live **block-production timeline** of the last 100 blocks per validator
- **Historical data** — 7-day high-resolution + long-term averaged archives
- **Nonce stall detection** — Alerts when a node stops producing blocks
- **Version performance regression** — Flags when a new node version is measurably slower at block processing, with a passive before/after report per node
- **Maintenance awareness** — Nodes you deliberately stop from the dashboard are recognized as maintenance and don't fire offline alerts; cleared automatically once running again
- **Alert rules** — Configurable alert rules with acknowledgement, plus a global alert badge on every page
- **GeoIP detection** — Automatic server region detection

### Tools
- **Batch Config** — Change one config parameter across all nodes at once, with a side-by-side view of the current value per node and optional restart-after-apply
- **Slot Inspector** — Enter a slot number, pick nodes, and the Hub pulls the matching logs from every agent, filters around that slot, and parses `validatorTime` / `lowerBound` into a comparison table
- **Server Hardware Benchmark** — One click runs the official Klever benchmark (Disk I/O, Network, CPU, Memory, KV Store) with a score and grade
- **Docker Cleanup** — Reclaim disk from old `klever-go` images left behind by upgrades; images still referenced by a container (running or stopped) are flagged in-use and protected

### Notifications
- **Telegram bot** — Alerts with Markdown formatting
- **Pushover** — Push notifications to any device
- **Webhook** — HTTP POST to any URL with custom headers and retry logic
- **Web Push** — Browser push notifications (works even when the tab is closed)
- **Per-channel filtering** — Choose which alert types and severities go to which channels

### Dashboard
- **Mobile-first** — Responsive UI that works on phone, tablet, and desktop
- **Progressive Web App** — Installable on mobile/desktop, works like a native app
- **Overview grid** — All servers and nodes at a glance with live status; flat or grouped-by-BLS-key view (Master on top, fallbacks indented) and a search mode that lifts the tables under the search box
- **Live log streaming** — Docker container logs in the browser
- **Agent auto-update** — Push agent updates from the dashboard with inline progress per server
- **Restart agent** — Restart an agent from the dashboard without touching the running nodes
- **Dashboard self-update** — One-click update from within the dashboard (Docker mode swaps the container cleanly via a finalize helper, with rollback)
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
docker run -d \
  -p 9443:9443 \
  -v klever-data:/root/.klever-node-hub \
  -v /var/run/docker.sock:/var/run/docker.sock \
  --name klever-node-hub \
  ctjaeger/klever-node-hub:latest \
  --domain your-server.example.com
```

> **One-click updates:** Mounting `/var/run/docker.sock` lets the dashboard pull a new image and recreate its own container when you click **Update Now** in the update banner. Leave the mount off if you'd rather update manually with `docker pull` + `docker run` — the dashboard will then just show the `docker pull` command instead of the button. The socket grants root-equivalent control of the Docker daemon, so only mount it if you trust the dashboard accordingly.

On first access (`https://your-server:9443`), a setup wizard will guide you through setting a password and optionally registering a Passkey. Recovery codes are printed to the log on first run.

> **Trusted HTTPS / PWA:** The dashboard uses a self-signed certificate by default. To get a trusted certificate (required for mobile PWA install), place a reverse proxy with Let's Encrypt in front of it. See the **[Reverse Proxy Setup Guide](docs/reverse-proxy.md)** for Apache, Nginx, and Caddy configurations.

> **Note:** Password login works via IP address — no domain required. Passkeys require a valid domain name. Klever Extension login requires the browser extension and a linked wallet address.

### Dashboard CLI Flags

| Flag | Default | Description |
|---|---|---|
| `--addr` | `:9443` | Listen address (host:port) |
| `--domain` | `localhost` | Domain for WebAuthn RP ID and TLS (optional, only needed for Passkey login) |
| `--data-dir` | `~/.klever-node-hub` | Data directory for DB, certs, config |
| `--reset-recovery-codes` | — | Generate new recovery codes and exit |

### Agent (on each validator server)

Each server that runs Klever nodes needs a lightweight agent. The agent connects back to your dashboard via encrypted mTLS.

#### Step 1: Generate a registration token

1. Open your dashboard in the browser (`https://your-server:9443`)
2. Click **"Add Server"** on the overview page
3. Click **"Generate Token"** — this creates a one-time token (valid for 1 hour)
4. Copy the displayed install command

#### Step 2: Run the install command on your node server

SSH into your node server and paste the copied command. It looks like this:

```bash
curl -sSL https://raw.githubusercontent.com/CTJaeger/KleverNodeHub/main/scripts/install-agent.sh \
  | sudo bash -s -- --token <YOUR_TOKEN> --dashboard https://<DASHBOARD_IP>:9443
```

The script will:
1. Install Docker if not present
2. Download the latest agent binary
3. Register with your dashboard using the one-time token
4. Create and start a `klever-agent` systemd service
5. Auto-discover existing Klever nodes on the server

> **Important:** The token can only be used once and expires after 1 hour. Generate a new token for each server you add.

#### Alternative: Docker

```bash
docker run -d \
  -v /var/run/docker.sock:/var/run/docker.sock \
  --name klever-agent \
  ctjaeger/klever-agent:latest \
  --dashboard-url https://<DASHBOARD_IP>:9443 --register-token <YOUR_TOKEN>
```

#### Alternative: Manual binary

```bash
# Download from GitHub Releases
wget https://github.com/CTJaeger/KleverNodeHub/releases/latest/download/klever-agent-linux-amd64

# Make executable and register
chmod +x klever-agent-linux-amd64
./klever-agent-linux-amd64 --dashboard-url https://<DASHBOARD_IP>:9443 --register-token <YOUR_TOKEN>
```

After registration, the agent stores its mTLS certificate locally and reconnects automatically — no token needed for subsequent starts.

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
│   │   ├── alerting/              # Alert evaluator, default rules, version regression
│   │   ├── handlers/              # HTTP handlers (nodes, servers, docker, config, keys, alerts, provision, restore, cleanup, validators, ...)
│   │   ├── klever/                # Klever chain client + validator block-production monitor
│   │   ├── scheduler/             # Metrics retention scheduler
│   │   └── ws/                    # WebSocket hub, agent handler, browser handler
│   ├── agent/                     # Agent logic, Docker ops, executor, provisioner, DB restore, image cleanup, metrics collector
│   ├── models/                    # Shared data structures and message types
│   ├── store/                     # SQLite database layer (servers, nodes, metrics, alerts, settings)
│   ├── notify/                    # Telegram, Pushover, webhook dispatchers
│   └── version/                   # Build version info
├── web/
│   ├── templates/                 # HTML templates (overview, server, node, validators, alerts, settings, login, batchconfig, slotinspector, docker-cleanup)
│   └── static/                    # JS (api, app, charts, datatable, login, passkey, klever, version, ws) + CSS + service worker
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

- **[Complete Guide / Tutorial](tutorial.md)** — Step-by-step walkthrough of every feature with screenshots
- **[Reverse Proxy Setup](docs/reverse-proxy.md)** — HTTPS with Let's Encrypt via Apache, Nginx, or Caddy (required for PWA install on mobile)
- **[Product Requirements Document](docs/PRD.md)** — Full specification with architecture, data models, API endpoints, workflows, and implementation phases

## License

MIT

## Contributing

Contributions are welcome! Please read the [Contributing Guide](CONTRIBUTING.md) before submitting a pull request.

For security vulnerabilities, please use [GitHub's private vulnerability reporting](https://github.com/CTJaeger/KleverNodeHub/security) instead of opening a public issue. See [SECURITY.md](SECURITY.md) for details.

This project follows the [Contributor Covenant Code of Conduct](CODE_OF_CONDUCT.md).
