#!/usr/bin/env bash
# [INPUT]: Local macOS toolchain, mygo.config.ts version, existing source and optional --verify-only.
# [OUTPUT]: Fully packaged Solco.app and DMG with signed universal resident hostd Helper.
# [POS]: Repeatable local counterpart to release.yml's resident Helper build step; never installs to user HOME.
# [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
#
# scripts/build-complete-macos.sh                 # build full local macOS artifact
# scripts/build-complete-macos.sh --verify-only   # verify current build, no compilation
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

APP="$ROOT/build/darwin-arm64/Solco.app"
HELPER="$APP/Contents/Resources/solco-hostd"
VERSION="$(sed -nE 's/^[[:space:]]*version:[[:space:]]*"([^"]+)".*/\1/p' mygo.config.ts | head -n1)"
if [[ -z "$VERSION" ]]; then
  echo "Cannot determine Solco version from mygo.config.ts" >&2
  exit 1
fi
DMG="$ROOT/build/darwin-arm64/Solco $VERSION arm64.dmg"

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "This full macOS package check requires macOS." >&2
  exit 2
fi

verify() {
  if [[ ! -x "$HELPER" ]]; then
    echo "Missing resident Host Helper in $APP (use this script without --verify-only)." >&2
    return 1
  fi
  for arch in arm64 x86_64; do
    if ! lipo -archs "$HELPER" | tr ' ' '\n' | grep -qx "$arch"; then
      echo "Resident Helper missing $arch slice" >&2
      return 1
    fi
  done
  if [[ ! -f "$DMG" ]]; then
    echo "Missing Solco DMG: $DMG" >&2
    return 1
  fi
  codesign --verify --deep --strict "$APP"
  "$HELPER" status >/dev/null  # read-only; does not install a service
  echo "Complete macOS package: PASS (signed App, universal hostd, DMG)"
}

if [[ "${1:-}" == "--verify-only" ]]; then
  if [[ "$#" != 1 ]]; then
    echo "Usage: scripts/build-complete-macos.sh [--verify-only]" >&2
    exit 2
  fi
  verify
  exit
fi
if (( "$#" > 0 )); then
  echo "Usage: scripts/build-complete-macos.sh [--verify-only]" >&2
  exit 2
fi

# Release CI already does this before MyGo packaging; raw local MyGo builds
# do not, and silently create an App missing its resident phone Host.
"$ROOT/scripts/build-hostd.sh" "$ROOT/resources/darwin/solco-hostd" "$VERSION"
GOTOOLCHAIN=auto go tool mygo build
verify
