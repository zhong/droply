#!/bin/sh
# Isolated installer contracts: no root, network, ACME CA, or real systemd.
set -eu
project_dir=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
test_tmp=$(mktemp -d)
trap 'rm -rf "$test_tmp"' EXIT HUP INT TERM
mkdir "$test_tmp/bin"
cat > "$test_tmp/bin/systemctl" <<'SCRIPT'
#!/bin/sh
printf '%s\n' "$*" >> "$SYSTEMCTL_LOG"
case "$*" in *caddy*|stop*|restart*) exit 92 ;; esac
SCRIPT
cat > "$test_tmp/bin/ss" <<'SCRIPT'
#!/bin/sh
if [ "${BUSY_PORT:-}" != '' ]; then printf 'LISTEN 0 128 0.0.0.0:%s 0.0.0.0:*\n' "$BUSY_PORT"; fi
SCRIPT
cat > "$test_tmp/server" <<'SCRIPT'
#!/bin/sh
[ "$1" = --help ] && printf '  -tls-mode string\n'
SCRIPT
chmod +x "$test_tmp/bin/"* "$test_tmp/server"
export PATH="$test_tmp/bin:$PATH"
export SYSTEMCTL="$test_tmp/bin/systemctl" SYSTEMCTL_LOG="$test_tmp/systemctl.log"
export LOCAL_BINARY="$test_tmp/server" DOMAIN=example.com
unset DATA_DIR UPGRADE
for mode in auto http manual cloudflare; do
    root="$test_tmp/$mode"
    printf 'fixture-cert' > "$test_tmp/cert.pem"
    printf 'fixture-key' > "$test_tmp/key.pem"
    printf 'fixture-secret-token' > "$test_tmp/token"
    DROPLY_SETUP_ROOT="$root" TLS_MODE="$mode" CERT_PATH="$test_tmp/cert.pem" KEY_PATH="$test_tmp/key.pem" CF_TOKEN_FILE="$test_tmp/token" \
        ACME_EMAIL=admin@example.com sh "$project_dir/scripts/setup.sh" > "$test_tmp/$mode.log"
    test -x "$root/usr/local/bin/droply-server"
    test "$(ls -l "$root/usr/local/bin/droply-server" | cut -c1-10)" = -rwxr-xr-x
    grep -q -- "--tls-mode $mode" "$root/etc/systemd/system/droply.service"
    test -f "$root/etc/droply/env"
    if grep -i caddy "$root/etc/systemd/system/droply.service"; then exit 1; fi
    if grep fixture-secret-token "$test_tmp/$mode.log"; then exit 1; fi
done
# Re-running never changes existing binary, service, or environment.
root="$test_tmp/auto"
printf 'CUSTOM_ENV=preserve\n' >> "$root/etc/droply/env"
cp "$root/etc/droply/env" "$test_tmp/original-env"
cp "$root/etc/systemd/system/droply.service" "$test_tmp/original-unit"
if DROPLY_SETUP_ROOT="$root" sh "$project_dir/scripts/setup.sh" > "$test_tmp/existing.log" 2>&1; then exit 1; fi
cmp "$test_tmp/original-env" "$root/etc/droply/env"
# Explicit upgrade takes a backup and leaves service/env/data untouched, without restart.
mkdir -p "$root/data/droply/sites/alice/blog"
printf 'keep-content' > "$root/data/droply/sites/alice/blog/index.html"
cp "$SYSTEMCTL_LOG" "$test_tmp/original-systemctl"
DROPLY_SETUP_ROOT="$root" UPGRADE=1 sh "$project_dir/scripts/setup.sh" > "$test_tmp/upgrade.log"
cmp "$test_tmp/original-env" "$root/etc/droply/env"
cmp "$test_tmp/original-unit" "$root/etc/systemd/system/droply.service"
cmp "$test_tmp/original-systemctl" "$SYSTEMCTL_LOG"
grep -q keep-content "$root/data/droply/sites/alice/blog/index.html"
test "$(find "$root/var/backups/droply" -name droply.service | wc -l | tr -d ' ')" = 1
# Both default TLS ports must be inspected before any installation mutation.
for port in 80 443; do
    root="$test_tmp/busy-$port"
    if DROPLY_SETUP_ROOT="$root" BUSY_PORT="$port" sh "$project_dir/scripts/setup.sh" > "$test_tmp/busy.log" 2>&1; then exit 1; fi
    grep -q "Port $port" "$test_tmp/busy.log"
    test ! -e "$root/usr/local/bin/droply-server"
done
# A legacy gateway installation is left intact during the explicit binary upgrade.
root="$test_tmp/legacy"
mkdir -p "$root/etc/systemd/system" "$root/etc/caddy" "$root/etc/droply" "$root/usr/local/bin"
printf 'After=caddy.service\nExecStart=/usr/local/bin/droply-server --site-addr :8081\n' > "$root/etc/systemd/system/droply.service"
printf 'old-gateway-config' > "$root/etc/caddy/Caddyfile"
printf 'KEEP_SECRET=fixture' > "$root/etc/droply/env"
cp "$test_tmp/server" "$root/usr/local/bin/droply-server"
cp "$root/etc/systemd/system/droply.service" "$test_tmp/legacy-unit"
DROPLY_SETUP_ROOT="$root" UPGRADE=1 sh "$project_dir/scripts/setup.sh" > "$test_tmp/legacy.log"
cmp "$test_tmp/legacy-unit" "$root/etc/systemd/system/droply.service"
grep -q old-gateway-config "$root/etc/caddy/Caddyfile"
grep -q KEEP_SECRET=fixture "$root/etc/droply/env"
# Old release binaries with successful --help but no standalone TLS support fail before mutation.
printf '#!/bin/sh\nexit 0\n' > "$test_tmp/old-server"
chmod +x "$test_tmp/old-server"
if DROPLY_SETUP_ROOT="$test_tmp/old-release" LOCAL_BINARY="$test_tmp/old-server" sh "$project_dir/scripts/setup.sh" > "$test_tmp/old-release.log" 2>&1; then exit 1; fi
grep -q 'predates standalone HTTPS' "$test_tmp/old-release.log"
test ! -e "$test_tmp/old-release/usr/local/bin/droply-server"
cmp "$project_dir/scripts/setup.sh" "$project_dir/website/setup.sh"
printf 'Installer tests passed (four modes, preservation, upgrade backup, ports 80/443, mirrored script).\n'
