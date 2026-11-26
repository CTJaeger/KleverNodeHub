#!/bin/bash
# Klever Node Hub - compromise check
#
# Read-only. Looks for known traces of the November 2026 attacks on dashboards
# that were reachable from the internet before v0.3.89. Run it as root on the
# dashboard host AND on every node server:
#
#   curl -sSL https://raw.githubusercontent.com/CTJaeger/KleverNodeHub/main/scripts/check-compromise.sh | sudo bash
#
# Options (after "bash -s --"):
#   --data-dir DIR   dashboard data directory (default: auto-detect)
#   --hub-log FILE   dashboard log file, if it does not log to the journal
#   --root DIR       where to search for attacker files (default: /)
#
# Exit code: 0 = nothing found, 1 = at least one warning.
# A clean result is not proof: an intruder with root access can remove traces.

set -u

# Official releases: up to v0.3.93 from CTJaeger, after that from Test0rMaik
# (tags v*-knh.*).
REPOS="CTJaeger/KleverNodeHub Test0rMaik/KleverNodeHub"
DATA_DIR=""
HUB_LOG=""
SEARCH_ROOT="/"

while [[ $# -gt 0 ]]; do
    case "$1" in
        --data-dir) DATA_DIR="$2"; shift 2 ;;
        --hub-log)  HUB_LOG="$2"; shift 2 ;;
        --root)     SEARCH_ROOT="$2"; shift 2 ;;
        -h|--help)  sed -n '2,18p' "$0" 2>/dev/null; exit 0 ;;
        *)          echo "Unknown argument: $1" >&2; exit 2 ;;
    esac
done

if [[ -t 1 ]]; then
    RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; RESET='\033[0m'
else
    RED=''; GREEN=''; YELLOW=''; RESET=''
fi

WARNINGS=0
NOTES=0
ok()   { echo -e "${GREEN}[ OK ]${RESET} $*"; }
warn() { echo -e "${RED}[WARN]${RESET} $*"; WARNINGS=$((WARNINGS + 1)); }
note() { echo -e "${YELLOW}[NOTE]${RESET} $*"; NOTES=$((NOTES + 1)); }
info() { echo "       $*"; }
section() { echo; echo "== $* =="; }

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

# A release version looks like v1.2.3; anything else an agent reports
# (dev, probe6, ...) was not installed from a release.
is_release_version() { [[ "$1" =~ ^v?[0-9]+\.[0-9]+\.[0-9]+(-knh\.[0-9]+)?$ ]]; }

# ---------------------------------------------------------------------------
# Official checksums of all releases: "<tag> <sha256> <asset>" per line.
# ---------------------------------------------------------------------------
CHECKSUMS="$TMP/official.txt"
: > "$CHECKSUMS"
load_official_checksums() {
    command -v curl >/dev/null 2>&1 || return 1
    local repo tags page more tag
    for repo in $REPOS; do
        tags=""
        for page in 1 2 3 4 5 6 7 8; do
            more=$(curl -fsSL "https://api.github.com/repos/${repo}/releases?per_page=100&page=${page}" 2>/dev/null \
                | grep -o '"tag_name": *"[^"]*"' | sed 's/.*"\([^"]*\)"$/\1/')
            [[ -n "$more" ]] || break
            tags="${tags} ${more}"
        done
        for tag in $tags; do
            curl -fsSL "https://github.com/${repo}/releases/download/${tag}/checksums.txt" 2>/dev/null \
                | awk -v t="$tag" 'NF == 2 { sub(/^\*/, "", $2); print t, tolower($1), $2 }' >> "$CHECKSUMS"
        done
    done
    [[ -s "$CHECKSUMS" ]]
}

# official_release <sha256> -> prints "<tag> <asset>" of the first match
official_release() {
    awk -v h="$1" '$2 == h { print $1, $3; exit }' "$CHECKSUMS"
}

