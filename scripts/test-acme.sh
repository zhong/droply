#!/bin/sh
# Exercise real local ACME HTTP/DNS validation without public CA or DNS credentials.
set -eu
test_tools=$(mktemp -d)
trap 'rm -rf "$test_tools"' EXIT HUP INT TERM
version=v2.10.1
GOBIN="$test_tools" go install "github.com/letsencrypt/pebble/v2/cmd/pebble@$version"
GOBIN="$test_tools" go install "github.com/letsencrypt/pebble/v2/cmd/pebble-challtestsrv@$version"
PEBBLE_DIR="$(go env GOMODCACHE)/github.com/letsencrypt/pebble/v2@$version"
export PEBBLE_DIR
PATH="$test_tools:$PATH" DROPLY_ACME_INTEGRATION=1 sh scripts/test-required.sh TestLocalACME ./internal/certificates -race
