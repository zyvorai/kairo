#!/usr/bin/env bash
# Copyright 2026 Zyvor AI Labs
# SPDX-License-Identifier: Apache-2.0
# ============================================================================
# deploy-remote.sh — Deploy Kairo to a remote host as a systemd service
# ============================================================================
# Kairo builds as a single static binary (CGO_ENABLED=0), so deployment needs
# no remote toolchain:
#   1. Detect the remote OS/arch over SSH
#   2. Cross-compile ./cmd/kairo for that target, locally
#   3. Copy the binary + demo examples + env file + systemd unit
#   4. Enable/start kairo.service, open the firewall port
#   5. Verify with scripts/smoke-remote.sh (health + dashboard + simulate)
#
# Usage:
#   ./scripts/deploy-remote.sh <host> [user] [password] [options]
#   ./scripts/deploy-remote.sh 212.8.248.187 sus                 # SSH key auth
#   ./scripts/deploy-remote.sh 212.8.248.187 sus mypassword      # SSH password auth
#   ./scripts/deploy-remote.sh 212.8.248.187 sus --uninstall
#   ./scripts/deploy-remote.sh 212.8.248.187 sus --dry-run
#
# Options:
#   --uninstall   Stop kairo.service and remove it + the binary from the host
#   --dry-run     Print what would happen; make no changes
#   --skip-smoke  Skip the smoke-remote.sh step at the end
#   --verbose     Show full remote command output
#
# Environment variables:
#   DEPLOY_HOST, DEPLOY_USER, DEPLOY_PASS   — same as the positional args
#   KAIRO_PORT                               — listen/health-check port (default 8080)
# ============================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
# shellcheck source=lib/deploy-common.sh
source "$SCRIPT_DIR/lib/deploy-common.sh"

info()  { kairo_info "$@"; }
warn()  { kairo_warn "$@"; }
error() { kairo_error "$@"; }
step()  { LAST_ACTION="$*"; deploy_ui_step_start "$*"; }

REMOTE_BIN=/usr/local/bin/kairo
REMOTE_DIR=/etc/kairo
REMOTE_ENV=/etc/kairo/kairo.env
REMOTE_EXAMPLES=/etc/kairo/examples
REMOTE_UNIT=/etc/systemd/system/kairo.service
KAIRO_PORT="${KAIRO_PORT:-8080}"

# ── Parse args ──
UNINSTALL_MODE=false
DRY_RUN=false
SKIP_SMOKE=false
VERBOSE=false
POSITIONAL=()
for arg in "$@"; do
    case "$arg" in
        --uninstall)  UNINSTALL_MODE=true ;;
        --dry-run)    DRY_RUN=true ;;
        --skip-smoke) SKIP_SMOKE=true ;;
        --verbose)    VERBOSE=true ;;
        --help|-h)
            sed -n '2,36p' "$0" | sed 's/^# \{0,1\}//'
            exit 0
            ;;
        *) POSITIONAL+=("$arg") ;;
    esac
done

HOST="${POSITIONAL[0]:-${DEPLOY_HOST:-}}"
USER="${POSITIONAL[1]:-${DEPLOY_USER:-root}}"
PASS="${POSITIONAL[2]:-${DEPLOY_PASS:-}}"

kairo_parse_target HOST USER
if [ -z "$HOST" ] && kairo_load_deploy_last "$REPO_DIR"; then
    info "Using .deploy-last → ${USER}@${HOST}"
fi
[ -z "$HOST" ] && error "Usage: $0 <host> [user] [password] [options]  (see --help)"

[ -f "$REPO_DIR/go.mod" ] || error "Not in the kairo repo: $REPO_DIR"
[ -d "$REPO_DIR/examples" ] || error "examples/ missing in $REPO_DIR"
kairo_build_metadata "$REPO_DIR"
DEPLOY_UI_PORT="$KAIRO_PORT"

SUDO=""
[ "$USER" != "root" ] && SUDO="sudo"

