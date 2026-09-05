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
   │  Visitor │ ──────────────────▶ │ droply-server HTTPS entry        │
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
- A current droply-server installation with WeCom configuration
- The configured callback and protected site must be reachable over HTTPS

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

Configure the trusted domain in the app's **网页授权及 JS-SDK** settings to match your intended OAuth setup. If the console requires a verification file such as `WW_verify_AbCdEf123.txt`, it must be reachable at the root of the exact hostname being verified.

Droply can serve this as an ordinary static file from a **public project's root hostname** (the project URL returned by deployment) or a **verified custom domain** bound to that project. Include the supplied file unchanged in the project's deployed directory, and check `https://<that-host>/WW_verify_AbCdEf123.txt` without a login cookie. Access rules must not block the verification request. A legacy URL such as `/docs/WW_verify_AbCdEf123.txt` is not a root-level file.

There is no special verification-file handler on `api.<domain>` or the bare base domain. Deploying a file on a different hostname does not verify those hosts. Confirm the hostname required by the console before choosing where to publish the file; Droply does not automatically provision it.

## Step 3: Configure droply-server

API and site traffic share the configured HTTP/HTTPS entry and are routed by Host. A separate site port and Caddy are not required. Follow [TLS and deployment configuration](operations-m3.md) first; the bare binary defaults to HTTP on `:8080`, so it does not enable HTTPS by itself. Use an existing HTTPS listener or trusted TLS gateway for the OAuth flow.

For an installation created by the repository installer, add or update these four values in its existing environment file (default `/etc/droply/env`). Preserve the other settings, file permissions, service user and `ExecStart`:

```sh
DROPLY_WEWORK_CORP_ID=ww1234567890abcdef
DROPLY_WEWORK_AGENT_ID=1000002
DROPLY_WEWORK_SECRET=xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
DROPLY_WEWORK_REDIRECT_URI=https://api.example.com/_droply/wework/callback
```

The equivalent flags are listed in the reference below. A custom service should use its existing environment/configuration mechanism; this example does not replace the service unit.

The callback path is `/_droply/wework/callback`. `api.<domain>` supports this central callback explicitly; a registered site hostname or verified custom domain can also serve it. The configured URL must point to a reachable handler and match the OAuth setup. Unknown hosts are rejected. Startup URL validation checks syntax, not domain ownership or the remote app configuration.

Droply uses one configured callback URL per server. When callback and original site are different subdomains of the configured base domain, the callback sets a parent-domain cookie so the redirect back can retain the session. On the same host it uses a host-only cookie. A callback on `api.example.com` cannot share its cookie with an unrelated custom domain; see Known Limitations.

Restart the existing service and inspect its status/logs:

```bash
sudo systemctl restart droply
sudo systemctl status droply
sudo journalctl -u droply -n 50 --no-pager
```

All four values unset leaves WeCom disabled. Setting only some values fails startup with `all four WeCom options must be configured`; an invalid URL fails with `invalid WeCom callback URL`. These checks run before opening the installation's data resources. Do not rely on a warning or an OAuth-enabled startup banner.

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
| `--wework-redirect-uri` | `DROPLY_WEWORK_REDIRECT_URI` | OAuth callback URL (`api.<domain>` or an allowed site host) |

All four unset disables WeCom. If any is set, all four are required and the callback URL must pass startup validation; incomplete configuration stops startup.

### Public Endpoints

The unified listener exposes these site routes when WeCom is configured. The API host also serves the callback route; start authorization on the protected site:

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

### 1. One configured callback and cookie boundaries

Droply uses one `--wework-redirect-uri` per server. Different hosts beneath the configured base domain can use a central callback such as `api.example.com`; parent-domain cookies are already implemented. The signed cookie still identifies its subdomain/project and is checked against current access rules. Making a cookie available to sibling hosts does not grant access to every project.

A callback and original site without that shared base-domain suffix do not receive a shared cookie. Central login across unrelated custom domains is unsupported. A same-host callback uses a host-only cookie.

### 2. Verification file hosting

Publish the file at a public project's root hostname or an already verified custom domain, as described in Step 2. There is no automatic verification-file route on the API host or bare base domain, and no `--wework-verify-file` option.

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

The running server has WeCom disabled, for example because all four settings are unset. Check the actual service environment and `journalctl -u droply`. A partial configuration prevents the current server from starting; complete all four settings and restart. A failed restart is not a running server with a warning-only disabled feature.

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

Compare the exact `DROPLY_WEWORK_REDIRECT_URI` with the app configuration in the WeCom console, including scheme, host and callback path. Re-check Step 2; Droply does not validate the remote configuration at startup.

### Login button does not appear on login page

The access rule has `wework_enabled=false` (default) or the server has WeCom not configured. Verify:

```bash
droply access get --subdomain alice --project docs
# Should show "WeCom login: enabled"
```

If the rule is correct but the button still does not show, verify that the running service received all four WeCom settings. Check its status and logs for startup failures; the login template only shows the button when the service has a WeCom client configured.
