#!/bin/sh
# Droply server setup script
# Usage: curl -fsSL https://droplydoc.com/setup.sh | bash
#
# This script sets up a complete droply server on a fresh VPS:
#   1. Downloads droply-server binary
#   2. Creates data directory and systemd service
#   3. Installs Caddy with Cloudflare DNS module
#   4. Configures Caddy for wildcard subdomains
#
# Environment variables:
#   VERSION      - specific version (default: latest)
#   DOMAIN       - base domain (default: interactive prompt)
#   CF_API_TOKEN - Cloudflare API token (default: interactive prompt)
#   SKIP_CADDY   - set to 1 to skip Caddy installation/configuration

set -e

REPO="zhong/droply"
DATA_DIR="/data/droply"
INSTALL_DIR="/usr/local/bin"

# Colors
if [ -t 1 ]; then
    BOLD='\033[1m'
    GREEN='\033[32m'
    RED='\033[31m'
    YELLOW='\033[33m'
    CYAN='\033[36m'
    RESET='\033[0m'
else
    BOLD='' GREEN='' RED='' YELLOW='' CYAN='' RESET=''
fi

info()    { printf "${GREEN}==>${RESET} ${BOLD}%s${RESET}\n" "$1"; }
warn()    { printf "${YELLOW}==> WARNING:${RESET} %s\n" "$1"; }
error()   { printf "${RED}==> ERROR:${RESET} %s\n" "$1" >&2; exit 1; }
step()    { printf "\n${CYAN}[Step %s]${RESET} ${BOLD}%s${RESET}\n" "$1" "$2"; }
ask()     { printf "${BOLD}%s${RESET} " "$1"; read -r REPLY; echo "$REPLY"; }

# ─── Pre-flight checks ───────────────────────────────────────────────

preflight() {
    [ "$(uname -s)" = "Linux" ] || error "Server setup is only supported on Linux"
    [ "$(uname -m)" = "x86_64" ] || error "Server setup requires x86_64 (amd64)"
    [ "$(id -u)" -eq 0 ] || error "Please run as root: curl -fsSL ... | sudo bash"

    for cmd in curl systemctl; do
        command -v "$cmd" >/dev/null 2>&1 || error "'$cmd' is required"
    done
}

# ─── Get latest version ──────────────────────────────────────────────

get_latest_version() {
    curl -fsSL -H "Accept: application/json" \
        "https://github.com/${REPO}/releases/latest" \
        | sed -e 's/.*"tag_name":"\([^"]*\)".*/\1/'
}

# ─── Collect configuration ───────────────────────────────────────────

collect_config() {
    if [ -z "$DOMAIN" ]; then
        DOMAIN=$(ask "Enter your base domain (e.g. droplydoc.com):")
        [ -z "$DOMAIN" ] && error "Domain is required"
    fi
    info "Base domain: ${DOMAIN}"

    if [ -z "$SKIP_CADDY" ] && [ -z "$CF_API_TOKEN" ]; then
        printf "\n"
        printf "  Wildcard TLS certificates require a Cloudflare API token.\n"
        printf "  Create one at: https://dash.cloudflare.com/profile/api-tokens\n"
        printf "  Permissions needed: Zone > DNS > Edit\n\n"
        CF_API_TOKEN=$(ask "Enter Cloudflare API token:")
        [ -z "$CF_API_TOKEN" ] && error "Cloudflare API token is required (or set SKIP_CADDY=1)"
    fi
}

# ─── Step 1: Install droply-server ───────────────────────────────────

