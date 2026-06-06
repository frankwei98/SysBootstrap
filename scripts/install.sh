#!/usr/bin/env bash
# install.sh — sys-bootstrap installer
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/frankwei98/SysBootstrap/main/scripts/install.sh | bash
#
# Supports: Debian 11+ / Ubuntu 22+

set -euo pipefail

REPO="frankwei98/SysBootstrap"
BINARY="sys-bootstrap"
INSTALL_DIR="/usr/local/bin"
GITHUB_API="https://api.github.com/repos/${REPO}/releases/latest"
JSDELIVR_API="https://data.jsdelivr.com/v1/package/gh/${REPO}"
REGION="overseas"
VERSION=""
LANG_CHOICE="en"
APT_MIRROR=""

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

info()    { echo -e "${GREEN}[INFO]${NC} $*" >&2; }
warn()    { echo -e "${YELLOW}[WARN]${NC} $*" >&2; }
error()   { echo -e "${RED}[ERROR]${NC} $*" >&2; }
die()     { error "$@"; exit 1; }

has_tty() {
    [[ -r /dev/tty && -w /dev/tty ]]
}

prompt_read() {
    local __var_name="$1"
    local __prompt="$2"
    local __value=""

    if has_tty; then
        read -r -p "$__prompt" __value < /dev/tty
    else
        read -r -p "$__prompt" __value
    fi

    printf -v "$__var_name" '%s' "$__value"
}

require_tty() {
    local message="${1:-This installer requires an interactive terminal.}"
    has_tty || die "$message"
}

run_with_tty() {
    if has_tty; then
        exec < /dev/tty
    fi
    exec "$@"
}

can_use_sudo() {
    command -v sudo &>/dev/null
}

can_run_as_root() {
    [[ $EUID -eq 0 ]] || can_use_sudo
}

root_access_label() {
    if [[ $EUID -eq 0 ]]; then
        if [[ "$LANG_CHOICE" == "zh-CN" ]]; then
            echo "已具备（当前为 root）"
        else
            echo "granted (running as root)"
        fi
    elif can_use_sudo; then
        if [[ "$LANG_CHOICE" == "zh-CN" ]]; then
            echo "可尝试（检测到 sudo，可能需要认证）"
        else
            echo "can attempt via sudo (may require authentication)"
        fi
    else
        if [[ "$LANG_CHOICE" == "zh-CN" ]]; then
            echo "不可用（未找到 sudo）"
        else
            echo "unavailable (sudo not found)"
        fi
    fi
}

root_required_label() {
    if [[ $EUID -eq 0 ]]; then
        if [[ "$LANG_CHOICE" == "zh-CN" ]]; then
            echo "需要 root：当前为 root"
        else
            echo "requires root: running as root"
        fi
    elif can_use_sudo; then
        if [[ "$LANG_CHOICE" == "zh-CN" ]]; then
            echo "需要 root：将尝试 sudo"
        else
            echo "requires root: will attempt sudo"
        fi
    else
        if [[ "$LANG_CHOICE" == "zh-CN" ]]; then
            echo "需要 root：不可用，未找到 sudo"
        else
            echo "requires root: unavailable, sudo not found"
        fi
    fi
}

run_as_root() {
    if [[ $EUID -eq 0 ]]; then
        "$@"
        return
    fi
    if can_use_sudo; then
        sudo "$@"
        return
    fi
    if [[ "$LANG_CHOICE" == "zh-CN" ]]; then
        die "此操作需要 root 权限，但当前用户不是 root，且未找到 sudo。"
    else
        die "This operation requires root, but the current user is not root and sudo was not found."
    fi
}

# --- Language Selection ---
choose_language() {
    local choice

    require_tty "This installer requires an interactive terminal when run via curl | bash."
    echo ""
    echo "Language / 语言:"
    echo "  1) English (default)"
    echo "  2) 中文"
    echo ""
    prompt_read choice "Selection / 选择 [1/2]: "

    case "${choice:-1}" in
        1)
            LANG_CHOICE="en"
            ;;
        2)
            LANG_CHOICE="zh-CN"
            ;;
        *)
            die "Invalid selection"
            ;;
    esac
}

