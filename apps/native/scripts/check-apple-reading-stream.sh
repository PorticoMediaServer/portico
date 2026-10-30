#!/bin/bash
# Public, unsigned Foundation checks only: no app, simulator, Pods, auth or private assets.
set -euo pipefail
export CI=1
: "${PORTICO_APPLE_TEST_OUT_DIR:?Set an owned output parent (RUNNER_TEMP on Actions)}"
mkdir -p "$PORTICO_APPLE_TEST_OUT_DIR"
work=$(mktemp -d "$PORTICO_APPLE_TEST_OUT_DIR/reading-foundation.XXXXXX")
trap 'rm -rf -- "$work"' EXIT
root=$(cd "$(dirname "$0")/.." && pwd)
export TMPDIR="$work/tmp" CLANG_MODULE_CACHE_PATH="$work/modules"
mkdir -p "$TMPDIR" "$CLANG_MODULE_CACHE_PATH"
target="$(uname -m)-apple-macos12.0"
run() {
  local name=$1
  shift
  if "$@" >"$work/$name.log" 2>&1; then
    printf '%s PASS\n' "$name"
  else
    local status=$?
    printf '%s FAIL exit=%s\n' "$name" "$status"
    tail -n 20 "$work/$name.log"
    return "$status"
  fi
}
sdk=$(xcrun --sdk iphonesimulator --show-sdk-path)
run reading-ios-types xcrun --sdk iphonesimulator swiftc -swift-version 5 \
  -target "$(uname -m)-apple-ios15.1-simulator" -sdk "$sdk" -typecheck \
  -module-cache-path "$CLANG_MODULE_CACHE_PATH" \
  -import-objc-header "$root/Native/PorticoReadingStreamDriver.h" "$root/Native/PorticoReadingByteStream.swift"
run reading-project plutil -lint "$root/ios/Portico.xcodeproj/project.pbxproj"
for name in apple-reading-stream-tests apple-reading-stream-prefetch-tests; do
  run "$name-objc" xcrun clang -fobjc-arc -fblocks -c "$root/test/$name.m" -o "$work/$name.o"
  run "$name-swift" xcrun swiftc -swift-version 5 -target "$target" -parse-as-library \
    -module-cache-path "$CLANG_MODULE_CACHE_PATH" \
    -import-objc-header "$root/Native/PorticoReadingStreamDriver.h" \
    "$root/Native/PorticoReadingByteStream.swift" "$work/$name.o" -o "$work/$name"
  run "$name" "$work/$name"
  tail -n 4 "$work/$name.log"
done
run reading-bytes-build xcrun clang -fobjc-arc -fblocks -framework Foundation -framework AVFoundation \
  "$root/test/apple-reading-bytes-tests.m" -o "$work/reading-bytes"
run reading-bytes "$work/reading-bytes"
