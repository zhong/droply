# Go toolchain policy

Droply requires **Go 1.27.1 or newer** to build from source. The `go` directive in `go.mod` is the source of truth for the minimum Go version and the exact toolchain used by CI and releases. At this revision both are **1.27.1**; there is no separate Go 1.26 compatibility promise or second version matrix.

The version was checked on 2026-09-05 against the [official stable downloads](https://go.dev/dl/) and [release history](https://go.dev/doc/devel/release). Choosing one supported version keeps this application's maintenance small and lets later syntax changes use the same tested language baseline. Installed prebuilt binaries do not require Go.

CI and release jobs install the exact version from `go.mod`, print `go version`, and set `GOTOOLCHAIN=local` so an implicit download cannot silently replace the selected compiler. Do not add a separate `toolchain` directive or floating `stable` release version. To change the release compiler, update `go.mod` in an isolated change, review release notes, run the checks below and update versioned examples. Later patch releases require the same verification; newer local compilers can be used for development but do not prove the pinned release compiler passes.

From the repository root with Go 1.27.1 installed:

```sh
export GOTOOLCHAIN=local
go version  # must report go1.27.1 for release acceptance
CGO_ENABLED=0 go build ./...
CGO_ENABLED=0 go vet -tags=integration ./...
CGO_ENABLED=0 go test -tags=integration -count=1 ./...
make build-all
```

The minimum and release versions are identical, so this is one actual compiler run, not two labels on different assumptions. CI repeats it on Linux. Full integration tests include CLI/API JSON, archive rejection, backup integrity/round trips, migration, HTTP and TLS contracts. The separate race, Chromium, local ACME and real new-directory restore checks documented in the [compatibility baseline](compatibility-baseline.md) remain required for the corresponding CI/release acceptance.

Pure-Go release targets remain:

| Binary | OS | Architecture |
| --- | --- | --- |
| CLI | Darwin | arm64, amd64 |
| CLI | Linux | amd64 |
| CLI | Windows | amd64 |
| Server | Linux | amd64 |

All release builds use `CGO_ENABLED=0`. Race testing alone enables CGO for Go's race instrumentation. This version alignment does not change dependencies, JSON APIs, wire/storage formats or target platforms; syntax modernization is a separate change.
