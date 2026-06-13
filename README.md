# Droply

Multi-user, multi-subdomain static content publishing platform. Publish static websites via CLI with automatic subdomain allocation and HTTPS.

[中文文档](README.zh-CN.md)

## Architecture

```
CLI (droply)                         Browser
     |                                  |
     | upload tar.gz                    | HTTPS
     v                                  v
+-------------------------------------------------+
|                Caddy (443/80)                    |
|          Auto HTTPS + Wildcard TLS               |
+-------------------+-----------------------------+
| api.droplydoc.com |  *.droplydoc.com            |
| reverse_proxy     |  file_server / reverse_proxy|
|    :8080          |  :8081 (protected sites)     |
+--------+----------+-----------------------------+
         |
         v
+------------------+    +-----------------+
|  droply-server   |--->|     SQLite      |
|  API :8080       |    |   droply.db     |
|  Site :8081      |    +-----------------+
+------------------+
```

- **Caddy** — TLS termination, auto HTTPS (wildcard + custom domains), API reverse proxy, static file serving, protected site reverse proxy
- **droply-server** — User auth, upload handling, metadata management, access control, dynamic route updates via Caddy Admin API
- **droply** — CLI client, packages directories and uploads

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

### One-Line Setup

Set up a complete droply server on a fresh VPS (Ubuntu/Debian):

```bash
curl -fsSL https://droplydoc.com/setup.sh | sudo bash
```

The script will prompt you to choose a **TLS mode**:

| Mode | Best For | Requirements |
|------|----------|--------------|
| **on-demand** (default) | Most users, <50 new subdomains/week | Ports 80 + 443 open, A records pointing to server |
| **cloudflare** | Large subdomain count, or port 80 unavailable | Cloudflare API token |
| **manual** | Corporate PKI, custom CA, airgapped | Your own certificate files |

**On-demand mode** (recommended) requires **zero DNS API configuration** — just point your A records to the server and droply handles the rest. Caddy obtains individual certificates per subdomain using HTTP-01 challenge on first access (2-5 second delay on first visit, instant thereafter).

For non-interactive setup:

```bash
# On-demand mode (default)
DOMAIN=example.com TLS_MODE=on-demand curl -fsSL https://droplydoc.com/setup.sh | sudo bash

# Cloudflare mode (wildcard certificate)
DOMAIN=example.com TLS_MODE=cloudflare CF_API_TOKEN=xxx curl -fsSL https://droplydoc.com/setup.sh | sudo bash

# Manual mode (bring your own certs)
DOMAIN=example.com TLS_MODE=manual CERT_PATH=/path/to/cert.pem KEY_PATH=/path/to/key.pem \
  curl -fsSL https://droplydoc.com/setup.sh | sudo bash
```

### TLS Mode Comparison

```
┌──────────────────┬──────────────┬────────────────┬─────────────────┐
│                  │ On-Demand    │ Cloudflare     │ Manual          │
├──────────────────┼──────────────┼────────────────┼─────────────────┤
│ DNS API needed   │ No           │ Yes            │ No              │
│ Port 80 needed   │ Yes          │ No             │ No              │
│ Cert type        │ Per-subdomain│ Wildcard       │ User-provided   │
│ First-visit lag  │ 2-5 sec      │ None           │ None            │
│ LE rate limit    │ 50/week/dom  │ Unaffected     │ N/A             │
│ Subdomain scale  │ Hundreds     │ Unlimited      │ Per cert limit  │
└──────────────────┴──────────────┴────────────────┴─────────────────┘
```

**When to use each mode:**

- **On-demand**: Default choice for most deployments. Works with any DNS provider (no API integration needed). Just configure A records and you're done.
- **Cloudflare**: When you have hundreds of subdomains or need to close port 80 (e.g., corporate firewall). Requires Cloudflare DNS and an API token.
- **Manual**: Enterprise environments with internal PKI, custom certificate authorities, or airgapped networks.

<details>
<summary>Manual setup</summary>

### Prerequisites

- A VPS (Ubuntu/Debian recommended)
- A domain (e.g. `droplydoc.com`) with DNS configured:
  - `A` record: `droplydoc.com` → server IP
  - `A` record: `*.droplydoc.com` → server IP
  - `A` record: `api.droplydoc.com` → server IP