install_server() {
    step "1/4" "Installing droply-server"

    if [ -z "$VERSION" ]; then
        info "Fetching latest version..."
        VERSION=$(get_latest_version)
    fi
    [ -z "$VERSION" ] && error "Could not determine version"
    info "Version: ${VERSION}"

    FILENAME="droply-server-linux-amd64"
    DOWNLOAD_URL="https://github.com/${REPO}/releases/download/${VERSION}/${FILENAME}"
    CHECKSUMS_URL="https://github.com/${REPO}/releases/download/${VERSION}/checksums.txt"

    TMPDIR=$(mktemp -d)
    trap 'rm -rf "$TMPDIR"' EXIT

    info "Downloading ${FILENAME}..."
    curl -fSL --progress-bar -o "${TMPDIR}/${FILENAME}" "$DOWNLOAD_URL" \
        || error "Download failed. Check that ${VERSION} exists at https://github.com/${REPO}/releases"

    # Verify checksum
    info "Verifying checksum..."
    if curl -fsSL -o "${TMPDIR}/checksums.txt" "$CHECKSUMS_URL" 2>/dev/null; then
        EXPECTED=$(grep "${FILENAME}" "${TMPDIR}/checksums.txt" | awk '{print $1}')
        if [ -n "$EXPECTED" ]; then
            ACTUAL=$(sha256sum "${TMPDIR}/${FILENAME}" | awk '{print $1}')
            if [ "$ACTUAL" != "$EXPECTED" ]; then
                error "Checksum mismatch!\n  Expected: ${EXPECTED}\n  Got:      ${ACTUAL}"
            fi
            info "Checksum verified"
        fi
    else
        warn "Could not download checksums, skipping verification"
    fi

    chmod +x "${TMPDIR}/${FILENAME}"
    mv "${TMPDIR}/${FILENAME}" "${INSTALL_DIR}/droply-server"
    info "Installed droply-server to ${INSTALL_DIR}/droply-server"
}

# ─── Step 2: Configure data directory and systemd ────────────────────

configure_service() {
    step "2/4" "Configuring systemd service"

    # Create data directory
    mkdir -p "${DATA_DIR}/sites"
    chown -R www-data:www-data "${DATA_DIR}" 2>/dev/null || true
    info "Data directory: ${DATA_DIR}"

    # Create systemd service
    cat > /etc/systemd/system/droply.service << EOF
[Unit]
Description=Droply Static Publishing Server
After=network.target caddy.service

[Service]
ExecStart=${INSTALL_DIR}/droply-server \\
  --addr :8080 \\
  --site-addr :8081 \\
  --data-dir ${DATA_DIR} \\
  --domain ${DOMAIN} \\
  --caddy-admin http://localhost:2019
Restart=always
User=www-data

[Install]
WantedBy=multi-user.target
EOF

    systemctl daemon-reload
    info "Created droply.service"
}

# ─── Step 3: Install Caddy with Cloudflare DNS ──────────────────────

install_caddy() {
    if [ "$SKIP_CADDY" = "1" ]; then
        step "3/4" "Skipping Caddy installation (SKIP_CADDY=1)"
        return
    fi

    step "3/4" "Installing Caddy with Cloudflare DNS module"

    # Install xcaddy if not present
    if ! command -v xcaddy >/dev/null 2>&1; then
        info "Installing xcaddy..."
        if ! command -v go >/dev/null 2>&1; then
            info "Installing Go..."
            curl -fsSL https://go.dev/dl/go1.24.1.linux-amd64.tar.gz -o /tmp/go.tar.gz
            rm -rf /usr/local/go
            tar -C /usr/local -xzf /tmp/go.tar.gz
            rm /tmp/go.tar.gz
            export PATH="/usr/local/go/bin:$PATH"
        fi
        go install github.com/caddyserver/xcaddy/cmd/xcaddy@latest
        export PATH="$(go env GOPATH)/bin:$PATH"
    fi

    info "Building Caddy with Cloudflare DNS plugin..."
    xcaddy build \
        --with github.com/caddy-dns/cloudflare \
        --output /tmp/caddy

    mv /tmp/caddy /usr/bin/caddy
    chmod +x /usr/bin/caddy
    info "Caddy installed: $(caddy version)"

    # Create Caddy systemd service if not exists
    if [ ! -f /etc/systemd/system/caddy.service ]; then
        cat > /etc/systemd/system/caddy.service << 'SVCEOF'
[Unit]
Description=Caddy
After=network.target network-online.target
Requires=network-online.target

[Service]
Type=notify
ExecStart=/usr/bin/caddy run --environ --config /etc/caddy/Caddyfile
ExecReload=/usr/bin/caddy reload --config /etc/caddy/Caddyfile
TimeoutStopSec=5s
LimitNOFILE=1048576
PrivateTmp=true
ProtectSystem=full
AmbientCapabilities=CAP_NET_BIND_SERVICE
EnvironmentFile=/etc/caddy/env

[Install]
WantedBy=multi-user.target
SVCEOF
    fi
}

