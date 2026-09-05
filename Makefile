export CGO_ENABLED := 0

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

.PHONY: test-integration test-acme
test-integration:
	CGO_ENABLED=0 go test -tags=integration ./...

test-acme:
	sh scripts/test-acme.sh

clean:
	rm -rf bin/ dist/

deploy:
	@echo "Stop and back up the service first; follow docs/operations-m3.md."
	@exit 1

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
