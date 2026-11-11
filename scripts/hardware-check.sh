#!/usr/bin/env bash
# Checks that the new server converts on this machine's graphics chip: the hardware trial
# the server runs at start, then three real conversions through the media engine (1080p
# HEVC, a 4K HDR10 film tone-mapped to SDR, and 1080p with a burned picture subtitle), each
# timed against real time. It ends with a short block to paste back: each step says PASS,
# FAIL, or NOT CHECKED (it was skipped or is missing), and only two PASSes exit 0.
#
# Two steps, because the server itself has no Go:
#   1. On a computer with Go and this checkout:
#        scripts/hardware-check.sh bundle            (writes ./portico-hardware-check)
#      Copy that folder to the server.
#   2. On the server, in that folder:
#        ./hardware-check.sh run qsv                  (or vaapi, or nvenc)
#
# It uses the ffmpeg on the PATH. To check another one (Portico's own bundle), set
# PORTICO_FFMPEG and PORTICO_FFPROBE to its two programs; both steps then use that pair. PORTICO_TEST_HARDWARE_DEVICE names
# a render device other than the first. It writes only inside its own folder.
set -euo pipefail

bundle() {
	local root out
	root=$(git rev-parse --show-toplevel)
	out=${1:-$PWD/portico-hardware-check}
	mkdir -p "$out"
	out=$(cd "$out" && pwd)
	mkdir -p "$out/mediaengine" "$out/streaming"
	(cd "$root/server-next" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test -c -o "$out/decoder.test" ./internal/decoder &&
		CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test -c -o "$out/mediaengine/mediaengine.test" ./internal/mediaengine)
	# The conversions read the device profiles beside the test program, as in the checkout.
	cp -R "$root/server-next/internal/streaming/profiles" "$out/streaming/"
	cp "$root/scripts/hardware-check.sh" "$out/"
	echo "Copy $out to the server and run ./hardware-check.sh run qsv in it."
}

# verdict LOG TEST: PASS only when the log shows TEST passed and nothing in it was skipped.
# A Go test program also exits 0 when its test skips or when no test matches, so the exit
# code alone would call an unchecked step a PASS.
verdict() {
	local log=$1 test=$2 code=$3 reason
	if [ "$code" -ne 0 ] || grep -q -- '--- FAIL' "$log"; then
		echo FAIL
	elif grep -q -- "^--- PASS: $test " "$log" && ! grep -q -- '--- SKIP' "$log"; then
		echo PASS
	elif grep -q -- '--- SKIP' "$log"; then
		reason=$(grep -B1 -- '--- SKIP' "$log" | grep -v -- '--- SKIP\|^--$' | sed 's/^ *[^ ]*_test\.go:[0-9]*: //' | sort -u | paste -sd';' - || true)
		echo "NOT CHECKED (skipped: ${reason:-no reason given})"
	else
		echo "NOT CHECKED ($test is not in this bundle)"
	fi
}

run() {
	local backend=${1:?say which chip: qsv, vaapi or nvenc}
	local here log ffmpeg ffprobe device code status_trial status_media trial_ffmpeg
	here=$(cd "$(dirname "$0")" && pwd)
	log="$here/hardware-check.log"
	ffmpeg=${PORTICO_FFMPEG:-$(command -v ffmpeg || true)}
	ffprobe=${PORTICO_FFPROBE:-$(command -v ffprobe || true)}
	device=${PORTICO_TEST_HARDWARE_DEVICE:-/dev/dri/renderD128}
	: >"$log"
	if [ -z "$ffmpeg" ] || [ -z "$ffprobe" ]; then
		echo "No ffmpeg and ffprobe on this machine. Install them, or set PORTICO_FFMPEG and PORTICO_FFPROBE." >&2
		exit 1
	fi
	if [ "$backend" != nvenc ] && ! { : <"$device"; } 2>/dev/null; then
		echo "This user cannot open $device. Add it to its group (sudo usermod -aG $(stat -c %G "$device" 2>/dev/null || echo render) \$USER) and log in again." >&2
		exit 1
	fi
	export PORTICO_TEST_HARDWARE_BACKEND=$backend PORTICO_MEDIA_MATRIX=1
	# The trial finds ffmpeg and ffprobe on the PATH, so the chosen pair goes first on it.
	PATH="$(dirname "$ffmpeg"):$(dirname "$ffprobe"):$PATH"
	# The EXIT trap runs after this function returns, so keep its path in script scope.
	work=$(mktemp -d "$here/work.XXXXXX")
	trap 'rm -rf "$work"' EXIT

	echo "Trying the chip the way the server does at start (about a minute)..."
	code=0
	(cd "$here" && "$here/decoder.test" -test.run '^TestHardwareProbeConfinedWithResolvedLibraries$' -test.count=1 -test.v) >"$work/trial.log" 2>&1 || code=$?
	status_trial=$(verdict "$work/trial.log" TestHardwareProbeConfinedWithResolvedLibraries "$code")
	trial_ffmpeg=$(grep -m1 -o 'trial ffmpeg: .*' "$work/trial.log" | cut -d' ' -f3- || true)
	echo "Converting three films on the chip (a few minutes; the 4K one is made first)..."
	code=0
	(cd "$here/mediaengine" && TMPDIR="$work" ./mediaengine.test -test.run '^TestMediaMatrixHardware$' -test.count=1 -test.v -test.timeout 30m) >"$work/media.log" 2>&1 || code=$?
	status_media=$(verdict "$work/media.log" TestMediaMatrixHardware "$code")
	cat "$work/trial.log" "$work/media.log" >"$log"

	echo
	echo "----- PORTICO HARDWARE CHECK: paste from here -----"
	echo "machine: $(grep -m1 'model name' /proc/cpuinfo | cut -d: -f2- | xargs) | $(uname -sr)"
	echo "ffmpeg: $ffmpeg: $("$ffmpeg" -hide_banner -version | head -1)"
	if [ -n "$trial_ffmpeg" ] && [ "$trial_ffmpeg" != "$ffmpeg" ]; then
		echo "NOTE: the trial used $trial_ffmpeg, not the ffmpeg above"
	fi
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
