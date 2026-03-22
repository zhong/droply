#!/bin/sh
# Droply CLI installer
# Usage: curl -fsSL https://droplydoc.com/install.sh | bash
#
# Environment variables:
#   VERSION  - specific version to install (e.g. v0.1.0), default: latest
#   INSTALL_DIR - installation directory, default: /usr/local/bin

set -e

REPO="zhong/droply"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"
BINARY="droply"

# Colors (only if terminal supports it)
if [ -t 1 ]; then
    BOLD='\033[1m'
    GREEN='\033[32m'
    RED='\033[31m'
    YELLOW='\033[33m'
    RESET='\033[0m'
else
    BOLD='' GREEN='' RED='' YELLOW='' RESET=''
fi

info()  { printf "${GREEN}==>${RESET} ${BOLD}%s${RESET}\n" "$1"; }
warn()  { printf "${YELLOW}==> WARNING:${RESET} %s\n" "$1"; }
error() { printf "${RED}==> ERROR:${RESET} %s\n" "$1" >&2; exit 1; }

# Detect OS
detect_os() {
    case "$(uname -s)" in
        Linux*)  echo "linux" ;;
        Darwin*) echo "darwin" ;;
        MINGW*|MSYS*|CYGWIN*) echo "windows" ;;
        *) error "Unsupported OS: $(uname -s)" ;;
    esac
}

# Detect architecture
detect_arch() {
    case "$(uname -m)" in
        x86_64|amd64)  echo "amd64" ;;
        arm64|aarch64) echo "arm64" ;;
        *) error "Unsupported architecture: $(uname -m)" ;;
    esac
}

# Check required commands
check_deps() {
    for cmd in curl; do
        command -v "$cmd" >/dev/null 2>&1 || error "'$cmd' is required but not found"
    done
}

# Get latest version from GitHub
get_latest_version() {
    curl -fsSL -H "Accept: application/json" \
        "https://github.com/${REPO}/releases/latest" \
        | sed -e 's/.*"tag_name":"\([^"]*\)".*/\1/'
}

main() {
    check_deps

    OS=$(detect_os)
    ARCH=$(detect_arch)

    # Validate platform combination
    if [ "$OS" = "darwin" ] && [ "$ARCH" != "arm64" ] && [ "$ARCH" != "amd64" ]; then
        error "Unsupported macOS architecture: $ARCH"
    fi
    if [ "$OS" = "windows" ] && [ "$ARCH" != "amd64" ]; then
        error "Windows only supports amd64"
    fi

    info "Detected platform: ${OS}/${ARCH}"

    # Determine version
    if [ -z "$VERSION" ]; then
        info "Fetching latest version..."
        VERSION=$(get_latest_version)
    fi
    [ -z "$VERSION" ] && error "Could not determine version"
    info "Installing droply ${VERSION}"

    # Build download URL
    EXT=""
    [ "$OS" = "windows" ] && EXT=".exe"
    FILENAME="${BINARY}-${OS}-${ARCH}${EXT}"
    DOWNLOAD_URL="https://github.com/${REPO}/releases/download/${VERSION}/${FILENAME}"
    CHECKSUMS_URL="https://github.com/${REPO}/releases/download/${VERSION}/checksums.txt"

    # Download binary
    TMPDIR=$(mktemp -d)
    trap 'rm -rf "$TMPDIR"' EXIT

    info "Downloading ${FILENAME}..."
    curl -fSL --progress-bar -o "${TMPDIR}/${FILENAME}" "$DOWNLOAD_URL" \
        || error "Download failed. Check that ${VERSION} exists at https://github.com/${REPO}/releases"

    # Verify checksum
    info "Verifying checksum..."
    curl -fsSL -o "${TMPDIR}/checksums.txt" "$CHECKSUMS_URL" \
        || warn "Could not download checksums, skipping verification"

    if [ -f "${TMPDIR}/checksums.txt" ]; then
        EXPECTED=$(grep "${FILENAME}" "${TMPDIR}/checksums.txt" | awk '{print $1}')
        if [ -n "$EXPECTED" ]; then
            if command -v sha256sum >/dev/null 2>&1; then
                ACTUAL=$(sha256sum "${TMPDIR}/${FILENAME}" | awk '{print $1}')
            elif command -v shasum >/dev/null 2>&1; then
                ACTUAL=$(shasum -a 256 "${TMPDIR}/${FILENAME}" | awk '{print $1}')
            else
                warn "No sha256sum or shasum found, skipping verification"
                ACTUAL="$EXPECTED"
            fi
            if [ "$ACTUAL" != "$EXPECTED" ]; then
                error "Checksum mismatch!\n  Expected: ${EXPECTED}\n  Got:      ${ACTUAL}"
            fi
            info "Checksum verified"
        else
            warn "Binary not found in checksums file, skipping verification"
        fi
    fi

    # Install
    chmod +x "${TMPDIR}/${FILENAME}"

    if [ -w "$INSTALL_DIR" ]; then
        mv "${TMPDIR}/${FILENAME}" "${INSTALL_DIR}/${BINARY}${EXT}"
    else
        info "Installing to ${INSTALL_DIR} (requires sudo)..."
        sudo mv "${TMPDIR}/${FILENAME}" "${INSTALL_DIR}/${BINARY}${EXT}"
    fi

    info "droply ${VERSION} installed to ${INSTALL_DIR}/${BINARY}${EXT}"

    # Verify
    if command -v droply >/dev/null 2>&1; then
        printf "\n"
        info "Installation complete! Run 'droply version' to verify."
    else
        printf "\n"
        warn "${INSTALL_DIR} is not in your PATH. Add it with:"
        printf "  export PATH=\"%s:\$PATH\"\n" "$INSTALL_DIR"
    fi
}

main
