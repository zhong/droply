# Multi-Platform Build & Auto Release Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add cross-compilation for 4 platforms and GitHub Actions auto-release on tag push.

**Architecture:** Version string injected via ldflags at build time, passed from `main` into `cli.NewRootCmd(version)`. Makefile gets per-platform targets outputting to `dist/`. GitHub Actions workflow: test → build (matrix) → release with checksums.

**Tech Stack:** Go cross-compilation (`CGO_ENABLED=0 GOOS/GOARCH`), GitHub Actions, `gh release create`

**Spec:** `docs/superpowers/specs/2026-03-22-multi-platform-build-design.md`

---

### Task 1: Add version injection to CLI binary

**Files:**
- Modify: `cmd/droply/main.go`
- Modify: `internal/cli/root.go`
- Create: `internal/cli/version.go`

- [ ] **Step 1: Add version var to `cmd/droply/main.go` and pass to NewRootCmd**

```go
package main

import (
	"os"

	"github.com/zhong/droply/internal/cli"
)

var version string

func main() {
	if err := cli.NewRootCmd(version).Execute(); err != nil {
		os.Exit(1)
	}
}
```

- [ ] **Step 2: Update `internal/cli/root.go` to accept version parameter**

Change signature from `NewRootCmd()` to `NewRootCmd(version string)`, and add the version subcommand:

```go
package cli

import "github.com/spf13/cobra"

// NewRootCmd creates and returns the root cobra command for the droply CLI.
func NewRootCmd(version string) *cobra.Command {
	root := &cobra.Command{
		Use:   "droply",
		Short: "droply — deploy static sites from the command line",
	}

	root.AddCommand(newRegisterCmd())
	root.AddCommand(newLoginCmd())
	root.AddCommand(newLogoutCmd())
	root.AddCommand(newWhoamiCmd())
	root.AddCommand(newSubdomainCmd())
	root.AddCommand(newDeployCmd())
	root.AddCommand(newListCmd())
	root.AddCommand(newDomainCmd())
	root.AddCommand(newAccessCmd())
	root.AddCommand(newProjectCmd())
	root.AddCommand(newVersionCmd(version))

	return root
}
```

- [ ] **Step 3: Create `internal/cli/version.go`**

```go
package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newVersionCmd(version string) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version of droply",
		Run: func(cmd *cobra.Command, args []string) {
			if version == "" {
				version = "dev"
			}
			fmt.Println(version)
		},
	}
}
```

- [ ] **Step 4: Build and verify version injection works**

Run:
```bash
go build -ldflags "-X main.version=v0.0.1-test" -o bin/droply ./cmd/droply
./bin/droply version
```
Expected: `v0.0.1-test`

Then without ldflags:
```bash
go build -o bin/droply ./cmd/droply && ./bin/droply version
```
Expected: `dev`

- [ ] **Step 5: Commit**

```bash
git add cmd/droply/main.go internal/cli/root.go internal/cli/version.go
git commit -m "feat: add version injection and droply version command"
```

---

### Task 2: Add version logging to server binary

**Files:**
- Modify: `cmd/droply-server/main.go`

- [ ] **Step 1: Add version var and log it at startup**

Add at the top of `cmd/droply-server/main.go` (after imports):

```go
var version string
```

Add as the first line inside `func main()`:

```go
	if version == "" {
		version = "dev"
	}
	log.Printf("droply-server %s starting", version)
```

- [ ] **Step 2: Build and verify**

Run:
```bash
go build -ldflags "-X main.version=v0.0.1-test" -o bin/droply-server ./cmd/droply-server
./bin/droply-server --help 2>&1 | head -1
```
Note: server will log the version line before starting. You can also just run it briefly and ctrl-c to see the log.

- [ ] **Step 3: Commit**

```bash
git add cmd/droply-server/main.go
git commit -m "feat: add version logging to droply-server"
```

---

### Task 3: Update Makefile with cross-compilation targets

**Files:**
- Modify: `Makefile`

- [ ] **Step 1: Replace entire Makefile content**

```makefile
VERSION ?= $(shell git describe --tags --always --dirty)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build server cli test clean deploy build-all build-darwin-arm64 build-darwin-amd64 build-linux-amd64 build-windows-amd64

build: server cli

server:
	go build -ldflags "$(LDFLAGS)" -o bin/droply-server ./cmd/droply-server

cli:
	go build -ldflags "$(LDFLAGS)" -o bin/droply ./cmd/droply

test:
	go test ./...

clean:
	rm -rf bin/ dist/

deploy:
	git pull
	go build -ldflags "$(LDFLAGS)" -o bin/droply-server ./cmd/droply-server
	sudo systemctl stop droply
	sudo cp bin/droply-server /usr/local/bin/droply-server
	sudo systemctl start droply

build-all: build-darwin-arm64 build-darwin-amd64 build-linux-amd64 build-windows-amd64

build-darwin-arm64:
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o dist/droply-darwin-arm64 ./cmd/droply

build-darwin-amd64:
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/droply-darwin-amd64 ./cmd/droply

build-linux-amd64:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/droply-linux-amd64 ./cmd/droply
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/droply-server-linux-amd64 ./cmd/droply-server

build-windows-amd64:
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/droply-windows-amd64.exe ./cmd/droply
```

- [ ] **Step 2: Verify cross-compilation works**

Run:
```bash
make build-all
file dist/droply-darwin-arm64
file dist/droply-darwin-amd64
file dist/droply-linux-amd64
file dist/droply-server-linux-amd64
file dist/droply-windows-amd64.exe
```
Expected: each binary shows correct architecture (Mach-O arm64, Mach-O x86_64, ELF 64-bit x86-64, PE32+)

- [ ] **Step 3: Verify clean target**

Run:
```bash
make clean && ls dist/ 2>&1
```
Expected: `ls: dist/: No such file or directory`

- [ ] **Step 4: Commit**

```bash
git add Makefile
git commit -m "feat: add cross-compilation targets for 4 platforms"
```

---

### Task 4: Add `dist/` to .gitignore

**Files:**
- Modify: `.gitignore`

- [ ] **Step 1: Add `dist/` line to `.gitignore`**

Add `dist/` after the existing `bin/` line.

- [ ] **Step 2: Commit**

```bash
git add .gitignore
git commit -m "chore: add dist/ to gitignore"
```

---

### Task 5: Add GitHub Actions release workflow

**Files:**
- Create: `.github/workflows/release.yml`

- [ ] **Step 1: Create directory structure**

```bash
mkdir -p .github/workflows
```

- [ ] **Step 2: Create `.github/workflows/release.yml`**

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

- [ ] **Step 3: Validate YAML syntax**

Run:
```bash
python3 -c "import yaml; yaml.safe_load(open('.github/workflows/release.yml'))" && echo "YAML valid"
```
Expected: `YAML valid`

- [ ] **Step 4: Commit**

```bash
git add .github/workflows/release.yml
git commit -m "ci: add GitHub Actions release workflow for multi-platform builds"
```

---

### Task 6: Run all tests and verify

- [ ] **Step 1: Run full test suite with CGO disabled**

Run:
```bash
CGO_ENABLED=0 go test ./...
```
Expected: all tests pass

- [ ] **Step 2: Verify local cross-compilation**

Run:
```bash
make clean && make build-all
ls -lh dist/
```
Expected: 5 binaries in dist/ (4 CLI + 1 server)

- [ ] **Step 3: Clean up**

Run:
```bash
make clean
```
