#!/usr/bin/env bash
# Checks that the new server converts on this machine's graphics chip: the hardware trial
# the server runs at start, then three real conversions through the media engine (1080p
# HEVC, a 4K HDR10 film tone-mapped to SDR, and 1080p with a burned picture subtitle), each
# timed against real time. It ends with a short PASS or FAIL block to paste back.
#
# Two steps, because the server itself has no Go:
#   1. On a computer with Go and this checkout:
#        scripts/hardware-check.sh bundle            (writes ./portico-hardware-check)
#      Copy that folder to the server.
#   2. On the server, in that folder:
#        ./hardware-check.sh run qsv                  (or vaapi, or nvenc)
#
# It uses the ffmpeg on the PATH. To check another one (Portico's own bundle), set
# PORTICO_FFMPEG and PORTICO_FFPROBE to its two programs. PORTICO_TEST_HARDWARE_DEVICE names
# a render device other than the first. It writes only inside its own folder.
set -euo pipefail

bundle() {
	local root out
	root=$(git rev-parse --show-toplevel)
	out=${1:-$PWD/portico-hardware-check}
	mkdir -p "$out/mediaengine" "$out/streaming/testdata"
	(cd "$root/server-next" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test -c -o "$out/decoder.test" ./internal/decoder &&
		CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test -c -o "$out/mediaengine/mediaengine.test" ./internal/mediaengine)
	cp "$root/server-next/internal/streaming/testdata/client-profiles.json" "$out/streaming/testdata/"
	cp "$root/scripts/hardware-check.sh" "$out/"
	echo "Copy $out to the server and run ./hardware-check.sh run qsv in it."
}

run() {
	local backend=${1:?say which chip: qsv, vaapi or nvenc}
	local here log ffmpeg device status_trial status_media
	here=$(cd "$(dirname "$0")" && pwd)
	log="$here/hardware-check.log"
	ffmpeg=${PORTICO_FFMPEG:-$(command -v ffmpeg || true)}
	device=${PORTICO_TEST_HARDWARE_DEVICE:-/dev/dri/renderD128}
	: >"$log"
	if [ -z "$ffmpeg" ]; then
		echo "No ffmpeg on this machine. Install it, or set PORTICO_FFMPEG and PORTICO_FFPROBE." >&2
		exit 1
	fi
	if [ "$backend" != nvenc ] && ! { : <"$device"; } 2>/dev/null; then
		echo "This user cannot open $device. Add it to the render group (sudo usermod -aG render \$USER) and log in again." >&2
		exit 1
	fi
	export PORTICO_TEST_HARDWARE_BACKEND=$backend PORTICO_MEDIA_MATRIX=1
	local work
	work=$(mktemp -d "$here/work.XXXXXX")
	trap 'rm -rf "$work"' EXIT

	echo "Trying the chip the way the server does at start (about a minute)..."
	if (cd "$here" && "$here/decoder.test" -test.run '^TestHardwareProbeConfinedWithResolvedLibraries$' -test.count=1 -test.v) >>"$log" 2>&1; then
		status_trial=PASS
	else
		status_trial=FAIL
	fi
	echo "Converting three films on the chip (a few minutes; the 4K one is made first)..."
	if (cd "$here/mediaengine" && TMPDIR="$work" ./mediaengine.test -test.run '^TestMediaMatrixHardware$' -test.count=1 -test.v -test.timeout 30m) >>"$log" 2>&1; then
		status_media=PASS
	else
		status_media=FAIL
	fi

	echo
	echo "----- PORTICO HARDWARE CHECK: paste from here -----"
	echo "machine: $(grep -m1 'model name' /proc/cpuinfo | cut -d: -f2- | xargs) | $(uname -sr)"
	echo "ffmpeg: $("$ffmpeg" -hide_banner -version | head -1)"
	echo "chip asked for: $backend"
	echo "hardware trial: $status_trial"
	grep -E 'hardware trial: |available=' "$log" | sed 's/^ *//' | sort -u | head -12 || true
	echo "conversions: $status_media"
	grep -E 'SPEED |FINDING|planned on ' "$log" | sed 's/^ *//' || true
	grep -E '^\s+(hevc-|.* web )' "$log" | sed 's/^ *//' | sort -u || true
	grep -E -- '--- (FAIL|SKIP)' "$log" | sed 's/^ *//' || true
	echo "----- to here (the full log is $log) -----"
	[ "$status_trial" = PASS ] && [ "$status_media" = PASS ]
}

case "${1:-}" in
bundle) shift && bundle "$@" ;;
run) shift && run "$@" ;;
*)
	echo "usage: $0 bundle [folder]   (on a computer with Go)" >&2
	echo "       $0 run qsv|vaapi|nvenc (on the server, in the bundle folder)" >&2
	exit 2
	;;
esac
