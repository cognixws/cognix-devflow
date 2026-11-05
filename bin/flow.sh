#!/bin/sh
# Runs this machine's prebuilt Dev flow engine, or builds one with Go once
# (cached by version) when no prebuilt binary matches.
set -eu
here=$(cd "$(dirname "$0")" && pwd)
os=$(uname -s | tr 'A-Z' 'a-z')
arch=$(uname -m)
case "$arch" in x86_64|amd64) arch=amd64 ;; aarch64|arm64) arch=arm64 ;; esac
bin="$here/flow-$os-$arch"
if [ -x "$bin" ]; then exec "$bin" "$@"; fi
version=$(cat "$here/../VERSION" 2>/dev/null || echo dev)
cache="${XDG_CACHE_HOME:-$HOME/.cache}/picode-devflow/$version"
if [ ! -x "$cache/flow" ]; then
  if ! command -v go >/dev/null 2>&1; then
    echo "Dev flow: no prebuilt engine for $os-$arch and no Go to build one" >&2
    exit 127
  fi
  mkdir -p "$cache"
  (cd "$here/.." && go build -trimpath -o "$cache/flow" ./cmd/flow) >&2
fi
exec "$cache/flow" "$@"
