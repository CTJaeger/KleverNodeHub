#!/bin/bash
set -euo pipefail

# Klever Node Hub — Agent Installation Script
# Usage: curl -sSL https://raw.githubusercontent.com/CTJaeger/KleverNodeHub/main/scripts/install-agent.sh | bash -s -- --token TOKEN --dashboard URL

AGENT_BIN="/usr/local/bin/klever-agent"
AGENT_CONFIG_DIR="/etc/klever-agent"
AGENT_USER="klever-agent"
SERVICE_NAME="klever-agent"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RESET='\033[0m'

log()   { echo -e "${GREEN}[+]${RESET} $*"; }
warn()  { echo -e "${YELLOW}[!]${RESET} $*"; }
error() { echo -e "${RED}[✗]${RESET} $*" >&2; exit 1; }

# Parse arguments
TOKEN=""
DASHBOARD_URL=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        --token)    TOKEN="$2"; shift 2 ;;
        --dashboard) DASHBOARD_URL="$2"; shift 2 ;;
        *)          error "Unknown argument: $1" ;;
    esac
done

[[ -z "$TOKEN" ]] && error "Missing --token argument"
[[ -z "$DASHBOARD_URL" ]] && error "Missing --dashboard argument"

# Check root
[[ $EUID -ne 0 ]] && error "This script must be run as root (sudo)"

# Detect architecture
ARCH=$(uname -m)
case "$ARCH" in
    x86_64)  GOARCH="amd64" ;;
    aarch64) GOARCH="arm64" ;;
    *)       error "Unsupported architecture: $ARCH" ;;
esac

GOOS="linux"

log "Klever Node Hub — Agent Installer"
log "Architecture: ${GOOS}/${GOARCH}"
log "Dashboard: ${DASHBOARD_URL}"

# Install Docker if not present
if ! command -v docker &>/dev/null; then
    log "Docker not found. Installing Docker..."
    if command -v apt-get &>/dev/null; then
        apt-get update -qq
        apt-get install -y -qq ca-certificates curl gnupg
        install -m 0755 -d /etc/apt/keyrings
        DISTRO=$(. /etc/os-release && echo "$ID")
        curl -fsSL "https://download.docker.com/linux/${DISTRO}/gpg" | gpg --dearmor -o /etc/apt/keyrings/docker.gpg
        chmod a+r /etc/apt/keyrings/docker.gpg
        echo \
          "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/${DISTRO} \
          $(. /etc/os-release && echo "$VERSION_CODENAME") stable" > /etc/apt/sources.list.d/docker.list
        apt-get update -qq
        apt-get install -y -qq docker-ce docker-ce-cli containerd.io docker-buildx-plugin
    elif command -v yum &>/dev/null; then
        yum install -y -q yum-utils
        yum-config-manager --add-repo https://download.docker.com/linux/centos/docker-ce.repo
        yum install -y -q docker-ce docker-ce-cli containerd.io docker-buildx-plugin
    else
        warn "Could not detect package manager. Trying Docker convenience script..."
        curl -fsSL https://get.docker.com | sh
    fi
    systemctl enable docker
    systemctl start docker
    log "Docker installed and started"
else
    log "Docker already installed: $(docker --version)"
fi

# Install jq if not present
if ! command -v jq &>/dev/null; then
    log "Installing jq..."
    if command -v apt-get &>/dev/null; then
        apt-get install -y -qq jq
    elif command -v yum &>/dev/null; then
        yum install -y -q jq
    else
        warn "Could not install jq automatically. Please install it manually."
    fi
    log "jq installed"
else
    log "jq already installed"
fi

# Download agent binary
RELEASE_URL="https://github.com/CTJaeger/KleverNodeHub/releases/latest/download/klever-agent-${GOOS}-${GOARCH}"

log "Downloading agent binary..."
if ! curl -sSL -o /tmp/klever-agent "$RELEASE_URL"; then
    error "Failed to download agent binary"
fi

chmod +x /tmp/klever-agent
mv /tmp/klever-agent "$AGENT_BIN"
log "Agent installed to ${AGENT_BIN}"

# Create config directory
mkdir -p "$AGENT_CONFIG_DIR"
chmod 700 "$AGENT_CONFIG_DIR"

# Create systemd service
cat > /etc/systemd/system/${SERVICE_NAME}.service << EOF
[Unit]
Description=Klever Node Hub Agent
After=network-online.target docker.service
Wants=network-online.target

[Service]
Type=simple
ExecStart=${AGENT_BIN} --config-dir ${AGENT_CONFIG_DIR}
Restart=always
RestartSec=10
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable "${SERVICE_NAME}"
log "Systemd service created and enabled"

# Register with dashboard
log "Registering with dashboard..."
if ! "${AGENT_BIN}" --register-token "$TOKEN" --dashboard-url "$DASHBOARD_URL" --config-dir "$AGENT_CONFIG_DIR"; then
    error "Registration failed"
fi

# Start the service
systemctl start "${SERVICE_NAME}"
log "Agent started successfully!"
log ""
log "Commands:"
log "  systemctl status ${SERVICE_NAME}   — Check status"
log "  journalctl -u ${SERVICE_NAME} -f   — View logs"
log "  systemctl restart ${SERVICE_NAME}  — Restart agent"
