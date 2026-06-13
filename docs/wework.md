# WeCom (WeWork) QR Code Login Integration

This document describes how to enable WeCom (WeWork) QR code login as an access control method for sites deployed on droply.

> 📖 中文版：[wework-zh-CN.md](wework-zh-CN.md)

## Overview

WeCom QR code login is one of three access control methods in droply (alongside IP whitelist and password). All three can be combined — any one passing grants access.

```
                    ┌─────────────────┐
   Visit a site →   │ IP whitelist?   │ ─Yes→ Allow
                    └────────┬────────┘
                             │ No
                    ┌────────▼────────┐
                    │ Valid cookie?   │ ─Yes→ Allow
                    │ (pwd or WeCom)  │
                    └────────┬────────┘
                             │ No
                    ┌────────▼────────┐
                    │ Show login page │
                    │ password input  │
                    │   + WeCom QR    │
                    └─────────────────┘
```

## Architecture

```
                                                  ┌─────────────────┐
                                                  │  WeCom Backend  │
                                                  └────────┬────────┘
                                                           │
                                                           │ 4. OAuth callback with code
                                                           ▼
   ┌──────────┐  1. Click "Login"   ┌──────────────────────────────────┐
   │  Visitor │ ──────────────────▶ │ droply-server :8081 (site)        │
   │ (Browser)│ ◀──────────────────│ /_droply/wework/auth → redirect    │
   └────┬─────┘  2. Redirect to     │ /_droply/wework/callback ← OAuth  │
        │           WeCom OAuth     └──────────────┬───────────────────┘
        │                                          │
        │ 3. Scan QR code                          │ 5. Exchange code → user_id
        ▼                                          ▼
   ┌─────────────┐                       ┌──────────────────┐
   │ WeCom App   │                       │  WeCom API       │
   │ (Mobile)    │                       │  qyapi.weixin... │
   └─────────────┘                       └──────────────────┘
```

## Prerequisites

- A WeCom organization with admin access to create a custom app
- droply-server **v0.4.0 or later** with WeCom flags configured
- Your droply base domain must be reachable from WeCom's servers (for OAuth callbacks)

---

## Step 1: Create a WeCom App

