# Droply

A lightweight, privately deployed alternative to the static publishing workflow of Cloudflare Pages: one server binary, SQLite and local disk, built-in HTTPS, immutable versions, project root hosts, previews and team permissions. Caddy is not required.

Droply targets a single server or small team. It does not provide a global CDN, Workers/Functions, a managed build fleet or multi-node high availability. Deploy an already-built directory. Account registration is closed by default; account roles and visitor access rules are separate, so sites and previews remain public unless protected.

[中文文档](README.zh-CN.md)

## Architecture

```text
CLI / Browser → Droply HTTP + HTTPS
                    ├─ api.example.com → authentication / deployment API
                    └─ site hosts      → access control / files / statistics
                                         ↓
                                    SQLite + disk
```

One Droply process serves the API, static files and HTTPS. An external gateway is optional; all sites share the same access-control path.

## Private deployment and operations

Initialize an administrator locally before inviting accounts. Open `https://api.<base-domain>/console/` for the embedded console. CLI and console share owner/deployer/viewer permissions: owners manage members and visitor rules; deployers publish, roll back and manage their own project tokens; viewers read project information. Visitor password, IP and WeCom rules remain independent.

```sh
# On the server: stop serving; password file must be mode 0600 and contain 8–72 bytes
sudo systemctl stop droply
sudo /usr/local/bin/droply-server init-admin --data-dir /data/droply \
  --email admin@example.com --password-file /secure/initial-password
# When using the installer's droply service account, retain its data ownership
sudo chown -R droply:droply /data/droply
sudo systemctl start droply

# On your workstation
droply login --api-url https://api.example.com
droply invitation create colleague@example.com
droply member set colleague@example.com --role deployer --sub alice --project blog
droply audit --sub alice --project blog --limit 50
```

Create the `alice/blog` project and let the invited account register before granting membership. For an existing installation without an administrator, use local `claim-admin --email ...` to explicitly select an existing account. Keep public registration closed during initialization. See [identity setup](docs/identity-m3.md), [console](docs/console-m3.md), [audit](docs/audit-m3.md), [backup and restore](docs/backup-m3.md), and [operations, upgrades and acceptance checks](docs/operations-m3.md).

## Quick Start

### Install CLI

One-line install (auto-detects OS and architecture):

```bash
curl -fsSL https://droplydoc.com/install.sh | bash
```

Or install a specific version:

```bash
curl -fsSL https://droplydoc.com/install.sh -o install.sh
VERSION=vX.Y.Z bash install.sh
```

<details>
<summary>Alternative installation methods</summary>

