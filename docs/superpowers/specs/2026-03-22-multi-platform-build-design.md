# Multi-Platform Build & Auto Release Design

## Overview

Add cross-compilation support for macOS (ARM/x64), Linux x64, and Windows x64. Add GitHub Actions workflow to automatically build and publish releases when a version tag is pushed.

## Key Decisions

1. **Git tag triggered releases** — push `v*` tag to trigger build + release
2. **Pure Go SQLite driver** — already using `modernc.org/sqlite`, no migration needed. Enables `CGO_ENABLED=0` cross-compilation.
3. **Platform matrix:**

| Platform | `droply` (CLI) | `droply-server` |
|---|---|---|
| macOS ARM (darwin/arm64) | ✅ | ❌ |
| macOS x64 (darwin/amd64) | ✅ | ❌ |
| Linux x64 (linux/amd64) | ✅ | ✅ |
| Windows x64 (windows/amd64) | ✅ | ❌ |

## 1. Version Injection

The CLI binary delegates to `cli.NewRootCmd()`, so version must be passed into the cli package:

- Add `var version string` in `cmd/droply/main.go`, set via `-ldflags "-X main.version=..."`
- Change `NewRootCmd()` to `NewRootCmd(version string)` to accept version
- Add `droply version` subcommand in new file `internal/cli/version.go`
- For `droply-server`, add `var version string` in `cmd/droply-server/main.go` and log it at startup

## 2. Makefile Updates

Add cross-compilation targets to the existing Makefile:

```makefile
VERSION ?= $(shell git describe --tags --always --dirty)

.PHONY: build server cli test clean deploy build-all build-darwin-arm64 build-darwin-amd64 build-linux-amd64 build-windows-amd64

# Existing targets remain unchanged (build, server, cli, test, clean, deploy)

# New: build all platforms
build-all: build-darwin-arm64 build-darwin-amd64 build-linux-amd64 build-windows-amd64

build-darwin-arm64:
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags "-s -w -X main.version=$(VERSION)" -o dist/droply-darwin-arm64 ./cmd/droply

build-darwin-amd64:
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -ldflags "-s -w -X main.version=$(VERSION)" -o dist/droply-darwin-amd64 ./cmd/droply

build-linux-amd64:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-s -w -X main.version=$(VERSION)" -o dist/droply-linux-amd64 ./cmd/droply
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-s -w -X main.version=$(VERSION)" -o dist/droply-server-linux-amd64 ./cmd/droply-server

build-windows-amd64:
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags "-s -w -X main.version=$(VERSION)" -o dist/droply-windows-amd64.exe ./cmd/droply

clean:
	rm -rf bin/ dist/
```

Binary naming convention: `droply-{os}-{arch}[.exe]`, with `-s -w` ldflags to strip debug symbols and reduce binary size.

## 3. GitHub Actions Workflow

File: `.github/workflows/release.yml`

```yaml
name: Release
on:
  push:
    tags: ['v*']

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      - run: CGO_ENABLED=0 go test ./...

  build:
    needs: test
    runs-on: ubuntu-latest
    strategy:
      matrix:
        include:
          - goos: darwin
            goarch: arm64
            binaries: cli
          - goos: darwin
            goarch: amd64
            binaries: cli
          - goos: linux
            goarch: amd64
            binaries: cli+server
          - goos: windows
            goarch: amd64
            binaries: cli
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      - name: Build
        run: |
          VERSION=${GITHUB_REF_NAME}
          CGO_ENABLED=0 GOOS=${{ matrix.goos }} GOARCH=${{ matrix.goarch }} \
            go build -ldflags "-s -w -X main.version=$VERSION" \
            -o dist/droply-${{ matrix.goos }}-${{ matrix.goarch }}${{ matrix.goos == 'windows' && '.exe' || '' }} \
            ./cmd/droply
          if [ "${{ matrix.binaries }}" = "cli+server" ]; then
            CGO_ENABLED=0 GOOS=${{ matrix.goos }} GOARCH=${{ matrix.goarch }} \
              go build -ldflags "-s -w -X main.version=$VERSION" \
              -o dist/droply-server-${{ matrix.goos }}-${{ matrix.goarch }} \
              ./cmd/droply-server
          fi
      - uses: actions/upload-artifact@v4
        with:
          name: binaries-${{ matrix.goos }}-${{ matrix.goarch }}
          path: dist/droply-*

  release:
    needs: build
    runs-on: ubuntu-latest
    permissions:
      contents: write
    steps:
      - uses: actions/download-artifact@v4
        with:
          merge-multiple: true
      - name: Generate checksums
        run: sha256sum droply-* > checksums.txt
      - name: Create Release
        env:
          GH_TOKEN: ${{ github.token }}
        run: |
          gh release create ${{ github.ref_name }} \
            --repo ${{ github.repository }} \
            --title "${{ github.ref_name }}" \
            --generate-notes \
            droply-* checksums.txt
```

## 4. File Changes Summary

| File | Change |
|---|---|
| `cmd/droply/main.go` | Add version var, pass to NewRootCmd |
| `cmd/droply-server/main.go` | Add version var, log at startup |
| `internal/cli/root.go` | Change `NewRootCmd()` → `NewRootCmd(version string)`, add version cmd |
| `internal/cli/version.go` | New: `droply version` subcommand |
| `Makefile` | Add VERSION var, cross-compilation targets, update clean/phony |
| `.github/workflows/release.yml` | New: test → build → release workflow |
| `.gitignore` | Add `dist/` |
