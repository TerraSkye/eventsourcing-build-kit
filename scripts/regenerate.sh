#!/usr/bin/env bash
# Regenerates what the kit checks in from esgen's output:
#   examples/cart/app  from examples/cart/model
#   templates/root     the empty project the Event Modelers CLI installs
# and verifies the example: gofmt, go vet, go build, go test.
set -euo pipefail
cd "$(dirname "$0")/.."
root=$PWD

esgen() { go -C "$root/templates/build-kit/gen" run . "$@"; }

# Start from a clean tree, keeping go.mod and go.sum (go mod tidy owns them).
clean() {
	find "$1" -mindepth 1 -type f ! -name go.mod ! -name go.sum ! -name README.md -delete
	find "$1" -mindepth 1 -type d -empty -delete
}

echo "== examples/cart/app"
clean examples/cart/app
esgen -slices "$root/examples/cart/model" -out "$root/examples/cart/app" -module example.com/cart -init
(
	cd examples/cart/app
	go mod tidy
	test -z "$(gofmt -l .)" || { gofmt -l .; exit 1; }
	go vet ./...
	go build ./...
	go test ./...
)

echo "== templates/root"
clean templates/root
esgen -slices "$root/templates/root/.no-slices" -out "$root/templates/root" -module app -init
(
	cd templates/root
	go mod tidy
	go vet ./...
	go test ./...
)

echo "== esgen"
(cd templates/build-kit/gen && go vet ./... && go test ./...)
