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
| `isValidAccessCookie()` | `internal/server/site.go` | Add `passwordHash` parameter, include in HMAC verification. When cookie is subdomain-scoped (`cookieProj==""`) but the passed hash is from a project-level rule, look up the subdomain-level rule's hash via `s.store.FindAccessRuleForSite(cookieSub, "")` |
| `handleLogin()` | `internal/server/site.go` | Pass `rule.PasswordHash` to `signCookie()` |
| `siteHandler()` | `internal/server/site.go` | Pass `rule.PasswordHash` to `isValidAccessCookie()` |

### Edge Case: Subdomain-Scoped Cookie with Project-Level Rule

When both a subdomain-level rule and a project-level rule exist:

- `FindAccessRuleForSite()` returns the project-level rule (higher priority)
- But the browser may hold a subdomain-scoped cookie (signed with the subdomain-level rule's hash)
- `isValidAccessCookie()` must detect this: when `cookieProj == ""`, it looks up the subdomain-level rule via `s.store.FindAccessRuleForSite(cookieSub, "")` to get the correct hash for HMAC verification
- This extra DB query only occurs in the rare case where both rule levels coexist AND the user has a subdomain-scoped cookie

### Behavior

- Password updated → `password_hash` changes → old cookie HMAC fails → user must re-enter password
- Password unchanged → `password_hash` unchanged → old cookie remains valid
- Access rule deleted → no rule exists → cookie check not reached (unaffected)
- Subdomain password changed → subdomain-scoped cookies invalidated across all projects
- Project password changed → only project-scoped cookies invalidated; subdomain-scoped cookies unaffected

### Deployment: One-Time Breaking Change

After deployment, all existing cookies will immediately fail HMAC verification because the server will include `password_hash` in the payload but old cookies were signed without it. This is acceptable — all users will simply need to re-enter their password once.

### Test Coverage

1. `TestSiteHandlerCookieInvalidAfterPasswordChange` — Set password → login → get cookie → update password → old cookie rejected → must re-login
2. `TestSiteHandlerSubdomainCookieValidAcrossProjects` — Subdomain-level login → subdomain cookie → access project (with project-level rule) → subdomain cookie still valid
3. `TestSiteHandlerSubdomainPasswordChangeInvalidatesCookie` — Subdomain login → get cookie → change subdomain password → old cookie rejected

### Non-Goals

- Invalidating cookies when access rules are deleted (not needed — no rule means no protection)
- Per-rule HMAC keys (unnecessary complexity)
- Database schema changes (none required)
