#!/usr/bin/env bash
# The load proof's evening, on this machine: what the "wire" job of
# .github/workflows/server-next-load.yml does on a GitHub runner, for hardware a runner is not.
#
# A throwaway Portico server, built from this checkout, with a data folder of its own in a
# temporary folder (removed afterwards), on 127.0.0.1:32599, seeded with 400,000 items; then 200
# people for ten minutes, 80 of them streaming a real film, and CONVERTING of those 80 playing one
# the server must convert in software. Your own Portico server and its data are not touched. The
# run has a network of its own with only loopback, so nothing reaches any outside service.
#
#   scripts/server-load-here.sh [converting] [items] [minutes]     (defaults: 10 400000 10)
#
# Needs Linux, Go, ffmpeg (and bubblewrap for the decoder sandbox). The report is printed and kept
# in ./portico-load-report.txt.
set -euo pipefail

converting=${1:-10}
items=${2:-400000}
minutes=${3:-10}
repo=$(cd "$(dirname "$0")/.." && pwd)
report=$PWD/portico-load-report.txt

for tool in go ffmpeg unshare curl; do
  command -v "$tool" >/dev/null || { echo "This needs $tool." >&2; exit 1; }
done
if ! unshare -rn true 2>/dev/null; then
  echo "This machine does not let a user make a network of their own, which keeps the run off the internet." >&2
  echo "On Ubuntu: sudo sysctl -w kernel.apparmor_restrict_unprivileged_userns=0 (until the next restart)." >&2
  exit 1
fi

work=$(mktemp -d "${TMPDIR:-/tmp}/portico-load.XXXXXX")
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/bin" "$work/data" "$work/media"

echo "Building the server, the seeder and the load driver..."
(cd "$repo/server-next" && CGO_ENABLED=0 go build -o "$work/bin/" ./cmd/portico-seed ./cmd/portico-load ./cmd/portico-server)

echo "Seeding $items items and making two films..."
"$work/bin/portico-seed" -db "$work/data/main.db" -items "$items" -phase import 2>&1 | tail -2
ffmpeg -nostdin -hide_banner -loglevel error -f lavfi -i testsrc2=size=1920x1080:rate=24 -f lavfi -i sine=frequency=440:sample_rate=48000 \
  -t 60 -c:v libx264 -preset veryfast -b:v 8M -pix_fmt yuv420p -c:a aac -ac 2 "$work/media/film.mkv"
# One minute of 10-bit HEVC, repeated to eleven by copying, so no converting player reaches its end.
ffmpeg -nostdin -hide_banner -loglevel error -f lavfi -i testsrc2=size=1920x1080:rate=24 -f lavfi -i sine=frequency=330:sample_rate=48000 \
  -t 60 -c:v libx265 -preset ultrafast -x265-params log-level=error -pix_fmt yuv420p10le -b:v 6M -c:a aac -ac 2 "$work/media/minute.mkv"
ffmpeg -nostdin -hide_banner -loglevel error -stream_loop 10 -i "$work/media/minute.mkv" -c copy "$work/media/hevc.mkv"

echo "200 people for $minutes minutes, 80 streaming, $converting converted in software..."
cd "$repo/server-next" # the driver reads the device profiles beside it
TMPDIR=$work WORK=$work CONVERTING=$converting MINUTES=$minutes unshare -rn bash -c '
  set -eu
  ip link set lo up 2>/dev/null || true
  "$WORK/bin/portico-server" -data "$WORK/data" -listen 127.0.0.1:32599 > "$WORK/server.log" 2>&1 &
  server=$!
  for _ in $(seq 120); do [ -f "$WORK/data/setup-code.txt" ] && curl -s -o /dev/null http://127.0.0.1:32599/ && break; sleep 1; done
  "$WORK/bin/portico-load" -mode http -url http://127.0.0.1:32599 -setup-code "$WORK/data/setup-code.txt" -media "$WORK/media/film.mkv" \
    -convert-media "$WORK/media/hevc.mkv" -converting "$CONVERTING" -pid "$server" -people 200 -streams 80 \
    -crowd-time "${MINUTES}m" -out "$WORK/wire.json" > "$WORK/wire.txt" 2>&1 || true
  kill "$server"; wait "$server" || true'

{
  echo "Portico load proof on this machine: $(nproc) threads, $(grep -m1 'model name' /proc/cpuinfo | cut -d: -f2 | xargs)," \
    "$(awk '/MemTotal/ {printf "%.0f", $2 / 1048576}' /proc/meminfo) GB; $items items, 200 people, 80 streams, $converting converted in software"
  echo
  cat "$work/wire.txt"
} | tee "$report"
echo
echo "Kept in $report"
