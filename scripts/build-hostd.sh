#!/usr/bin/env bash
#
# Build the resident Remote Host (cmd/solco-hostd) as a universal macOS binary.
#
#   scripts/build-hostd.sh <output path> [version]
#
# Pure Go (CGO off), so both slices cross-compile from any Mac. Release CI writes it to
# resources/darwin/solco-hostd (mygo ships that folder as Contents/Resources); local installs go through
# scripts/embed-hostd.sh instead.
set -euo pipefail

OUT="${1:?usage: build-hostd.sh <output> [version]}"
VERSION="${2:-dev}"
HERE="$(cd "$(dirname "$0")/.." && pwd)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

for arch in arm64 amd64; do
  (cd "$HERE" && GOTOOLCHAIN=auto CGO_ENABLED=0 GOOS=darwin GOARCH="$arch" \
    go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$TMP/solco-hostd-$arch" ./cmd/solco-hostd)
done
mkdir -p "$(dirname "$OUT")"
lipo -create -output "$OUT" "$TMP/solco-hostd-arm64" "$TMP/solco-hostd-amd64"
chmod +x "$OUT"
