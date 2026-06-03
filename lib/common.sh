#!/usr/bin/env bash
# common.sh — 通用函数：日志、错误处理、确认提示

set -euo pipefail

# 颜色定义
readonly RED='\033[0;31m'
readonly GREEN='\033[0;32m'
readonly YELLOW='\033[1;33m'
readonly BLUE='\033[0;34m'
readonly NC='\033[0m' # No Color

# 日志函数
log_info()    { echo -e "${BLUE}[INFO]${NC}  $*"; }
log_success() { echo -e "${GREEN}[OK]${NC}    $*"; }
log_warn()    { echo -e "${YELLOW}[WARN]${NC}  $*"; }
log_error()   { echo -e "${RED}[ERROR]${NC} $*" >&2; }

# 致命错误，退出
die() {
    log_error "$@"
    exit 1
}

# 检查是否为 root
require_root() {
    [[ $EUID -eq 0 ]] || die "此脚本需要 root 权限运行，请使用 sudo"
}

# 确认提示 (默认 yes)
confirm() {
    local prompt="${1:-继续执行?}"
    read -rp "$prompt [Y/n] " answer
    [[ -z "$answer" || "$answer" =~ ^[Yy]$ ]]
}

# 幂等安装 apt 包
ensure_installed() {
    local to_install=()
    for pkg in "$@"; do
        if ! dpkg -s "$pkg" &>/dev/null; then
            to_install+=("$pkg")
        fi
    done
    if [[ ${#to_install[@]} -gt 0 ]]; then
        log_info "安装: ${to_install[*]}"
        apt-get install -y "${to_install[@]}"
        log_success "安装完成: ${to_install[*]}"
    else
        log_info "已安装，跳过: $*"
    fi
}
