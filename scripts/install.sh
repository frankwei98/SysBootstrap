#!/usr/bin/env bash
# install.sh — sys-bootstrap installer
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/FrankWiZe/OneLineSetup/main/scripts/install.sh | bash
#
# Supports: Debian 11+ / Ubuntu 22+

set -euo pipefail

REPO="FrankWiZe/OneLineSetup"
BINARY="sys-bootstrap"
INSTALL_DIR="/usr/local/bin"
GITHUB_API="https://api.github.com/repos/${REPO}/releases/latest"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

info()    { echo -e "${GREEN}[INFO]${NC} $*"; }
warn()    { echo -e "${YELLOW}[WARN]${NC} $*"; }
error()   { echo -e "${RED}[ERROR]${NC} $*" >&2; }
die()     { error "$@"; exit 1; }

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

# Download the latest release
download() {
    local platform="$1"
    local version url

    # Get latest tag from GitHub API, then construct direct URL
    version=$(curl -fsSL "$GITHUB_API" | grep '"tag_name"' | head -1 | sed 's/.*"tag_name": *"//' | sed 's/".*//')

    if [[ -z "$version" ]]; then
        die "Could not determine latest release version. Check https://github.com/${REPO}/releases"
    fi

    url="https://github.com/${REPO}/releases/download/${version}/${BINARY}_${platform}"
    info "Downloading ${version} from: $url"
    curl -fsSL "$url" -o "/tmp/${BINARY}"
    chmod +x "/tmp/${BINARY}"
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

    local platform
    platform=$(detect_platform)
    info "Detected platform: $platform"

    download "$platform"
    install_or_run
}

main "$@"
