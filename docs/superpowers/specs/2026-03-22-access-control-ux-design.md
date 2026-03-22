# Access Control CLI UX Optimization

## Problem

After setting access control via `droply access set`, the CLI output is not optimized for sharing. Users need to manually compose the access URL and credentials to share with others. Additionally, there's no way to set a password that effectively never expires — the maximum TTL is 30 days.

## Changes

### 1. `--expire never` Support

**CLI (`internal/cli/access.go`):**
- `parseDuration("never")` returns `876000h` (100 years = 3,153,600,000 seconds).
- When displaying TTL, if the value ≥ 315,360,000 (10 years in seconds), show `never` instead of the raw duration.

**Server (`internal/server/access.go`):**
- Remove the TTL upper bound of 2,592,000. New validation: `ttl >= 300` only. This allows the CLI to send the large TTL value without server rejection.

### 2. Single-Line Share Output

After `access set` succeeds, print a copy-friendly single line with only fields that have values, separated by ` | `:

```
https://{subdomain}.droplydoc.com/{project} | Password: {password} | IP: {ips} | Expires: {ttl}
```

**Field rules:**
- **URL**: Always present. Constructed from `{subdomain}.droplydoc.com` + optional `/{project}`.
- **Password**: Only if password is set. Shows the actual password for `auto` or custom; omitted for IP-only rules.
- **IP**: Only if `allowed_ips` is non-empty. Multiple IPs comma-separated.
- **Expires**: Only if password is set (IP-only rules have no session concept). Shows human-friendly duration or `never`.

**URL construction:** The site domain `droplydoc.com` is derived from the existing API URL config (`api.droplydoc.com` → `droplydoc.com`). This keeps it consistent without adding a new config field.

### 3. TTL Display Helper

A `formatTTL(seconds float64) string` function handles display:
- `≥ 315,360,000` → `"never"`
- `≥ 86400` and divisible by 86400 → `"Xd"` (e.g., `"7d"`)
- Otherwise → Go duration string (e.g., `"24h0m0s"`)

### 4. `access get` Also Uses Friendly TTL

The `access get` command also uses `formatTTL` for consistent display.

## Files to Change

| File | Change |
|------|--------|
| `internal/cli/access.go` | `parseDuration` handles `"never"`, new `formatTTL` + `buildShareLine` helpers, update `set` and `get` output |
| `internal/server/access.go` | Remove TTL upper bound check |
| `internal/server/access_test.go` | Add test for large TTL acceptance |
| `internal/cli/access_test.go` | Tests for `parseDuration("never")`, `formatTTL`, share line formatting (create if not exists) |

## Examples

```bash
# Password with default expiry
$ droply access set --subdomain alice --project blog --password auto
Access control updated.
https://alice.droplydoc.com/blog | Password: a1b2c3d4e5f6g7h8 | Expires: 24h

# Password that never expires
$ droply access set --subdomain alice --password auto --expire never
Access control updated.
https://alice.droplydoc.com | Password: xYz123AbCdEf9876 | Expires: never

# IP only
$ droply access set --subdomain alice --ip 10.0.0.0/8
Access control updated.
https://alice.droplydoc.com | IP: 10.0.0.0/8

# Password + IP + custom expiry
$ droply access set --subdomain alice --project docs --password "my-secret" --ip 10.0.0.0/8 --expire 7d
Access control updated.
https://alice.droplydoc.com/docs | Password: my-secret | IP: 10.0.0.0/8 | Expires: 7d
```
