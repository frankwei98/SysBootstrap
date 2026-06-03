#!/usr/bin/env bash
# base.sh — 基础包安装 & 系统更新

set -euo pipefail

# 基础工具列表
BASE_PACKAGES=(
    sudo
    zsh
    gnupg
    apt-transport-https
    git
    curl
    wget
    unzip
    tree
    neovim
)

module_base() {
    log_info "=== 基础环境 ==="

    # 更新系统
    log_info "apt update & upgrade..."
    apt-get update -y
    apt-get upgrade -y
    log_success "系统更新完成"

    # 安装基础包
    log_info "安装基础工具..."
    ensure_installed "${BASE_PACKAGES[@]}"

    # 安装 zellij
    if command -v zellij &>/dev/null; then
        log_info "zellij 已安装 ($(zellij --version))，跳过"
    else
        log_info "安装 zellij ..."
        curl -fsSL https://zellij.dev/launch | bash || die "zellij 安装失败，请检查网络"
        log_success "zellij 安装完成"
    fi

    log_success "基础环境配置完成"
}
