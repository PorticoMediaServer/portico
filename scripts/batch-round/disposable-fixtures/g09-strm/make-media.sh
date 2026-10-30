#!/usr/bin/env bash
# Generate the G09 origin media. Real encodes; `--tiny` only validates syntax.
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
out=""; tiny=0
while [ $# -gt 0 ]; do
  case "$1" in
    --out) out="$2"; shift 2 ;;
    --tiny) tiny=1; shift ;;
    *) echo "usage: $0 --out DIR [--tiny]" >&2; exit 2 ;;
  esac
done
[ -n "$out" ] || { echo "usage: $0 --out DIR [--tiny]" >&2; exit 2; }
command -v ffmpeg >/dev/null || { echo "ffmpeg is required" >&2; exit 2; }
[ ! -e "$out" ] || { echo "refusing to reuse $out" >&2; exit 2; }
mkdir -m 700 -p "$out"

seconds=30; rate=30; vbitrate=4M
if [ "$tiny" = 1 ]; then seconds=3; rate=15; vbitrate=500k; fi
src=(-hide_banner -loglevel error -y -f lavfi -i "testsrc2=size=1280x720:rate=$rate" -f lavfi -i "sine=frequency=440:sample_rate=48000")

ffmpeg "${src[@]}" -t "$seconds" -map 0:v:0 -map 1:a:0 \
  -c:v libx264 -preset veryfast -b:v "$vbitrate" -pix_fmt yuv420p -g 48 \
  -c:a aac -b:a 128k -movflags +faststart "$out/direct.mp4"

ffmpeg "${src[@]}" -t "$seconds" -map 0:v:0 -map 1:a:0 \
  -c:v libx264 -preset veryfast -b:v "$vbitrate" -pix_fmt yuv420p -g 48 \
  -c:a aac -b:a 128k "$out/remux.mkv"

ffmpeg "${src[@]}" -t "$seconds" -map 0:v:0 -map 1:a:0 \
  -c:v libx265 -preset ultrafast -b:v "$vbitrate" -pix_fmt yuv420p10le \
  -c:a aac -b:a 128k "$out/hevc10.mkv"

cp "$out/direct.mp4" "$out/replace.mp4"
cp "$out/direct.mp4" "$out/offline.mp4"
chmod 600 "$out"/*
printf 'G09 media written to %s\n' "$out"
