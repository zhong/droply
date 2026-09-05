# Static configuration cache

Issue #58 follows the request-state separation in #46. `staticweb.Load` already
returns an immutable, concurrently reusable `Site`; `staticweb.Options` supplies
path, prefix, private/preview policy and ETag seed separately on each request.
No additional configuration abstraction is needed.

## Measurement

Measured on 2026-09-05, Go 1.27.1, Darwin arm64, Apple M3 Pro, against the
pre-cache implementation at `300259d`. Both benchmark variants run in the same
binary: `load` calls the original `staticweb.Load` for every response, while
`cache` calls the new bounded cache on every response, including its mutex/map
lookup. Each serves a ten-byte regular file from disk through `ServeHTTP` into
an HTTP recorder. The rules fixture includes TOML mode, a header block and one
302 redirect; the other fixture contains no rule files.

Run the benchmarks serially, without other tests or builds:

```sh
go test ./internal/staticweb -run '^$' -bench BenchmarkSiteRules -benchmem -count=10 -benchtime=300ms
go test ./internal/server -run '^$' -bench BenchmarkCachedSite -benchmem -count=10 -benchtime=300ms
benchstat docs/static-cache-performance/cache-before.txt docs/static-cache-performance/cache-after.txt
```

The first benchmark measures separation alone; the second includes the actual
cache lookup. Raw output and normalized comparison inputs are in
[static-cache-performance](static-cache-performance/). Normalization only removes
`/load`, `/reuse` or `/cache` from benchmark names so benchstat can match rows.

| Static response | Load every request | Warm cache | Time change | Bytes/op | Allocs/op |
| --- | ---: | ---: | ---: | ---: | ---: |
| No rule files | 57.32 µs | 49.72 µs | -13.27% | 6436 → 2451 | 48 → 29 |
| With rule files | 244.75 µs | 50.19 µs | -79.49% | 15135 → 2486 | 125 → 30 |

Both time comparisons have p < 0.001, n=10. These are local warm-filesystem,
small-file static-layer measurements, **not** end-to-end server throughput or
network/TLS latency. Authorization and SQLite queries are outside this benchmark.
The hit scenario does not predict miss-heavy workloads or quantify contention
under production concurrency. It does establish repeatable benefit from avoiding
configuration reads and parsing, sufficient for this small bounded cache.

## Lifetime and correctness

- Each Server starts with an empty cache of at most 64 artifact IDs. A fixed FIFO
  evicts one retained configuration when full; hits do not update ordering. The
  existing parser limits each configuration file to 64 KiB and bounds rule/header
  counts. This bounds retained entries and parsed input, not an exact RSS budget.
- First authorized access loads configuration. Parallel misses may duplicate
  temporary parsing work, but insertion keeps one result. A failed load is never
  cached and the next request retries. No background jobs, TTL, generic cache
  library, persisted cache or open file handles are introduced.
- Cleanup forgets entries under the existing deployment write lock before removing
  artifacts. Requests retain the read lock through loading and response completion,
  so removal cannot race with a miss or serving. Process restart discards the cache.
- Artifact content/configuration is immutable after publication. Editing artifact
  directories manually is unsupported; publish a new deployment to change rules.
- Visitor identity, access rules, domain/branch bindings and the production pointer
  remain live store reads. Private, preview, prefix and ETag policy are passed for
  each response; production and preview may safely share compiled configuration.
- The existing filesystem checks on file serving, authorization, URL revocation,
  range/HEAD behavior and protected response headers remain in effect.

## Validation

`TestSiteCacheBoundedAndRetry` covers FIFO capacity, reuse, invalidation, missing
roots and parsing-error retry. `TestSiteCacheConcurrentRequestPolicy` exercises
concurrent misses and different private/preview policies on the same artifact.
`TestCachedRulesFollowDeploymentAndLiveAccess` warms distinct deployment rules,
promotes preview to production and changes/deletes live access rules through the
real API/SQLite/artifact boundary. Existing Pages lifecycle tests cover rollback,
branch movement and cleanup revocation; the request matrix covers GET/HEAD/Range
and rule deletion across legacy, project and preview hosts. Run server/staticweb
under the race detector as well as the full integration suite.
