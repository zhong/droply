# Multi-Platform Build & Auto Release Design

## Overview

Add cross-compilation support for macOS (ARM/x64), Linux x64, and Windows x64. Add GitHub Actions workflow to automatically build and publish releases when a version tag is pushed.

## Key Decisions

1. **Git tag triggered releases** — push `v*` tag to trigger build + release
2. **Pure Go SQLite driver** — migrate from `github.com/mattn/go-sqlite3` (CGo) to `modernc.org/sqlite` (pure Go) to enable simple cross-compilation with `CGO_ENABLED=0`
3. **Platform matrix:**

| Platform | `droply` (CLI) | `droply-server` |
|---|---|---|
| macOS ARM (darwin/arm64) | ✅ | ❌ |
| macOS x64 (darwin/amd64) | ✅ | ❌ |
| Linux x64 (linux/amd64) | ✅ | ✅ |
| Windows x64 (windows/amd64) | ✅ | ❌ |

## 1. SQLite Driver Migration

Replace `github.com/mattn/go-sqlite3` with `modernc.org/sqlite`.

- Change import path in `internal/store/sqlite.go`
- Update driver name from `"sqlite3"` to `"sqlite"`
- Run `go mod tidy` to update dependencies
- Verify all existing tests pass with `CGO_ENABLED=0`

## 2. Makefile Updates

Add cross-compilation targets to the existing Makefile:

```makefile
# Existing targets remain unchanged (build, server, cli, test, clean, deploy)

# New: build all platforms
build-all: build-darwin-arm64 build-darwin-amd64 build-linux-amd64 build-windows-amd64

build-darwin-arm64:
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags "-X main.version=$(VERSION)" -o dist/droply-darwin-arm64 ./cmd/droply

build-darwin-amd64:
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -ldflags "-X main.version=$(VERSION)" -o dist/droply-darwin-amd64 ./cmd/droply

build-linux-amd64:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-X main.version=$(VERSION)" -o dist/droply-linux-amd64 ./cmd/droply
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-X main.version=$(VERSION)" -o dist/droply-server-linux-amd64 ./cmd/droply-server

build-windows-amd64:
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags "-X main.version=$(VERSION)" -o dist/droply-windows-amd64.exe ./cmd/droply
```

Binary naming convention: `droply-{os}-{arch}[.exe]`

## 3. Version Injection

- Add `var version string` in both `cmd/droply/main.go` and `cmd/droply-server/main.go`
- Inject at build time via `-ldflags "-X main.version=v1.0.0"`
- Add `droply version` subcommand to the CLI

## 4. GitHub Actions Workflow

File: `.github/workflows/release.yml`

```yaml
name: Release
on:
  push:
    tags: ['v*']

jobs:
  build:
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
            go build -ldflags "-X main.version=$VERSION" \
            -o droply-${{ matrix.goos }}-${{ matrix.goarch }}${{ matrix.goos == 'windows' && '.exe' || '' }} \
            ./cmd/droply
          if [ "${{ matrix.binaries }}" = "cli+server" ]; then
            CGO_ENABLED=0 GOOS=${{ matrix.goos }} GOARCH=${{ matrix.goarch }} \
              go build -ldflags "-X main.version=$VERSION" \
              -o droply-server-${{ matrix.goos }}-${{ matrix.goarch }} \
              ./cmd/droply-server
          fi
      - uses: actions/upload-artifact@v4
        with:
          name: binaries-${{ matrix.goos }}-${{ matrix.goarch }}
          path: droply-*

  release:
    needs: build
    runs-on: ubuntu-latest
    permissions:
      contents: write
    steps:
      - uses: actions/download-artifact@v4
        with:
          merge-multiple: true
      - name: Create Release
        env:
          GH_TOKEN: ${{ github.token }}
        run: |
          gh release create ${{ github.ref_name }} \
            --repo ${{ github.repository }} \
            --title "${{ github.ref_name }}" \
            --generate-notes \
            droply-*
```

## 5. File Changes Summary

| File | Change |
|---|---|
| `internal/store/sqlite.go` | Migrate SQLite driver import |
| `go.mod` / `go.sum` | Update dependencies |
| `cmd/droply/main.go` | Add version var |
| `cmd/droply-server/main.go` | Add version var |
| `internal/cli/root.go` | Add `version` subcommand |
| `Makefile` | Add cross-compilation targets |
| `.github/workflows/release.yml` | New: auto build & release workflow |
| `.gitignore` | Add `dist/` |