Download pre-built binaries from the [latest release](https://github.com/zhong/droply/releases/latest):

| Platform | Binary |
|----------|--------|
| macOS (Apple Silicon) | `droply-darwin-arm64` |
| macOS (Intel) | `droply-darwin-amd64` |
| Linux (x86_64) | `droply-linux-amd64` |
| Windows (x86_64) | `droply-windows-amd64.exe` |

```bash
# Example: macOS Apple Silicon
curl -Lo droply https://github.com/zhong/droply/releases/latest/download/droply-darwin-arm64
chmod +x droply
sudo mv droply /usr/local/bin/
```

Or install with Go:

```bash
go install github.com/zhong/droply/cmd/droply@latest
```

</details>

### Build from Source

```bash
git clone https://github.com/zhong/droply.git
cd droply
make build
```

Produces two binaries:
- `bin/droply-server` — Server
- `bin/droply` — CLI client

#### Cross-compile for All Platforms

```bash
make build-all
```

Produces binaries in `dist/` for all supported platforms.

### Run Tests

```sh
make test
make test-integration
go vet ./...
```

### Deploy to Server

Follow the [upgrade and downgrade procedure](docs/operations-m3.md) to stop serving, take a full backup and retain the old binary before building and switching. `make deploy` refuses an unprepared automatic upgrade and points to that guide.

```sh
make build
```

### Deploy Website

The project website at `droplydoc.com` is hosted via droply itself. To update:

```bash
make website
cd website
droply deploy
```

The standalone downloads `website/install.sh` and `website/setup.sh` are generated copies. Edit only their sources in `scripts/`, then run `make website` before publishing. `make check-website` and the installer CI check both copies and fail on drift; publishing needs no frontend build service.

## Server Deployment

New installations run only `droply-server`; Caddy is not required. Read the [M3 operations guide](docs/operations-m3.md) before production changes. Existing installations should follow the [M0 migration and rollback guide](docs/migration-m0.md) before changing their service.

### Linux installation

Download the script, then pass environment variables to the process executing it:

```bash
curl -fsSL https://droplydoc.com/setup.sh -o setup.sh
sudo env DOMAIN=example.com TLS_MODE=auto ACME_EMAIL=admin@example.com sh setup.sh
```

The installer verifies release checksums and creates a dedicated `droply` user and systemd service on ports 80/443. Point the base domain, `*.example.com` and `api.example.com` to the server first. It checks listening ports and refuses to overwrite an existing service, environment or data directory. It never stops or uninstalls an existing gateway.

Other modes:

```bash
# Cloudflare DNS wildcard certificates; keep the token in a protected file
sudo env DOMAIN=example.com TLS_MODE=cloudflare \
  CF_TOKEN_FILE=/root/cloudflare-token ACME_EMAIL=admin@example.com sh setup.sh

# Existing certificate; it must cover api.example.com and the served site names
sudo env DOMAIN=example.com TLS_MODE=manual \
  CERT_PATH=/root/cert.pem KEY_PATH=/root/key.pem sh setup.sh

# Behind an existing gateway; trust only its actual source addresses
sudo env DOMAIN=example.com TLS_MODE=http HTTP_ADDR=127.0.0.1:8080 \
  TRUSTED_PROXIES=127.0.0.1/32 sh setup.sh

# Install a locally built server binary without downloading a release
sudo env DOMAIN=example.com TLS_MODE=auto \
  LOCAL_BINARY="$PWD/bin/droply-server" sh setup.sh
```

`UPGRADE=1` backs up and replaces only the existing binary, preserving the service, environment, data and certificates without restarting. Stop serving and take a [complete backup](docs/backup-m3.md) before switching; follow the [upgrade and downgrade procedure](docs/operations-m3.md). `VERSION=vX.Y.Z` pins a release; `DATA_DIR` selects a new data directory; `ACME_CA` can select an ACME staging endpoint.

### Run directly

```bash
# Automatic per-host HTTPS; certificates persist in data-dir/certificates
./bin/droply-server --domain example.com --data-dir ./data \
  --addr :80 --https-addr :443 --tls-mode auto --acme-email admin@example.com

# Platform wildcard via DNS-01; other names still need HTTP-01 on port 80
./bin/droply-server --domain example.com --data-dir ./data \
  --addr :80 --tls-mode cloudflare --cloudflare-token-file ./cloudflare-token

# Manual PEM certificate and key
./bin/droply-server --domain example.com --data-dir ./data \
  --addr :8080 --https-addr :8443 --tls-mode manual --tls-cert ./cert.pem --tls-key ./key.pem

# HTTP behind an optional existing TLS gateway
./bin/droply-server --domain example.com --data-dir ./data \
  --addr 127.0.0.1:8080 --tls-mode http --trusted-proxies 127.0.0.1/32
```

Binding ports 80/443 requires permission; the installer grants `CAP_NET_BIND_SERVICE` through systemd. Automatic per-host mode requires public HTTP-01 challenge reachability on port 80. Cloudflare mode uses DNS-01 only for the platform wildcard, requiring `Zone:DNS:Edit` and `Zone:Zone:Read` permissions for its zone. Names outside that wildcard, including custom domains, still use HTTP-01: blocking public port 80 prevents their issuance and renewal even when wildcard renewal succeeds. Custom domains do not require additional DNS credentials. Administrators renew manual certificates and restart the server to load them.

| Flag | Default | Purpose |
|---|---|---|
| `--addr` | `:8080` | Unified API/site HTTP listener; normally `:80` for auto mode |
| `--https-addr` | `:443` | HTTPS listener |
| `--tls-mode` | `http` | `http` / `manual` / `auto` / `cloudflare` |
| `--domain` | `droplydoc.com` | Base domain |
| `--data-dir` | `/data/droply` | Database, content and persistent session signing key |
| `--cert-dir` | `data-dir/certificates` | ACME accounts and certificate storage |
| `--tls-cert`, `--tls-key` | empty | Manual PEM certificate chain and key |
| `--acme-email`, `--acme-ca` | empty / Let's Encrypt production | ACME account email and directory |
| `--cloudflare-token-file` | empty | DNS token file; alternatively `DROPLY_CLOUDFLARE_API_TOKEN` |
| `--trusted-proxies` | empty | Trusted proxy CIDRs; forwarded IPs are ignored by default |
| `--hmac-secret` | generated and persisted | Preserve an existing explicit session signing key |
| `--log-retention-days` | `30` | Detailed visit log retention |
| `--open-registration` | `false` | Explicitly open registration; also DROPLY_OPEN_REGISTRATION=true |
| `--audit-retention-days` | `90` | Audit retention; must be positive |
| `--deploy-max-expanded-bytes`, `--deploy-max-files` | `268435456`, `10000` | Per-deployment extracted byte/entry limits |
| `--artifact-max-bytes` | `0` | Artifact/staging quota; 0 means disk capacity only |
| `--deployment-retain-count`, `--deployment-retain-days` | `10`, `30` | History retention protections |
| `--artifact-orphan-grace` | `1h` | Grace period before orphan cleanup |

Run `droply-server --help` for all flags. Normal HTTP shutdown drains for up to 15 seconds; an in-flight DNS job can delay exit until its library timeout (up to five minutes), so the installed unit allows 360 seconds. `--site-addr` temporarily supports an additional unified HTTP listener; `--caddy-admin` is ignored. New installations should omit both. `on-demand` remains an alias for `auto`.

```bash
sudo systemctl status droply
sudo journalctl -u droply -f
```

### Data Directory Structure

```
/data/droply/
├── droply.db                  SQLite: accounts, projects, history, sessions, audit
├── hmac.key                   Persistent visitor/console signing key
├── certificates/              Default ACME certificates and accounts
├── server.lock                Exclusive installation lock
├── upgrade-backups/           Database-only snapshots before schema migration
└── sites/
    ├── .artifacts/<id>/files/  Immutable deployment content
    ├── .artifacts/<id>/manifest.json
    ├── .artifacts/.staging/    In-progress uploads
    └── <sub>/<project>/       Legacy directories awaiting migration
```

## CLI Guide

### Installation

Download from [GitHub Releases](https://github.com/zhong/droply/releases/latest) (see Quick Start above), or build from source with `make build`.

Check your installation:

```bash
droply version
```

### Configuration

CLI config file is located at `~/.config/droply/config.toml` and supports multiple **contexts** (connection profiles) — one for each droply server you connect to.

```toml
current_context = "default"

[contexts.default]
api_url = "https://api.droplydoc.com"
token = "dp_xxxxxxxxxxxx"

[contexts.staging]
api_url = "https://api.staging.example.com"
token = "dp_yyyyyyyyyyyy"
```

This file is automatically created and updated on login/register. Old single-server config (top-level `api_url` + `token`) is read as `contexts.default` without modifying the file; migration is persisted on the next explicit configuration save.

#### Working with Multiple Servers

```bash
# Login to a self-hosted server with an explicit context name
droply auth login --api-url https://api.staging.example.com --context staging

# Or omit --context and droply derives one from the URL
droply auth login --api-url https://api.staging.example.com   # context "example"

# List configured contexts (* marks the active one)
droply context list

# Switch between servers
droply context use staging

# Add a context without authenticating yet
droply context add corp --api-url https://api.corp.example.com

# Remove a context
droply context remove corp

# One-shot override (does not persist)
droply --context staging deploy
```

#### Per-Project Context Binding

`.droply.toml` in a project directory can pin a specific context:

```toml
context = "staging"
subdomain = "alice"
project = "blog"
```

When running `droply` commands inside that directory, the `staging` context is used automatically.

`DROPLY_API_URL` and `DROPLY_TOKEN` override the selected connection fields without saving them. **Context selection priority** (highest to lowest):
1. `--context X` flag on the command line
2. `context = "X"` in `.droply.toml`
3. `current_context` in `~/.config/droply/config.toml`

### Register and Login

```bash
# Register using an administrator invitation (set DROPLY_INVITE first)
droply register --api-url https://api.example.com
# Interactive email and password input

# Login to existing account
droply login

# Check current login status
droply whoami

# Logout
droply logout
```

### Manage Subdomains

Each user can create multiple subdomains. Name requirements: lowercase letters + digits + hyphens, 3-32 characters.

```bash
# Create a subdomain
droply subdomain create alice
# alice.droplydoc.com is now available

# List all subdomains
droply subdomain list

# Delete a subdomain (also deletes all projects under it)
droply subdomain delete alice
```

### Deploy Sites

```bash
# Deploy current directory to a subdomain and project
droply deploy --sub alice --project blog

# Deploy a specific directory
droply deploy ./dist --sub alice --project blog

# The response prints the project root URL and deployed version.
```

#### Project Config File

Create `.droply.toml` in your project root to avoid specifying flags every time:

```toml
subdomain = "alice"
project = "blog"
exclude_paths = ["dist/private", "public/secret.txt"]
exclude_files = ["draft.html", "robots-local.txt"]
```

```bash
# With .droply.toml, just run:
droply deploy
```

`exclude_paths` uses exact relative paths from the project root. If a path points to a directory, the whole directory is excluded. If it points to a file, only that file is excluded.

`exclude_files` uses exact file name matches and excludes matching files from any directory in the deployment source.

#### Exclusion Rules

The following files and directories are automatically excluded during deployment:

- `.git`
- `node_modules`
- `__pycache__`
- `.DS_Store`
- `.env`
- All hidden directories (starting with `.`)
- Any exact paths listed in `.droply.toml` under `exclude_paths`
- Any exact file names listed in `.droply.toml` under `exclude_files`

#### Upload Limit

Compressed uploads are limited to **50 MiB** including multipart overhead; defaults allow 256 MiB extracted file bytes and 10,000 files/directories. A complete immutable artifact is published only after validation; failed uploads preserve production.

### Project hosts and previews

New deployments return a stable project root URL, so `/assets/app.js` works without a project path prefix. Existing `alice.example.com/blog/` URLs and verified custom domains remain supported. Each preview gets an immutable URL; specifying a branch also updates its stable alias only after a successful upload.

```sh
droply deploy dist --sub alice --project blog --preview --branch feature/docs --commit abc123 --json
droply deployment promote 42 --sub alice --project blog --json
droply deployment events --sub alice --project blog
```

Preview publication leaves production unchanged. Promotion switches production to the existing artifact and records an event; preview URLs still serve that artifact. Access rules are checked on every request, including old previews. Preview URLs are excluded from indexing but are public unless access rules protect the project.

Put `_droply.toml`, `_headers` and `_redirects` in the uploaded directory to configure SPA fallback, response headers and redirects. These rules are validated before publication and travel with the version through preview, promotion and rollback. See [static rule syntax and supported subset](docs/static-rules-m2.md), [CI configuration and retry contract](docs/ci-m2.md), and [project credentials](docs/project-tokens-m2.md).

### Deployment history, rollback and cleanup

```sh
droply deployment list --sub alice --project blog --json
droply deployment rollback 1 --sub alice --project blog
# Preview by default; add --apply to delete eligible artifacts.
droply deployment cleanup --sub alice --project blog --keep 5 --days 0
```

Rollback uses retained complete artifacts; legacy metadata-only history is explicitly unavailable. Hourly server maintenance protects the newest 10 successful versions, versions from the past 30 days, and production/referenced artifacts. Back up the entire data directory before upgrading; see [M1 migration, retention and verification](docs/migration-m1.md).


### Manage Projects

```bash
# List projects in a subdomain
droply project list --sub alice

# Delete a project and its metadata; orphan artifacts are reclaimed after the grace period
droply project delete blog --sub alice
```

### Custom Domains

```bash
# Add a custom domain to a project
droply domain add blog.example.com --sub alice --project blog
# Outputs a CNAME target — add this record at your DNS provider

# Verify DNS is configured correctly
droply domain verify blog.example.com --sub alice --project blog

# List custom domains
droply domain list --sub alice --project blog

# Remove a custom domain
droply domain remove blog.example.com --sub alice --project blog
```

After adding a custom domain, add a CNAME or A record at your DNS provider pointing to the output target, then run `droply domain verify` to confirm. Publish the dedicated `_droply-verification` TXT record printed by the CLI, then retry verification. Matching an A/CNAME target alone does not prove ownership. Droply only serves verified bindings and authorizes their automatic certificates.

### Access Control

Protect subdomains or projects with IP whitelists and passwords. Two granularity levels: subdomain-level (shared across all projects) and project-level (overrides subdomain rules).

```bash
# Set subdomain-level access control: IP whitelist + auto-generated password
droply access set --subdomain alice --ip 10.0.0.0/8 --password auto --expire 24h
# Output: https://alice.droplydoc.com | Password: a1b2c3d4e5f6g7h8 | IP: 10.0.0.0/8 | Expires: 1d

# Set a password that never expires
droply access set --subdomain alice --password auto --expire never
# Output: https://alice.droplydoc.com | Password: xYz123AbCdEf9876 | Expires: never

# Set project-level access control (overrides subdomain rules)
droply access set --subdomain alice --project blog --password "my-secret" --expire 7d
# Output: https://alice.droplydoc.com/blog | Password: my-secret | Expires: 7d

# View access control rules
droply access get --subdomain alice
droply access get --subdomain alice --project blog

# Remove access control
droply access remove --subdomain alice
droply access remove --subdomain alice --project blog
```

After setting access control, a copy-friendly share line is printed with the access URL, password, IP restrictions, and expiry — ready to paste into chat or email.

#### Access Control Flags

| Flag | Description |
|------|-------------|
| `--subdomain` | Subdomain name (required) |
| `--project` | Project name (optional; omit for subdomain-level rules) |
| `--ip` | Allowed IP or CIDR (repeatable for multiple entries) |
| `--password` | Password (`auto` to generate, or a custom value, minimum 8 characters) |
| `--wework` | Enable WeCom (WeWork) QR code login (any corp member) |
| `--wework-user` | Allow specific WeCom user_id; repeatable; implies `--wework` |
| `--expire` | Session TTL (e.g. `1h`, `24h`, `7d`, `never`, default `24h`) |

#### How It Works

- **IP whitelist**: Only requests from specified IPs/subnets are allowed
- **Password protection**: Visitors enter a password on a login page; a cookie maintains the session
- **WeCom QR code login**: Visitors scan a QR code with WeCom mobile app; session cookie is bound to the user_id and the configured allow-list
- **Combined rules**: When multiple methods are configured, **any one passing grants access** (OR logic). IP is checked first; if not allowed, the visitor sees a login page showing whichever of password / WeCom buttons are enabled.
- **Rule priority**: Project-level rules completely override subdomain-level rules

All site requests, including custom domains, pass through Droply access control. Protected responses use `Cache-Control: private, no-store`; project rules override subdomain sessions.

### WeCom (WeWork) QR Code Login

droply supports WeCom QR code login as a third access control method (alongside IP whitelist and password). Visitors click a "Login with WeCom" button, scan with the WeCom mobile app, and gain access if their WeCom user_id is on the allow-list.

**Quick start:**

```bash
# Server: configure WeCom OAuth (in /etc/systemd/system/droply.service)
Environment="DROPLY_WEWORK_CORP_ID=ww1234567890abcdef"
Environment="DROPLY_WEWORK_AGENT_ID=1000002"
Environment="DROPLY_WEWORK_SECRET=xxx"
Environment="DROPLY_WEWORK_REDIRECT_URI=https://login.example.com/_droply/wework/callback"

# CLI: enable WeCom on a project (allow any corp member)
droply access set --subdomain alice --project docs --wework

# Or restrict to specific WeCom user_ids
droply access set --subdomain alice --project docs \
  --wework-user zhangsan --wework-user lisi
```

📖 **Full setup guide**: [docs/wework.md](docs/wework.md) covers creating the WeCom custom app, configuring trusted domains, the OAuth verification file, troubleshooting, and known limitations.

## API

API endpoints use `api.<base-domain>` and JSON. CLI authentication uses `Authorization: Bearer <token>`; the console uses a secure session Cookie, with Origin/CSRF checks on writes.

| Method | Path | Description |
|--------|------|-------------|
| POST | `/auth/register` | Invite-only registration unless explicitly opened |
| POST | `/auth/login` | Login |
| GET | `/healthz` | Database reachability without sensitive details |
| GET | `/auth/me` | Current account and administrator flag |
| GET | `/projects` | Projects accessible to the current account |
| GET / POST | `/admin/invitations` | Administrator invitation list/create |
| DELETE | `/admin/invitations/:id` | Administrator invitation revocation |
| GET / PUT | `/subdomains/:sub/projects/:name/members` | List/set project members |
| DELETE | `/subdomains/:sub/projects/:name/members/:id` | Remove member |
| GET | `/subdomains/:sub/projects/:name/audit` | Paginated project audit |
| GET | `/admin/audit` | Administrator installation audit |
| POST | `/subdomains` | Create subdomain |
| GET | `/subdomains` | List subdomains |
| DELETE | `/subdomains/:name` | Delete subdomain |
| GET | `/subdomains/:sub/projects` | List projects |
| DELETE | `/subdomains/:sub/projects/:name` | Delete project |
| POST | `/subdomains/:sub/projects/:name/deploy` | Deploy (multipart) |
| GET | `/subdomains/:sub/projects/:name/deployments` | Deployment history |
| POST | `/subdomains/:sub/projects/:name/rollback/:version` | Rollback to a retained version |
| POST | `/subdomains/:sub/projects/:name/promote/:version` | Promote a retained preview |
| GET | `/subdomains/:sub/projects/:name/events` | Promotion events |
| GET / POST | `/subdomains/:sub/projects/:name/tokens` | List / create project credentials |
| DELETE | `/subdomains/:sub/projects/:name/tokens/:id` | Revoke a project credential |
| GET / POST | `/subdomains/:sub/projects/:name/cleanup` | Preview / apply retention cleanup |
| POST | `/subdomains/:sub/projects/:name/domains` | Add custom domain |
| GET | `/subdomains/:sub/projects/:name/domains` | List custom domains |
| DELETE | `/subdomains/:sub/projects/:name/domains/:domain` | Remove custom domain |
| POST | `/subdomains/:sub/projects/:name/domains/:domain/verify` | Verify custom domain DNS |
| PUT | `/subdomains/:sub/access` | Set subdomain access control |
| GET | `/subdomains/:sub/access` | Get subdomain access control |
| DELETE | `/subdomains/:sub/access` | Remove subdomain access control |
| PUT | `/subdomains/:sub/projects/:name/access` | Set project access control |
| GET | `/subdomains/:sub/projects/:name/access` | Get project access control |
| DELETE | `/subdomains/:sub/projects/:name/access` | Remove project access control |

## Tech Stack

| Component | Technology |
|-----------|-----------|
| Language | Go |
| HTTP Router | [chi](https://github.com/go-chi/chi) |
| CLI Framework | [cobra](https://github.com/spf13/cobra) |
| Database | SQLite ([modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite)) |
| Password Hashing | bcrypt |
| Cookie Signing | HMAC-SHA256 |
| Rate Limiting | golang.org/x/time/rate |
| Configuration | TOML |
| HTTP/HTTPS | Go net/http + lego/ACME |

## License

MIT
