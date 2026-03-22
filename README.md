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

### Build

```bash
git clone https://github.com/zhong/droply.git
cd droply
make build
```

Produces two binaries:
- `bin/droply-server` — Server
- `bin/droply` — CLI client

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

### Prerequisites

- A VPS (Ubuntu/Debian recommended)
- A domain (e.g. `droplydoc.com`) with DNS configured:
  - `A` record: `droplydoc.com` → server IP
  - `A` record: `*.droplydoc.com` → server IP
  - `A` record: `api.droplydoc.com` → server IP
- [Caddy](https://caddyserver.com/docs/install) installed (with DNS challenge support for wildcard certificates)

### 1. Install Caddy

Wildcard certificates require DNS challenge. Use a Caddy build with a DNS provider module. Example with Cloudflare:

```bash
# Build Caddy with Cloudflare DNS module using xcaddy
# Note: --replace is needed to work around a compatibility issue with Cloudflare's
# new API token format (cfut_/cfat_ prefixes). See: https://github.com/caddy-dns/cloudflare/issues/125
# Once the upstream fix is merged, you can remove the --replace line.
xcaddy build \
  --with github.com/caddy-dns/cloudflare \
  --replace github.com/caddy-dns/cloudflare=github.com/ogerman/cloudflare@master
sudo mv caddy /usr/bin/caddy
```

### 2. Obtain Cloudflare API Token

Caddy needs a Cloudflare API token to complete DNS challenges for wildcard certificates (`*.droplydoc.com`) and the `api.droplydoc.com` certificate.

1. Go to [Cloudflare Dashboard](https://dash.cloudflare.com/profile/api-tokens)
2. Click **Create Token**
3. Use the **Edit zone DNS** template, or create a custom token with:
   - **Permissions**: Zone → DNS → Edit
   - **Zone Resources**: Include → Specific zone → `droplydoc.com`
4. Copy the generated token

Store the token on your server for Caddy to use:

```bash
# Create environment file for Caddy (readable only by root)
sudo tee /etc/caddy/env > /dev/null << 'EOF'
CLOUDFLARE_API_TOKEN=your-cloudflare-api-token-here
EOF
sudo chmod 600 /etc/caddy/env
```

### 3. Deploy droply-server

```bash
# Create data directory
sudo mkdir -p /data/droply/sites

# Copy the compiled binary to your server
scp bin/droply-server your-server:/usr/local/bin/

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

### 4. Configure Caddy

Create `/etc/caddy/Caddyfile`:

```caddyfile
{
    admin localhost:2019
}

# Wildcard certificate for all subdomains via DNS challenge
*.droplydoc.com {
    tls {
        dns cloudflare {env.CLOUDFLARE_API_TOKEN}
    }

    # Proxy all subdomain requests to droply-server's site handler,
    # which serves files and enforces access control.
    reverse_proxy localhost:8081
}

# API endpoint — gets its own certificate automatically
api.droplydoc.com {
    reverse_proxy localhost:8080
}
```

Update the Caddy systemd service to load the environment file:

```bash
# Override Caddy's systemd unit to include the env file
sudo systemctl edit caddy
```

Add the following:

```ini
[Service]
EnvironmentFile=/etc/caddy/env
```

Then start/restart:

```bash
sudo systemctl daemon-reload
sudo systemctl restart caddy
```

droply-server automatically registers subdomain routes via the Caddy Admin API on startup. No manual configuration needed.

### 5. Data Directory Structure

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

Copy `bin/droply` to a directory in your `$PATH`:

```bash
sudo cp bin/droply /usr/local/bin/
```

Or install directly with Go:

```bash
go install github.com/zhong/droply/cmd/droply@latest
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
```

```bash
# With .droply.toml, just run:
droply deploy
```

#### Exclusion Rules

The following files and directories are automatically excluded during deployment:

- `.git`
- `node_modules`
- `__pycache__`
- `.DS_Store`
- `.env`
- All hidden directories (starting with `.`)

#### Upload Limit

Maximum **50MB** per deployment.

### List Projects

```bash
droply list --sub alice
```

### Custom Domains

```bash
# Add a custom domain to a project
droply domain add blog.example.com --sub alice --project blog
# Outputs a CNAME target — add this record at your DNS provider

# List custom domains
droply domain list --sub alice --project blog

# Remove a custom domain
droply domain remove blog.example.com --sub alice --project blog
```

After adding a custom domain, add a CNAME record at your DNS provider pointing to the output target. Caddy will automatically provision HTTPS certificates for verified custom domains.

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
