#!/usr/bin/env bash
# Generate the G07 source media: high-bitrate 1080p H.264 with two audio tracks
# and one embedded text subtitle track. Real encoding, small enough to stay on
# one disposable disk. `--tiny` is for syntax validation only and is never
# acceptance evidence.
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
out=""
tiny=0
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

seconds=120
size="1920x1080"
rate="30"
vbitrate="12M"
abitrate="256k"
subs=1
if [ "$tiny" = 1 ]; then
  seconds=6; size="320x180"; rate="15"; vbitrate="300k"; abitrate="64k"
fi

# A real text subtitle track, so a burn-in rendition has something to publish.
srt="$out/subtitles.srt"
cat >"$srt" <<'SRT'
1
00:00:00,500 --> 00:00:05,000
Fixture subtitle, first cue.

2
00:00:05,000 --> 00:00:11,000
Second cue with punctuation: commas, stops, and a dash.

3
00:00:11,000 --> 00:00:18,000
Third cue for seek and restart coverage.

4
00:00:18,000 --> 00:00:26,000
Fourth cue, deliberately longer than the others.
SRT

common=(-hide_banner -loglevel error -y
  -f lavfi -i "testsrc2=size=$size:rate=$rate"
  -f lavfi -i "sine=frequency=440:sample_rate=48000"
  -f lavfi -i "sine=frequency=660:sample_rate=48000")

# MP4, H.264 + AAC stereo + AC3 5.1. The compatible direct-play case.
ffmpeg "${common[@]}" -t "$seconds" \
  -map 0:v:0 -map 1:a:0 -map 2:a:0 \
  -c:v libx264 -preset veryfast -b:v "$vbitrate" -maxrate "$vbitrate" -bufsize 2M \
  -pix_fmt yuv420p -g 48 -keyint_min 48 -sc_threshold 0 \
  -c:a:0 aac -b:a:0 "$abitrate" -ac:a:0 2 \
  -c:a:1 ac3 -b:a:1 384k -ac:a:1 6 \
  -movflags +faststart \
  "$out/movie-1080p.mp4"

# Matroska with the same video plus an embedded SubRip track: remux and
# subtitle-publication coverage.
ffmpeg "${common[@]}" -i "$srt" -t "$seconds" \
  -map 0:v:0 -map 1:a:0 -map 3:s:0 \
  -c:v libx264 -preset veryfast -b:v "$vbitrate" -maxrate "$vbitrate" -bufsize 2M \
  -pix_fmt yuv420p -g 48 -keyint_min 48 -sc_threshold 0 \
  -c:a aac -b:a "$abitrate" -ac 2 \
  -c:s srt -metadata:s:s:0 language=eng -metadata:s:s:0 title="Fixture English" \
  "$out/movie-1080p-subs.mkv"

ffprobe -v error -show_entries stream=index,codec_type,codec_name,width,height \
  -of json "$out/movie-1080p.mp4" >"$out/probe-mp4.json"
ffprobe -v error -show_entries stream=index,codec_type,codec_name \
  -of json "$out/movie-1080p-subs.mkv" >"$out/probe-mkv.json"
chmod 600 "$out"/*
printf 'media written to %s\n' "$out"
