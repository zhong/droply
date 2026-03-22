# Caddy Cloudflare Token Compatibility Fix

## Problem

Cloudflare's new API tokens use `cfut_`/`cfat_` prefixes and are longer than 50 characters. The `caddy-dns/cloudflare` plugin has a hardcoded regex `^[A-Za-z0-9_-]{35,50}$` that rejects these tokens at startup with:

> `provision dns.providers.cloudflare: API token appears invalid`

Tracked in: https://github.com/caddy-dns/cloudflare/issues/125

## Solution

Use `xcaddy --replace` to substitute the official plugin with ogerman's fork (PR #123), which adds proper regex support for the new token formats.

## Changes

Update the xcaddy build command in both `README.md` and `README.zh-CN.md`:

**Before:**
```bash
xcaddy build --with github.com/caddy-dns/cloudflare
```

**After:**
```bash
xcaddy build \
  --with github.com/caddy-dns/cloudflare \
  --replace github.com/caddy-dns/cloudflare=github.com/ogerman/cloudflare@master
```

Add a note explaining the workaround and linking to the upstream issue for future cleanup.

## Scope

- Documentation only (two README files)
- No code changes