# official_hash <tag> <os> <arch> -> the release's checksum of its agent asset
official_agent_hash() {
    awk -v t="$1" -v a="klever-agent-$2-$3" -v b="agent-$2-$3" \
        '$1 == t && ($3 == a || $3 == a ".exe" || $3 == b || $3 == b ".exe") { print $2; exit }' "$CHECKSUMS"
}

sha256_of() { sha256sum "$1" 2>/dev/null | awk '{print $1}'; }

# ---------------------------------------------------------------------------
section "Attacker files"
# ---------------------------------------------------------------------------
found=0
for f in /tmp/.knh_probe_*; do
    [[ -e "$f" ]] || continue
    warn "attacker work file: $f"
    found=1
done
while IFS= read -r f; do
    [[ -n "$f" ]] || continue
    warn "attacker output file: $f"
    found=1
done < <(find "$SEARCH_ROOT" \( -path /proc -o -path /sys -o -path /dev -o -path /run \) -prune -o \
    -type f \( -name 'probe_result*.json' -o -name 'probe_status*.json' \) -print 2>/dev/null)
[[ $found -eq 0 ]] && ok "no probe_result*.json / probe_status*.json / /tmp/.knh_probe_* files under ${SEARCH_ROOT}"

# ---------------------------------------------------------------------------
section "Running dashboard and agent programs"
# ---------------------------------------------------------------------------
if load_official_checksums; then
    HAVE_OFFICIAL=1
else
    HAVE_OFFICIAL=0
    note "could not download the official release checksums (no internet access?): binaries are listed but not compared"
fi

procs=0
# Matched by executable name: a command-line match would also catch this
# script's own shell when a log path contains "klever-node-hub".
for p in /proc/[0-9]*; do
    exe=$(readlink "$p/exe" 2>/dev/null) || continue
    base="${exe##*/}"
    base="${base% (deleted)}"
    case "$base" in
        *klever-node-hub*|*klever-agent*) ;;
        *) continue ;;
    esac
    pid="${p#/proc/}"
    hash=$(sha256_of "$p/exe")
    [[ -n "$hash" ]] || continue
    procs=$((procs + 1))
    if [[ $HAVE_OFFICIAL -eq 1 ]]; then
        match=$(official_release "$hash")
        if [[ -n "$match" ]]; then
            ok "pid $pid $exe = official release ${match}"
        else
            warn "pid $pid $exe is NOT an official release binary (sha256 ${hash:0:16}...)"
            info "expected only if you built it yourself; otherwise treat this host as compromised"
        fi
    else
        info "pid $pid $exe sha256 $hash"
    fi
done
[[ $procs -eq 0 ]] && info "no running klever-node-hub / klever-agent process found on this host"

if command -v docker >/dev/null 2>&1; then
    while read -r name image; do
        [[ -n "$name" ]] || continue
        case "$image" in
            ctjaeger/klever-node-hub*|ctjaeger/klever-agent*|ghcr.io/test0rmaik/klever-node-hub*|ghcr.io/test0rmaik/klever-agent*)
                info "container $name runs image $image" ;;
            *) note "container $name runs image $image (not an official image)" ;;
        esac
    done < <(docker ps --format '{{.Names}} {{.Image}}' 2>/dev/null | grep -iE 'klever-node-hub|klever-agent')
fi

# ---------------------------------------------------------------------------
section "Agent update history on this host"
# ---------------------------------------------------------------------------
if command -v journalctl >/dev/null 2>&1; then
    updates=$(journalctl -u klever-agent --no-pager 2>/dev/null | grep -i 'agent binary updated to' | tail -20)
    if [[ -n "$updates" ]]; then
        bad=0
        while IFS= read -r line; do
            v=$(echo "$line" | sed -n 's/.*agent binary updated to \([^ ]*\).*/\1/p')
            if is_release_version "$v"; then
                info "updated to $v"
            else
                warn "agent was updated to a non-release build: $v"
                bad=1
            fi
        done <<< "$updates"
        [[ $bad -eq 0 ]] && ok "agent journal shows release versions only"
    else
        info "no agent updates in the journal (agent not run by systemd here, or journal rotated)"
    fi
