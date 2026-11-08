#!/usr/bin/env bash
#
# Embed the Remote Desktop helper into a built Solco.app: build it (universal), copy it to
# Contents/Helpers/, sign it, and re-seal the bundle. Run it after `go tool mygo build` and before
# the bundle is installed or archived.
#
#   scripts/embed-remote-helper.sh <path/to/Solco.app> [codesign identity]
#
# The helper lives in its own repository (SOLCO_REMOTE_HELPER_REPO, default: ../solco-remote-helper
# next to this checkout, or next to the main checkout when run from a worktree). A missing helper
# repo is a warning, not a failure: the app then simply offers no Remote Desktop ("unavailable").
# SOLCO_REMOTE_HELPER_BIN points at a prebuilt binary instead (CI).
set -euo pipefail

APP="${1:?usage: embed-remote-helper.sh <Solco.app> [identity]}"
IDENTITY="${2:--}"
HERE="$(cd "$(dirname "$0")/.." && pwd)"

BIN="${SOLCO_REMOTE_HELPER_BIN:-}"
if [ -z "$BIN" ]; then
  REPO="${SOLCO_REMOTE_HELPER_REPO:-}"
  if [ -z "$REPO" ]; then
    COMMON="$(git -C "$HERE" rev-parse --git-common-dir 2>/dev/null || true)"
    for cand in "$HERE/../solco-remote-helper" "$(dirname "$COMMON")/../solco-remote-helper"; do
      [ -d "$cand" ] && REPO="$cand" && break
    done
  fi
  if [ -z "$REPO" ] || [ ! -x "$REPO/scripts/build-universal.sh" ]; then
    echo "warning: solco-remote-helper not found — Remote Desktop will be unavailable in this build" >&2
    exit 0
  fi
  BIN="$("$REPO/scripts/build-universal.sh")"
fi
[ -x "$BIN" ] || { echo "ERROR: helper binary $BIN is not executable" >&2; exit 1; }

DEST="$APP/Contents/Helpers"
mkdir -p "$DEST"
cp "$BIN" "$DEST/solco-remote-helper"
codesign --force --sign "$IDENTITY" --identifier com.whstudio.solco.remote-helper "$DEST/solco-remote-helper"
# Seal the bundle again around the new nested code. No --deep: the helper already carries its own signature.
codesign --force --sign "$IDENTITY" "$APP"
codesign --verify --deep --strict "$APP"
echo "embedded: $DEST/solco-remote-helper ($(lipo -archs "$DEST/solco-remote-helper"))"
