#!/usr/bin/env bash
# Run the Go toolchain in a throwaway container, so nothing has to be installed on
# the Pi. Runs as the calling user so generated files and go.sum stay owned by you.
#
#   ./dev.sh vet ./... && ./dev.sh test ./...
#   ./dev.sh tool templ generate
set -euo pipefail

GO_IMAGE=golang:1.27
cd "$(dirname "$0")"
mkdir -p "$HOME/.cache/finance-go"

exec docker run --rm \
	-u "$(id -u):$(id -g)" \
	-v "$PWD":/src -w /src \
	-v "$HOME/.cache/finance-go":/cache \
	-e GOCACHE=/cache/build -e GOMODCACHE=/cache/mod -e HOME=/tmp -e GOTOOLCHAIN=local \
	"$GO_IMAGE" go "$@"
