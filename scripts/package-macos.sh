#!/usr/bin/env bash
# Turn the macOS payload (scripts/package-release.sh darwin arm64 …) into
# Portico Media Server.app inside a DMG. Runs on macOS.
#   scripts/package-macos.sh VERSION PAYLOAD_DIR OUT_DIR BUILD_NUMBER
set -euo pipefail
export COPYFILE_DISABLE=1
if [[ $# -ne 4 ]]; then echo "usage: $0 VERSION PAYLOAD_DIR OUT_DIR BUILD_NUMBER" >&2; exit 2; fi
VERSION="$1"; PAYLOAD="$2"; OUT="$3"; BUILD_NUMBER="$4"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DMG_ROOT="$OUT/dmg-root"
APP="$DMG_ROOT/Portico Media Server.app"
rm -rf "${DMG_ROOT:?}"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources/server" "$APP/Contents/Resources/launchd"
cp -R "$PAYLOAD/." "$APP/Contents/Resources/server/"
cp "$ROOT/packaging/macos/tv.getportico.server.service.plist" "$ROOT/packaging/macos/uninstall.sh" "$APP/Contents/Resources/launchd/"
sed -e "s/__PORTICO_VERSION__/$VERSION/g" -e "s/__PORTICO_BUILD__/$BUILD_NUMBER/g" "$ROOT/packaging/macos/Info.plist" > "$APP/Contents/Info.plist"
cp "$ROOT/packaging/macos/launcher.sh" "$APP/Contents/MacOS/Portico Media Server"
chmod +x "$APP/Contents/MacOS/Portico Media Server" "$APP/Contents/Resources/server/portico-server" "$APP/Contents/Resources/server/third_party/ffmpeg/bin/"* "$APP/Contents/Resources/server/third_party/rclone/rclone"
cp "$ROOT/packaging/macos/PorticoServer.icns" "$APP/Contents/Resources/PorticoServer.icns"
if [[ -n "${PORTICO_MACOS_SIGNING_IDENTITY:-}" ]]; then
  : "${PORTICO_RELEASE_SIGNING_KEY_FILE:?A private release key is required to seal codesigned binaries}"
  PUBLIC_KEY_FILE="${PORTICO_RELEASE_PUBLIC_KEY_FILE:?A pinned public release key is required}"
  python3 "$ROOT/scripts/seal-macos-payload.py" "$APP/Contents/Resources/server" --key "$PORTICO_RELEASE_SIGNING_KEY_FILE" --identity "$PORTICO_MACOS_SIGNING_IDENTITY" --trusted-public-key "$PUBLIC_KEY_FILE"
  codesign --force --options runtime --timestamp=none --sign "$PORTICO_MACOS_SIGNING_IDENTITY" "$APP"
  codesign --verify --deep --strict "$APP"
  python3 "$ROOT/scripts/seal-macos-payload.py" "$APP/Contents/Resources/server" --public-key "$PUBLIC_KEY_FILE" --verify
fi
if find "$DMG_ROOT" -type f \( -name '._*' -o -name '.DS_Store' \) | grep -q .; then echo "macOS package contains Finder metadata" >&2; exit 1; fi

PKG_ROOT="$OUT/pkg-root"
rm -rf "${PKG_ROOT:?}"
mkdir -p "$PKG_ROOT/Applications"
cp -R "$APP" "$PKG_ROOT/Applications/"
chmod +x "$ROOT/packaging/macos/package-scripts/"* "$PKG_ROOT/Applications/Portico Media Server.app/Contents/Resources/launchd/uninstall.sh"
sign=()
if [[ -n "${PORTICO_MACOS_INSTALLER_IDENTITY:-}" ]]; then sign=(--sign "$PORTICO_MACOS_INSTALLER_IDENTITY"); fi
pkgbuild --component-plist "$ROOT/packaging/macos/components.plist" --root "$PKG_ROOT" --identifier tv.getportico.server --version "$VERSION" --install-location / --scripts "$ROOT/packaging/macos/package-scripts" "${sign[@]}" "$OUT/Portico-Media-Server-macOS-arm64.pkg"

rm -rf "${DMG_ROOT:?}"
mkdir -p "$DMG_ROOT"
cp "$OUT/Portico-Media-Server-macOS-arm64.pkg" "$DMG_ROOT/"
hdiutil create -quiet -volname "Portico Media Server" -srcfolder "$DMG_ROOT" -ov -format UDZO "$OUT/Portico-Media-Server-macOS-arm64.dmg"
