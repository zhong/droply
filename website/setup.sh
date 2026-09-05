#!/bin/sh
# Install only Droply. Existing installations require UPGRADE=1 (binary only).
# DOMAIN=example.com TLS_MODE=auto ACME_EMAIL=admin@example.com sh setup.sh
# LOCAL_BINARY=/path/droply-server skips release downloads.
set -eu
umask 077
fail() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }
ROOT=${DROPLY_SETUP_ROOT:-}
SYSTEMCTL=${SYSTEMCTL:-systemctl}
if [ -n "$ROOT" ]; then
    case "$ROOT" in /*) ;; *) fail 'DROPLY_SETUP_ROOT must be absolute' ;; esac
    [ "$SYSTEMCTL" != systemctl ] || fail 'Isolated setup requires an explicit fake SYSTEMCTL executable'
else
    [ "$(uname -s)" = Linux ] || fail 'Server installation requires Linux'
    [ "$(id -u)" = 0 ] || fail 'Run setup as root'
fi
BIN_DIR="$ROOT/usr/local/bin"
CONF_DIR="$ROOT/etc/droply"
UNIT="$ROOT/etc/systemd/system/droply.service"
DATA_DIR=${DATA_DIR:-$ROOT/data/droply}
for value in "$ROOT" "$DATA_DIR"; do
    case "$value" in *[!a-zA-Z0-9_./-]*) fail 'Installation paths must not contain spaces or shell/systemd metacharacters' ;; esac
done
command -v "$SYSTEMCTL" >/dev/null 2>&1 || fail 'systemctl executable is required'
existing=0
if [ -e "$UNIT" ] || [ -e "$CONF_DIR/env" ] || [ -e "$BIN_DIR/droply-server" ]; then existing=1; fi
if [ "$existing" = 1 ] && [ "${UPGRADE:-0}" != 1 ]; then
    fail 'Existing installation detected; no files changed. Read docs/operations-m3.md, or set UPGRADE=1 for a backed-up binary-only upgrade.'
fi
if [ "$existing" = 0 ]; then
    DOMAIN=${DOMAIN:-}
    if [ -z "$DOMAIN" ]; then
        printf 'Base domain: ' >&2
        read -r DOMAIN </dev/tty || fail 'Set DOMAIN for noninteractive installation'
    fi
    case "$DOMAIN" in ''|*[!a-zA-Z0-9.-]*|.*|*..*|*.) fail 'DOMAIN must be a DNS hostname' ;; esac
    case "$DOMAIN" in *.*) ;; *) fail 'DOMAIN must contain a dot' ;; esac
    TLS_MODE=${TLS_MODE:-auto}
    [ "$TLS_MODE" != on-demand ] || TLS_MODE=auto
    case "$TLS_MODE" in http|manual|auto|cloudflare) ;; *) fail 'TLS_MODE must be http, manual, auto or cloudflare' ;; esac
    if [ "$TLS_MODE" = http ]; then HTTP_ADDR=${HTTP_ADDR:-127.0.0.1:8080}; else HTTP_ADDR=${HTTP_ADDR:-:80}; fi
    HTTPS_ADDR=${HTTPS_ADDR:-:443}
    for value in "$HTTP_ADDR" "$HTTPS_ADDR" "${ACME_EMAIL:-}" "${ACME_CA:-}" "${TRUSTED_PROXIES:-}"; do
        case "$value" in *[!a-zA-Z0-9_./:@,\[\]-]*) fail 'Invalid listener, email, CA or proxy value' ;; esac
    done
    command -v ss >/dev/null 2>&1 || fail 'ss (iproute2) is required for listener preflight'
    listeners=$(ss -H -ltn) || fail 'Cannot inspect listening ports'
    addresses="$HTTP_ADDR"
    [ "$TLS_MODE" = http ] || addresses="$addresses $HTTPS_ADDR"
    for address in $addresses; do
        port=${address##*:}
        case "$port" in ''|*[!0-9]*) fail "Invalid listener: $address" ;; esac
        [ "$port" -ge 1 ] && [ "$port" -le 65535 ] || fail "Invalid port: $port"
        if printf '%s\n' "$listeners" | awk -v port="$port" '$4 ~ (":" port "$") { found=1 } END { exit !found }'; then
            fail "Port $port is already listening. Configure another address or explicitly migrate your existing gateway; setup never stops it."
        fi
    done
    if [ "$TLS_MODE" = manual ]; then
        [ -r "${CERT_PATH:-}" ] && [ -r "${KEY_PATH:-}" ] || fail 'Set readable CERT_PATH and KEY_PATH for manual TLS'
    fi
    if [ "$TLS_MODE" = cloudflare ]; then
        [ -r "${CF_TOKEN_FILE:-}" ] || fail 'Set CF_TOKEN_FILE to a readable Cloudflare API token file'
    fi
    # Never overwrite an existing certificate, secret, or data owner configuration.
    [ ! -e "$CONF_DIR" ] || fail 'Existing /etc/droply configuration directory; migrate it explicitly'
    [ ! -e "$DATA_DIR" ] || fail 'Existing data directory; use the migration procedure'
fi
setup_tmp=$(mktemp -d)
trap 'rm -rf "$setup_tmp"' EXIT HUP INT TERM
if [ -n "${LOCAL_BINARY:-}" ]; then
    [ -f "$LOCAL_BINARY" ] || fail 'LOCAL_BINARY does not exist'
    cp "$LOCAL_BINARY" "$setup_tmp/droply-server"
else
    [ "$(uname -m)" = x86_64 ] || fail 'Release installer supports linux-amd64; use LOCAL_BINARY on other architectures'
    command -v curl >/dev/null 2>&1 || fail 'curl is required'
    command -v sha256sum >/dev/null 2>&1 || fail 'sha256sum is required'
    VERSION=${VERSION:-}
    if [ -z "$VERSION" ]; then
        VERSION=$(curl -fsSL https://api.github.com/repos/zhong/droply/releases/latest | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p')
    fi
    case "$VERSION" in v[0-9]* ) ;; *) fail 'Cannot resolve a valid release version' ;; esac
    release="https://github.com/zhong/droply/releases/download/$VERSION"
    curl -fsSL "$release/droply-server-linux-amd64" -o "$setup_tmp/droply-server"
    curl -fsSL "$release/checksums.txt" -o "$setup_tmp/checksums.txt"
    expected=$(awk '$2 == "droply-server-linux-amd64" { print $1 }' "$setup_tmp/checksums.txt")
    actual=$(sha256sum "$setup_tmp/droply-server" | awk '{ print $1 }')
    [ -n "$expected" ] && [ "$expected" = "$actual" ] || fail 'Missing or mismatched release checksum'
fi
chmod 755 "$setup_tmp/droply-server"
binary_help=$("$setup_tmp/droply-server" --help 2>&1) || fail 'Downloaded/local binary is not executable on this host'
case "$binary_help" in *-tls-mode*) ;; *) fail 'Binary predates standalone HTTPS; select an M0-capable VERSION or LOCAL_BINARY' ;; esac
case "$binary_help" in *-audit-retention-days*) ;; *) fail 'Binary predates the M3 private platform; select an M3-capable VERSION or LOCAL_BINARY' ;; esac
(umask 022; mkdir -p "$BIN_DIR")
if [ "$existing" = 1 ]; then
    backup="$ROOT/var/backups/droply/$(date +%Y%m%d-%H%M%S)-$$"
    mkdir -p "$backup"
    [ ! -e "$BIN_DIR/droply-server" ] || cp -p "$BIN_DIR/droply-server" "$backup/droply-server"
    [ ! -e "$UNIT" ] || cp -p "$UNIT" "$backup/droply.service"
    [ ! -e "$CONF_DIR/env" ] || cp -p "$CONF_DIR/env" "$backup/env"
    cp "$setup_tmp/droply-server" "$BIN_DIR/droply-server.new"
chmod 755 "$BIN_DIR/droply-server.new"
    mv "$BIN_DIR/droply-server.new" "$BIN_DIR/droply-server"
    printf 'Binary updated. Backup: %s\nService, environment, data and certificates were preserved. No service was restarted. Follow docs/operations-m3.md before restarting.\n' "$backup"
    exit 0
fi
service_user=droply
if [ -z "$ROOT" ]; then
    if ! id droply >/dev/null 2>&1; then useradd --system --home-dir "$DATA_DIR" --shell /usr/sbin/nologin droply; fi
fi
(umask 022; mkdir -p "$(dirname "$CONF_DIR")" "$(dirname "$DATA_DIR")" "$(dirname "$UNIT")")
mkdir -p "$CONF_DIR" "$DATA_DIR/sites"
cp "$setup_tmp/droply-server" "$BIN_DIR/droply-server.new"
chmod 755 "$BIN_DIR/droply-server.new"
mv "$BIN_DIR/droply-server.new" "$BIN_DIR/droply-server"
printf '# Private signup is the default. Set true only to deliberately allow public registration.\nDROPLY_OPEN_REGISTRATION=false\n# Other environment overrides are preserved during upgrades.\n' > "$CONF_DIR/env"
args="--domain $DOMAIN --data-dir $DATA_DIR --addr $HTTP_ADDR --tls-mode $TLS_MODE"
if [ "$TLS_MODE" != http ]; then args="$args --https-addr $HTTPS_ADDR"; fi
if [ -n "${ACME_EMAIL:-}" ]; then args="$args --acme-email $ACME_EMAIL"; fi
if [ -n "${ACME_CA:-}" ]; then args="$args --acme-ca $ACME_CA"; fi
if [ -n "${TRUSTED_PROXIES:-}" ]; then args="$args --trusted-proxies $TRUSTED_PROXIES"; fi
case "$TLS_MODE" in
    manual)
        cp "$CERT_PATH" "$CONF_DIR/cert.pem"; cp "$KEY_PATH" "$CONF_DIR/key.pem"
        args="$args --tls-cert $CONF_DIR/cert.pem --tls-key $CONF_DIR/key.pem" ;;
    cloudflare)
        cp "$CF_TOKEN_FILE" "$CONF_DIR/cloudflare-token"
        args="$args --cloudflare-token-file $CONF_DIR/cloudflare-token" ;;
esac
cat > "$UNIT" <<UNIT
[Unit]
Description=Droply Static Publishing Server
After=network-online.target
Wants=network-online.target

[Service]
User=$service_user
EnvironmentFile=$CONF_DIR/env
ExecStart=$BIN_DIR/droply-server $args
Restart=on-failure
RestartSec=5
TimeoutStopSec=360
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
NoNewPrivileges=true
PrivateTmp=true
UMask=0077

[Install]
WantedBy=multi-user.target
UNIT
chmod 644 "$UNIT"
if [ -z "$ROOT" ]; then chown -R droply:droply "$CONF_DIR" "$DATA_DIR"; fi
"$SYSTEMCTL" daemon-reload
"$SYSTEMCTL" enable --now droply
"$SYSTEMCTL" is-active --quiet droply || fail 'Droply did not become active; inspect journalctl -u droply'
printf 'Droply installed. Service: %s\nData: %s\nInspect: systemctl status droply; journalctl -u droply\n' "$UNIT" "$DATA_DIR"
printf 'Point the base domain, wildcard and api hostname DNS to this server. Existing gateways were not changed.\n'
printf 'Registration is closed by default. Stop Droply and initialize a local administrator using init-admin; see docs/identity-m3.md.\n'
printf 'Console: https://api.%s/console/ (HTTPS required for sign-in). Health: /healthz on the API hostname.\n' "$DOMAIN"