1. Log into [WeCom Admin Console](https://work.weixin.qq.com/)
2. Go to **应用管理** (App Management) → **自建** (Custom) → **创建应用** (Create App)
3. Fill in:
   - **应用名称** (App Name): e.g. "Droply Documentation Access"
   - **应用 Logo**: any icon
   - **可见范围** (Visibility): select departments/members who may scan to log in
4. After creation, note three values from the app detail page:
   - **CorpID**: under **我的企业** (My Org) → **企业信息** (Info) → bottom of page
   - **AgentID**: on the app detail page
   - **Secret**: on the app detail page (you may need to **查看 / View** to reveal)

## Step 2: Configure Trusted Domain

In the app detail page → **网页授权及 JS-SDK** (Web Authorization & JS-SDK) → **设置可信域名** (Set Trusted Domain):

```
Trusted domain: example.com   ← your droply base domain (without leading dot)
```

WeCom will require you to download a verification file (e.g. `WW_verify_AbCdEf123.txt`) and host it at the root of your domain to prove ownership.

> ⚠️ **droply does not yet provide a built-in way to serve this file.** You have two workarounds:
>
> **Option A — Static file via Caddy**: Add a temporary route in `/etc/caddy/Caddyfile`:
> ```caddyfile
> example.com {
>     handle /WW_verify_AbCdEf123.txt {
>         respond "AbCdEf123" 200
>     }
>     # ... your other config
> }
> ```
> Reload Caddy: `sudo systemctl reload caddy`
>
> **Option B — Deploy as a project**: Use droply itself to deploy a directory containing the verification file to a subdomain matching the trusted domain. This only works if the trusted domain happens to be a deployable subdomain.

Once WeCom successfully verifies, you can remove the static route.

## Step 3: Configure droply-server

Add these environment variables to your `/etc/systemd/system/droply.service`:

```ini
[Service]
Environment="DROPLY_WEWORK_CORP_ID=ww1234567890abcdef"
Environment="DROPLY_WEWORK_AGENT_ID=1000002"
Environment="DROPLY_WEWORK_SECRET=xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
Environment="DROPLY_WEWORK_REDIRECT_URI=https://login.example.com/_droply/wework/callback"
ExecStart=/usr/local/bin/droply-server \
  --addr :8080 \
  --site-addr :8081 \
  --data-dir /data/droply \
  --domain example.com \
  --caddy-admin http://localhost:2019
Restart=always
User=www-data
```

Equivalent CLI flags also work:

```ini
ExecStart=/usr/local/bin/droply-server \
  --wework-corp-id ww1234567890abcdef \
  --wework-agent-id 1000002 \
  --wework-secret xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx \
  --wework-redirect-uri https://login.example.com/_droply/wework/callback \
  --domain example.com \
  ...
```

**Important constraints on `DROPLY_WEWORK_REDIRECT_URI`:**

1. The host **must** be one of your droply subdomains (not `api.`), because the callback is served by the site server on port `:8081`.
2. WeCom only allows **one** redirect URI per app. If you want multiple sites (`alice.example.com`, `bob.example.com`) to share WeCom login, you must use a single "login portal" subdomain (e.g. `login.example.com`) for the callback.
3. After the callback, the session cookie is scoped to the callback host. If you visit `alice.example.com` first, you'll need to ensure the cookie can be read there too — see **Known Limitations** below.

Reload systemd and restart:

```bash
sudo systemctl daemon-reload
sudo systemctl restart droply
sudo journalctl -u droply -f | grep -i wework
# Look for: "WeWork OAuth enabled (corp=..., agent=...)"
```

## Step 4: Enable WeCom Login per Project

Use the droply CLI to set an access rule with WeCom enabled.

### Allow any corp member

```bash
droply access set --subdomain alice --project docs --wework
```

Any member of your WeCom organization can scan and log in.

### Restrict to specific users

```bash
droply access set --subdomain alice --project docs \
  --wework-user zhangsan --wework-user lisi
```

`--wework-user` takes the **WeCom user_id**, which you can find in **通讯录 → 成员详情 → 账号** (Contacts → Member detail → Account) on the admin console. It is *not* the display name or phone number.

`--wework-user` implies `--wework`, so the flag is optional when an allow-list is provided.

### Combine with password and IP whitelist

The three methods stack with **OR** semantics — any one passing grants access:

```bash
droply access set --subdomain alice --project docs \
  --ip 10.0.0.0/8 \
  --password "myteamsecret" \
  --wework-user zhangsan
```

This means:
- Visitors from `10.0.0.0/8` → instant access
- Visitors elsewhere → see a login page with both password input and a WeCom QR button
- Password OR successful WeCom scan grants a session cookie

### Subdomain-level rules

Drop `--project` to apply the rule to all projects under a subdomain:

```bash
droply access set --subdomain alice --wework
```

Project-level rules override subdomain-level rules entirely.

## Step 5: Verify

Visit your protected site:

```
https://alice.example.com/docs/
```

What happens depends on the access rule:

- **WeCom-only rule** (no password): the browser is automatically redirected straight into the WeCom OAuth flow — no manual click required.
  - Desktop browser → QR code page
  - WeCom mobile app in-app browser → silent authorization
- **WeCom + password rule**: a login page is shown with both a password input and a **"Login with WeCom"** button so the visitor can pick.

After successful authorization the visitor is redirected to the original URL with a session cookie set.

If the OAuth flow fails (state expired, allow-list miss, etc.), the user is shown the login page with the WeCom button instead of being looped back into another OAuth attempt — a short-lived cookie marker prevents redirect loops for ~60 seconds.

To check the access rule is applied correctly:

```bash
droply access get --subdomain alice --project docs
# Expected output includes:
#   WeCom login: enabled (any corp member)
# or
#   WeCom login: enabled (allow-list: [zhangsan lisi])
```

---

## Reference

### Command Flags

| Flag | Description |
|------|-------------|
| `--wework` | Enable WeCom QR code login (any corp member) |
| `--wework-user <user_id>` | Allow specific WeCom user; repeatable; implies `--wework` |

### Server Flags / Env Vars

| Flag | Env Var | Description |
|------|---------|-------------|
| `--wework-corp-id` | `DROPLY_WEWORK_CORP_ID` | WeCom CorpID |
| `--wework-agent-id` | `DROPLY_WEWORK_AGENT_ID` | WeCom AgentID for the custom app |
| `--wework-secret` | `DROPLY_WEWORK_SECRET` | WeCom app secret |
| `--wework-redirect-uri` | `DROPLY_WEWORK_REDIRECT_URI` | OAuth callback URL (must be a site-served subdomain) |

All four must be set for WeCom login to activate; if any is missing, the server logs a warning and the feature stays off (existing IP/password rules continue to work).

### Public Endpoints

The site server exposes two routes when WeCom is configured:

| Path | Method | Purpose |
|------|--------|---------|
| `/_droply/wework/auth` | GET | Generate state token and redirect to WeCom OAuth |
| `/_droply/wework/callback` | GET | Receive OAuth code, exchange for user_id, set session cookie |

### Cookie Format

Successful WeCom login sets a cookie `_droply_access` with format:

```
v2:{subdomain}:{project}:wework:{userid}:{expiry}:{hmac}
```

The HMAC payload includes `sha256(allowed_users_json + userid)`, so:
- Changing the allow-list invalidates all outstanding cookies
- A user removed from the allow-list immediately loses access on next request

### Security Notes

- **CSRF protection**: OAuth state tokens are single-use, 10-minute TTL, stored in memory
- **No external session storage**: state tokens live only in droply-server process memory; restarting the server invalidates in-flight OAuth flows (users just retry)
- **Token isolation**: WeCom Secret is only read at startup; rotating it requires a server restart
- **HTTPS only**: cookies are set with `Secure; HttpOnly; SameSite=Lax`

---

## Known Limitations

### 1. Single redirect URI per WeCom app

WeCom allows only one OAuth callback URL per app. droply uses a fixed `--wework-redirect-uri` at startup. If you have many subdomains needing WeCom login, you must use a single "portal" subdomain for the callback. Cookies set on the portal subdomain are not automatically shared with other subdomains.

**Current workarounds**:

- **One app per subdomain**: register a separate WeCom app per subdomain, run multiple droply-server processes
- **Parent-domain cookies** (planned): scope the cookie to `.example.com` so all subdomains share the session (sacrifices isolation between subdomains)

### 2. Verification file hosting

droply has no built-in handler for WeCom's domain verification file. Configure it manually in Caddy (see Step 2). A future release may add a `--wework-verify-file` flag.

### 3. WeCom user_id requirement

The `--wework-user` allow-list uses WeCom's internal `user_id`, not display name or email. Admins must look up user_ids in the WeCom console manually.

### 4. External (non-corp) users

The OAuth flow auto-selects the right endpoint based on the visitor's User-Agent:

- **Inside the WeCom mobile app** (User-Agent contains `wxwork/`): uses `snsapi_base` scope for silent authorization — the user is signed in without ever seeing a QR code, because the WeCom in-app browser already has a session.
- **All other browsers** (desktop Chrome/Safari, mobile Safari, etc.): uses the SSO login endpoint that renders a QR code page. Users scan with the WeCom mobile app to authenticate.

Both flows hit the same `auth/getuserinfo` API to exchange the OAuth code for a WeCom user_id, so the rest of the login pipeline is identical.

External contacts who scan/visit will hit an error. This is intentional — droply is designed for internal access control.

---

## Troubleshooting

### "WeWork login is not configured" (503)

Server didn't start with all four `--wework-*` flags. Check `journalctl -u droply` for the startup banner:

```
WeWork OAuth enabled (corp=ww1234..., agent=1000002)
```

If you see `WeWork OAuth NOT enabled: all of corp-id, agent-id, secret, redirect-uri are required` — fix the missing variable in your systemd unit.

### "Access denied: user not in allow-list" (403)

The WeCom user scanned successfully but their user_id isn't in `allowed_wework_users`. Check:

```bash
droply access get --subdomain alice --project docs
# WeCom login: enabled (allow-list: [zhangsan lisi])
```

Verify the user's actual `user_id` in WeCom admin console (not their name or phone).

### "invalid or expired state" (400)

OAuth state tokens expire after 10 minutes. If the user took too long to scan, ask them to refresh the login page. If you see this consistently, check the server clock is in sync (`timedatectl status`).

### WeCom redirects to `redirect_uri_mismatch`

The `DROPLY_WEWORK_REDIRECT_URI` does not match the **trusted domain** in WeCom console. They must share the same root domain. Re-configure Step 2 if needed.

### Login button does not appear on login page

The access rule has `wework_enabled=false` (default) or the server has WeCom not configured. Verify:

```bash
droply access get --subdomain alice --project docs
# Should show "WeCom login: enabled"
```

If the rule is correct but the button still doesn't show, the template only renders WeCom button when `s.wework != nil` server-side — check the server startup log.
