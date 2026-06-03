#!/usr/bin/env bash
# node.sh — nvm + Node.js + pnpm + bun

set -euo pipefail

NVM_VERSION="v0.40.4"

module_node() {
    log_info "=== Node.js 环境 ==="

    # --- nvm ---
    export NVM_DIR="${NVM_DIR:-$HOME/.nvm}"

    if [[ -s "$NVM_DIR/nvm.sh" ]]; then
        log_info "nvm 已安装，跳过安装"
    else
        log_info "安装 nvm $NVM_VERSION ..."
        curl -o- "https://raw.githubusercontent.com/nvm-sh/nvm/${NVM_VERSION}/install.sh" | bash || die "nvm 安装失败，请检查网络"
        log_success "nvm 安装完成"
    fi

    # 加载 nvm 到当前 shell
    # shellcheck source=/dev/null
    [ -s "$NVM_DIR/nvm.sh" ] && source "$NVM_DIR/nvm.sh" || die "nvm 加载失败"

    # --- Node.js ---
    if command -v node &>/dev/null; then
        log_info "Node.js 已安装 ($(node -v))，跳过"
    else
        log_info "安装 Node.js LTS ..."
        nvm install --lts
        nvm use --lts
        nvm alias default lts/*
        log_success "Node.js 安装完成 ($(node -v))"
    fi

    # --- pnpm ---
    if command -v pnpm &>/dev/null; then
        log_info "pnpm 已安装 ($(pnpm -v))，跳过"
    else
        log_info "安装 pnpm ..."
        curl -fsSL https://get.pnpm.io/install.sh | sh - || die "pnpm 安装失败，请检查网络"
        # 加载 pnpm 到当前 shell
        export PNPM_HOME="${HOME}/.local/share/pnpm"
        export PATH="$PNPM_HOME:$PATH"
        log_success "pnpm 安装完成 ($(pnpm -v))"
    fi

    # --- bun ---
    if command -v bun &>/dev/null; then
        log_info "bun 已安装 ($(bun -v))，跳过"
    else
        log_info "安装 bun ..."
        curl -fsSL https://bun.sh/install | bash || die "bun 安装失败，请检查网络"
        # 加载 bun 到当前 shell
        export BUN_INSTALL="${HOME}/.bun"
        export PATH="$BUN_INSTALL/bin:$PATH"
        log_success "bun 安装完成 ($(bun -v))"
    fi

    log_success "Node.js 环境配置完成"
}
