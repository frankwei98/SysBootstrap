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

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

info()    { echo -e "${GREEN}[INFO]${NC} $*"; }
warn()    { echo -e "${YELLOW}[WARN]${NC} $*"; }
error()   { echo -e "${RED}[ERROR]${NC} $*" >&2; }
die()     { error "$@"; exit 1; }

choose_region() {
    local choice

    echo ""
    echo "Download region:"
    echo "  1) Overseas (default)"
    echo "  2) China mainland (prefer mirror/proxy CDN)"
    echo ""
    read -rp "Selection [1/2]: " choice

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
            apt-get update -qq && apt-get install -y -qq curl ca-certificates
        else
            die "curl is required. Please install it manually."
        fi
    fi
}

resolve_version() {
    local version raw_version

    if [[ "$REGION" == "china" ]]; then
        raw_version=$(
            curl -fsSL "$JSDELIVR_API" \
                | sed -n '/"versions"/,/\]/p' \
                | grep -o '"[^"]*"' \
                | sed -n '2p' \
                | tr -d '"'
        )
        version="v${raw_version}"
    else
        version=$(curl -fsSL "$GITHUB_API" | grep '"tag_name"' | head -1 | sed 's/.*"tag_name": *"//' | sed 's/".*//')
    fi

    [[ -n "${version:-}" ]] || die "Could not determine latest release version."
    echo "$version"
}

download_from_url() {
    local url="$1"
    local tmp_file="/tmp/${BINARY}.download"

    rm -f "$tmp_file"
    info "Downloading from: $url"
    curl -fsSL "$url" -o "$tmp_file" || return 1
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
    read -rp "Continue without verification? [y/N]: " choice
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
    echo "sys-bootstrap installer"
    echo "======================="
    echo ""
    echo "Choose installation method:"
    echo "  1) Run temporarily (download and execute)"
    echo "  2) Install to ${INSTALL_DIR} (requires sudo)"
    echo ""
    read -rp "Selection [1/2]: " choice

    case "$choice" in
        1)
            info "Running sys-bootstrap..."
            exec "/tmp/${BINARY}"
            ;;
        2)
            if [[ $EUID -ne 0 ]]; then
                warn "Installing to ${INSTALL_DIR} requires root."
                info "Re-running with sudo..."
                sudo cp "/tmp/${BINARY}" "${INSTALL_DIR}/${BINARY}"
            else
                cp "/tmp/${BINARY}" "${INSTALL_DIR}/${BINARY}"
            fi
            info "Installed to ${INSTALL_DIR}/${BINARY}"
            info "Run: sys-bootstrap --help"
            ;;
        *)
            die "Invalid selection"
            ;;
    esac
}

main() {
    check_deps
    choose_region

    local platform
    platform=$(detect_platform)
    info "Detected platform: $platform"

    download "$platform"
    install_or_run
}

main "$@"
