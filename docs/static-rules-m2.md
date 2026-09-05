# Versioned static-site rules (M2)

Droply reads `_droply.toml`, `_headers` and `_redirects` from the root of each deployment's files. They belong to the immutable artifact, so previews, promotion and rollback use that artifact's configuration. The deployment path validates these files before publication. They are never public downloads. The CLI's optional `.droply.toml` selects a server/project; it is a different file.

## Static and SPA modes

Without `_droply.toml`, the mode is `static`. To enable SPA navigation:

```toml
[site]
mode = "spa"
```

`static` and `spa` are the only modes. Unknown TOML keys and invalid values reject the deployment. An empty configuration retains static mode.

Both modes serve regular files, resolve `/about` to `about.html`, and serve `folder/index.html` at `/folder/`. A request for `/folder` redirects to `/folder/` when that directory contains an index. Explicit `.html` URLs remain usable. `/about/` does not alias `about.html`. Directory listings are disabled.

SPA fallback serves `/index.html` only for missing extensionless GET/HEAD navigation when `Accept` permits HTML, or the header is absent. An explicit HTML quality of zero is respected. Missing `.js`, `.css`, images and other file extensions remain 404; catch-all rewrites also cannot turn a missing asset into an HTML entry point. JSON-only requests do not receive an implicit SPA fallback.

A regular `/404.html` replaces the missing-page body while retaining HTTP **404**, including for HEAD and conditional requests. Without it, Droply returns a plain 404. Rewrites do not execute server-side code.

## File and platform boundaries

All URL paths must be local, canonical paths. Dot segments, repeated slashes, backslashes, control characters and percent escapes left after URL decoding are rejected. Hidden path components are not served. The first component `.well-known` is the sole exception, allowing files such as `/.well-known/security.txt`; missing `.well-known` resources never use SPA fallback. `/.well-known/acme-challenge` and its descendants belong to the HTTPS service and cannot be served or targeted by deployment rules.

The following components are reserved at every depth: `_droply`, `_auth`, `_internal`, `_droply.toml`, `_headers`, `_redirects`, and `manifest.json`. Other hidden metadata such as `.git` and `.env` is also blocked. A PWA can use a non-reserved filename such as `manifest.webmanifest`. Symlinks and directory browsing are not supported.

Rules execute inside the already-authorized project. Rewrites resolve only that artifact's files; they cannot dispatch into platform management, login or other project handlers. Platform authentication must run before the static-site handler, including on previews.

## `_headers`

A block starts with an unindented exact path or a path ending in one `*`. Indented lines contain `Name: value`:

```text
/*
  X-Content-Type-Options: nosniff
  Referrer-Policy: strict-origin-when-cross-origin
  Content-Security-Policy: default-src 'self'
/assets/*
  Cache-Control: public, max-age=3600
```

Matching uses the original project-relative request path. Every matching block applies in file order; a later block replaces an earlier value for the same header. Header names are case-insensitive. Duplicate path blocks, duplicate headers within a block, empty blocks, non-terminal wildcards, placeholders, deletion syntax, host conditions and other extended syntax are unsupported and reject deployment. Blank lines and full-line `#` comments are allowed; inline comments are part of a header value.

Droply rejects rules for authentication/cookie headers (`Authorization`, `Proxy-Authorization`, `WWW-Authenticate`, `Proxy-Authenticate`, `Authentication-Info`, `Cookie`, `Set-Cookie`, `Set-Cookie2`), `Location`, `Host`, hop-by-hop headers (`Connection`, `Keep-Alive`, `Proxy-Connection`, `Transfer-Encoding`, `TE`, `Trailer`, `Upgrade`), and representation-management headers (`Content-Length`, `Content-Range`, `Content-Encoding`, `Accept-Ranges`, `ETag`, `Last-Modified`). Use `_redirects` for redirects. Precompressed representation negotiation and proxying are outside this subset.

### Cache and response constraints

- HTML is always `Cache-Control: no-cache`, permitting storage with revalidation on the next use.
- A basename containing a dot- or hyphen-delimited hexadecimal fingerprint of at least eight characters, such as `app.0123abcd.js`, defaults to `public, max-age=31536000, immutable`. Use genuinely content-derived filenames and change the filename when its bytes change.
- Other files default to `public, max-age=0, must-revalidate`.
- `_headers` can customize public non-HTML caching. It cannot relax HTML revalidation or private-site constraints.
- Private responses always use `private, no-store` and add `Vary: Cookie`, preserving other Vary values. This applies to redirects, errors, 304 and range responses as well.
- Previews always set `X-Robots-Tag: noindex, nofollow`, overriding user rules.

ETags incorporate the immutable deployment identity and selected representation. A rollback or publication therefore cannot mistakenly reuse a modification-time validator from another artifact. File-time `Last-Modified` validators are not used. GET, HEAD, `If-None-Match`, `If-Match`, byte ranges and ETag-based `If-Range` use Go's static response implementation. Date-based `If-Range` falls back to the complete response. Custom 404 pages remain 404 and ignore range/conditional headers.

## `_redirects`

Each non-comment line has exactly three whitespace-separated fields:

```text
/old /new 301
/docs/* /guide/:splat 302
/search /find?source=old 303
/external https://example.org/ 307
/page /landing.html 200
```

Sources are exact project-relative paths or paths with one terminal `*`; the captured suffix can appear once as `:splat` in the target path. Named parameters, conditions, forced-status suffixes and omitted status codes are unsupported. The first matching rule wins.

Supported redirect codes are **301, 302, 303, 307 and 308**. External destinations must use HTTP or HTTPS without embedded credentials. Protocol-relative destinations such as `//example.org/path` are rejected. External redirects never fetch or proxy the destination.

Status **200** is a terminal local rewrite: Droply resolves its target as a file/clean HTML/directory index inside the same artifact. It does not re-run `_redirects` on the rewritten path, and a directory target is served without changing the visible URL. A 200 rewrite to an external destination is rejected. A common `/* /index.html 200` rule is accepted when `index.html` exists, subject to the missing-asset restriction above.

A target without a query preserves the incoming query. A target with an explicit query replaces it. A trailing `?` explicitly clears it. Captures cannot be substituted into the host, query or fragment. For old project URLs such as `/blog/old`, local redirect locations retain `/blog`; standalone project and preview hosts use root-relative locations. External locations do not receive a project prefix.

Validation rejects unsafe/reserved targets and possible local rule cycles, including cycles through directory slash normalization. Cycle detection is deliberately conservative for overlapping wildcard rules. Direct runtime self-redirects are rejected as an additional defense; terminal rewrites never recursively follow another rule. External redirect chains on other servers cannot be checked by Droply.

## Limits and verification

Each configuration/rule file is limited to 64 KiB. Rule lines are limited to 2,048 bytes, with at most 100 redirect rules, 100 header blocks and 1,000 total header declarations. Errors reject configuration instead of silently ignoring unsupported directives.

The `internal/staticweb` HTTP tests cover mode selection, deep links, missing assets, custom 404/HEAD, old-path prefixes, query behavior, unsafe rules, directory-loop detection, private/preview constraints, version validators and byte ranges. Run:

```sh
go test -race ./internal/staticweb
```
