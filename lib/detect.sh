#!/usr/bin/env bash
# detect.sh — 系统检测：发行版、版本号、兼容性校验

set -euo pipefail

# 检测结果变量
OS_ID=""
OS_VERSION=""
OS_VERSION_MAJOR=""
OS_CODENAME=""

detect_os() {
    if [[ ! -f /etc/os-release ]]; then
        die "无法检测系统: /etc/os-release 不存在"
    fi

    # 用 local 避免污染全局变量
    local ID VERSION_ID VERSION_CODENAME
    # shellcheck source=/dev/null
    . /etc/os-release

    OS_ID="${ID:-unknown}"
    OS_VERSION="${VERSION_ID:-unknown}"
    OS_CODENAME="${VERSION_CODENAME:-unknown}"

    # 取主版本号 (22.04 -> 22, 12 -> 12)
    OS_VERSION_MAJOR="${OS_VERSION%%.*}"

    log_info "检测到系统: ${OS_ID} ${OS_VERSION} (${OS_CODENAME})"
}

# 校验是否为支持的系统
validate_os() {
    detect_os

    case "$OS_ID" in
        debian)
            if [[ "$OS_VERSION_MAJOR" -lt 11 ]]; then
                die "需要 Debian 11+，当前版本: ${OS_VERSION}"
            fi
            ;;
        ubuntu)
            if [[ "$OS_VERSION_MAJOR" -lt 22 ]]; then
                die "需要 Ubuntu 22+，当前版本: ${OS_VERSION}"
            fi
            ;;
        *)
            die "不支持的系统: ${OS_ID} (仅支持 Debian 11+ / Ubuntu 22+)"
            ;;
    esac

    log_success "系统兼容性检查通过"
}
