#!/usr/bin/env bash
#
# Publish the Remote Desktop helper for release CI: build the sibling solco-remote-helper checkout
# as a universal binary, upload it as the asset of a prerelease `remote-helper-<sha>` on this repo
# (a prerelease is invisible to the updater), and pin that tag in scripts/remote-helper.pin.
# release.yml downloads the pinned asset into resources/darwin/, where `mygo build` ships it as
# Contents/Resources/solco-remote-helper (platform/macos/locate.go finds it there).
#
#   scripts/publish-remote-helper.sh            # build + upload + write the pin
set -euo pipefail

HERE="$(cd "$(dirname "$0")/.." && pwd)"
REPO="${SOLCO_REMOTE_HELPER_REPO:-$HERE/../solco-remote-helper}"
[ -x "$REPO/scripts/build-universal.sh" ] || { echo "ERROR: no helper checkout at $REPO" >&2; exit 1; }
[ -z "$(git -C "$REPO" status --porcelain)" ] || { echo "ERROR: the helper checkout is dirty" >&2; exit 1; }
git -C "$REPO" fetch -q origin
[ "$(git -C "$REPO" rev-parse HEAD)" = "$(git -C "$REPO" rev-parse '@{upstream}')" ] || { echo "ERROR: push the helper checkout first" >&2; exit 1; }

SHA="$(git -C "$REPO" rev-parse --short=7 HEAD)"
TAG="remote-helper-$SHA"
BIN="$("$REPO/scripts/build-universal.sh")"
OUT="$(mktemp -d)/solco-remote-helper"
cp "$BIN" "$OUT"

if ! gh release view "$TAG" >/dev/null 2>&1; then
  gh release create "$TAG" --prerelease \
    --title "Remote Desktop helper $SHA" \
    --notes "Universal solco-remote-helper built from solco-remote-helper@$SHA. Consumed by release.yml; not a Solco release."
fi
gh release upload "$TAG" "$OUT" --clobber
echo "$TAG" > "$HERE/scripts/remote-helper.pin"
echo "pinned: $TAG ($(lipo -archs "$OUT"))"