- [Caddy](https://caddyserver.com/docs/install) installed

### 1. Install Caddy

**For on-demand or manual TLS modes** (most users):

```bash
curl -fsSL "https://caddyserver.com/api/download?os=linux&arch=amd64" -o /tmp/caddy
sudo mv /tmp/caddy /usr/bin/caddy
sudo chmod +x /usr/bin/caddy
```

**For Cloudflare mode** (wildcard certificates via DNS-01):

```bash
# Install Go if needed
curl -fsSL https://go.dev/dl/go1.24.1.linux-amd64.tar.gz | sudo tar -C /usr/local -xz
export PATH="/usr/local/go/bin:$PATH"

# Install xcaddy and build Caddy with Cloudflare DNS module
go install github.com/caddyserver/xcaddy/cmd/xcaddy@latest
~/go/bin/xcaddy build --with github.com/caddy-dns/cloudflare
sudo mv caddy /usr/bin/caddy
```

### 2. TLS Configuration

Choose your TLS mode below.

#### Option A: On-Demand TLS (recommended)

Caddy obtains certificates dynamically on first subdomain access. Requires port 80 + 443 open.

Create `/etc/caddy/Caddyfile`:

```caddyfile
{
    admin localhost:2019
    on_demand_tls {
        ask http://localhost:8080/_droply/tls-check
    }
}

*.droplydoc.com, droplydoc.com {
    tls {
        on_demand
    }
    reverse_proxy localhost:8081
}

api.droplydoc.com {
    tls {
        on_demand
    }
    reverse_proxy localhost:8080
}
```

#### Option B: Cloudflare DNS (wildcard certificate)

One wildcard certificate covers all subdomains. Requires Cloudflare API token.

1. Obtain a Cloudflare API token at [dash.cloudflare.com/profile/api-tokens](https://dash.cloudflare.com/profile/api-tokens)
   - **Permissions**: Zone → DNS → Edit
   - **Zone Resources**: Include → Specific zone → `droplydoc.com`

2. Store the token:

```bash
sudo tee /etc/caddy/env > /dev/null << 'EOF'
CLOUDFLARE_API_TOKEN=your-token-here
EOF
sudo chmod 600 /etc/caddy/env
```

3. Create `/etc/caddy/Caddyfile`:

```caddyfile
{
    admin localhost:2019
}

*.droplydoc.com {
    tls {
        dns cloudflare {env.CLOUDFLARE_API_TOKEN}
    }
    reverse_proxy localhost:8081
}

api.droplydoc.com {
    reverse_proxy localhost:8080
}
```

#### Option C: Manual (bring your own certs)

Use your own certificate files (e.g., from corporate PKI).

```bash
# Copy your cert and key to /etc/caddy/
sudo cp /path/to/cert.pem /etc/caddy/cert.pem
sudo cp /path/to/key.pem /etc/caddy/key.pem
sudo chmod 600 /etc/caddy/key.pem
```

Create `/etc/caddy/Caddyfile`:

```caddyfile
{
    admin localhost:2019
}

*.droplydoc.com {
    tls /etc/caddy/cert.pem /etc/caddy/key.pem
    reverse_proxy localhost:8081
}

api.droplydoc.com {
    tls /etc/caddy/cert.pem /etc/caddy/key.pem
    reverse_proxy localhost:8080
}
```

### 3. Deploy droply-server

```bash
# Create data directory
sudo mkdir -p /data/droply/sites

# Download latest release
VERSION=$(curl -fsSL https://api.github.com/repos/zhong/droply/releases/latest | grep '"tag_name"' | sed -E 's/.*"([^"]+)".*/\1/')
curl -fsSL -o /tmp/droply-server "https://github.com/zhong/droply/releases/download/${VERSION}/droply-server-linux-amd64"
sudo mv /tmp/droply-server /usr/local/bin/droply-server
sudo chmod +x /usr/local/bin/droply-server

# Create systemd service
sudo tee /etc/systemd/system/droply.service > /dev/null << 'EOF'
[Unit]
Description=Droply Static Publishing Server
After=network.target caddy.service

[Service]
ExecStart=/usr/local/bin/droply-server \
  --addr :8080 \
  --site-addr :8081 \
  --data-dir /data/droply \
  --domain droplydoc.com \
  --caddy-admin http://localhost:2019
Restart=always
User=www-data

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl daemon-reload
sudo systemctl enable --now droply
```

#### Server Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--addr` | `:8080` | API listen address |
| `--site-addr` | `:8081` | Site server address (protected sites) |
| `--data-dir` | `/data/droply` | Data directory (database + site files) |
| `--domain` | `droplydoc.com` | Base domain |
| `--caddy-admin` | `http://localhost:2019` | Caddy Admin API address |
| `--hmac-secret` | (auto-generated) | Cookie signing key (auto-generated and persisted to `hmac.key` if empty) |
| `--wework-corp-id` | | WeWork corp ID for QR code login (optional) |
| `--wework-agent-id` | | WeWork agent ID (optional) |
| `--wework-secret` | | WeWork agent secret (optional) |
| `--wework-redirect-uri` | | WeWork OAuth callback URL (optional) |

### 4. Start Caddy

```bash
# Create Caddy systemd service
sudo tee /etc/systemd/system/caddy.service > /dev/null << 'EOF'
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
EnvironmentFile=-/etc/caddy/env

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl daemon-reload
sudo systemctl enable --now caddy
```

### 5. Verify

```bash
# Check services
sudo systemctl status droply caddy

# Test API
curl https://api.droplydoc.com

# Logs
sudo journalctl -u droply -f
sudo journalctl -u caddy -f
```

</details>

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

CLI config file is located at `~/.config/droply/config.toml`:

```toml
api_url = "https://api.droplydoc.com"
token = "dp_xxxxxxxxxxxx"
```

This file is automatically created and updated on login/register. For self-hosted instances, create the config file manually and set the `api_url`.

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

After adding a custom domain, add a CNAME or A record at your DNS provider pointing to the output target, then run `droply domain verify` to confirm. Caddy will automatically provision HTTPS certificates for verified custom domains.

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
| `--expire` | Session TTL (e.g. `1h`, `24h`, `7d`, `never`, default `24h`) |

#### How It Works

- **IP whitelist**: Only requests from specified IPs/subnets are allowed
- **Password protection**: Visitors enter a password on a login page; a cookie maintains the session
- **Combined rules**: When both IP and password are configured, both must be satisfied (AND logic)
- **Rule priority**: Project-level rules completely override subdomain-level rules

Protected sites are reverse-proxied through Caddy to droply-server's site serving port (`:8081`), where the server handles verification.

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
| Reverse Proxy/HTTPS | Caddy |

## License

MIT
