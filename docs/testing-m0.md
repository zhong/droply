# M0 verification

M0 replaces the Caddy process and Admin API with Droply's unified host router and built-in TLS listeners. It does not add deployment version storage or previews; those remain M1/M2 work.

## Certificate implementation decision

The implementation pins `github.com/go-acme/lego/v4` at `v4.35.2` (MIT), a maintained ACME client with HTTP-01 and a Cloudflare DNS provider. Only the required provider is imported. Neither Caddy nor CertMagic is linked into the application. The version was checked against Go module metadata and the pinned source, rather than copying the current v5 API from main-branch examples.

Lego performs ACME registration, order validation and certificate issuance. Droply owns domain authorization, certificate/account persistence, issuance deduplication, retry backoff, renewal scheduling and safe status reporting. Renewal obtains a replacement certificate using the existing account and validates/persists it before switching the live cache. Certificate and account files are written atomically with private permissions. A CA-specific storage namespace prevents production and test accounts from being confused.

References: [lego library](https://github.com/go-acme/lego/tree/v4.35.2), [license](https://github.com/go-acme/lego/blob/v4.35.2/LICENSE), [Cloudflare provider](https://github.com/go-acme/lego/tree/v4.35.2/providers/dns/cloudflare), [Pebble](https://github.com/letsencrypt/pebble/tree/v2.10.1).

## Repeatable checks

```sh
CGO_ENABLED=0 make build
CGO_ENABLED=0 make test
go vet ./...
make test-integration
make test-acme
sh scripts/setup_test.sh
```

`test-acme` installs pinned Pebble test executables into a temporary directory and exercises a real local CA with local HTTP/DNS challenges. Go downloads require network access on a cold cache; the test never contacts a public CA or modifies public DNS. It verifies trusted TLS handshakes, concurrent issuance, cached restart, renewal, CA failures, DNS propagation/cleanup failures, revoked authorization and failed persistence. It requires a race-detector-capable Go toolchain and runs with the integration build tag.

The ordinary integration suite covers host-based routing, trusted proxy rejection, API permissions, private access, HTTP and manual TLS listeners, startup errors, shutdown and legacy database migration. Tests use temporary SQLite databases, files and listeners. A real Cloudflare production account is not part of automated verification; operators must validate their token's zone permissions when enabling that provider.

Lego may finish an in-progress DNS operation or propagation wait before cancellation takes effect. Service shutdown configuration allows for those bounded provider operations. CA requests and queued issuance waits use cancellable contexts; application resources close only after HTTP requests and background jobs have drained.

Certificate status authorization: authenticated accounts can read sanitized status for the platform base domain and API domain. Tenant subdomain and custom-domain status remains owner-only. Unauthenticated requests cannot read either category. API responses contain expiry, state and controlled failure codes, never ACME account material or provider credentials.