# --- APT Mirror ---
ask_apt_mirror() {
    local choice

    echo ""
    if [[ "$LANG_CHOICE" == "zh-CN" ]]; then
        echo "是否切换 APT 镜像到 CERNET（mirrors.cernet.edu.cn）？"
        echo "  仅影响 Debian/Ubuntu 官方 APT 源，不影响 nvm/bun/npm 等其他下载源。"
        echo "  安全更新源保持不变。"
    else
        echo "Switch APT mirror to CERNET (mirrors.cernet.edu.cn)?"
        echo "  Only affects Debian/Ubuntu official APT sources."
        echo "  Security sources remain unchanged."
    fi
    echo ""
    prompt_read choice "[y/N]: "

    case "${choice:-N}" in
        [Yy]*)
            APT_MIRROR="cernet"
            info "APT 镜像将切换到 CERNET / APT mirror will be switched to CERNET"
            ;;
        *)
            info "保持默认 APT 源 / Using default APT sources"
            ;;
    esac
}

# --- Region Selection ---
choose_region() {
    local choice

    echo ""
    if [[ "$LANG_CHOICE" == "zh-CN" ]]; then
        echo "下载区域："
        echo "  1) 海外（默认）"
        echo "  2) 中国大陆（优先使用镜像/代理 CDN）"
        echo ""
        prompt_read choice "选择 [1/2]: "
    else
        echo "Download region:"
        echo "  1) Overseas (default)"
        echo "  2) China mainland (prefer mirror/proxy CDN)"
        echo ""
        prompt_read choice "Selection [1/2]: "
    fi

    case "${choice:-1}" in
        1)
            REGION="overseas"
            ;;
        2)
            REGION="china"
            ;;
        *)
            die "Invalid selection"
            ;;
    esac

    info "Using region: ${REGION}"
}

# Detect OS and architecture
detect_platform() {
    local os arch

    case "$(uname -s)" in
        Linux*)  os="linux" ;;
        *)       die "Unsupported OS: $(uname -s). Only Linux is supported." ;;
    esac

    case "$(uname -m)" in
        x86_64|amd64)  arch="amd64" ;;
        aarch64|arm64) arch="arm64" ;;
        *)             die "Unsupported architecture: $(uname -m)" ;;
    esac

    echo "${os}_${arch}"
}

# Check for required tools
check_deps() {
    if ! command -v curl &>/dev/null; then
        warn "curl not found, attempting to install..."
        if command -v apt-get &>/dev/null; then
            run_as_root apt-get update -qq
            run_as_root apt-get install -y -qq curl ca-certificates
        else
            die "curl is required. Please install it manually."
        fi
    fi
}

resolve_version() {
    local version github_version raw_version

    if [[ "$REGION" == "china" ]]; then
        raw_version=$(
            curl -fsSL "$JSDELIVR_API" \
                | sed -n '/"versions"/,/\]/p' \
                | grep -o '"[^"]*"' \
                | tail -1 \
                | tr -d '"'
        )
        if [[ -n "${raw_version:-}" ]]; then
            version="v${raw_version}"
        fi
    fi

    github_version=$(curl -fsSL "$GITHUB_API" | grep '"tag_name"' | head -1 | sed 's/.*"tag_name": *"//' | sed 's/".*//')
    [[ -n "${github_version:-}" ]] || die "Could not determine latest release version from GitHub API."

    if [[ "$REGION" == "china" && -n "${version:-}" && "$version" != "$github_version" ]]; then
        warn "jsDelivr metadata is stale (${version}); using GitHub latest ${github_version}"
    fi
    version="$github_version"

    [[ -n "${version:-}" ]] || die "Could not determine latest release version."
    echo "$version"
}

download_from_url() {
    local url="$1"
    local tmp_file="/tmp/${BINARY}.download"

    rm -f "$tmp_file"
    info "Downloading from: $url"
    curl -fsSL "$url" -o "$tmp_file" || return 1
    [[ -s "$tmp_file" ]] || return 1
    chmod +x "$tmp_file"
    mv "$tmp_file" "/tmp/${BINARY}"
}

