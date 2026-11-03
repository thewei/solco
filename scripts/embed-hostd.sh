#!/usr/bin/env bash
#
# Embed the resident Remote Host into a built Solco.app: build it, copy it to Contents/Helpers/, sign it and
# re-seal the bundle (same shape as embed-remote-helper.sh). Settings → Remote Access then offers
# "Keep Running When Solco Quits", which installs it as a launchd user service.
#
#   scripts/embed-hostd.sh <path/to/Solco.app> [codesign identity]
set -euo pipefail

APP="${1:?usage: embed-hostd.sh <Solco.app> [identity]}"
IDENTITY="${2:--}"
HERE="$(cd "$(dirname "$0")/.." && pwd)"
VERSION="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' "$APP/Contents/Info.plist" 2>/dev/null || echo dev)"

DEST="$APP/Contents/Helpers"
mkdir -p "$DEST"
"$HERE/scripts/build-hostd.sh" "$DEST/solco-hostd" "$VERSION"
codesign --force --sign "$IDENTITY" --identifier com.whstudio.solco.hostd "$DEST/solco-hostd"
codesign --force --sign "$IDENTITY" "$APP"
codesign --verify --deep --strict "$APP"
echo "embedded: $DEST/solco-hostd ($(lipo -archs "$DEST/solco-hostd"))"