DEPLOY_SSH_OPTS=(
    -o StrictHostKeyChecking=no
    -o ConnectTimeout=15
    -o ServerAliveInterval=15
    -o ServerAliveCountMax=8
)
DEPLOY_SSH_TTY_OPTS=()
[ "$USER" != "root" ] && DEPLOY_SSH_TTY_OPTS=(-tt)

if [ -n "$PASS" ] && ! command -v sshpass &>/dev/null; then
    error "sshpass required for password auth (brew install sshpass / dnf install sshpass)"
fi

_ssh() {
    local -a ssh_args=("${DEPLOY_SSH_OPTS[@]}" "${DEPLOY_SSH_TTY_OPTS[@]}")
    if [ -n "$PASS" ]; then
        SSHPASS="$PASS" sshpass -e ssh "${ssh_args[@]}" "${USER}@${HOST}" "$@"
    else
        ssh "${ssh_args[@]}" "${USER}@${HOST}" "$@"
    fi
}

_ssh_batch() {
    local -a ssh_args=("${DEPLOY_SSH_OPTS[@]}")
    if [ -n "$PASS" ]; then
        SSHPASS="$PASS" sshpass -e ssh "${ssh_args[@]}" "${USER}@${HOST}" "$@"
    else
        ssh "${ssh_args[@]}" "${USER}@${HOST}" "$@"
    fi
}

_scp() {
    local -a scp_args=("${DEPLOY_SSH_OPTS[@]}")
    if [ -n "$PASS" ]; then
        SSHPASS="$PASS" sshpass -e scp "${scp_args[@]}" "$@"
    else
        scp "${scp_args[@]}" "$@"
    fi
}

if $DRY_RUN; then
    deploy_ui_banner "${DEPLOY_UI_ICON_MAGIC} Dry run" "no changes will be made"
    deploy_ui_kv "🎯" "Target" "${USER}@${HOST}"
    deploy_ui_kv "📦" "Binary" "$REMOTE_BIN"
    deploy_ui_kv "📄" "Examples" "$REMOTE_EXAMPLES"
    deploy_ui_kv "📄" "Env file" "$REMOTE_ENV (refreshed each deploy)"
    deploy_ui_kv "⚙️" "Unit" "$REMOTE_UNIT"
    echo ""
    deploy_ui_note "Would: detect arch → cross-compile locally → scp binary+examples+unit → enable/start → smoke-remote.sh"
    echo ""
    exit 0
fi

deploy_ui_banner "Remote Deploy" "${KAIRO_GIT_VERSION} (${KAIRO_GIT_COMMIT}) → ${USER}@${HOST}"
deploy_ui_kv "🎯" "Target" "${USER}@${HOST}"
deploy_ui_kv "🔐" "Auth" "$([ -n "$PASS" ] && echo 'password' || echo 'SSH key')"
deploy_ui_kv "🌐" "Port" "$KAIRO_PORT"
echo ""

# ── Uninstall mode ──
if $UNINSTALL_MODE; then
    deploy_ui_uninstall_banner
    step "Uninstalling kairo from ${HOST}"
    _ssh "
        $SUDO systemctl disable --now kairo.service 2>/dev/null || true
        $SUDO rm -f $REMOTE_BIN $REMOTE_UNIT
        $SUDO rm -rf /etc/kairo
        $SUDO systemctl daemon-reload 2>/dev/null || true
    "
    info "kairo removed from ${HOST}"
    exit 0
fi

# ── Step 1: detect remote OS/arch ──
step "Detecting remote architecture"
REMOTE_ARCH_RAW=$(_ssh_batch "uname -m" | tr -d '\r')
case "$REMOTE_ARCH_RAW" in
    x86_64)         GOARCH=amd64 ;;
    aarch64|arm64)  GOARCH=arm64 ;;
    *) error "Unsupported remote architecture: $REMOTE_ARCH_RAW" ;;
esac
info "Remote: linux/${GOARCH}"

