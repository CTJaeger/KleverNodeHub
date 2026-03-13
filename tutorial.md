# Klever Node Hub — Complete Guide

> **Self-hosted management dashboard for Klever validator nodes**
> Manage and monitor all your Klever nodes across multiple servers — from any device, anywhere.

<!-- 📸 IMAGE: Screenshot of the main overview dashboard (docs/dash.png) -->

---

## Table of Contents

1. [What is Klever Node Hub?](#what-is-klever-node-hub)
2. [Architecture](#architecture)
3. [Installation](#installation)
   - [Dashboard Setup](#1-dashboard-setup)
   - [Agent Setup](#2-agent-setup)
4. [First Login & Setup Wizard](#first-login--setup-wizard)
5. [Dashboard Overview](#dashboard-overview)
6. [Managing Servers](#managing-servers)
7. [Managing Nodes](#managing-nodes)
8. [Node Provisioning (Install from Scratch)](#node-provisioning-install-from-scratch)
9. [Configuration Management](#configuration-management)
10. [Validator Key Management](#validator-key-management)
11. [Docker Image Upgrades & Downgrades](#docker-image-upgrades--downgrades)
12. [Monitoring & Metrics](#monitoring--metrics)
13. [Alerting](#alerting)
14. [Notifications](#notifications)
15. [Agent Updates](#agent-updates)
16. [Dashboard Self-Update](#dashboard-self-update)
17. [Settings](#settings)
18. [Security Deep Dive](#security-deep-dive)
19. [FAQ](#faq)

---

## What is Klever Node Hub?

Klever Node Hub is a **free, open-source, self-hosted** web dashboard that lets Klever validator operators manage and monitor all their nodes across multiple servers — without SSH sessions and manual bash scripts.

It consists of two components:

- **Dashboard** — A web application you run on one of your servers. This is your control center, accessible from any browser.
- **Agent** — A lightweight process that runs on each server that hosts Klever nodes. It communicates with the dashboard over an encrypted WebSocket connection.

**What it replaces:**

| Before | After |
|--------|-------|
| SSH into each server manually | One dashboard for all servers |
| Run Docker commands by hand | Click buttons to start/stop/restart/upgrade |
| No monitoring unless you set up Prometheus/Grafana | Built-in metrics, charts, and alerting |
| No alerts when a node stalls | Automatic nonce stall detection + Telegram/Pushover notifications |
| Copy config files with scp | Edit configs remotely, push to multiple nodes at once |
| Generate validator keys via command line | Generate/import/export keys from the dashboard |
| Manual Docker image upgrades | One-click upgrades with rollback support |

**What it is NOT:**

- Not a cloud service — everything runs on your own infrastructure
- Not a wallet — it does not handle KLV tokens or transactions
- Not a staking service — it manages the infrastructure, not the staking itself
- Does not require opening additional ports on your node servers (the agent connects outbound to the dashboard)

---

## Architecture

```
Any Device (Browser)
        │
        │ HTTPS + Password/Passkey/Klever Wallet Auth (port 9443)
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

- The **Dashboard** is the brain. It serves the web UI, stores data in SQLite, and orchestrates all operations.
- **Agents** are the hands. They sit on each node server, execute Docker commands, collect metrics, and stream logs.
- Communication between Dashboard and Agents uses **mTLS** (mutual TLS with Ed25519 certificates). No SSH keys are stored. No passwords are shared.
- The Dashboard generates a **Certificate Authority** on first run. When an agent registers, it receives a signed client certificate. All subsequent communication is encrypted and mutually authenticated.

---

## Installation

### 1. Dashboard Setup

The dashboard runs on **one** of your servers (or a dedicated management server). It needs to be reachable by your browser and by all agent servers on port **9443** (configurable).

#### Option A: One-liner Install Script

```bash
curl -sSL https://raw.githubusercontent.com/CTJaeger/KleverNodeHub/main/scripts/install-agent.sh \
  | sudo bash -s -- --token <TOKEN> --dashboard https://<DASHBOARD_IP>:9443
```

#### Option B: Docker (recommended)

```bash
docker run -d \
  -p 9443:9443 \
  -v klever-data:/root/.klever-node-hub \
  --name klever-node-hub \
  ctjaeger/klever-node-hub:latest \
  --domain your-server.example.com
```

The `--domain` flag is optional. It's only required if you want to use **Passkey login** (WebAuthn requires a domain). Password login works fine with just an IP address.

#### Option C: Binary

```bash
# Download the latest release
wget https://github.com/CTJaeger/KleverNodeHub/releases/latest/download/dashboard-linux-amd64

# Make executable
chmod +x dashboard-linux-amd64

# Run
./dashboard-linux-amd64 --domain your-server.example.com
```

#### Option D: Build from Source

```bash
git clone https://github.com/CTJaeger/KleverNodeHub.git
cd KleverNodeHub
make build-linux
scp bin/dashboard-linux-amd64 user@your-server:/opt/klever/klever-node-hub
```

#### Dashboard CLI Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--addr` | `:9443` | Listen address (host:port) |
| `--domain` | `localhost` | Domain for TLS cert and Passkey RP ID |
| `--data-dir` | `~/.klever-node-hub` | Data directory (DB, certs, backups) |
| `--reset-recovery-codes` | — | Generate new recovery codes and exit |

After starting, the dashboard generates a self-signed TLS certificate and is accessible at `https://your-server:9443`.

<!-- 📸 IMAGE: Terminal output showing dashboard startup with "Listening on :9443" -->

---

### 2. Agent Setup

Each server that runs Klever validator/observer nodes needs a lightweight agent. The agent connects **outbound** to your dashboard — no inbound ports need to be opened on the node server.

#### Step 1: Generate a Registration Token

1. Open the dashboard in your browser
2. Click **"Add Server"** on the overview page
3. Click **"Generate Token"**
4. Copy the displayed install command

<!-- 📸 IMAGE: Screenshot of the "Add Server" dialog showing the generated token and install command -->

The token is **one-time use** and expires after **1 hour**.

#### Step 2: Install the Agent

**Option A: Install Script (recommended)**

SSH into your node server and paste the copied command:

```bash
curl -sSL https://raw.githubusercontent.com/CTJaeger/KleverNodeHub/main/scripts/install-agent.sh \
  | sudo bash -s -- --token <YOUR_TOKEN> --dashboard https://<DASHBOARD_IP>:9443
```

The script automatically:
1. Installs Docker if not present
2. Downloads the correct agent binary for your architecture (amd64/arm64)
3. Registers with the dashboard using the one-time token
4. Creates a `klever-agent` systemd service
5. Starts the agent
6. Auto-discovers existing Klever nodes on the server

**Option B: Docker**

```bash
docker run -d \
  -v /var/run/docker.sock:/var/run/docker.sock \
  --name klever-agent \
  ctjaeger/klever-agent:latest \
  --dashboard-url https://<DASHBOARD_IP>:9443 \
  --register-token <YOUR_TOKEN>
```

**Option C: Manual binary**

```bash
wget https://github.com/CTJaeger/KleverNodeHub/releases/latest/download/agent-linux-amd64
chmod +x agent-linux-amd64
./agent-linux-amd64 --dashboard-url https://<DASHBOARD_IP>:9443 --register-token <YOUR_TOKEN>
```

#### Agent CLI Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--config-dir` | `~/.klever-agent` | Config directory (stores mTLS certificate) |
| `--dashboard-url` | — | Dashboard URL (required for first registration) |
| `--register-token` | — | One-time registration token |
| `--docker-socket` | `/var/run/docker.sock` | Docker socket path |

After registration, the agent stores its mTLS certificate locally. On subsequent starts, it reconnects automatically — no token needed.

<!-- 📸 IMAGE: Terminal output showing agent registration success and node discovery -->

#### Repeat for Each Server

Generate a new token for each server you want to add. Each token can only be used once.

---

## First Login & Setup Wizard

When you open `https://your-server:9443` for the first time, you'll see the **Setup Wizard**.

<!-- 📸 IMAGE: Screenshot of the setup wizard — Step 1: Set Password -->

### Step 1: Set a Password

Choose a strong password (minimum 8 characters). This is your primary login method.

### Step 2: Register a Passkey (optional)

If your browser supports WebAuthn (most modern browsers do), you can register a **Passkey**. This allows biometric login (Face ID, Touch ID, Windows Hello) or hardware key login (YubiKey).

<!-- 📸 IMAGE: Screenshot of the Passkey registration prompt -->

Passkeys require a valid domain (not just an IP). If you started the dashboard with `--domain`, this step will be available.

### Step 3: Save Recovery Codes

The wizard generates **8 single-use recovery codes**. These are your fallback if you lose your password and all passkeys.

**Save these codes in a safe place.** Each code can only be used once.

<!-- 📸 IMAGE: Screenshot of the recovery codes display -->

### Step 4: Link Klever Wallet (optional)

If you have the **Klever Extension** browser plugin installed, you can link your Klever wallet address for one-click wallet login. The dashboard verifies your ownership by requesting a signature challenge.

<!-- 📸 IMAGE: Screenshot of the Klever wallet linking step -->

### Login Methods Summary

| Method | Requirements | When to Use |
|--------|-------------|-------------|
| **Password** | Works via IP or domain | Default, always available |
| **Passkey** | Requires domain + WebAuthn-capable browser | Fast biometric login |
| **Klever Extension** | Requires browser extension + linked wallet | One-click wallet login |
| **Recovery Code** | Single-use codes from setup | Emergency access |

---

## Dashboard Overview

The overview page is your main control center. It shows all your servers and nodes at a glance.

<!-- 📸 IMAGE: Full screenshot of the overview page with servers and nodes visible -->

### Server List

Each server appears as a row showing:
- **Server name** and hostname
- **Agent version** currently running
- **Node count** on that server
- **CPU / Memory / Disk** utilization bars (live)
- **Status** — online (green) or offline (grey)
- **Actions** menu — Details, Delete

<!-- 📸 IMAGE: Close-up of the server list table with metric bars -->

### Node List

Below the servers, all nodes are listed in a searchable, filterable data table:
- **Node name** and server association
- **Status** — running, syncing, stopped, error
- **Type** — validator or observer
- **Docker image tag** currently running
- **BLS public key** (truncated)

You can filter by status or type using the dropdown filters, and search by any field.

<!-- 📸 IMAGE: Close-up of the node list with filters active -->

### Active Alerts Banner

If any alerts are currently firing (e.g., a node stalled, high CPU), a banner appears at the top of the overview page showing the count and severity.

<!-- 📸 IMAGE: Screenshot showing the alerts banner (if available) -->

---

## Managing Servers

### Adding a Server

1. Click **"Add Server"** on the overview page
2. Click **"Generate Token"** to create a one-time registration token
3. Copy the install command and run it on your node server
4. The server appears in the list as soon as the agent connects

<!-- 📸 IMAGE: Screenshot of the "Add Server" modal with token generated -->

### Server Detail Page

Click on a server row to see its detail page:
- **System info** — hostname, IP, public IP, region (auto-detected via GeoIP), OS
- **System metrics** — CPU, memory, disk utilization with visual gauges
- **Agent version** currently running
- **Nodes** hosted on this server

<!-- 📸 IMAGE: Screenshot of the server detail page -->

### Removing a Server

Open the server's action menu (⋯ button) and click **"Delete server"**. This removes the server from the dashboard. The agent on the server will stop connecting.

---

## Managing Nodes

### Node Detail Page

Click on any node to see its detail page with comprehensive information:

<!-- 📸 IMAGE: Full screenshot of the node detail page -->

#### Status Header
- Node name, server, current status
- Sync progress (if syncing)

#### Blockchain Metrics
- **Nonce** — current block nonce
- **Epoch & Slot** — current epoch and slot position
- **Finalized nonce** — last finalized block
- **Consensus state** — participation status
- **Peers** — connected peer count
- **TPS** — transactions per second

<!-- 📸 IMAGE: Close-up of the blockchain metrics section -->

#### Resource Charts
- **CPU usage** over time (time-series chart)
- **Memory usage** over time
- **Disk usage** over time
- **Network** — recv/sent bandwidth sparklines

<!-- 📸 IMAGE: Close-up of the resource charts -->

#### Node Actions

The node detail page provides buttons for:
- **Start / Stop / Restart** — container lifecycle
- **Upgrade / Downgrade** — change Docker image version
- **Delete** — remove the node entirely

### Batch Operations

From the overview page, you can select multiple nodes using checkboxes and apply batch operations:
- **Batch Start** — start multiple stopped nodes
- **Batch Stop** — stop multiple running nodes
- **Batch Restart** — restart multiple nodes
- **Batch Upgrade** — upgrade multiple nodes to a specific Docker image tag

<!-- 📸 IMAGE: Screenshot showing multiple nodes selected with batch action buttons -->

---

## Node Provisioning (Install from Scratch)

You can provision a **brand new Klever node** directly from the dashboard — no SSH required.

1. Navigate to a server's detail page
2. Click **"Provision Node"**
3. Select the node type (validator/observer) and network (mainnet/testnet)
4. Click **"Provision"**

The agent executes a 7-step process:
1. **Preflight** — checks Docker, disk space, connectivity
2. **Pull** — downloads the Klever Docker image
3. **Directories** — creates data directories
4. **Config download** — fetches initial config from official Klever backup servers
5. **Container create** — creates the Docker container with correct volume mounts
6. **Start** — starts the container
7. **Verify** — confirms the node is running and responding

Progress is reported in real-time. If any step fails, the agent cleans up automatically.

<!-- 📸 IMAGE: Screenshot of the provisioning progress dialog -->

---

## Configuration Management

### Viewing & Editing Config Files

1. Open a node's detail page
2. Navigate to the **Config** section
3. Select a config file from the list (e.g., `config.toml`)
4. The file content is displayed in an editor
5. Make changes and click **"Save"**

<!-- 📸 IMAGE: Screenshot of the config editor with a file open -->

Every save automatically creates a **backup** of the previous version. You can view and restore backups from the backup list.

### Config Push (Multi-Node)

Push a config file to multiple nodes at once:

1. Edit a config file on one node
2. Click **"Push to Other Nodes"**
3. Select which nodes should receive this config
4. Confirm

This is useful when you want all your validators to use the same configuration.

<!-- 📸 IMAGE: Screenshot of the config push dialog with node selection -->

### Config Backups & Restore

- Every config save creates an automatic backup
- View all backups for a file with timestamps
- Restore any previous version with one click

### Config Version Upgrade

When upgrading a node to a new Docker image, the config format may change. The dashboard can:
1. Download a fresh config from the official Klever backup servers
2. Create a versioned backup of your current config
3. Apply the new config

You can restore to any previous version at any time.

---

## Validator Key Management

### Generate a New Key

1. Open a node's detail page
2. Go to the **Keys** section
3. Click **"Generate Key"**
4. A new BLS validator key is generated inside the node's container
5. The public key is displayed

<!-- 📸 IMAGE: Screenshot of the key management section showing BLS public key -->

### Import an Existing Key

If you already have a BLS key pair (PEM file), you can import it:
1. Click **"Import Key"**
2. Paste the PEM content
3. The key is written to the node's key directory

### Export a Key

To backup or migrate a key:
1. Click **"Export Key"**
2. The PEM content is displayed / downloaded

### Key Backups

Key operations automatically create backups. You can view the backup list and restore if needed.

> **Security note:** Keys are transferred over the encrypted mTLS WebSocket connection. They are never stored in the dashboard database — only on the node server's filesystem.

---

## Docker Image Upgrades & Downgrades

### Upgrade a Node

1. Open a node's detail page
2. Click **"Upgrade"**
3. Select the target Docker image tag from the dropdown (tags are fetched from Docker Hub and cached for 15 minutes)
4. Confirm

<!-- 📸 IMAGE: Screenshot of the upgrade dialog with tag selector -->

The agent:
1. Pulls the new image
2. Stops the current container
3. Creates a new container with the same configuration but the new image
4. Starts the new container
5. Verifies it's running

If the upgrade fails, the agent rolls back to the previous image.

### Downgrade a Node

Same process as upgrade, but select an older tag. Useful for rolling back problematic releases.

### Batch Upgrade

From the overview page:
1. Select multiple nodes using checkboxes
2. Click **"Batch Upgrade"**
3. Select the target tag
4. All selected nodes are upgraded sequentially

---

## Monitoring & Metrics

### Real-Time Metrics

The dashboard collects metrics from all nodes every **15 seconds**:

**System metrics** (per server):
- CPU usage %
- Memory usage (used / total)
- Disk usage (used / total)
- Network bandwidth (recv / sent)

**Klever node metrics** (per node, from `/node/status`):
- Nonce, epoch, slot, finalized nonce
- Sync status and progress
- Connected peers
- Consensus state and participation count
- TPS (average and peak)
- Transaction pool load
- App version

### Historical Data

Metrics are stored in two tiers:

| Tier | Resolution | Retention |
|------|-----------|-----------|
| **Hot** (recent) | Every 10 seconds | 7 days |
| **Cold** (archive) | 5-minute averages | 90 days |

An automatic background job aggregates hot data into cold storage and purges old records. This happens daily.

### Charts

The node detail page shows time-series charts for CPU, memory, disk, and network. These are rendered with a custom lightweight chart library — no external dependencies.

<!-- 📸 IMAGE: Screenshot of the time-series charts on the node detail page -->

### GeoIP Detection

When an agent connects, it reports its public IP. The dashboard resolves this to a geographic region (e.g., "Frankfurt", "Singapore") and displays it on the server card.

---

## Alerting

### How Alerts Work

The alert engine evaluates all rules every **15 seconds**. When a condition is met for the configured duration, an alert fires. When the condition clears, the alert resolves.

**State machine:** Normal → Pending (condition met) → Firing (duration threshold exceeded) → Resolved (condition cleared)

### Default Alert Rules

The dashboard comes with 7 pre-configured alert rules:

| Alert | Condition | Severity | Default Threshold |
|-------|-----------|----------|-------------------|
| **Nonce Stall** | Node nonce hasn't incremented | Critical | 16 seconds (4 missed slots) |
| **Node Down** | Container not running | Critical | 60 seconds |
| **High CPU** | CPU usage exceeds threshold | Warning | 90% |
| **High Memory** | Memory usage exceeds threshold | Warning | 90% |
| **Disk Full** | Disk usage exceeds threshold | Critical | 85% |
| **Low Peers** | Connected peers below threshold | Warning | 5 peers |
| **Sync Lag** | Blocks behind exceeds threshold | Warning | 100 blocks |

### Nonce Stall Detection

This is one of the most important alerts for validators. The agent tracks each node's nonce. If the nonce hasn't changed for 16+ seconds (meaning the node hasn't produced or finalized a block in 4 slot cycles), a **critical** alert fires immediately.

### Custom Alert Rules

You can customize existing rules or create new ones:

1. Go to the **Alerts** page
2. Click on a rule to edit it
3. Adjust the **threshold**, **cooldown period**, or **duration**
4. Choose which **notification channels** should receive this alert

<!-- 📸 IMAGE: Screenshot of the alerts page with rules list -->

### Alert History

Every alert event (firing and resolved) is logged with:
- Timestamp
- Severity
- Affected node/server
- Which notification channels were notified
- Resolution time

<!-- 📸 IMAGE: Screenshot of the alert history table -->

### Acknowledging Alerts

Click **"Acknowledge"** on an active alert to silence it. This does not resolve the alert — it just marks it as seen.

---

## Notifications

### Supported Channels

| Channel | Description |
|---------|-------------|
| **Telegram** | Send alerts to a Telegram bot. Supports Markdown formatting. |
| **Pushover** | Push notifications to any device via the Pushover app. Maps critical alerts to emergency priority. |
| **Webhook** | Send alerts as HTTP POST to any URL. Supports custom headers. Retry logic with exponential backoff. |
| **Web Push** | Browser push notifications. Works even when the dashboard tab is closed. |

### Setting Up Telegram

1. Go to **Settings → Notifications**
2. Click **"Add Channel"**
3. Select **Telegram**
4. Enter your **Bot Token** (from @BotFather) and **Chat ID**
5. Click **"Test"** to verify
6. Save

<!-- 📸 IMAGE: Screenshot of the Telegram channel setup form -->

### Setting Up Pushover

1. Add a new Pushover channel
2. Enter your **User Key** and **API Token** (from pushover.net)
3. Test and save

### Setting Up a Webhook

1. Add a new Webhook channel
2. Enter the **URL** and optional custom **headers**
3. Test and save

The webhook sends a JSON POST with alert details (type, severity, message, node, server, timestamp).

### Setting Up Web Push

1. Go to **Settings → Notifications**
2. Toggle **"Browser Push Notifications"** on
3. Allow the browser notification permission when prompted
4. Click **"Test"** to verify

<!-- 📸 IMAGE: Screenshot of the Web Push toggle in settings -->

### Per-Channel Filtering

Each channel can be configured to only receive specific alerts:

- **By severity:** info, warning, critical
- **By alert type:** node_down, nonce_stall, resource, metric, resolved

For example, you might send all critical alerts to Telegram but only nonce stalls to Pushover.

<!-- 📸 IMAGE: Screenshot of the channel filter configuration -->

### Notification History

The dashboard keeps a history of all sent notifications. View it under **Settings → Notifications → History**.

---

## Agent Updates

The dashboard can update agents remotely — no SSH required.

### Download a Release

1. Click **"Agent Update"** in the sidebar
2. Select a version from the **"Available Versions"** dropdown (fetched from GitHub Releases)
3. Click **"Download"**

<!-- 📸 IMAGE: Screenshot of the Agent Update modal showing the version dropdown and download button -->

The dashboard automatically downloads only the binaries needed for your registered servers' architectures. For example, if all your servers are `linux/amd64`, only the amd64 binary is downloaded.

### Update Agents

1. In the **"Server Agents"** table, select the agents you want to update using the checkboxes
2. Choose the target version from the dropdown in the action bar
3. Click **"Update"**
4. Confirm the update

<!-- 📸 IMAGE: Screenshot of the Agent Update modal with agents selected and version chosen -->

Each agent shows its update progress inline:
- ⏳ **Waiting...** — queued for update
- **Updating...** — binary being transferred
- **Restarting...** — agent is restarting with the new version
- ✅ **v0.x.x** — successfully updated
- ❌ **Failed** — update failed

The dashboard sends the binary over the WebSocket connection. The agent:
1. Verifies the SHA-256 checksum
2. Creates a backup of the current binary
3. Replaces the binary atomically
4. Restarts itself

The dashboard waits up to 15 seconds for the agent to reconnect with the new version before marking the update as successful.

### Rollback

If an update causes issues, you can update to a previous version — all downloaded versions remain available in the dropdown.

---

## Dashboard Self-Update

The dashboard can update itself:

1. Go to **Settings** or check the version indicator in the sidebar
2. Click **"Check for Updates"**
3. If a new version is available, click **"Update"**
4. The dashboard downloads the new binary, replaces itself, and restarts

> **Note:** When running in Docker, use `docker pull` instead to update the container image.

---

## Settings

The settings page is organized into tabs:

<!-- 📸 IMAGE: Full screenshot of the settings page -->

### General
- Dashboard display name

### Metrics
- Collection intervals
- Hot data retention window (default: 7 days)
- Archive retention window (default: 90 days)

### Notifications
- Manage notification channels (Telegram, Pushover, Webhook)
- Per-channel alert type and severity filters
- Test buttons
- Notification history

### Agents
- Heartbeat timeout
- Discovery interval

### Security (within Settings)
- **Change password**
- **Manage passkeys** — add or remove WebAuthn passkeys
- **Link/unlink Klever wallet**
- **Push notifications** — enable/disable browser push
- **Reset to defaults** — restore all settings

---

## Security Deep Dive

### Authentication

| Layer | Technology |
|-------|-----------|
| Password | Argon2id (64 MB memory, 3 iterations, 4 parallelism) |
| Passkey | WebAuthn / FIDO2 (biometric, hardware key) |
| Klever Extension | Ed25519 challenge-response signature |
| Recovery | 8 single-use codes (Argon2id hashed) |
| Sessions | JWT with 15-minute access tokens + refresh rotation |

### Rate Limiting

Login attempts are limited to **5 per 15 minutes per IP address**. After that, you receive HTTP 429 (Too Many Requests).

### Agent Communication

- **mTLS** — mutual TLS with Ed25519 certificates
- The dashboard acts as a Certificate Authority
- Each agent receives a signed client certificate during registration
- No SSH keys are stored anywhere
- All communication is encrypted (TLS 1.3)

### Agent Command Whitelist

Agents only execute **predefined commands** (start, stop, restart, upgrade, config read/write, key operations, etc.). There is no shell access or arbitrary command execution. This limits the blast radius even if the dashboard is compromised.

### Config Encryption at Rest

Configuration files stored in the database are encrypted with **AES-256-GCM**. The encryption key is stored in the settings database and protected by the system.

### Path Traversal Prevention

Config and key operations validate file paths to prevent directory traversal attacks. Only files within the expected directories can be read or written.

### Security Headers

The dashboard sets standard security headers:
- Content-Security-Policy
- X-Frame-Options (DENY)
- HSTS (Strict-Transport-Security)
- X-XSS-Protection

### Open Source Security

The code is open source under the MIT license. Security follows [Kerckhoffs's principle](https://en.wikipedia.org/wiki/Kerckhoffs%27s_principle) — knowing the source code does not help an attacker without the encryption keys. No security through obscurity.

For security vulnerabilities, use [GitHub's private vulnerability reporting](https://github.com/CTJaeger/KleverNodeHub/security).

---

## FAQ

### Do I need to open ports on my node servers?

**No.** The agent connects **outbound** to the dashboard. Only the dashboard needs an open port (default 9443). Your node servers don't need any additional inbound ports.

### Does it work with existing nodes?

**Yes.** When you install the agent on a server that already runs Klever nodes, the agent **auto-discovers** all existing `kleverapp/klever-go` Docker containers. They appear in the dashboard immediately.

### Can I run the dashboard in Docker?

**Yes.** Both the dashboard and agent are available as Docker images on Docker Hub:
- `ctjaeger/klever-node-hub:latest`
- `ctjaeger/klever-agent:latest`

### What if my dashboard goes down?

Your Klever nodes continue running normally. The dashboard is a management layer — it does not affect node operation. When the dashboard comes back, agents reconnect automatically.

### Can multiple people use the dashboard?

Currently, the dashboard supports a single admin account. Multiple people can be logged in simultaneously using the same credentials.

### What architectures are supported?

- **linux/amd64** (x86_64) — standard servers
- **linux/arm64** (aarch64) — ARM servers (e.g., Raspberry Pi, Ampere, Graviton)

### Is there a mobile app?

The dashboard is a **Progressive Web App (PWA)**. You can install it on your phone's home screen from the browser. It works like a native app with push notifications.

### How much resources does the agent use?

The agent is extremely lightweight — typically under 20 MB memory and negligible CPU. It's a single Go binary with no dependencies.

### Where is data stored?

- **Dashboard:** SQLite database in `~/.klever-node-hub/` (configurable with `--data-dir`)
- **Agent:** mTLS certificate in `~/.klever-agent/` (configurable with `--config-dir`)

### Can I use this for testnet nodes?

**Yes.** The dashboard works with both mainnet and testnet nodes. When provisioning, you select the network.

---

## Links

- **GitHub:** [github.com/CTJaeger/KleverNodeHub](https://github.com/CTJaeger/KleverNodeHub)
- **Docker Hub:** [hub.docker.com/r/ctjaeger/klever-node-hub](https://hub.docker.com/r/ctjaeger/klever-node-hub)
- **Issues & Feature Requests:** [GitHub Issues](https://github.com/CTJaeger/KleverNodeHub/issues)
- **License:** MIT

---

*Klever Node Hub is an independent community project and is not affiliated with or endorsed by Klever.*
