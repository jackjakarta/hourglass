#!/bin/sh
# gocron installer — curl -fsSL https://gocron.jackjakarta.xyz/install.sh | sh
#
# Environment variables:
#   GOCRON_VERSION      — specific version to install (e.g. v0.1.0), default: latest
#   GOCRON_INSTALL_DIR  — custom install directory, default: /usr/local/bin or ~/.local/bin
#   GH_TOKEN            — GitHub token, if you hit API rate limits (also accepts GITHUB_TOKEN)

set -e

REPO="jackjakarta/gocron"
RELEASES_URL="${GOCRON_RELEASES_URL:-https://github.com/${REPO}/releases}"

main() {
    setup_colors
    check_dependencies
    setup_auth
    detect_platform
    resolve_version

    TMPDIR=$(mktemp -d)
    trap 'rm -rf "$TMPDIR"' EXIT INT TERM

    download
    verify_checksum
    install_binary

    printf "\n"
    printf "%s  gocron %s installed successfully!%s\n" "$GREEN" "$VERSION" "$RESET"
    printf "\n"
    printf "  Location: %s\n" "$INSTALL_PATH"
    printf "  Version:  %s\n" "$("$INSTALL_PATH" version 2>/dev/null || printf '%s' "$VERSION")"
    printf "\n"
    printf "  Check your cron setup, then open the TUI:\n"
    printf "\n"
    printf "    $ gocron doctor\n"
    printf "    $ gocron\n"
    printf "\n"
    printf "  Tracked jobs call this binary by its full path. When you upgrade,\n"
    printf "  install to the same place (or run %sgocron doctor --fix%s afterwards).\n" "$BOLD" "$RESET"
    printf "\n"
}

setup_colors() {
    if [ -t 1 ]; then
        RED='\033[0;31m'
        GREEN='\033[0;32m'
        YELLOW='\033[0;33m'
        BLUE='\033[0;34m'
        BOLD='\033[1m'
        RESET='\033[0m'
    else
        RED=''
        GREEN=''
        YELLOW=''
        BLUE=''
        BOLD=''
        RESET=''
    fi
}

info() {
    printf "%b[info]%b %s\n" "$BLUE" "$RESET" "$1"
}

warn() {
    printf "%b[warn]%b %s\n" "$YELLOW" "$RESET" "$1"
}

error() {
    printf "%b[error]%b %b\n" "$RED" "$RESET" "$1" >&2
    exit 1
}

setup_auth() {
    AUTH_HEADER=""
    if [ -n "$GH_TOKEN" ]; then
        AUTH_HEADER="Authorization: token $GH_TOKEN"
    elif [ -n "$GITHUB_TOKEN" ]; then
        AUTH_HEADER="Authorization: token $GITHUB_TOKEN"
    fi
}

check_dependencies() {
    HAS_CURL=false
    HAS_WGET=false

    if command -v curl >/dev/null 2>&1; then
        HAS_CURL=true
    fi
    if command -v wget >/dev/null 2>&1; then
        HAS_WGET=true
    fi

    if [ "$HAS_CURL" = false ] && [ "$HAS_WGET" = false ]; then
        error "Either curl or wget is required but neither was found."
    fi

    if ! command -v tar >/dev/null 2>&1; then
        error "tar is required but was not found."
    fi

    if ! command -v uname >/dev/null 2>&1; then
        error "uname is required but was not found."
    fi

    SHA_CMD=""
    if command -v sha256sum >/dev/null 2>&1; then
        SHA_CMD="sha256sum"
    elif command -v shasum >/dev/null 2>&1; then
        SHA_CMD="shasum -a 256"
    fi
}

# fetch URL [OUTPUT] downloads URL to OUTPUT, or to stdout without one.
fetch() {
    out="${2:--}"
    if [ "$HAS_CURL" = true ]; then
        if [ -n "$AUTH_HEADER" ]; then
            curl -fsSL -H "$AUTH_HEADER" -o "$out" "$1"
        else
            curl -fsSL -o "$out" "$1"
        fi
    else
        if [ -n "$AUTH_HEADER" ]; then
            wget -qO "$out" --header="$AUTH_HEADER" "$1"
        else
            wget -qO "$out" "$1"
        fi
    fi
}

detect_platform() {
    OS=$(uname -s | tr '[:upper:]' '[:lower:]')
    ARCH=$(uname -m)

    case "$OS" in
        linux)  OS="linux" ;;
        darwin) OS="darwin" ;;
        *)      error "Unsupported operating system: $OS (supported: linux, darwin)" ;;
    esac

    case "$ARCH" in
        x86_64|amd64)   ARCH="amd64" ;;
        aarch64|arm64)  ARCH="arm64" ;;
        *)              error "Unsupported architecture: $ARCH (supported: amd64, arm64)" ;;
    esac

    info "Detected platform: ${OS}/${ARCH}"
}

