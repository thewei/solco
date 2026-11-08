#!/bin/sh
# Plugin build step for the Solco fork: builds from source. The upstream
# release-asset download is gone — this fork publishes no releases, and a
# silent fallback to source is not what a plugin install should be doing.
set -eu

OUT="bin/solco-automations"
VERSION=$(sed -n 's/^version *= *"\(.*\)"/\1/p' herdr-plugin.toml | head -1)

if ! command -v go >/dev/null 2>&1; then
	echo "solco-automations: Go is not installed; it is required to build this plugin." >&2
	echo "Install Go (https://go.dev/dl/) and reinstall the plugin." >&2
	exit 1
fi

echo "solco-automations: building v${VERSION} from source"
mkdir -p bin
# A developer go.work at the repository root (it links solco-gitkit for the client) does not list this
# module; the plugin never needs it, so build outside any workspace.
GOWORK=off go build -ldflags "-X main.Version=${VERSION}" -o "$OUT" .
chmod +x "$OUT"
echo "solco-automations: built $OUT"