# ── Step 2: cross-compile locally ──
step "Cross-compiling kairo for linux/${GOARCH}"
BUILD_DIR="$(mktemp -d)"
trap 'rm -rf "$BUILD_DIR"' EXIT
(
    cd "$REPO_DIR"
    CGO_ENABLED=0 GOOS=linux GOARCH="$GOARCH" \
        go build -trimpath -ldflags="-s -w" -o "$BUILD_DIR/kairo" ./cmd/kairo
)
info "Built $BUILD_DIR/kairo"

# ── Step 3: install binary + examples + env + unit ──
step "Installing kairo on ${HOST}"
tar -C "$REPO_DIR" -czf "$BUILD_DIR/examples.tgz" examples
_ssh "$SUDO mkdir -p $REMOTE_DIR"
_scp "$BUILD_DIR/kairo" "${USER}@${HOST}:/tmp/kairo.new"
_scp "$BUILD_DIR/examples.tgz" "${USER}@${HOST}:/tmp/kairo-examples.tgz"
_ssh "
    $SUDO install -m 755 /tmp/kairo.new $REMOTE_BIN && rm -f /tmp/kairo.new
    $SUDO tar -C $REMOTE_DIR -xzf /tmp/kairo-examples.tgz
    rm -f /tmp/kairo-examples.tgz
"

cat > "$BUILD_DIR/kairo.env" <<ENVEOF
KAIRO_ADDR=0.0.0.0:${KAIRO_PORT}
ENVEOF
_scp "$BUILD_DIR/kairo.env" "${USER}@${HOST}:/tmp/kairo.env.new"
# Refresh listen address on every deploy so KAIRO_PORT changes take effect.
_ssh "
    $SUDO install -m 640 /tmp/kairo.env.new $REMOTE_ENV
    rm -f /tmp/kairo.env.new
"

_scp "$REPO_DIR/systemd/kairo.service" "${USER}@${HOST}:/tmp/kairo.service.new"
_ssh "$SUDO install -m 644 /tmp/kairo.service.new $REMOTE_UNIT && rm -f /tmp/kairo.service.new"
info "Binary + examples + config + systemd unit installed"

# ── Step 4: enable/start service, open firewall ──
step "Starting kairo.service"
_ssh "
    $SUDO systemctl daemon-reload
    $SUDO systemctl enable --now kairo.service
    $SUDO systemctl restart kairo.service
    if command -v firewall-cmd &>/dev/null; then
        $SUDO firewall-cmd --permanent --add-port=${KAIRO_PORT}/tcp 2>/dev/null || true
        $SUDO firewall-cmd --reload 2>/dev/null || true
    elif command -v ufw &>/dev/null; then
        $SUDO ufw allow ${KAIRO_PORT}/tcp 2>/dev/null || true
    fi
    sleep 1
    if $SUDO systemctl is-active kairo.service &>/dev/null; then
        echo 'kairo.service: running'
    else
        echo 'kairo.service: FAILED TO START'
        $SUDO journalctl -u kairo.service --no-pager -n 20
        exit 1
    fi
"
info "kairo.service active"

# ── Step 5: verify ──
step "Verifying deployment"
BASE_URL="http://${HOST}:${KAIRO_PORT}"
DEPLOY_UI_SCHEME="http"
_ssh "curl -fsS http://127.0.0.1:${KAIRO_PORT}/healthz >/dev/null" \
    && info "Health check OK (http://127.0.0.1:${KAIRO_PORT}/healthz, on-host)"

kairo_save_deploy_last "$REPO_DIR" "$HOST" "$USER" "full"

deploy_ui_highlight "📋 Final checklist"
deploy_ui_checklist "service" "$(_ssh_batch "$SUDO systemctl is-active kairo.service" | tr -d '\r')"
deploy_ui_checklist "health"  "$(_ssh_batch "curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:${KAIRO_PORT}/healthz" | tr -d '\r')"

kairo_print_success "$HOST" 0

# ── Step 6: smoke from the workstation against the remote URL ──
if $SKIP_SMOKE; then
    info "Skipped smoke-remote.sh (--skip-smoke)"
else
    step "Running scripts/smoke-remote.sh against ${BASE_URL}"
    ( cd "$REPO_DIR" && KAIRO_URL="$BASE_URL" ./scripts/smoke-remote.sh )
fi
