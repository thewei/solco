#!/bin/sh
# Plugin build step for the Solco fork: builds from source. The upstream
# release-asset download is gone — this fork publishes no releases, and a
# silent fallback to source is not what a plugin install should be doing.
set -eu

OUT="bin/herdr-automations"
VERSION=$(sed -n 's/^version *= *"\(.*\)"/\1/p' herdr-plugin.toml | head -1)

if ! command -v go >/dev/null 2>&1; then
	echo "herdr-automations: Go is not installed; it is required to build this plugin." >&2
	echo "Install Go (https://go.dev/dl/) and reinstall the plugin." >&2
	exit 1
fi

echo "herdr-automations: building v${VERSION} from source"
mkdir -p bin
go build -ldflags "-X main.Version=${VERSION}" -o "$OUT" .
chmod +x "$OUT"
echo "herdr-automations: built $OUT"
