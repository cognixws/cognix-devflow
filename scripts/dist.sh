#!/usr/bin/env bash
# Builds the prebuilt engines the extension package carries (bin/flow-<os>-<arch>).
set -euo pipefail
cd "$(dirname "$0")/.."
v=$(cat VERSION)
for t in linux/amd64 linux/arm64 darwin/arm64 darwin/amd64; do
  os=${t%/*}; arch=${t#*/}
  CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath -ldflags "-s -w -X main.version=$v" -o "bin/flow-$os-$arch" ./cmd/flow
done
ls -la bin/
