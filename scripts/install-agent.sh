#!/bin/bash
set -euo pipefail

# Klever Node Hub — Agent Installation Script
# Usage: curl -sSL https://raw.githubusercontent.com/CTJaeger/KleverNodeHub/main/scripts/install-agent.sh | bash -s -- --token TOKEN --dashboard URL

AGENT_BIN="/usr/local/bin/klever-agent"
AGENT_CONFIG_DIR="/etc/klever-agent"
AGENT_USER="klever-agent"
SERVICE_NAME="klever-agent"

# Release signing public key (SubjectPublicKeyInfo, base64) — the same key as
# internal/release.PublicKeyHex. Downloads are installed only if they are listed
# in a checksums.txt carrying this key's signature.
RELEASE_PUBKEY_B64="MCowBQYDK2VwAyEACcSVhfPQxGiEeg9VIvdi+JeCq4tnoLcXYMM5x417MtU="

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
CA_FINGERPRINT=""   # SHA-256 of the dashboard CA, pins the dashboard during registration

while [[ $# -gt 0 ]]; do
    case "$1" in
        --token)           TOKEN="$2"; shift 2 ;;
        --dashboard)       DASHBOARD_URL="$2"; shift 2 ;;
        --ca-fingerprint)  CA_FINGERPRINT="$2"; shift 2 ;;
        *)                 error "Unknown argument: $1" ;;
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
# Resolve "latest" once, so binary and checksums come from the same release.
RELEASE_TAG=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/CTJaeger/KleverNodeHub/releases/latest" | sed 's#.*/tag/##')
[[ "$RELEASE_TAG" =~ ^v[0-9A-Za-z.-]+$ ]] || error "Could not determine the latest release (got: ${RELEASE_TAG})"
RELEASE_BASE="https://github.com/CTJaeger/KleverNodeHub/releases/download/${RELEASE_TAG}"
ASSET="klever-agent-${GOOS}-${GOARCH}"
RELEASE_URL="${RELEASE_BASE}/${ASSET}"

log "Downloading agent binary (${RELEASE_TAG})..."
AGENT_TMP=$(mktemp) # private file: a fixed /tmp path could be swapped between verify and install
if ! curl -fsSL -o "$AGENT_TMP" "$RELEASE_URL"; then
    error "Failed to download agent binary from: ${RELEASE_URL}"
fi

# Verify the download is actually an ELF binary, not an HTML error page.
MAGIC=$(head -c 4 "$AGENT_TMP" | od -A n -t x1 | tr -d ' \n')
if [[ "$MAGIC" != "7f454c46" ]]; then
    rm -f "$AGENT_TMP"
    error "Downloaded file is not a valid ELF binary. Check: ${RELEASE_URL}"
fi

# Verify the download against the signed release checksums. The checksum is
# always checked; the Ed25519 signature over checksums.txt needs OpenSSL 3
# (pkeyutl -rawin). On older systems the script warns and continues with the
# checksum only — the agent itself verifies the signature on every later update.
verify_release() {
    local bin="$1" dir expected actual ossl_major
    dir=$(mktemp -d)
    trap 'rm -rf "$dir"' RETURN
    curl -fsSL -o "$dir/checksums.txt" "${RELEASE_BASE}/checksums.txt" || error "Failed to download release checksums"

    ossl_major=0
    if command -v openssl &>/dev/null; then
        ossl_major=$(openssl version | awk '{print $2}' | cut -d. -f1)
        [[ "$ossl_major" =~ ^[0-9]+$ ]] || ossl_major=0
    fi
    if [[ "$ossl_major" -ge 3 ]]; then
        curl -fsSL -o "$dir/release.sig" "${RELEASE_BASE}/release.sig" || error "Release ${RELEASE_TAG} is not signed (no release.sig) — not installing"
        printf -- '-----BEGIN PUBLIC KEY-----\n%s\n-----END PUBLIC KEY-----\n' "$RELEASE_PUBKEY_B64" > "$dir/release.pub"
        base64 -d "$dir/release.sig" > "$dir/sig.bin" 2>/dev/null || error "Malformed release signature"
        openssl pkeyutl -verify -pubin -inkey "$dir/release.pub" -rawin \
            -in "$dir/checksums.txt" -sigfile "$dir/sig.bin" &>/dev/null \
            || error "Release signature verification FAILED — not installing"
        grep -Fqx "# release ${RELEASE_TAG}" "$dir/checksums.txt" || error "Signed checksums are not for release ${RELEASE_TAG}"
        log "Release ${RELEASE_TAG}: signature verified"
    else
        warn "OpenSSL 3 not found: the release signature cannot be checked here, verifying the checksum only"
    fi

    expected=$(awk -v a="$ASSET" '$2 == a || $2 == "*" a {print tolower($1)}' "$dir/checksums.txt")
    actual=$(sha256sum "$bin" | awk '{print $1}')
    [[ -n "$expected" && "$expected" == "$actual" ]] || error "Agent binary does not match the release checksum — not installing"
    log "Release ${RELEASE_TAG}: checksum verified"
}

verify_release "$AGENT_TMP"

chmod 755 "$AGENT_TMP"
mv "$AGENT_TMP" "$AGENT_BIN"
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
REGISTER_ARGS=(--register-token "$TOKEN" --dashboard-url "$DASHBOARD_URL" --config-dir "$AGENT_CONFIG_DIR")
[[ -n "$CA_FINGERPRINT" ]] && REGISTER_ARGS+=(--ca-fingerprint "$CA_FINGERPRINT")
if ! "${AGENT_BIN}" "${REGISTER_ARGS[@]}"; then
    if [[ -z "$CA_FINGERPRINT" ]]; then
        warn "No --ca-fingerprint in the install command: it comes from dashboards v0.3.89 and newer."
        warn "Update the dashboard, generate a new install command there and run it again."
    fi
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
log ""
log "════════════════════════════════════════════════════════════"
log "  Installation complete! The agent runs as a systemd service."
log "  You can safely close this terminal now."
log "════════════════════════════════════════════════════════════"
