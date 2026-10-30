#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "usage: $0 BTBN_BUILD_SCRIPT" >&2
  exit 2
fi

recipe="$1"
tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT

awk '
  { print }
  $0 == "    cd ffmpeg" {
    print ""
    print "    # FFmpeg 8.1.2 uses std::system_error in the Windows Graphics Capture"
    print "    # source without including its standard header. Clang/MinGW ARM64 does"
    print "    # not supply that declaration transitively."
    print "    [[ \047$TARGET\047 != \047winarm64\047 ]] || sed -i \047/#include <string>/a #include <system_error>\047 libavfilter/vsrc_gfxcapture_winrt.cpp"
  }
' "$recipe" > "$tmp"

test "$(grep -c 'std::system_error in the Windows Graphics Capture' "$tmp")" -eq 1
chmod +x "$tmp"
mv "$tmp" "$recipe"
trap - EXIT
