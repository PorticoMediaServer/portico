#!/usr/bin/env bash
# Package one sealed server build (scripts/build-server-release.py) with the web
# app into the release assets for its platform.
#   scripts/package-release.sh OS ARCH VERSION STAGE_DIR WEB_DIR OUT_DIR BUILD_NUMBER
# OS is linux, windows or darwin; ARCH is amd64 or arm64. Linux makes a .tar.gz,
# a .deb and an .rpm (nfpm); Windows a portable .zip and an NSIS Setup.exe
# (makensis); macOS the payload directory the DMG step turns into an app.
set -euo pipefail
if [[ $# -ne 7 ]]; then echo "usage: $0 OS ARCH VERSION STAGE_DIR WEB_DIR OUT_DIR BUILD_NUMBER" >&2; exit 2; fi
OS="$1"; ARCH="$2"; VERSION="$3"; STAGE="$4"; WEB="$5"; OUT="$6"; BUILD_NUMBER="$7"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
FILE_ARCH=$([[ "$ARCH" == amd64 ]] && echo x64 || echo arm64)
EXE=$([[ "$OS" == windows ]] && echo portico-server.exe || echo portico-server)
PAYLOAD="$OUT/payload-$OS-$FILE_ARCH"
mkdir -p "$OUT"
rm -rf "${PAYLOAD:?}"
mkdir -p "$PAYLOAD/web"
for required in "$STAGE/$EXE" "$STAGE/manifest.json" "$STAGE/manifest.sig" "$STAGE/third_party/ffmpeg/toolchain-manifest.json" "$STAGE/third_party/rclone/bundle-manifest.json" "$WEB/index.html"; do
  [[ -s "$required" ]] || { echo "missing release input: $required" >&2; exit 1; }
done
python3 "$ROOT/scripts/rclone-bundle.py" verify-release "$STAGE" --target "$OS-$ARCH" --public-key "${PORTICO_RELEASE_PUBLIC_KEY_FILE:?A pinned release public key is required}"
cp "$STAGE/$EXE" "$STAGE/manifest.json" "$STAGE/manifest.sig" "$PAYLOAD/"
cp -R "$STAGE/third_party" "$PAYLOAD/third_party"
cp -R "$WEB/." "$PAYLOAD/web/"
cp "$ROOT/LICENSE" "$ROOT/THIRD-PARTY-NOTICES.md" "$PAYLOAD/"
mkdir -p "$PAYLOAD/licenses"
cp "$STAGE/third_party/rclone/LICENSE" "$PAYLOAD/licenses/rclone.txt"
cp "$STAGE/third_party/rclone/DEPENDENCY-NOTICES.txt" "$PAYLOAD/licenses/rclone-dependencies.txt"
cp "$ROOT/server/internal/imagework/SCALE_LICENSE" "$PAYLOAD/licenses/image-scaling.txt"
cp "$ROOT/server/internal/thirdparty/sqlite/LICENSE" "$PAYLOAD/licenses/sqlite-driver.txt"
cp "$ROOT/server/internal/mediaexec/fonts/LICENSE" "$PAYLOAD/licenses/decoder-fonts.txt"
cp "$ROOT/third_party/hls.js/LICENSE" "$PAYLOAD/licenses/hls-js.txt"
mkdir -p "$PAYLOAD/licenses/reader"
cp "$ROOT/third_party/reader/"*-LICENSE "$ROOT/third_party/reader/provenance.json" "$PAYLOAD/licenses/reader/"
cp "$ROOT/packages/reader-core/assets/OFL.txt" "$PAYLOAD/licenses/reader/literata-OFL.txt"
cp "$ROOT/web/src/kit/fonts/OFL.txt" "$PAYLOAD/licenses/manrope.txt"
cp "$ROOT/packages/design/LICENSE-LUCIDE" "$PAYLOAD/licenses/lucide.txt"
find "$PAYLOAD" \( -name '.DS_Store' -o -name '._*' \) -delete
printf '{"version":"%s","build":"%s"}\n' "$VERSION" "$BUILD_NUMBER" > "$PAYLOAD/web/portico-build.json"

case "$OS" in
  linux)
    cp "$ROOT/packaging/portable/README.txt" "$ROOT/packaging/portable/start-portico.sh" "$PAYLOAD/"
    chmod +x "$PAYLOAD/start-portico.sh"
    tar --sort=name --mtime='UTC 2020-01-01' --owner=0 --group=0 --numeric-owner -C "$PAYLOAD" -czf "$OUT/portico-media-server-linux-$FILE_ARCH.tar.gz" .
    config="$OUT/nfpm-$FILE_ARCH.yaml"
    sed -e "s|\${PORTICO_PACKAGE_VERSION}|$VERSION|g" -e "s|\${PORTICO_PACKAGE_ARCH}|$ARCH|g" -e "s|\${PORTICO_PACKAGE_ROOT}|$PAYLOAD|g" "$ROOT/packaging/linux/nfpm.yaml" > "$config"
    if grep -q '\${PORTICO_PACKAGE_' "$config"; then echo "unresolved package variable in $config" >&2; exit 1; fi
    (cd "$ROOT" && nfpm package --config "$config" --packager deb --target "$OUT/portico-media-server-linux-$FILE_ARCH.deb")
    (cd "$ROOT" && nfpm package --config "$config" --packager rpm --target "$OUT/portico-media-server-linux-$FILE_ARCH.rpm")
    ;;
  windows)
    # The archive is written from inside the payload folder, and makensis works
    # from the installer script's folder, so neither may see a relative path.
    OUT="$(cd "$OUT" && pwd)"
    PAYLOAD="$OUT/payload-$OS-$FILE_ARCH"
    PORTABLE_PAYLOAD="$OUT/portable-windows-$FILE_ARCH"
    rm -rf "${PORTABLE_PAYLOAD:?}"
    cp -R "$PAYLOAD" "$PORTABLE_PAYLOAD"
    cp "$ROOT/packaging/portable/README.txt" "$ROOT/packaging/portable/start-portico.cmd" "$PORTABLE_PAYLOAD/"
    (cd "$PORTABLE_PAYLOAD" && zip -qr "$OUT/Portico-Media-Server-Windows-$FILE_ARCH-Portable.zip" .)
    makensis -V2 "-DOUTPUT_FILE=$OUT/Portico-Media-Server-Windows-$FILE_ARCH-Setup.exe" "-DSTAGE_DIR=$PAYLOAD" "-DPRODUCT_VERSION=$VERSION" "$ROOT/packaging/windows/installer.nsi"
    ;;
  darwin)
    echo "$PAYLOAD"
    ;;
  *) echo "unknown OS: $OS" >&2; exit 2 ;;
esac
