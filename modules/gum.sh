#!/usr/bin/env bash
# gum.sh — 安装 Charmbracelet Gum (终端 UI 组件)

set -euo pipefail

CHARM_REPO_URL="https://repo.charm.sh/apt"
CHARM_KEY_URL="https://repo.charm.sh/apt/gpg.key"
CHARM_KEYRING="/etc/apt/keyrings/charm.gpg"
CHARM_LIST="/etc/apt/sources.list.d/charm.list"

module_gum() {
    log_info "=== 安装 Gum ==="

    # 已安装则跳过
    if command -v gum &>/dev/null; then
        log_success "gum 已安装 ($(gum --version))，跳过"
        return 0
    fi

    # 添加 Charmbracelet apt 仓库 (幂等)
    if [[ ! -f "$CHARM_LIST" ]]; then
        log_info "添加 Charmbracelet apt 仓库..."
        mkdir -p /etc/apt/keyrings
        curl -fsSL "$CHARM_KEY_URL" | gpg --dearmor -o "$CHARM_KEYRING"
        echo "deb [signed-by=$CHARM_KEYRING] $CHARM_REPO_URL * *" > "$CHARM_LIST"
        apt-get update -y
        log_success "仓库添加完成"
    fi

    # 安装 gum
    ensure_installed gum

    log_success "Gum 安装完成 ($(gum --version))"
}