# ─── Step 4: Configure Caddy ────────────────────────────────────────

configure_caddy() {
    if [ "$SKIP_CADDY" = "1" ]; then
        step "4/4" "Skipping Caddy configuration"
        return
    fi

    step "4/4" "Configuring Caddy"

    mkdir -p /etc/caddy

    # Store Cloudflare token
    cat > /etc/caddy/env << EOF
CLOUDFLARE_API_TOKEN=${CF_API_TOKEN}
EOF
    chmod 600 /etc/caddy/env
    info "Cloudflare API token saved to /etc/caddy/env"

    # Write Caddyfile
    cat > /etc/caddy/Caddyfile << EOF
{
    admin localhost:2019
}

# Wildcard certificate for all subdomains via DNS challenge
*.${DOMAIN} {
    tls {
        dns cloudflare {env.CLOUDFLARE_API_TOKEN}
    }

    reverse_proxy localhost:8081
}

# API endpoint
api.${DOMAIN} {
    reverse_proxy localhost:8080
}
EOF
    info "Caddyfile written to /etc/caddy/Caddyfile"
}

# ─── Start services ─────────────────────────────────────────────────

start_services() {
    printf "\n"
    info "Starting services..."

    if [ "$SKIP_CADDY" != "1" ]; then
        systemctl daemon-reload
        systemctl enable --now caddy
        info "Caddy started"
    fi

    systemctl enable --now droply
    info "droply-server started"
}

# ─── Summary ─────────────────────────────────────────────────────────

print_summary() {
    printf "\n"
    printf "${GREEN}════════════════════════════════════════════════════════${RESET}\n"
    printf "${GREEN}  Droply server ${VERSION} is running!${RESET}\n"
    printf "${GREEN}════════════════════════════════════════════════════════${RESET}\n"
    printf "\n"
    printf "  Domain:      ${BOLD}${DOMAIN}${RESET}\n"
    printf "  API:         ${BOLD}https://api.${DOMAIN}${RESET}\n"
    printf "  Sites:       ${BOLD}https://*.${DOMAIN}${RESET}\n"
    printf "  Data:        ${DATA_DIR}\n"
    printf "  Service:     systemctl status droply\n"
    printf "  Logs:        journalctl -u droply -f\n"
    printf "\n"
    printf "  ${BOLD}DNS records required:${RESET}\n"
    printf "    A   ${DOMAIN}       → $(curl -fsSL ifconfig.me 2>/dev/null || echo '<server-ip>')\n"
    printf "    A   *.${DOMAIN}     → $(curl -fsSL ifconfig.me 2>/dev/null || echo '<server-ip>')\n"
    printf "    A   api.${DOMAIN}   → $(curl -fsSL ifconfig.me 2>/dev/null || echo '<server-ip>')\n"
    printf "\n"
    printf "  ${BOLD}Install the CLI on your local machine:${RESET}\n"
    printf "    curl -fsSL https://${DOMAIN}/install.sh | bash\n"
    printf "\n"
}

# ─── Main ────────────────────────────────────────────────────────────

main() {
    printf "\n"
    printf "${BOLD}Droply Server Setup${RESET}\n"
    printf "───────────────────\n\n"

    preflight
    collect_config
    install_server
    configure_service
    install_caddy
    configure_caddy
    start_services
    print_summary
}

main