resolve_version() {
    if [ -n "$GOCRON_VERSION" ]; then
        VERSION="$GOCRON_VERSION"
        info "Using specified version: ${VERSION}"
        return
    fi

    info "Fetching latest release version..."

    API_URL="https://api.github.com/repos/${REPO}/releases/latest"
    RESPONSE=$(fetch "$API_URL" 2>/dev/null) || error "Failed to fetch latest release from GitHub API."

    VERSION=$(printf '%s' "$RESPONSE" | grep '"tag_name"' | sed 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/')

    if [ -z "$VERSION" ]; then
        error "Could not determine latest version. Set GOCRON_VERSION to install a specific version."
    fi

    info "Latest version: ${VERSION}"
}

download() {
    ARCHIVE="gocron-${OS}-${ARCH}.tar.gz"
    DOWNLOAD_URL="${RELEASES_URL}/download/${VERSION}/${ARCHIVE}"

    info "Downloading ${ARCHIVE}..."
    fetch "$DOWNLOAD_URL" "${TMPDIR}/${ARCHIVE}" || error "Failed to download ${DOWNLOAD_URL}"
}

verify_checksum() {
    CHECKSUMS_URL="${RELEASES_URL}/download/${VERSION}/checksums.txt"
    fetch "$CHECKSUMS_URL" "${TMPDIR}/checksums.txt" 2>/dev/null || true

    if [ ! -s "${TMPDIR}/checksums.txt" ]; then
        warn "Checksums file not available, skipping verification."
        return
    fi

    if [ -z "$SHA_CMD" ]; then
        warn "No SHA256 tool found (sha256sum or shasum), skipping checksum verification."
        return
    fi

    EXPECTED=$(grep " \*\{0,1\}${ARCHIVE}\$" "${TMPDIR}/checksums.txt" | awk '{print $1}')

    if [ -z "$EXPECTED" ]; then
        warn "No checksum found for ${ARCHIVE}, skipping verification."
        return
    fi

    ACTUAL=$(cd "$TMPDIR" && $SHA_CMD "$ARCHIVE" | awk '{print $1}')

    if [ "$EXPECTED" != "$ACTUAL" ]; then
        error "Checksum verification failed!\n  Expected: ${EXPECTED}\n  Actual:   ${ACTUAL}"
    fi

    info "Checksum verified."
}

install_binary() {
    tar -xzf "${TMPDIR}/${ARCHIVE}" -C "$TMPDIR"

    if [ ! -f "${TMPDIR}/gocron" ]; then
        error "Archive did not contain the 'gocron' binary."
    fi

    chmod +x "${TMPDIR}/gocron"

    # Determine install directory
    if [ -n "$GOCRON_INSTALL_DIR" ]; then
        INSTALL_DIR="$GOCRON_INSTALL_DIR"
    elif [ -w "/usr/local/bin" ]; then
        INSTALL_DIR="/usr/local/bin"
    elif command -v sudo >/dev/null 2>&1; then
        INSTALL_DIR="/usr/local/bin"
    else
        INSTALL_DIR="$HOME/.local/bin"
    fi

    INSTALL_PATH="${INSTALL_DIR}/gocron"

    # Create directory if it doesn't exist
    if [ ! -d "$INSTALL_DIR" ]; then
        mkdir -p "$INSTALL_DIR" 2>/dev/null || {
            if command -v sudo >/dev/null 2>&1; then
                sudo mkdir -p "$INSTALL_DIR"
            else
                error "Cannot create directory: ${INSTALL_DIR}"
            fi
        }
    fi

    # Install the binary
    if [ -w "$INSTALL_DIR" ]; then
        mv "${TMPDIR}/gocron" "$INSTALL_PATH"
    elif command -v sudo >/dev/null 2>&1; then
        info "Elevated permissions required to install to ${INSTALL_DIR}"
        sudo mv "${TMPDIR}/gocron" "$INSTALL_PATH"
    else
        error "Cannot write to ${INSTALL_DIR} and sudo is not available. Set GOCRON_INSTALL_DIR to a writable directory."
    fi

    # macOS: remove quarantine attribute
    if [ "$OS" = "darwin" ]; then
        xattr -d com.apple.quarantine "$INSTALL_PATH" 2>/dev/null || true
    fi

    # Warn if install dir is not in PATH
    case ":$PATH:" in
        *":${INSTALL_DIR}:"*) ;;
        *)
            warn "${INSTALL_DIR} is not in your PATH."
            printf "  Add it by running:\n"
            printf "    export PATH=\"%s:\$PATH\"\n" "$INSTALL_DIR"
            printf "\n"
            ;;
    esac
}

main
