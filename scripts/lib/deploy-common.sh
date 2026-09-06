# SPDX-License-Identifier: Apache-2.0
# shellcheck shell=bash
# Kairo deploy library (self-contained under scripts/lib/).

_DEPLOY_LIB_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

DEPLOY_UI_PROJECT="kairo"
DEPLOY_UI_ICON="⚡"
DEPLOY_UI_ICON_UNINSTALL="🗑️"
DEPLOY_UI_ICON_MAGIC="✨"
DEPLOY_UI_PORT="${KAIRO_PORT:-0}"
DEPLOY_UI_SCHEME="http"
DEPLOY_UI_DASH_PATH="/"
DEPLOY_UI_HEALTH_PATH="/healthz"

# shellcheck source=deploy-ui.sh
source "$_DEPLOY_LIB_DIR/deploy-ui.sh"

kairo_build_metadata() {
    local repo_dir="$1"
    KAIRO_GIT_VERSION=$(git -C "$repo_dir" describe --tags --always --dirty 2>/dev/null || echo 'dev')
    KAIRO_GIT_COMMIT=$(git -C "$repo_dir" rev-parse --short HEAD 2>/dev/null || echo 'unknown')
    export KAIRO_GIT_VERSION KAIRO_GIT_COMMIT
}

kairo_parse_target() { deploy_ui_parse_target "$@"; }
kairo_deploy_state_file() { deploy_ui_deploy_state_file "$1"; }
kairo_save_deploy_last() {
    deploy_ui_save_deploy_last "$1" "$2" "$3" "$4" "${KAIRO_GIT_VERSION:-}" "${KAIRO_GIT_COMMIT:-}"
}
kairo_load_deploy_last() { deploy_ui_load_deploy_last "$1"; }
kairo_print_success() {
    deploy_ui_success "$1" "$2" "./scripts/deploy-remote.sh $1 --uninstall"
}

kairo_info()  { deploy_ui_info "$@"; }
kairo_warn()  { deploy_ui_warn "$@"; }
kairo_error() { deploy_ui_error "$@"; }
