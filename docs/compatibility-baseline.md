# Refactor compatibility baseline

Baseline: main `473ef90` (M0–M3), before Spec #31. This is the observable contract to preserve during structural refactoring. Deliberate behavior fixes have separate issues and must update the affected contract and tests. Existing bugs are not compatibility promises.

| Surface | Contract | Existing executable evidence |
| --- | --- | --- |
| Management API | Unauthenticated requests return 401 JSON; project visibility and owner/deployer/viewer operations stay scoped. Revoked membership and project tokens cannot publish after waiting for the publication lock. | `TestConsoleSecureSessionAndPermissions`, `TestProjectCollaborationPermissionMatrixAndRevocation`, `TestProjectTokenHTTPAuthorizationMatrix`, `TestPublicationRechecksPermissionAfterWaitingForLock` |
| Deployment API | Production and preview remain distinct; history, promote, rollback and repeated operations retain their status/result semantics. Failed or corrupt uploads preserve production; cleanup protects current references and live readers. | `TestPagesDeploymentLifecycle`, `TestRollbackRetainsVersionsAndReportsRepeatedRequest`, `TestDeployRejectsCorruptionWithoutChangingProduction`, `TestCleanupDryRunAndApplyProtectReferences` |
| CLI | Existing commands, flags, contexts, environment precedence and machine JSON remain usable. CI environment overrides do not write config; deployment uploads are not transparently replayed on redirects or failures. Legacy configuration migrates. | `TestCIEnvironmentPrecedenceWithoutConfigWrites`, `TestCIDeployJSONAndNoReplay`, `TestLoadConfigMigratesLegacy`, `TestCIProjectTokensPreviewAndPromote` |
| Sites | Production, preview and verified custom domains resolve to the intended immutable artifact. Current access rules apply after promotion/rollback; validators track deployment changes. Static configuration is validated before publication; unsafe archives cannot change production. | `TestUnifiedSiteAccessAndStatistics`, `TestRollbackCustomDomainKeepsCurrentAccessRules`, `TestDeploymentValidatorsChangeOnPublishAndRollback`, `TestPagesStaticConfigurationPublication`, `TestDeployUnsafeArchivePreservesProduction` |
| Console | Real Chromium validates login/logout, authorized visibility, viewer refusal, HttpOnly credentials, empty state, confirmation/cancel, promote, rollback, access edits, audit, failures and expired sessions. | `TestConsoleBrowser` (explicit opt-in required) |
| TLS | Built-in TLS serves authorized names; certificate persistence, renewal and HTTP/DNS challenges use a controlled CA. Ordinary test success does not prove ACME ran. | `TestListenersServeTLSAndShutdown`, `TestLocalACME` (explicit opt-in required) |
| Backup | gzip/tar backup format version 1; manifest records `version`, `source_user_version`, `created_at`, `hmac_mode`, `files`, `directories`, `external`. File entries retain path, size and SHA-256; external mappings retain source/path/kind. Restore rejects existing targets, unsupported versions, links, corruption, entry mismatches and unsafe paths. SQLite schema version is 3 at this baseline. HMAC, certificates and external configuration survive new-directory restore; restored production/private/domain behavior works over real HTTP/TLS. | `TestRoundTripAndSecondGeneration`, `TestRestoreRejectsIntegrityVersionLinksAndExisting`, `TestOverrideAndExternalSecrets`, `TestRestoreRealHTTPAndTLSAccessDomainsAndRollback` |
| Installer and releases | Standalone setup preserves explicit upgrade/backup behavior and built-in TLS configuration; no Caddy dependency. CLI targets Darwin arm64/amd64, Linux amd64 and Windows amd64; server target Linux amd64. | `scripts/setup_test.sh`, release build matrix |

The named tests live in the corresponding `internal/cli`, `internal/server`, `internal/hosting`, `internal/certificates` and `internal/backup` packages. This index describes behavior, not internal helper layout; update names when tests move. No byte-for-byte promise is made for JSON whitespace, object key ordering or gzip metadata. Public fields and meanings remain stable.

## Continuous verification

`CI` runs on every PR and main push and can be dispatched manually. Release tag pushes (`v*`) reuse it. There are no path filters, coverage percentage gates or additional overlapping lint stacks.

- `integration`: pure-Go build, vet including integration-tagged code, uncached full integration tests and isolated installer contracts. Verbose Go output exposes the optional browser/ACME skips; these are not acceptance evidence.
- `race`: uncached full integration suite with CGO enabled, race instrumentation and randomized test order.
- `browser`: pinned Playwright/Chromium installation followed by the required browser test. Missing Node, browser or dependencies fail the job.
- Release `acceptance`: local Pebble ACME and new-directory restore with real HTTP/TLS. Both must execute and pass before the original platform build matrix and publishing can proceed. Missing tools, downloads or environment fail the job.

Local equivalents, from the repository root:

```sh
CGO_ENABLED=0 go build ./...
CGO_ENABLED=0 go vet -tags=integration ./...
CGO_ENABLED=0 go test -tags=integration -count=1 -v ./...
sh scripts/setup_test.sh
CGO_ENABLED=1 go test -race -tags=integration -shuffle=on -count=1 ./...
# Install playwright@1.61.0 and its Chromium first; set the module's absolute path.
DROPLY_CONSOLE_BROWSER=1 DROPLY_PLAYWRIGHT_PATH=/absolute/path/to/playwright/index.mjs \
  sh scripts/test-required.sh TestConsoleBrowser ./internal/server
sh scripts/test-acme.sh
sh scripts/test-required.sh TestRestoreRealHTTPAndTLSAccessDomainsAndRollback ./internal/backup
make build-all
```

`test-required.sh` needs Python 3 and fails if its named test skips, disappears, or fails. To check fail-closed behavior, run it for `TestConsoleBrowser` without the opt-in environment variable, or for `TestDoesNotExist`; both must exit nonzero. Enabling the browser test with a nonexistent Playwright module must also fail. A green ordinary integration suite alone is never a claim that browser or ACME acceptance passed.

Go versions follow `go.mod`; see the [minimum/release toolchain policy](toolchain.md). Branch protection is a repository setting: this change supplies checks, but does not claim administrators cannot bypass them.