sha256_command() {
    if command -v sha256sum &>/dev/null; then
        echo "sha256sum"
        return 0
    fi
    if command -v shasum &>/dev/null; then
        echo "shasum -a 256"
        return 0
    fi
    return 1
}

file_sha256() {
    local file="$1"
    local cmd

    cmd=$(sha256_command) || return 1
    $cmd "$file" | awk '{print $1}'
}

confirm_unverified_continue() {
    local actual_hash="$1"
    local choice

    warn "Could not fetch checksum file from GitHub release; unable to verify download."
    warn "Downloaded file SHA256: ${actual_hash}"
    warn "Check the expected checksum on https://github.com/${REPO}/releases/tag/${VERSION} if needed."
    echo ""
    prompt_read choice "Continue without verification? [y/N]: "
    [[ "$choice" =~ ^[Yy]$ ]]
}

verify_download() {
    local platform="$1"
    local checksum_url expected_hash actual_hash
    local tmp_checksum="/tmp/${BINARY}.sha256"

    actual_hash=$(file_sha256 "/tmp/${BINARY}") || {
        warn "No SHA256 tool available (sha256sum/shasum). Skipping verification."
        return 0
    }

    checksum_url="https://github.com/${REPO}/releases/download/${VERSION}/${BINARY}_${platform}.sha256"
    rm -f "$tmp_checksum"
    if ! curl -fsSL "$checksum_url" -o "$tmp_checksum"; then
        if confirm_unverified_continue "$actual_hash"; then
            return 0
        fi
        die "Aborted because the downloaded file could not be verified."
    fi

    expected_hash=$(awk '{print $1}' "$tmp_checksum")
    [[ -n "${expected_hash:-}" ]] || die "Checksum file is empty or malformed: ${checksum_url}"

    if [[ "$actual_hash" != "$expected_hash" ]]; then
        error "Checksum mismatch for /tmp/${BINARY}"
        error "Expected: ${expected_hash}"
        error "Actual:   ${actual_hash}"
        die "Refusing to continue with an untrusted binary."
    fi

    info "Checksum verified: ${actual_hash}"
}

download() {
    local platform="$1"
    local github_url jsdelivr_url ghproxy_url ghfast_url

    VERSION=$(resolve_version)
    github_url="https://github.com/${REPO}/releases/download/${VERSION}/${BINARY}_${platform}"

    if [[ "$REGION" == "china" ]]; then
        ghproxy_url="https://gh-proxy.org/${github_url}"
        ghfast_url="https://ghfast.top/${github_url}"
        jsdelivr_url="https://cdn.jsdelivr.net/gh/${REPO}@releases/download/${VERSION}/${BINARY}_${platform}"

        if download_from_url "$ghproxy_url"; then
            :
        elif download_from_url "$ghfast_url"; then
            :
        elif download_from_url "$jsdelivr_url"; then
            :
        else
            warn "China mirrors/proxies failed, falling back to GitHub release"
            download_from_url "$github_url"
        fi
    else
        download_from_url "$github_url"
    fi

    verify_download "$platform"
}

