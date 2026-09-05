# M1: immutable deployments, rollback and retention

M1 implements Issues #9–#11. Builds still run locally or in external CI. Preview URLs, branch aliases, promotion and project tokens remain later milestones.

## Upgrade from M0

Stop Droply and make a consistent backup of the entire data directory, the service configuration/environment, and the previous server binary. The data backup must include SQLite, the original `sites` directories, certificates, account material and `hmac.key`. Start the new binary against that data only after the backup is complete. The installer’s binary-only backup does **not** replace a data backup.

Startup copies each existing current site into a checksummed immutable artifact. The current deployment record is attached to that real copy; if a site has no current record, startup creates an initial deployment. Older metadata-only records keep their original versions but have `artifact_state=legacy` and `available=false`: they cannot be rolled back because their files were never retained. Duplicate versions or multiple active records stop migration with an explicit error instead of guessing the history. Legacy links, unsupported files or oversized content also require operator attention; extraction limits can be configured before retrying.

The old site directory is retained as a migration backup, and is no longer served or updated. It is outside automatic retention and the managed artifact quota. Remove it manually only after validating the migrated site and retaining a separate backup. Migration needs enough free space for the copy.

Verify old URLs, custom domains, current access rules, `deployment list --json`, and a deployment/rollback of a test project. A second Droply process using the same data directory is rejected on Linux/macOS; an OS-held lock is released even after a process crash. Do not share the directory between servers or modify managed artifact files while the server runs.

To abandon the upgrade, stop the new server and restore the **complete pre-upgrade data and binary** together. Merely replacing the M1 binary with M0 is not a supported downgrade: M0 reads the original site directories, which no longer track new publications.

## Publish and rollback

```sh
droply deploy ./dist --sub alice --project blog
droply deployment list --sub alice --project blog
droply deployment list --sub alice --project blog --json
droply deployment rollback 1 --sub alice --project blog
```

The deployment commands also read `subdomain` and `project` from `.droply.toml`. History shows version, status, production state, creation time, file bytes, artifact state/availability and failure reason. A repeated rollback returns `changed=false`. A rollback can only select an available, intact, successful version in the caller’s project. Deleted, corrupt, failed or metadata-only versions are rejected. Both old URLs and verified custom domains serve the selected version under **current** access rules.

Uploads are serialized in one server to bound aggregate staging usage. Version reservation is transactional and unique per project; successful publication transaction order determines production. Static requests remain available during upload. The publication/rollback/cleanup lock waits for complete in-flight static responses, so a slow download can delay a switch or cleanup, but its files cannot disappear mid-response. Each request resolves one immutable artifact; separate browser requests can straddle a publication. Deployment-specific ETags prevent rapid switches from incorrectly revalidating old content using second-resolution file timestamps.

## Durability and interruption

Managed data lives under `sites/.artifacts/`: `.staging/<artifact-id>` contains incomplete uploads, and `<artifact-id>/files` plus `manifest.json` contain a published artifact. The manifest records sorted paths, sizes and SHA-256 hashes. The history stores its checksum. Archives must have valid gzip checksums and tar termination, use local canonical paths, and contain only regular files and directories. Duplicate entries, traversal, links and special files are rejected.

The server validates and syncs files and directories before renaming the complete artifact, then activates it in one SQLite transaction. SQLite’s unique active deployment is the production authority. This is **not** a transaction spanning SQLite and the filesystem. A crash before the database commit can leave an unreferenced directory but cannot partially switch production. Startup records unfinished uploads as failed, checks retained artifacts and resumes deletion tombstones. An uncertain publication result should be resolved through history before retrying; the server never deletes a potentially committed artifact merely because a commit returned an error.

## Retention and capacity

```sh
# Preview: no deletion; omitted parameters use server policy.
droply deployment cleanup --sub alice --project blog

# Keep the newest 5 successful versions; disable age protection.
droply deployment cleanup --sub alice --project blog --keep 5 --days 0
droply deployment cleanup --sub alice --project blog --keep 5 --days 0 --apply
```

Cleanup always emits JSON. `used_bytes` is managed disk usage for that project **before** cleanup, including manifests and partial staging; `reclaimable_bytes` and `candidates` describe the selection. `deleted_versions` reports completed removals. Partial failures appear in `errors`, and the CLI exits nonzero. Metadata is retained after removal with `available=false` and `artifact_state=deleted`.

The server runs maintenance hourly. Production, in-progress uploads and internal named references are always protected. The reference primitive is reserved for future aliases; M1 does not expose preview or alias management. Count and age protections are combined: a version remains if either applies. A deletion is first marked `deleting` in SQLite, preventing rollback or new references, then files are removed; failures remain retryable across restarts. Aged abandoned staging and unreferenced artifacts are reclaimed as well. Project deletion immediately removes routing and metadata; immutable files become orphans and are reclaimed after the grace period.

| Server option | Default | Meaning |
| --- | --- | --- |
| `--deployment-retain-count` | `10` | Newest successful versions protected; `0` disables count protection |
| `--deployment-retain-days` | `30` | Minimum age protection; `0` disables it |
| `--artifact-orphan-grace` | `1h` | Minimum age for orphan/incomplete artifact reclamation |
| `--artifact-max-bytes` | `0` | Managed artifacts and staging quota; `0` means no configured quota |
| `--deploy-max-expanded-bytes` | `268435456` | File bytes extracted per upload (256 MiB) |
| `--deploy-max-files` | `10000` | Files and directory entries, including implicit parents |

The compressed HTTP upload limit remains 50 MiB, including multipart overhead. Quota accounting includes manifests and partial staging, but excludes SQLite, certificates and retained legacy directories. Physical disk exhaustion also fails the upload while preserving production. Quota limits do not reserve disk space against unrelated applications. New quota settings do not automatically remove protected artifacts to satisfy a smaller limit.

## API and verification

All endpoints require the project owner’s bearer token:

- `GET /subdomains/:sub/projects/:project/deployments`
- `POST /subdomains/:sub/projects/:project/rollback/:version`
- `GET /subdomains/:sub/projects/:project/cleanup?keep=5&days=0` (preview)
- `POST /subdomains/:sub/projects/:project/cleanup?keep=5&days=0` (apply)

```sh
CGO_ENABLED=0 make build
CGO_ENABLED=0 make test
go vet ./...
make test-integration
go test -tags=integration -race ./...
CGO_ENABLED=0 make build-all
sh scripts/setup_test.sh
```

The integration suite uses real temporary SQLite and files, with API/CLI plus site HTTP assertions. It covers corrupt/interrupted archives, limits and quota failure, actual statement/commit failures, current-only legacy migration, crash-state recovery, unique concurrent versions, A→B→A, current access rules/custom domains, damaged/cleaned artifacts, dry-run and count/age retention, references, deletion failure and restart retry, and concurrent serving/publication/rollback/cleanup. Existing M0 ACME verification remains available with `make test-acme`.
