# Klever Node Hub — Product Requirements Document

> **Version**: 1.0
> **Date**: 2026-03-11
> **Author**: Fernando Sobreira
> **Status**: Draft

---

## 1. Vision & Purpose

Klever Node Hub is a **self-hosted, single-binary web dashboard** that lets Klever validator operators manage, monitor, and control all their nodes across multiple servers — from any device, anywhere in the world.

It replaces manual SSH sessions and bash scripts (like [KleverNodeManagement](https://github.com/CTJaeger/KleverNodeManagement)) with a secure, centralized web interface that communicates with lightweight agents deployed on each server.

### Goals

- **Zero SSH**: Manage all nodes without ever logging into a server
- **Full lifecycle**: Install, configure, start, stop, upgrade, downgrade nodes remotely
- **Real-time visibility**: Live metrics, sync status, and logs from every node
- **Historical analytics**: Persistent metrics stored in a database for trend analysis
- **Proactive alerts**: Instant notifications via Telegram, Pushover, and extensible channels
- **Security-first**: mTLS, encryption at rest, no shell access on agents

### Non-Goals (v1)

- Multi-user / RBAC (single operator for now)
- Proxy nodes with ElasticSearch (future expansion)
- High availability for the dashboard itself
- Mobile native app (responsive web UI is sufficient)

---

## 2. Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                        User (Browser)                       │
│              Any device — phone, tablet, laptop             │
└────────────────────────┬────────────────────────────────────┘
                         │ HTTPS + Passkey 2FA
                         ▼
┌─────────────────────────────────────────────────────────────┐
│                     DASHBOARD SERVER                        │
│                                                             │
│  ┌──────────┐  ┌──────────┐  ┌───────────┐  ┌───────────┐ │
│  │ Web UI   │  │ REST API │  │ WebSocket │  │ Scheduler │ │
│  │(embedded)│  │          │  │   Hub     │  │           │ │
│  └──────────┘  └──────────┘  └───────────┘  └───────────┘ │
│  ┌──────────┐  ┌──────────┐  ┌───────────┐  ┌───────────┐ │
│  │ Auth     │  │ Crypto   │  │ Notifier  │  │ Metrics   │ │
│  │ Module   │  │ Module   │  │ Engine    │  │ Collector │ │
│  └──────────┘  └──────────┘  └───────────┘  └───────────┘ │
│  ┌──────────────────────────────────────────────────────┐  │
│  │              SQLite (encrypted at rest)               │  │
│  └──────────────────────────────────────────────────────┘  │
└────────────────────────┬────────────────────────────────────┘
                         │ mTLS (Ed25519 certificates)
            ┌────────────┼────────────┐
            ▼            ▼            ▼
┌──────────────┐ ┌──────────────┐ ┌──────────────┐
│   AGENT 1    │ │   AGENT 2    │ │   AGENT N    │
│              │ │              │ │              │
│ ┌──────────┐ │ │ ┌──────────┐ │ │ ┌──────────┐ │
│ │ Executor │ │ │ │ Executor │ │ │ │ Executor │ │
│ │ (Docker) │ │ │ │ (Docker) │ │ │ │ (Docker) │ │
│ ├──────────┤ │ │ ├──────────┤ │ │ ├──────────┤ │
│ │ Metrics  │ │ │ │ Metrics  │ │ │ │ Metrics  │ │
│ │ Reporter │ │ │ │ Reporter │ │ │ │ Reporter │ │
│ ├──────────┤ │ │ ├──────────┤ │ │ ├──────────┤ │
│ │ Config   │ │ │ │ Config   │ │ │ │ Config   │ │
│ │ Manager  │ │ │ │ Manager  │ │ │ │ Manager  │ │
│ ├──────────┤ │ │ ├──────────┤ │ │ ├──────────┤ │
│ │ Log      │ │ │ │ Log      │ │ │ │ Log      │ │
│ │ Streamer │ │ │ │ Streamer │ │ │ │ Streamer │ │
│ └──────────┘ │ │ └──────────┘ │ │ └──────────┘ │
│              │ │              │ │              │
│ [Node 1..M] │ │ [Node 1..M] │ │ [Node 1..M] │
└──────────────┘ └──────────────┘ └──────────────┘
```

### Component Responsibilities

| Component | Description |
|---|---|
| **Dashboard** | Go binary with embedded web UI. Central control plane. Manages all agents and serves the user interface. |
| **Agent** | Lightweight Go binary on each server. Executes whitelisted commands against Docker and the local filesystem. Reports metrics back to dashboard. |
| **WebSocket Hub** | Persistent bidirectional connection between dashboard and each agent. Carries commands, metrics, and log streams. |
| **Scheduler** | Background jobs: metrics aggregation, data decimation, alert evaluation, agent health checks. |
| **Notifier Engine** | Dispatches alerts to configured channels (Telegram, Pushover, etc.) based on user-defined rules. |
| **Metrics Collector** | Receives and stores agent metrics in SQLite. Manages hot/cold data lifecycle. |

### Key Architectural Decisions

- **Single binary per component** — no runtime dependencies, easy deployment
- **Embedded web UI** — HTML/CSS/JS served from the Go binary via `embed.FS`
- **SQLite** — no external database server; encrypted at rest with AES-256-GCM
- **mTLS** — mutual certificate authentication between dashboard and agents; no SSH keys
- **Command whitelist** — agents only execute predefined operations (no shell access)
- **stdlib-only** — Go standard library + `golang.org/x/crypto` only; minimal attack surface

---

## 3. Security Model

### Authentication & Authorization

| Layer | Technology | Details |
|---|---|---|
| Dashboard login | WebAuthn Passkey (passwordless) | Single-step login via Passkey — biometric (Face ID, Touch ID, Windows Hello) or hardware key (YubiKey). No password needed. Cryptographic challenge-response, phishing-resistant. |
| Recovery access | One-time recovery codes | 8 single-use codes generated at setup. Stored as Argon2id hashes. For account recovery if all passkey devices are lost. |
| Session management | JWT (HS256) | Short-lived tokens (15min) with refresh rotation. Stored in httpOnly secure cookies. |
| Agent auth | mTLS | Ed25519 certificates. Dashboard is CA. Agents present client certs on every connection. |

#### Passkey Details

- **Multiple passkeys**: User can register multiple devices (phone, laptop, hardware key) for redundancy
- **Recovery codes**: 8 single-use codes generated during setup, each usable exactly once. Displayed once, user must save them. Hashed with Argon2id before storage.
- **Login flow**: Browser calls `navigator.credentials.get()` → device biometric/PIN → server verifies signature → JWT issued
- **Recovery flow**: If no passkey device available, user enters a recovery code → new passkey registration is required immediately after
- **No passwords**: Eliminates phishing, credential stuffing, and password reuse attacks entirely

### Encryption

| At rest | In transit |
|---|---|
| SQLite database encrypted with AES-256-GCM | All agent communication over mTLS (WebSocket + TLS 1.3) |
| Config files encrypted before storage | Dashboard UI served over HTTPS |
| Validator PEM files encrypted in database | |

### Agent Security

- **Command whitelist**: Agents only accept a predefined set of operations (see Section 7)
- **No shell access**: The agent never spawns a shell or executes arbitrary commands
- **Read-only filesystem access**: Agent can only read/write within the node directories it manages
- **Auto-update**: Dashboard pushes signed agent binaries; agent verifies signature before applying

### Certificate Lifecycle

1. Dashboard generates its own CA on first setup
2. Agent registration produces a one-time token
3. Agent presents token to dashboard, receives signed client certificate
4. Certificate is stored on agent; dashboard stores the agent's public key
5. All subsequent communication requires mutual TLS

---

## 4. Features

### 4.1 Agent Registration & Setup

**Purpose**: Onboard new servers with minimal effort.

#### Registration Flow

```
Dashboard                           Server
   │                                   │
   ├── Generate one-time token ────────┤
   │                                   │
   │   curl install-agent.sh | bash    │
   │                                   │
   │◄── Agent presents token ──────────┤
   │                                   │
   ├── Validate token ─────────────────┤
   ├── Issue mTLS certificate ─────────┤
   ├── Send agent config ──────────────┤
   │                                   │
   │◄── Agent connects via WebSocket ──┤
   │                                   │
   ├── Discover existing nodes ────────┤
   │◄── Report found nodes ───────────┤
   │                                   │
   └── Agent ready ────────────────────┘
```

#### Auto-Discovery

When an agent first connects, it scans the server for existing Klever nodes:
- Searches running Docker containers matching `kleverapp/klever-go`
- Extracts mount paths, ports, redundancy level, display name
- Reports all found nodes to the dashboard for registration

#### Agent Auto-Update

- Dashboard stores the latest agent binary
- On version mismatch, dashboard pushes the new binary to the agent
- Agent verifies the binary signature, replaces itself, and restarts
- Rollback if the new agent fails to connect within a timeout

### 4.2 Node Provisioning (Install from Scratch)

**Purpose**: Deploy new Klever nodes on a server without SSH.

#### Provisioning Steps (executed by agent)

1. **System prerequisites**: Install Docker, jq, and required tools (apt-based)
2. **Pull Docker image**: `kleverapp/klever-go:<tag>` — tag selected from dashboard
3. **Create directory structure**:
   ```
   /opt/node<N>/
   ├── config/    # Node configuration files
   ├── db/        # Blockchain data
   ├── logs/      # Node logs
   └── wallet/    # Validator keys
   ```
4. **Download configuration**: From `backup.mainnet.klever.org` (primary) or GitHub fallback
5. **Set permissions**: `chown -R 999:999` (Docker container user)
6. **Generate or import validator keys**: BLS key generation via `keygenerator` entrypoint, or import existing `validatorKey.pem`
7. **Start container**:
   ```
   docker run -d \
     --restart unless-stopped \
     --user 999:999 \
     --name klever-node<N> \
     -v /opt/node<N>/config:/opt/klever-blockchain/config/node \
     -v /opt/node<N>/db:/opt/klever-blockchain/db \
     -v /opt/node<N>/logs:/opt/klever-blockchain/logs \
     -v /opt/node<N>/wallet:/opt/klever-blockchain/wallet \
     --network=host \
     --entrypoint=/usr/local/bin/validator \
     kleverapp/klever-go:<tag> \
     --log-save --use-log-view \
     --rest-api-interface=0.0.0.0:<port> \
     --display-name=<name> \
     --start-in-epoch \
     [--redundancy-level=<0|1>]
   ```

#### Port Management

- First node starts at port `8080`
- Each additional node increments: `8081`, `8082`, ...
- Agent checks port availability before assignment
- Dashboard tracks all port allocations

#### Node Types

| Type | Redundancy Level | Description |
|---|---|---|
| Validator (active) | `0` (default) | Active block-producing validator |
| Validator (fallback) | `1` | Backup validator; takes over if primary fails |
| Full node / Observer | `0` | Syncs blockchain but doesn't validate (same Docker image) |

### 4.3 Node Lifecycle Management

**Purpose**: Full control over running nodes from the dashboard.

#### Operations

| Operation | Description | Agent Action |
|---|---|---|
| **Start** | Start a stopped node | `docker start klever-node<N>` |
| **Stop** | Gracefully stop a node | `docker stop klever-node<N>` |
| **Restart** | Restart a running node | `docker restart klever-node<N>` |
| **Upgrade** | Change to a newer Docker image tag | Stop → remove container → pull new image → recreate container with same volumes |
| **Downgrade** | Revert to an older Docker image tag | Same as upgrade but with an older tag |
| **Delete** | Remove a node completely | Stop → remove container → optionally remove data directory |
| **Batch operations** | Apply an operation to multiple nodes | Execute on selected nodes (or all) simultaneously |

#### Docker Image Tag Management

- Dashboard fetches available tags from Docker Hub: `kleverapp/klever-go`
- Filters out dev/testnet/devnet/alpine/val-only tags
- Shows current running version vs available version
- Supports selecting specific tags for upgrade/downgrade
- Stores previous tag for quick rollback

### 4.4 Configuration Management

**Purpose**: View, edit, and push node configuration files remotely.

#### Config Files Per Node

| File | Format | Purpose |
|---|---|---|
| `config.yaml` | YAML | Main node configuration (P2P, NTP, antiflood, storage, consensus) |
| `api.yaml` | YAML | API route access control and credentials |
| `enableEpochs.yaml` | YAML | Feature flags by epoch |
| `external.yaml` | YAML | External integrations (ElasticSearch) |
| `gasScheduleV1.yaml` | YAML | Gas pricing schedule |
| `genesis.json` | JSON | Genesis block data |
| `nodesSetup.json` | JSON | Network bootstrap configuration |

#### Config Operations

| Operation | Description |
|---|---|
| **View** | Load a config file from a node and display in web editor |
| **Edit & Save** | Edit in browser with YAML/JSON syntax highlighting, save back to node |
| **Push centralized config** | Select a config file, pick target nodes, push to all selected at once |
| **Diff** | Compare config between two nodes or between current and a saved version |
| **Backup** | Auto-backup config before any modification (stored in dashboard DB) |
| **Download fresh config** | Pull latest official config from Klever backup servers |

#### Validator Key Management

| Operation | Description |
|---|---|
| **Generate key** | Create new BLS validator key pair via Docker `keygenerator` |
| **Import PEM** | Upload a `validatorKey.pem` to a node's config directory |
| **Export PEM** | Download a node's `validatorKey.pem` (encrypted in transit) |
| **View BLS public key** | Extract and display the BLS public key from PEM for validator registration |

### 4.5 Monitoring & Metrics

**Purpose**: Real-time and historical visibility into node and server health.

#### System Metrics (from agent)

Collected by agent at configurable intervals (default: 10s).

| Metric | Source | Description |
|---|---|---|
| `cpu_load_percent` | `/proc/stat` or equivalent | CPU usage percentage |
| `mem_used_bytes` | `/proc/meminfo` | Memory usage in bytes |
| `mem_total_bytes` | `/proc/meminfo` | Total memory |
| `mem_load_percent` | Calculated | Memory usage percentage |
| `disk_used_bytes` | `statfs` | Disk usage for node data directories |
| `disk_total_bytes` | `statfs` | Total disk space |
| `disk_load_percent` | Calculated | Disk usage percentage |
| `network_recv_bps` | `/proc/net/dev` | Network receive bytes per second |
| `network_sent_bps` | `/proc/net/dev` | Network send bytes per second |
| `docker_status` | Docker API | Container running/stopped/not found |
| `docker_uptime` | Docker API | Container uptime |
| `docker_image_tag` | Docker API | Current Docker image tag |

#### Klever Node Metrics (from node REST API)

Collected by agent by querying `http://localhost:<port>/node/status` on each node.

| Metric | Type | Description |
|---|---|---|
| `klv_app_version` | string | Node software version |
| `klv_chain_id` | string | Chain identifier |
| `klv_nonce` | int | Current block nonce |
| `klv_highest_final_nonce` | int | Highest finalized nonce |
| `klv_probable_highest_nonce` | int | Network's probable highest nonce |
| `klv_is_syncing` | int | 0 = synced, 1 = syncing |
| `klv_epoch_number` | int | Current epoch |
| `klv_current_slot` | int | Current slot number |
| `klv_connected_nodes` | int | Number of connected nodes |
| `klv_num_connected_peers` | int | Number of connected peers |
| `klv_node_type` | string | `validator` or `observer` |
| `klv_peer_type` | string | `eligible`, `waiting`, `observer` |
| `klv_consensus_state` | string | Consensus participation status |
| `klv_consensus_slot_state` | string | `signed`, `not signed`, etc. |
| `klv_count_consensus` | int | Times participated in consensus |
| `klv_count_leader` | int | Times acted as block proposer |
| `klv_count_accepted_blocks` | int | Blocks accepted |
| `klv_count_consensus_accepted_blocks` | int | Consensus blocks accepted |
| `klv_live_validator_nodes` | int | Active validators in network |
| `klv_num_validators` | int | Total validators |
| `klv_redundancy_level` | int | Redundancy configuration |
| `klv_cpu_load_percent` | int | CPU from node's perspective |
| `klv_mem_load_percent` | int | Memory from node's perspective |
| `klv_mem_heap_inuse` | int | Go heap memory in use |
| `klv_mem_total` | int | Total system memory |
| `klv_network_recv_bps` | int | Network receive bps |
| `klv_network_sent_bps` | int | Network send bps |
| `klv_network_recv_bps_peak` | int | Peak receive bps |
| `klv_network_sent_bps_peak` | int | Peak send bps |
| `klv_average_tps` | int | Average transactions per second |
| `klv_peak_tps` | int | Peak TPS |
| `klv_tx_pool_load` | int | Transaction pool size |
| `klv_node_display_name` | string | Node display name |
| `klv_public_key_block_sign` | string | BLS public key |
| `klv_latest_tag_software_version` | string | Latest available software version |
| `klv_slot_duration` | int | Slot duration in ms |
| `klv_slots_per_epoch` | int | Slots per epoch |
| `klv_start_time` | int | Node start timestamp |

#### Derived / Computed Metrics

| Metric | Calculation | Purpose |
|---|---|---|
| `sync_progress_percent` | `nonce / probable_highest_nonce * 100` | How far along sync is |
| `blocks_behind` | `probable_highest_nonce - nonce` | How many blocks behind network |
| `version_up_to_date` | `app_version == latest_tag_software_version` | Whether node needs update |
| `consensus_participation_rate` | `count_consensus / expected_consensus * 100` | Validator effectiveness |

#### Data Retention Strategy

```
┌─────────────────────────────────────────────────────┐
│                    HOT DATA                         │
│            metrics_recent table                     │
│         Full resolution (10s intervals)             │
│              Last 7 days                            │
└───────────────────┬─────────────────────────────────┘
                    │ Scheduled job (daily at 03:00)
                    │ Aggregates to 5-minute averages
                    ▼
┌─────────────────────────────────────────────────────┐
│                   COLD DATA                         │
│            metrics_archive table                    │
│         5-minute averaged resolution                │
│              Kept indefinitely                      │
└─────────────────────────────────────────────────────┘
```

- **Hot table** (`metrics_recent`): Raw 10s data, last 7 days
- **Cold table** (`metrics_archive`): 5-minute averages, indefinite retention
- **Decimation job**: Runs daily, aggregates hot data older than 7 days into cold table, then deletes the raw records
- **Configurable**: Retention period and aggregation interval are configurable

### 4.6 Dashboard UI

**Purpose**: Responsive, mobile-first web interface for monitoring and control.

#### Pages & Views

| Page | Description |
|---|---|
| **Login** | Passkey authentication (biometric/hardware key) with recovery code fallback |
| **Overview** | Grid/list of all servers and nodes with at-a-glance status (synced, syncing, stopped, error) |
| **Server Detail** | System metrics for a specific server + list of its nodes |
| **Node Detail** | Full node metrics, charts, configuration, logs |
| **Node Provisioning** | Wizard to create new nodes (select server, image tag, redundancy, key generation) |
| **Config Editor** | YAML/JSON editor with syntax highlighting for node config files |
| **Config Push** | Select config file → pick target nodes → push |
| **Logs Viewer** | Live Docker log streaming with basic text filtering |
| **Alerts Config** | Configure notification channels and alert rules |
| **Settings** | Dashboard settings, agent management, metrics interval, retention |

#### Overview Dashboard Widgets

| Widget | Content |
|---|---|
| **Total Nodes** | Count of all nodes by status (running / stopped / syncing / error) |
| **Network Health** | Average sync progress, total peers, consensus participation |
| **Alerts** | Recent alerts with severity indicators |
| **Server Map** | List of servers with node count, CPU/memory/disk summary |
| **Version Status** | Nodes needing updates (current vs latest tag) |

#### Node Detail View

| Section | Content |
|---|---|
| **Status Header** | Name, server, status badge, uptime, version, sync progress bar |
| **Blockchain** | Nonce, epoch, slot, finalized nonce, blocks behind |
| **Consensus** | State, slot state, count leader/consensus/accepted blocks |
| **Network** | Connected peers/nodes, recv/sent bps with sparkline |
| **Resources** | CPU, memory, disk gauges + historical charts |
| **Actions** | Start, stop, restart, upgrade, downgrade, view config, view logs |

### 4.7 Log Streaming

**Purpose**: Access Docker container logs from the dashboard.

#### v1 (MVP)

- Stream live Docker logs via `docker logs -f --tail <N>`
- Agent forwards log lines over WebSocket to dashboard
- Basic text filter (client-side substring search)
- Configurable tail size (default: 500 lines)
- Auto-scroll with pause button

#### v2 (Future)

- Server-side regex filtering
- Log level parsing and color coding
- Log download (export to file)
- Full-text search with indexing

### 4.8 Notification System

**Purpose**: Proactive alerts for node issues.

#### Notification Channels

| Channel | Configuration | Protocol |
|---|---|---|
| **Telegram** | Bot token + chat ID | Telegram Bot API |
| **Pushover** | App token + user key | Pushover API |
| *(Extensible)* | Webhook URL | HTTP POST with JSON payload |

#### Alert Types

| Alert | Severity | Trigger |
|---|---|---|
| **Node Down** | Critical | Container not running for > 60s |
| **Sync Behind** | Warning | `blocks_behind > threshold` (configurable, default: 100) |
| **High CPU** | Warning | `cpu_load_percent > threshold` (configurable, default: 90%) |
| **High Memory** | Warning | `mem_load_percent > threshold` (configurable, default: 90%) |
| **Disk Full** | Critical | `disk_load_percent > threshold` (configurable, default: 85%) |
| **Version Outdated** | Info | `app_version != latest_tag_software_version` |
| **Agent Disconnected** | Critical | No heartbeat from agent for > 60s |
| **Nonce Stalled** | Critical | `klv_nonce` has not incremented for > threshold (configurable, default: 16s — 4 slots at 4s each). Node may be stuck even though container is running. |
| **Consensus Lost** | Critical | Node dropped out of consensus group |
| **Peer Count Low** | Warning | `num_connected_peers < threshold` (configurable, default: 5) |

#### Alert Configuration

- Each channel can subscribe to specific alert types independently
- Multiple channels can receive the same alert
- Per-alert cooldown period to prevent spam (default: 5 minutes)
- Alert history stored in database for review

#### Telegram Bot Commands (v1)

| Command | Response |
|---|---|
| `/nodes` | List all nodes with status |
| `/status <node_name>` | Detailed status of a specific node |
| `/help` | List available commands |

### 4.9 Agent Self-Update

**Purpose**: Keep agents updated without SSH access.

#### Update Flow

```
Dashboard                           Agent
   │                                  │
   ├── Detect version mismatch ───────┤
   ├── Send new binary (chunked) ─────┤
   │                                  ├── Verify checksum
   │                                  ├── Write to temp file
   │                                  ├── Replace binary
   │                                  ├── Restart self
   │◄── Reconnect with new version ───┤
   │                                  │
   └── Update confirmed ──────────────┘
```

- Dashboard embeds or stores the latest agent binary
- Binary transfer is chunked over the WebSocket connection
- Agent verifies SHA-256 checksum before applying
- Graceful restart: agent spawns new process, old process exits after handoff
- Timeout: if new agent doesn't connect within 120s, dashboard marks update as failed

---

## 5. Data Models

### 5.1 Server (Agent)

```
servers
├── id              TEXT PRIMARY KEY (UUID)
├── name            TEXT NOT NULL
├── hostname        TEXT NOT NULL
├── ip_address      TEXT NOT NULL
├── os_info         TEXT            -- e.g., "Ubuntu 22.04"
├── agent_version   TEXT
├── status          TEXT            -- "online", "offline", "updating"
├── last_heartbeat  INTEGER         -- Unix timestamp
├── certificate     BLOB            -- Agent's mTLS certificate
├── registered_at   INTEGER         -- Unix timestamp
├── updated_at      INTEGER         -- Unix timestamp
└── metadata        TEXT            -- JSON blob for extensibility
```

### 5.2 Node

```
nodes
├── id              TEXT PRIMARY KEY (UUID)
├── server_id       TEXT NOT NULL REFERENCES servers(id)
├── name            TEXT NOT NULL   -- e.g., "node1"
├── container_name  TEXT NOT NULL   -- e.g., "klever-node1"
├── node_type       TEXT NOT NULL   -- "validator", "observer"
├── redundancy_level INTEGER DEFAULT 0
├── rest_api_port   INTEGER NOT NULL
├── display_name    TEXT
├── docker_image_tag TEXT           -- e.g., "latest", "v1.7.16"
├── data_directory  TEXT NOT NULL   -- e.g., "/opt/node1"
├── bls_public_key  TEXT            -- Extracted from validatorKey.pem
├── status          TEXT            -- "running", "stopped", "syncing", "error"
├── created_at      INTEGER
├── updated_at      INTEGER
└── metadata        TEXT            -- JSON blob
```

### 5.3 Metrics (Hot — Recent)

```
metrics_recent
├── id              INTEGER PRIMARY KEY AUTOINCREMENT
├── node_id         TEXT NOT NULL REFERENCES nodes(id)
├── timestamp       INTEGER NOT NULL -- Unix timestamp
├── cpu_percent     REAL
├── mem_percent     REAL
├── mem_used_bytes  INTEGER
├── disk_percent    REAL
├── disk_used_bytes INTEGER
├── network_recv_bps INTEGER
├── network_sent_bps INTEGER
├── nonce           INTEGER
├── highest_final_nonce INTEGER
├── probable_highest_nonce INTEGER
├── is_syncing      INTEGER
├── epoch           INTEGER
├── current_slot    INTEGER
├── connected_peers INTEGER
├── connected_nodes INTEGER
├── consensus_state TEXT
├── count_consensus INTEGER
├── count_leader    INTEGER
├── count_accepted_blocks INTEGER
├── tx_pool_load    INTEGER
├── app_version     TEXT
└── INDEX idx_metrics_recent_node_ts (node_id, timestamp)
```

### 5.4 Metrics (Cold — Archive)

```
metrics_archive
├── id              INTEGER PRIMARY KEY AUTOINCREMENT
├── node_id         TEXT NOT NULL REFERENCES nodes(id)
├── timestamp       INTEGER NOT NULL -- Start of 5-minute window
├── cpu_percent_avg REAL
├── cpu_percent_max REAL
├── mem_percent_avg REAL
├── mem_percent_max REAL
├── disk_percent_avg REAL
├── network_recv_bps_avg INTEGER
├── network_sent_bps_avg INTEGER
├── nonce_min       INTEGER
├── nonce_max       INTEGER
├── connected_peers_avg REAL
├── is_syncing_max  INTEGER         -- 1 if any sample was syncing
├── count_consensus_delta INTEGER   -- Increase during window
├── count_leader_delta INTEGER
├── count_accepted_blocks_delta INTEGER
└── INDEX idx_metrics_archive_node_ts (node_id, timestamp)
```

### 5.5 Config Backup

```
config_backups
├── id              INTEGER PRIMARY KEY AUTOINCREMENT
├── node_id         TEXT NOT NULL REFERENCES nodes(id)
├── file_name       TEXT NOT NULL   -- e.g., "config.yaml"
├── content         BLOB NOT NULL   -- File content (encrypted)
├── created_at      INTEGER NOT NULL
├── created_by      TEXT            -- "user", "auto-backup", "provisioning"
└── INDEX idx_config_node_file (node_id, file_name)
```

### 5.6 Notification Channel

```
notification_channels
├── id              TEXT PRIMARY KEY (UUID)
├── name            TEXT NOT NULL   -- User-friendly name
├── type            TEXT NOT NULL   -- "telegram", "pushover", "webhook"
├── config          TEXT NOT NULL   -- JSON: { "bot_token": "...", "chat_id": "..." }
├── enabled         INTEGER DEFAULT 1
├── created_at      INTEGER
└── updated_at      INTEGER
```

### 5.7 Alert Rule

```
alert_rules
├── id              TEXT PRIMARY KEY (UUID)
├── channel_id      TEXT NOT NULL REFERENCES notification_channels(id)
├── alert_type      TEXT NOT NULL   -- "node_down", "sync_behind", "high_cpu", etc.
├── enabled         INTEGER DEFAULT 1
├── threshold       REAL            -- Override default threshold (nullable)
├── cooldown_seconds INTEGER DEFAULT 300
├── created_at      INTEGER
└── updated_at      INTEGER
```

### 5.8 Alert History

```
alert_history
├── id              INTEGER PRIMARY KEY AUTOINCREMENT
├── alert_type      TEXT NOT NULL
├── node_id         TEXT REFERENCES nodes(id)
├── server_id       TEXT REFERENCES servers(id)
├── severity        TEXT NOT NULL   -- "critical", "warning", "info"
├── message         TEXT NOT NULL
├── channels_notified TEXT          -- JSON array of channel IDs
├── resolved        INTEGER DEFAULT 0
├── triggered_at    INTEGER NOT NULL
├── resolved_at     INTEGER
└── INDEX idx_alert_history_ts (triggered_at)
```

### 5.9 Dashboard Settings

```
settings
├── key             TEXT PRIMARY KEY
└── value           TEXT NOT NULL   -- JSON-encoded value

-- Keys:
-- "webauthn_credentials"    -> JSON array of registered WebAuthn credentials (id, public_key, name, registered_at)
-- "recovery_codes"          -> JSON array of Argon2id-hashed single-use recovery codes
-- "recovery_codes_remaining" -> Number of unused recovery codes
-- "metrics_interval_sec"    -> "10"
-- "hot_retention_days"      -> "7"
-- "cold_aggregation_min"    -> "5"
-- "agent_binary_version"    -> "1.0.0"
-- "agent_binary_checksum"   -> SHA-256 hex
```

---

## 6. Communication Protocol

### 6.1 WebSocket Messages

All messages use JSON over WebSocket with mTLS.

#### Message Envelope

```json
{
  "id": "uuid-v4",
  "type": "command|response|event|stream",
  "action": "string",
  "payload": {},
  "timestamp": 1234567890
}
```

#### Dashboard → Agent (Commands)

| Action | Payload | Description |
|---|---|---|
| `node.start` | `{ "container": "klever-node1" }` | Start a node |
| `node.stop` | `{ "container": "klever-node1" }` | Stop a node |
| `node.restart` | `{ "container": "klever-node1" }` | Restart a node |
| `node.create` | `{ "name": "node3", "port": 8082, "image_tag": "latest", "redundancy": 0, "generate_keys": true }` | Provision a new node |
| `node.delete` | `{ "container": "klever-node1", "remove_data": false }` | Delete a node |
| `node.upgrade` | `{ "container": "klever-node1", "image_tag": "v1.7.16" }` | Change Docker image tag |
| `config.get` | `{ "node": "node1", "file": "config.yaml" }` | Read a config file |
| `config.set` | `{ "node": "node1", "file": "config.yaml", "content": "..." }` | Write a config file |
| `config.push` | `{ "nodes": ["node1","node2"], "file": "config.yaml", "content": "..." }` | Push config to multiple nodes |
| `key.generate` | `{ "node": "node1" }` | Generate BLS key pair |
| `key.import` | `{ "node": "node1", "pem_content": "..." }` | Import validator PEM |
| `key.export` | `{ "node": "node1" }` | Export validator PEM |
| `logs.stream` | `{ "container": "klever-node1", "tail": 500 }` | Start log streaming |
| `logs.stop` | `{ "container": "klever-node1" }` | Stop log streaming |
| `system.install_prereqs` | `{}` | Install Docker, jq, etc. |
| `agent.update` | `{ "binary": "<base64_chunks>", "checksum": "sha256..." }` | Push agent update |
| `docker.tags` | `{}` | List available Docker image tags |
| `docker.pull` | `{ "image_tag": "v1.7.16" }` | Pull a Docker image |

#### Agent → Dashboard (Events & Responses)

| Action | Payload | Description |
|---|---|---|
| `metrics.report` | `{ "system": {...}, "nodes": [{...}] }` | Periodic metrics |
| `node.status` | `{ "nodes": [{ "name": "...", "status": "..." }] }` | Node status update |
| `command.result` | `{ "command_id": "uuid", "success": true, "output": "..." }` | Command execution result |
| `logs.data` | `{ "container": "...", "lines": ["..."] }` | Log stream data |
| `agent.info` | `{ "version": "1.0.0", "os": "...", "hostname": "..." }` | Agent metadata on connect |

### 6.2 Heartbeat

- Agent sends heartbeat every 30 seconds
- Dashboard marks agent as offline if no heartbeat for 60 seconds
- Heartbeat includes basic system status (CPU, memory, disk)

---

## 7. Agent Command Whitelist

The agent only executes predefined operations. No arbitrary shell commands.

| Category | Allowed Operations |
|---|---|
| **Docker** | `start`, `stop`, `restart`, `rm`, `pull`, `run` (with validated parameters), `inspect`, `logs`, `ps` |
| **Filesystem** | Read/write within `/opt/node*/config/`, `/opt/node*/wallet/` only |
| **System** | `apt-get install` (Docker, jq only), `chown 999:999`, `mkdir` (within allowed paths) |
| **Network** | HTTP GET to `localhost:<port>/node/status` (node metrics) |
| **Self** | Replace own binary, restart self |

---

## 8. API Endpoints (Dashboard REST API)

Internal API consumed by the embedded web UI.

### Authentication

All endpoints except `/api/auth/login` require a valid JWT in the `Authorization` header or httpOnly cookie.

### Endpoints

| Method | Path | Description |
|---|---|---|
| **Auth** | | |
| POST | `/api/auth/passkey/begin` | Begin Passkey authentication (WebAuthn challenge) |
| POST | `/api/auth/passkey/finish` | Complete Passkey authentication (verify assertion, issue JWT) |
| POST | `/api/auth/recovery` | Login with single-use recovery code |
| POST | `/api/auth/refresh` | Refresh JWT token |
| POST | `/api/auth/logout` | Invalidate session |
| **Setup** (unauthenticated, first-run only) | | |
| GET | `/api/setup/status` | Check if initial setup is complete |
| POST | `/api/setup/passkey/begin` | Begin first Passkey registration |
| POST | `/api/setup/passkey/finish` | Complete first Passkey registration + generate recovery codes |
| **Passkey Management** (authenticated) | | |
| GET | `/api/auth/passkeys` | List registered passkeys |
| POST | `/api/auth/passkeys/begin` | Begin registering a new passkey |
| POST | `/api/auth/passkeys/finish` | Complete passkey registration |
| DELETE | `/api/auth/passkeys/:id` | Remove a passkey (must keep at least one) |
| POST | `/api/auth/recovery/regenerate` | Generate new set of recovery codes (invalidates old ones) |
| **Servers** | | |
| GET | `/api/servers` | List all registered servers |
| GET | `/api/servers/:id` | Server details with nodes |
| POST | `/api/servers/register` | Generate one-time registration token |
| DELETE | `/api/servers/:id` | Unregister a server |
| **Nodes** | | |
| GET | `/api/nodes` | List all nodes (with filters: server, status) |
| GET | `/api/nodes/:id` | Node details |
| POST | `/api/nodes` | Create/provision a new node |
| DELETE | `/api/nodes/:id` | Delete a node |
| POST | `/api/nodes/:id/start` | Start node |
| POST | `/api/nodes/:id/stop` | Stop node |
| POST | `/api/nodes/:id/restart` | Restart node |
| POST | `/api/nodes/:id/upgrade` | Upgrade Docker image |
| POST | `/api/nodes/:id/downgrade` | Downgrade Docker image |
| POST | `/api/nodes/batch` | Batch operation on multiple nodes |
| **Config** | | |
| GET | `/api/nodes/:id/config/:file` | Read a config file |
| PUT | `/api/nodes/:id/config/:file` | Update a config file |
| POST | `/api/config/push` | Push config to multiple nodes |
| GET | `/api/nodes/:id/config/backups` | List config backups |
| GET | `/api/config/backups/:backup_id` | Retrieve a backup |
| **Keys** | | |
| POST | `/api/nodes/:id/keys/generate` | Generate BLS key |
| POST | `/api/nodes/:id/keys/import` | Import PEM file |
| GET | `/api/nodes/:id/keys/export` | Export PEM file |
| GET | `/api/nodes/:id/keys/bls` | Get BLS public key |
| **Metrics** | | |
| GET | `/api/nodes/:id/metrics` | Current metrics |
| GET | `/api/nodes/:id/metrics/history` | Historical metrics (query params: from, to, resolution) |
| GET | `/api/servers/:id/metrics` | Server-level aggregate metrics |
| GET | `/api/overview/metrics` | Global overview metrics |
| **Logs** | | |
| GET | `/api/nodes/:id/logs` | WebSocket upgrade for log streaming |
| **Notifications** | | |
| GET | `/api/notifications/channels` | List notification channels |
| POST | `/api/notifications/channels` | Create channel |
| PUT | `/api/notifications/channels/:id` | Update channel |
| DELETE | `/api/notifications/channels/:id` | Delete channel |
| POST | `/api/notifications/channels/:id/test` | Send test notification |
| GET | `/api/notifications/rules` | List alert rules |
| POST | `/api/notifications/rules` | Create alert rule |
| PUT | `/api/notifications/rules/:id` | Update alert rule |
| DELETE | `/api/notifications/rules/:id` | Delete alert rule |
| GET | `/api/notifications/history` | Alert history |
| **Docker** | | |
| GET | `/api/docker/tags` | Available Docker image tags |
| **Settings** | | |
| GET | `/api/settings` | Get dashboard settings |
| PUT | `/api/settings` | Update settings |
| **Agent** | | |
| POST | `/api/agent/upload` | Upload new agent binary |
| POST | `/api/servers/:id/agent/update` | Push agent update to server |

---

## 9. Project Structure

```
KleverNodeHub/
├── cmd/
│   ├── dashboard/              # Dashboard binary entry point
│   │   └── main.go
│   └── agent/                  # Agent binary entry point
│       └── main.go
├── internal/
│   ├── auth/                   # Authentication & session management
│   │   ├── webauthn.go          # WebAuthn/Passkey authentication
│   │   ├── recovery.go          # Recovery code generation & verification
│   │   ├── jwt.go              # JWT token management
│   │   └── middleware.go       # Auth middleware
│   ├── crypto/                 # Cryptography
│   │   ├── aes.go              # AES-256-GCM encryption
│   │   ├── ed25519.go          # Ed25519 certificate management
│   │   ├── mtls.go             # mTLS configuration
│   │   └── ca.go               # Certificate authority
│   ├── dashboard/              # Dashboard-specific logic
│   │   ├── server.go           # HTTP server setup
│   │   ├── handlers/           # HTTP route handlers
│   │   │   ├── auth.go
│   │   │   ├── servers.go
│   │   │   ├── nodes.go
│   │   │   ├── config.go
│   │   │   ├── keys.go
│   │   │   ├── metrics.go
│   │   │   ├── logs.go
│   │   │   ├── notifications.go
│   │   │   └── settings.go
│   │   ├── ws/                 # WebSocket hub
│   │   │   ├── hub.go          # Connection manager
│   │   │   ├── client.go       # Agent connection handler
│   │   │   └── messages.go     # Message types
│   │   └── scheduler/          # Background jobs
│   │       ├── scheduler.go
│   │       ├── metrics_decimation.go
│   │       ├── alert_evaluator.go
│   │       └── agent_health.go
│   ├── agent/                  # Agent-specific logic
│   │   ├── agent.go            # Agent main loop
│   │   ├── connection.go       # WebSocket client to dashboard
│   │   ├── executor.go         # Command execution engine
│   │   ├── docker.go           # Docker operations
│   │   ├── metrics.go          # System & node metrics collection
│   │   ├── config.go           # Config file operations
│   │   ├── keys.go             # Validator key operations
│   │   ├── logs.go             # Log streaming
│   │   ├── provisioner.go      # Node provisioning (install from scratch)
│   │   ├── updater.go          # Agent self-update
│   │   └── whitelist.go        # Command whitelist enforcement
│   ├── models/                 # Shared data structures
│   │   ├── server.go
│   │   ├── node.go
│   │   ├── metrics.go
│   │   ├── config.go
│   │   ├── notification.go
│   │   ├── alert.go
│   │   └── messages.go         # WebSocket message types
│   ├── store/                  # Database layer
│   │   ├── sqlite.go           # SQLite connection & migrations
│   │   ├── server_store.go
│   │   ├── node_store.go
│   │   ├── metrics_store.go
│   │   ├── config_store.go
│   │   ├── notification_store.go
│   │   ├── alert_store.go
│   │   └── settings_store.go
│   └── notify/                 # Notification dispatchers
│       ├── notifier.go         # Notifier interface
│       ├── telegram.go         # Telegram bot
│       ├── pushover.go         # Pushover
│       └── webhook.go          # Generic webhook
├── web/                        # Embedded frontend
│   ├── static/
│   │   ├── css/
│   │   ├── js/
│   │   └── img/
│   └── templates/
│       ├── index.html
│       ├── login.html
│       ├── overview.html
│       ├── server.html
│       ├── node.html
│       ├── config-editor.html
│       ├── logs.html
│       ├── alerts.html
│       └── settings.html
├── scripts/
│   └── install-agent.sh        # One-liner agent installation script
├── .github/workflows/
│   ├── ci.yaml                 # Test, lint, build on every push
│   └── release.yaml            # Cross-platform binary releases
├── Dockerfile                  # Dashboard container
├── Dockerfile.agent            # Agent container (optional)
├── go.mod
├── go.sum
├── CLAUDE.md
└── README.md
```

---

## 10. Workflows

### 10.1 First-Time Setup

```
1. User deploys dashboard (Docker or binary)
2. Dashboard generates:
   - CA certificate (Ed25519)
   - Encryption keys (AES-256-GCM)
   - Empty SQLite database with schema
3. User opens browser → HTTPS redirect
4. First-time setup wizard:
   a. Register first Passkey (biometric prompt — Face ID, Touch ID, Windows Hello, or hardware key)
   b. Display 8 recovery codes (user must save them — shown only once)
   c. Set dashboard name and basic preferences
5. User is automatically logged in
6. Dashboard is ready — no agents yet
7. User can register additional passkeys from Settings
```

### 10.2 Adding a New Server

```
1. Dashboard: User clicks "Add Server" → generates one-time registration token
2. Server: User runs one-liner install script with token
3. Agent: Installs binary, presents token to dashboard
4. Dashboard: Validates token, issues mTLS certificate
5. Agent: Stores certificate, connects via WebSocket
6. Agent: Scans for existing Klever nodes, reports to dashboard
7. Dashboard: Shows discovered nodes, user confirms import
8. Server is registered and online
```

### 10.3 Provisioning a New Node

```
1. User selects server in dashboard
2. User clicks "New Node" → provisioning wizard
3. User selects:
   - Docker image tag (from available tags)
   - Node type (validator / observer)
   - Redundancy level (0 = active, 1 = fallback)
   - Key management (generate new / import existing)
4. Dashboard sends provisioning command to agent
5. Agent executes:
   a. Install prerequisites (if needed)
   b. Pull Docker image
   c. Create directories
   d. Download official configuration
   e. Generate/import keys
   f. Start container
6. Agent reports success → dashboard updates node list
7. Node appears in dashboard, starts reporting metrics
```

### 10.4 Upgrading Nodes

```
1. Dashboard shows "update available" badge on outdated nodes
2. User selects nodes to upgrade
3. User picks target image tag
4. Dashboard sends upgrade command to each agent
5. Agent per node:
   a. Pull new Docker image
   b. Stop container
   c. Remove container
   d. Download latest config (preserving custom changes)
   e. Recreate container with new image + same volumes
   f. Start container
6. Agent reports new version → dashboard confirms upgrade
```

### 10.5 Config Push Workflow

```
1. User opens Config Editor
2. User edits config (or loads from another node)
3. User clicks "Push to nodes"
4. User selects target nodes (checkboxes)
5. Dashboard auto-backs up current config for each target
6. Dashboard sends config.push command to each agent
7. Agent writes config file, optionally restarts node
8. Dashboard shows success/failure per node
```

---

## 11. Implementation Phases

### Phase 1: Foundation (MVP)

Core infrastructure and basic node management.

| Task | Description |
|---|---|
| Project scaffolding | Go module, directory structure, build system |
| Crypto module | Ed25519 CA, mTLS setup, AES-256-GCM |
| Auth module | WebAuthn Passkey (passwordless), recovery codes, JWT sessions |
| SQLite store | Schema, migrations, encrypted database |
| Agent registration | One-time token, certificate issuance, WebSocket connection |
| Agent discovery | Scan for existing nodes on agent startup |
| Basic node operations | Start, stop, restart via dashboard |
| Web UI shell | Login page, overview page, basic node list |
| Docker operations | Pull image, create/remove containers |

### Phase 2: Monitoring & Provisioning

Full node lifecycle and metrics.

| Task | Description |
|---|---|
| Node provisioning | Install from scratch (prerequisites, config download, key generation) |
| Upgrade/downgrade | Docker image tag management |
| System metrics collection | CPU, memory, disk from agent |
| Node metrics collection | Klever `/node/status` endpoint polling |
| Metrics storage | Hot/cold tables, decimation scheduler |
| Dashboard UI: metrics | Charts, gauges, sparklines for node detail view |
| Dashboard UI: overview | Server grid, status badges, summary widgets |

### Phase 3: Configuration & Logs

Remote configuration management and log access.

| Task | Description |
|---|---|
| Config read/write | Load and save YAML/JSON config files via agent |
| Config editor UI | Syntax-highlighted editor in browser |
| Config push | Push config to multiple nodes at once |
| Config backup | Auto-backup before changes, backup history |
| Validator key management | Generate, import, export PEM files |
| Log streaming | Docker log forwarding over WebSocket |
| Log viewer UI | Live log display with text filter |

### Phase 4: Notifications & Polish

Alerting system and UX refinements.

| Task | Description |
|---|---|
| Notification channels | Telegram, Pushover, webhook integration |
| Alert rules engine | Configurable thresholds, per-channel subscriptions |
| Alert evaluation scheduler | Background job to evaluate metrics against rules |
| Telegram bot commands | `/nodes`, `/status <name>` |
| Agent auto-update | Binary push, checksum verification, graceful restart |
| Mobile responsiveness | Ensure all UI works on phone/tablet |
| Settings page | Dashboard configuration UI |
| Error handling & resilience | Reconnection logic, graceful degradation |

---

## 12. Tech Constraints

| Constraint | Rationale |
|---|---|
| **Go stdlib + x/crypto only** | Minimal attack surface, no supply chain risk |
| **Single binary per component** | Easy deployment, no runtime dependencies |
| **SQLite only** | No external database server to manage |
| **No shell access on agent** | Security: command whitelist only |
| **Embedded web UI** | No separate frontend build/deploy |
| **Ed25519 certificates** | Fast, secure, small keys |
| **Target: up to 100 nodes** | Architecture should handle 100 nodes comfortably |
| **Ubuntu/Debian servers** | Agent installation targets these distributions |

---

## 13. Success Metrics

| Metric | Target |
|---|---|
| Agent registration time | < 60 seconds from script run to dashboard visible |
| Node provisioning time | < 5 minutes from click to syncing node |
| Metrics latency | < 15 seconds from event to dashboard display |
| Dashboard page load | < 2 seconds for overview with 100 nodes |
| Alert delivery | < 30 seconds from trigger to notification |
| Agent binary size | < 15 MB |
| Dashboard binary size | < 25 MB (including embedded UI) |
| Memory usage (dashboard) | < 256 MB with 100 nodes |
| Memory usage (agent) | < 50 MB per agent |