# Install or run
install_or_run() {
    local choice

    echo ""
    if [[ "$LANG_CHOICE" == "zh-CN" ]]; then
        echo "sys-bootstrap 安装程序"
        echo "======================="
        echo ""
        echo "选择安装方式："
        echo "  1) 临时运行（$(root_required_label)）"
        echo "  2) 安装到 ${INSTALL_DIR}（$(root_required_label)）"
        echo ""
        echo "Root 权限：$(root_access_label)"
        echo ""
        prompt_read choice "选择 [1/2]: "
    else
        echo "sys-bootstrap installer"
        echo "======================="
        echo ""
        echo "Choose installation method:"
        echo "  1) Run temporarily ($(root_required_label))"
        echo "  2) Install to ${INSTALL_DIR} ($(root_required_label))"
        echo ""
        echo "Root access: $(root_access_label)"
        echo ""
        prompt_read choice "Selection [1/2]: "
    fi

    # Build environment variables for the binary
    local env_args=()
    if [[ "$LANG_CHOICE" == "zh-CN" ]]; then
        env_args+=("SYS_BOOTSTRAP_LANG=zh-CN")
    fi
    if [[ -n "$APT_MIRROR" ]]; then
        env_args+=("SYS_BOOTSTRAP_APT_MIRROR=${APT_MIRROR}")
    fi

    case "$choice" in
        1)
            if ! can_run_as_root; then
                if [[ "$LANG_CHOICE" == "zh-CN" ]]; then
                    die "临时运行需要 root 权限，但当前无法使用 sudo。"
                else
                    die "Running temporarily requires root, but sudo is not available."
                fi
            fi
            if [[ "$LANG_CHOICE" == "zh-CN" ]]; then
                if [[ $EUID -eq 0 ]]; then
                    info "正在运行 sys-bootstrap..."
                else
                    info "正在使用 sudo 临时运行 sys-bootstrap..."
                fi
            else
                if [[ $EUID -eq 0 ]]; then
                    info "Running sys-bootstrap..."
                else
                    info "Running sys-bootstrap temporarily with sudo..."
                fi
            fi
            if [[ ${#env_args[@]} -gt 0 ]]; then
                export "${env_args[@]}"
            fi
            if [[ $EUID -eq 0 ]]; then
                run_with_tty "/tmp/${BINARY}"
            else
                run_with_tty sudo env "${env_args[@]}" "/tmp/${BINARY}"
            fi
            ;;
        2)
            if ! can_run_as_root; then
                if [[ "$LANG_CHOICE" == "zh-CN" ]]; then
                    die "安装到 ${INSTALL_DIR} 需要 root 权限，但当前无法使用 sudo。"
                else
                    die "Installing to ${INSTALL_DIR} requires root, but sudo is not available."
                fi
            fi
            if [[ $EUID -ne 0 ]]; then
                if [[ "$LANG_CHOICE" == "zh-CN" ]]; then
                    info "正在使用 sudo 安装到 ${INSTALL_DIR}..."
                else
                    info "Installing to ${INSTALL_DIR} with sudo..."
                fi
            fi
            run_as_root cp "/tmp/${BINARY}" "${INSTALL_DIR}/${BINARY}"

            # Persist settings to system config
            local config_dir="/etc/sys-bootstrap"
            local config_file="${config_dir}/config.env"
            local config_content="lang=${LANG_CHOICE}"
            if [[ -n "$APT_MIRROR" ]]; then
                config_content="${config_content}
apt_mirror=${APT_MIRROR}"
            else
                config_content="${config_content}
apt_mirror=default"
            fi
            run_as_root mkdir -p "${config_dir}"
            local tmp_config
            tmp_config=$(mktemp "/tmp/sys-bootstrap-config.XXXXXX") || die "Failed to create temporary config file."
            trap 'rm -f "${tmp_config}"' RETURN
            printf '%s\n' "${config_content}" > "${tmp_config}"
            run_as_root cp "${tmp_config}" "${config_file}"
            run_as_root chmod 0644 "${config_file}"
            rm -f "${tmp_config}"
            trap - RETURN

            if [[ "$LANG_CHOICE" == "zh-CN" ]]; then
                info "已安装到 ${INSTALL_DIR}/${BINARY}"
                info "设置已保存到 ${config_file}"
                info "运行：sys-bootstrap --help"
            else
                info "Installed to ${INSTALL_DIR}/${BINARY}"
                info "Settings saved to ${config_file}"
                info "Run: sys-bootstrap --help"
            fi
            ;;
        *)
            die "Invalid selection"
            ;;
    esac
}

main() {
    choose_language
    check_deps
    choose_region
    ask_apt_mirror

    local platform
    platform=$(detect_platform)
    if [[ "$LANG_CHOICE" == "zh-CN" ]]; then
        info "检测到平台：$platform"
    else
        info "Detected platform: $platform"
    fi

    download "$platform"
    install_or_run
}

main "$@"
