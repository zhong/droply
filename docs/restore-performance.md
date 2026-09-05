# Restore performance (#57)

The restore staging tree now reuses the validated unpacked files through directory
renames. It no longer reads and writes every payload file a second time to build
`ready`. All archive checks, manifest/hash checks, SQLite checks and deployment
reference verification remain in place. The backup format is unchanged.

## Reproducible measurement

Baseline: `e5db47e22fb68b95709ab6d0dc52d4246bd4f6a2` (PR #78), with the
benchmark added but no production changes. Darwin/arm64, Apple M3 Pro, Go 1.27.1,
12 logical CPUs; local heavy tests were paused during timed sampling. Measurements
are on a warm developer filesystem, not a cold-disk or production throughput test.

`BenchmarkRestore` builds a real SQLite/project/artifact fixture plus external
files. Many-files adds 1,000 files of 4,096 bytes each; large-file adds one 32 MiB
file. A fixed PCG seed generates a buffer reused by all files in each workload;
the many-files case emphasizes metadata rather than unique compressed content.
Archive creation and destination cleanup are outside the timed region. The timed
operation is the full `Restore`, including validation and durability calls.

```sh
go test ./internal/backup -run '^$' -bench '^BenchmarkRestore$' -benchtime=1x -count=6 -benchmem
benchstat before.txt after.txt
DROPLY_RESTORE_FOOTPRINT=1 go test ./internal/backup -run '^TestRestoreFootprint$' -v
```

Six samples per case; benchstat from `golang.org/x/perf`
`v0.0.0-20260825160852-19be9d8e6c70`:

| Workload | Before | After | Difference |
| --- | ---: | ---: | ---: |
| Many-files duration | 10.500 s | 9.513 s | -9.39%, p=0.015 |
| Many-files allocated bytes/op | 109.71 MiB | 77.33 MiB | -29.52%, p=0.002 |
| Many-files allocations/op | 84.84k | 74.67k | -11.98%, p=0.002 |
| Large-file duration (interleaved) | 228.5 ms | 202.2 ms | -11.51%, p=0.002 |
| Large-file allocated bytes/op (interleaved) | 1.303 MiB | 1.032 MiB | -20.79%, p=0.002 |
| Large-file allocations/op (interleaved) | 2.635k | 2.466k | -6.43%, p=0.002 |

Sequential large-file sampling originally reported 340.0 ms to 205.6 ms. Because
the baseline drifted relative to an earlier trial, both binaries were rerun in
six alternating before/after pairs, reversing order every pair. The table uses
the smaller, interleaved improvement. These are cumulative Go allocations, not
peak RSS. Raw timed samples are in `restore-performance/`.

A short CPU profile confirmed the restore copy path, but includes fixture setup
and is too short for a precise CPU percentage claim. The removed copying also
eliminates its per-file `io.Copy` allocation; it does not remove hash validation.

## I/O and temporary space

The code performs these payload passes (deployment-specific verification adds
its own pass over referenced artifacts in both versions):

| Operation | Before | After |
| --- | --- | --- |
| Extract archive and hash files | one read/decompress/write/hash | same |
| Verify sealed unpack manifest | one read/hash | same |
| Verify backup inventory/manifest | one read/hash | same |
| Build final staging contents | one full read/write copy | directory renames, metadata only |
| Set final permissions and make durable | copy creation + file sync | chmod + file sync |
| Sync directories and publish | final rename + parent sync | same |

Counts describe the algorithm, not a syscall trace or filesystem physical I/O.
The optional footprint test polls regular-file logical sizes every millisecond;
it runs separately because scanning perturbs timing. It measures observed
scratch contents, excluding the input archive and source fixture. It is not a
filesystem allocated-block measurement, and sampling can miss the exact peak.

| Workload | Before observed scratch bytes | After observed scratch bytes | Final regular-file bytes |
| --- | ---: | ---: | ---: |
| Many-files | 8,928,438 | 4,792,316 | 4,474,446 |
| Large-file | 67,533,953 | 33,770,235 | 33,766,047–33,766,048 |

Observed scratch sizes fell about 46% and 50% respectively. The small manifest
and SQLite metadata account for the remaining overhead and run-to-run byte
variation. Raw footprint output is included alongside the timed samples.

## Safety and failure boundaries

The scratch directory is mode 0700 and lives beside the destination, so both
intermediate directory moves stay on the same filesystem. Moves occur only after
all validation succeeds. Final directories are mode 0700 and regular files 0600,
matching the former copy operation. A metadata walk rejects links/special files,
checks opened-file identity, sets permissions and syncs regular files. Existing
bottom-up directory syncing completes before publication. The final destination
absence check, atomic rename and parent directory sync are unchanged.

Before the final rename, any error/cancellation leaves the destination absent and
removes the private scratch tree. The new test cancels specifically after `data`
has moved into `ready`, verifies cleanup and successfully retries the same archive.
After final rename, a parent-sync error can leave a complete destination, exactly
as before. Power loss can leave private scratch debris before publication; this
change does not add scratch recovery or claim a hardware power-loss test.

Validation includes damaged archives/manifests, future schema versions, limits,
links, cancellation, private permissions, full integration tests and race tests.
The real HTTP/TLS restore test also checks old sessions, access rules, custom
domains and rollback. No validation pass was removed because the copy/space
reduction already provides measurable benefit without weakening those boundaries.
