# Cookie Invalidation on Password Change

**Date:** 2026-03-22
**Status:** Approved

## Problem

When a user updates the access control password (via CLI or API), browsers that previously authenticated still hold valid cookies. These cookies continue to pass HMAC verification because the signature is computed from `subdomain:project:expiry` — none of which change when the password is updated. Users expect that changing the password invalidates all existing sessions.

## Solution

Include `password_hash` in the HMAC payload used for cookie signing and verification. The cookie format visible to the browser remains unchanged.

### HMAC Payload Change

```
Before: HMAC-SHA256(subdomain:project:expiry)
After:  HMAC-SHA256(subdomain:project:expiry:password_hash)
```

`password_hash` participates only in HMAC computation — it does NOT appear in the cookie value. The browser-visible cookie format remains: `{subdomain}:{project}:{expiry}:{hmac_hex}`.

### Affected Functions

| Function | File | Change |
|----------|------|--------|
| `signCookie()` | `internal/server/site.go` | Add `passwordHash` parameter, include in HMAC payload |
| `isValidAccessCookie()` | `internal/server/site.go` | Add `passwordHash` parameter, include in HMAC verification |
| `handleLogin()` | `internal/server/site.go` | Pass `rule.PasswordHash` to `signCookie()` |
| `siteHandler()` | `internal/server/site.go` | Pass `rule.PasswordHash` to `isValidAccessCookie()` |

### Behavior

- Password updated → `password_hash` changes → old cookie HMAC fails → user must re-enter password
- Password unchanged → `password_hash` unchanged → old cookie remains valid
- Access rule deleted → no rule exists → cookie check not reached (unaffected)

### Test Coverage

New test: `TestSiteHandlerCookieInvalidAfterPasswordChange`
- Set password protection → login → get cookie → update password → request with old cookie → expect login page shown

### Non-Goals

- Invalidating cookies when access rules are deleted (not needed — no rule means no protection)
- Per-rule HMAC keys (unnecessary complexity)
- Database schema changes (none required)
