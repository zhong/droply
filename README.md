# Droply

Multi-user, multi-subdomain static content publishing platform. Publish static websites via CLI with automatic subdomain allocation and HTTPS.

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

## Quick Start

### Install CLI

One-line install (auto-detects OS and architecture):

```bash
curl -fsSL https://droplydoc.com/install.sh | bash
```

Or install a specific version:

```bash
VERSION=v0.1.0 curl -fsSL https://droplydoc.com/install.sh | bash
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

```bash
make test
```

### Deploy to Server

On the server, pull latest code, rebuild, and restart the service:

```bash
make deploy
```

### Deploy Website

The project website at `droplydoc.com` is hosted via droply itself. To update:

```bash
cd website
droply deploy
```

## Server Deployment

New installations run only `droply-server`; Caddy is not required. Existing installations should follow the [M0 migration and rollback guide](docs/migration-m0.md) before changing their service.

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

`UPGRADE=1` backs up and replaces only the existing binary, preserving the service, environment, data and certificates without restarting. Back up the database and content using the migration guide before switching. `VERSION=vX.Y.Z` pins a release; `DATA_DIR` selects a new data directory; `ACME_CA` can select an ACME staging endpoint.

### Run directly

```bash
# Automatic per-host HTTPS; certificates persist in data-dir/certificates
./bin/droply-server --domain example.com --data-dir ./data \
  --addr :80 --https-addr :443 --tls-mode auto --acme-email admin@example.com

# DNS wildcard HTTPS
./bin/droply-server --domain example.com --data-dir ./data \
  --addr :80 --tls-mode cloudflare --cloudflare-token-file ./cloudflare-token

# Manual PEM certificate and key
./bin/droply-server --domain example.com --data-dir ./data \
  --addr :8080 --https-addr :8443 --tls-mode manual --tls-cert ./cert.pem --tls-key ./key.pem

# HTTP behind an optional existing TLS gateway
./bin/droply-server --domain example.com --data-dir ./data \
  --addr 127.0.0.1:8080 --tls-mode http --trusted-proxies 127.0.0.1/32
```

Binding ports 80/443 requires permission; the installer grants `CAP_NET_BIND_SERVICE` through systemd. Automatic per-host mode requires public ACME challenge reachability. Cloudflare mode requires `Zone:DNS:Edit` and `Zone:Zone:Read` permissions for the zones of the names being issued. Administrators renew manual certificates and restart the server to load them.

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

Run `droply-server --help` for all flags. Normal HTTP shutdown drains for up to 15 seconds; an in-flight DNS job can delay exit until its library timeout (up to five minutes), so the installed unit allows 360 seconds. `--site-addr` temporarily supports an additional unified HTTP listener; `--caddy-admin` is ignored. New installations should omit both. `on-demand` remains an alias for `auto`.

```bash
sudo systemctl status droply
sudo journalctl -u droply -f
```

### Data Directory Structure

```
/data/droply/
├── droply.db              SQLite database
└── sites/
    ├── alice/
    │   ├── blog/          alice.droplydoc.com/blog
    │   └── portfolio/     alice.droplydoc.com/portfolio
    └── bob/
        └── docs/          bob.droplydoc.com/docs
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

This file is automatically created and updated on login/register. Old single-server config (top-level `api_url` + `token`) is **silently migrated** to `contexts.default` on first use.

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

**Resolution priority** (highest to lowest):
1. `--context X` flag on the command line
2. `context = "X"` in `.droply.toml`
3. `current_context` in `~/.config/droply/config.toml`

### Register and Login

```bash
# Register a new account
droply register
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

# Example output:
# Packaging ./dist...
# Deploying to alice.droplydoc.com/blog...
# Deployed! Version 1
# URL: https://alice.droplydoc.com/blog
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

Maximum **50MB** per deployment.

### Manage Projects

```bash
# List projects in a subdomain
droply project list --sub alice

# Delete a project (removes all files and deployments)
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

All API endpoints are accessed via `api.droplydoc.com` in JSON format. Authentication uses `Authorization: Bearer <token>` header.

| Method | Path | Description |
|--------|------|-------------|
| POST | `/auth/register` | Register |
| POST | `/auth/login` | Login |
| POST | `/subdomains` | Create subdomain |
| GET | `/subdomains` | List subdomains |
| DELETE | `/subdomains/:name` | Delete subdomain |
| GET | `/subdomains/:sub/projects` | List projects |
| DELETE | `/subdomains/:sub/projects/:name` | Delete project |
| POST | `/subdomains/:sub/projects/:name/deploy` | Deploy (multipart) |
| GET | `/subdomains/:sub/projects/:name/deployments` | Deployment history |
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