else
    info "journalctl not available"
fi

# ---------------------------------------------------------------------------
section "Dashboard data (only on the dashboard host)"
# ---------------------------------------------------------------------------
if [[ -z "$DATA_DIR" ]]; then
    # --data-dir of a running dashboard, else the default locations.
    if command -v pgrep >/dev/null 2>&1; then
        while read -r pid; do
            [[ -r "/proc/$pid/cmdline" ]] || continue
            d=$(tr '\0' '\n' < "/proc/$pid/cmdline" | awk 'prev == "--data-dir" || prev == "-data-dir" { print; exit } { prev = $0 }')
            [[ -z "$d" ]] && d=$(tr '\0' '\n' < "/proc/$pid/cmdline" | sed -n 's/^--\{0,1\}data-dir=//p' | head -1)
            [[ -n "$d" && -f "$d/dashboard.db" ]] && DATA_DIR="$d" && break
        done < <(pgrep -f 'klever-node-hub' 2>/dev/null)
    fi
    if [[ -z "$DATA_DIR" ]]; then
        for d in "$HOME/.klever-node-hub" /root/.klever-node-hub /home/*/.klever-node-hub \
                 /var/lib/docker/volumes/klever-data/_data; do
            [[ -f "$d/dashboard.db" ]] && DATA_DIR="$d" && break
        done
    fi
fi

if [[ -z "$DATA_DIR" || ! -f "$DATA_DIR/dashboard.db" ]]; then
    info "no dashboard database found - fine on a node server; on the dashboard host pass --data-dir DIR"
else
    info "data directory: $DATA_DIR"
    PY=""
    command -v python3 >/dev/null 2>&1 && PY=python3
    if [[ -n "$PY" ]]; then
        "$PY" - "$DATA_DIR/dashboard.db" > "$TMP/db.txt" 2> "$TMP/db.err" <<'PYEOF'
import json, re, sqlite3, sys, time

db = sqlite3.connect('file:' + sys.argv[1] + '?mode=ro', uri=True)


def setting(key):
    row = db.execute('select value from settings where key = ?', (key,)).fetchone()
    return row[0] if row else ''


def ts(t):
    return time.strftime('%Y-%m-%d %H:%M', time.localtime(t or 0))


passkeys = json.loads(setting('passkey_credentials') or '[]')
for p in passkeys:
    name = str(p.get('name', ''))
    flag = 'WARN' if name.lower() == 'knh-test' else 'LIST'
    print('%s\tpasskey "%s" registered %s, used %s times' % (flag, name, ts(p.get('registered_at')), p.get('sign_count')))
if not passkeys:
    print('OK\tno passkeys registered')

wallet = setting('klever_admin_address')
print('LIST\tlinked Klever wallet: %s' % (wallet or '(none)'))
print('LIST\tpassword set: %s' % ('yes' if setting('password_hash') else 'no'))

release = re.compile(r'^v?\d+\.\d+\.\d+$')
for name, version, registered in db.execute('select name, agent_version, registered_at from servers order by registered_at'):
    if version and not release.match(version):
        print('WARN\tserver "%s" reports agent version "%s" (not a release) - registered %s' % (name, version, ts(registered)))
    else:
        print('LIST\tserver "%s" agent %s, registered %s' % (name, version or '(unknown)', ts(registered)))
PYEOF
        if [[ -s "$TMP/db.txt" ]]; then
            while IFS=$'\t' read -r kind text; do
                case "$kind" in
                    WARN) warn "$text" ;;
                    OK)   ok "$text" ;;
                    *)    info "$text" ;;
                esac
            done < "$TMP/db.txt"
            note "review the passkeys, the wallet address and the servers listed above: every entry must be yours"
        else
            note "could not read the dashboard database: $(head -1 "$TMP/db.err")"
        fi
    else
        note "python3 not found: open Settings in the dashboard and check passkeys, wallet address and servers by hand"
    fi

    # Agent binaries stored for distribution to the agents.
    BIN_DIR="$DATA_DIR/agent-binaries"
    if [[ -d "$BIN_DIR" ]]; then
        stored=0
        matched=0
        for f in "$BIN_DIR"/agent-*; do
            [[ -f "$f" ]] || continue
            stored=$((stored + 1))
            hash=$(sha256_of "$f")
            base=$(basename "$f")
            if [[ $HAVE_OFFICIAL -ne 1 ]]; then
                info "stored agent binary $base sha256 $hash"
                continue
            fi
            if [[ -n "$(official_release "$hash")" ]]; then
                matched=$((matched + 1))
                continue
            fi
            # agent-<version>-<os>-<arch>
            rest="${base#agent-}"
            arch="${rest##*-}"; rest="${rest%-*}"
            os="${rest##*-}"; ver="${rest%-*}"
            mtime=$(stat -c %y "$f" 2>/dev/null | cut -d. -f1)
            want=$(official_agent_hash "$ver" "$os" "$arch")
            if [[ -n "$want" ]]; then
                warn "stored agent binary $base differs from the official $ver release (file from ${mtime:-unknown date})"
                info "sha256 ${hash:0:16}... instead of ${want:0:16}...; fine if you uploaded your own build back then"
                info "the dashboard hands stored binaries to agents: delete the file if you cannot explain it"
            else
                note "stored agent binary $base cannot be compared: no official checksum for $ver (file from ${mtime:-unknown date})"
            fi
        done
        [[ $matched -gt 0 ]] && ok "$matched stored agent binaries match official releases"
        [[ $stored -eq 0 ]] && info "no agent binaries stored"
    fi
fi

# ---------------------------------------------------------------------------
section "Dashboard log: agent versions"
# ---------------------------------------------------------------------------
log_lines=""
if command -v journalctl >/dev/null 2>&1; then
    log_lines=$(journalctl -u klever-node-hub --no-pager 2>/dev/null | grep 'version updated to')
fi
if [[ -n "$HUB_LOG" && -r "$HUB_LOG" ]]; then
    log_lines="${log_lines}"$'\n'"$(grep 'version updated to' "$HUB_LOG" 2>/dev/null)"
fi
if [[ -n "${log_lines//[$'\n ']/}" ]]; then
    bad=0
    while IFS= read -r line; do
        [[ -n "$line" ]] || continue
        v=$(echo "$line" | sed -n 's/.*version updated to \([^ ]*\).*/\1/p')
        if [[ -n "$v" ]] && ! is_release_version "$v"; then
            warn "dashboard log: an agent switched to a non-release build: ${line:0:160}"
            bad=1
        fi
    done <<< "$log_lines"
    [[ $bad -eq 0 ]] && ok "dashboard log shows release versions only"
else
    info "no agent version changes in the dashboard log (pass --hub-log FILE if it logs to a file)"
fi

# ---------------------------------------------------------------------------
echo
echo "=================================================================="
if [[ $WARNINGS -gt 0 ]]; then
    echo -e "${RED}${WARNINGS} warning(s).${RESET} If you cannot explain every one of them:"
    echo "  - treat the dashboard and ALL connected servers as compromised"
    echo "  - reinstall those servers, rotate validator (BLS) keys and move funds"
    echo "    of any wallet whose key or mnemonic was stored on them"
    echo "  - set up the dashboard with a fresh data directory (do not import an"
    echo "    old backup) and register the agents again"
    echo "  - replace SSH keys, TLS keys and passwords that were on those servers"
else
    echo -e "${GREEN}No known traces found.${RESET} ${NOTES} note(s) above to review."
    echo "This is not proof: someone with root access can remove traces."
fi
echo "Update the dashboard and the agents to the latest release in any case."
echo "=================================================================="

[[ $WARNINGS -gt 0 ]] && exit 1
exit 0
