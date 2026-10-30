#!/usr/bin/env bash
# M0 smoke: Apple simulators on the Mac, driven from mini-1 over the release SSH alias.
#
#   smoke/apple/flows.sh --surface ios --app /path/Portico.app --out /path/scratch/apple-ios
#   smoke/apple/flows.sh --surface tvos --app /path/Portico.app --out /path/scratch/apple-tvos \
#       --build 82662ca6 --checkpoint v2-M0-001
#
# `simctl` installs, launches and screenshots but cannot tap, so this script proves install,
# cold launch, a painted screen and a clean log. Interaction depth belongs to the Apple core
# lane (ds-apple-core), who were assigned core flows on these simulators.
set -euo pipefail

SURFACE=ios
APP=""
OUT=""
BUILD="unknown"
CHECKPOINT=""
MAC=portico-mac-build

while [ $# -gt 0 ]; do
  case "$1" in
    --surface) SURFACE="$2"; shift 2 ;;
    --app) APP="$2"; shift 2 ;;
    --out) OUT="$2"; shift 2 ;;
    --build) BUILD="$2"; shift 2 ;;
    --checkpoint) CHECKPOINT="$2"; shift 2 ;;
    --mac) MAC="$2"; shift 2 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done

if [ -z "$APP" ] || [ -z "$OUT" ]; then
  echo "usage: $0 --surface ios|tvos --app /path/App.app --out /path/scratch [--build SHA]" >&2
  exit 2
fi

case "$SURFACE" in
  ios) BUNDLE=tv.getportico.portico.ios; DEVICE_NAME="iPhone 16 Pro"; RUNTIME_OS=ios ;;
  tvos) BUNDLE=tv.getportico.portico.tv; DEVICE_NAME="Apple TV"; RUNTIME_OS=tvos ;;
  *) echo "surface must be ios or tvos" >&2; exit 2 ;;
esac

STATUS=0
say() { printf '%s %s\n' "$1" "$2"; }

# One Xcode job and at most two simulators fleet-wide, and Release owns the Mac lock.
LOCK=/tmp/portico-apple-smoke.lock
exec 9>"$LOCK"
if ! flock -n 9; then
  echo "another Apple job holds $LOCK; not stacking jobs" >&2
  exit 75
fi

run() { ssh -o BatchMode=yes -o ConnectTimeout=15 "$MAC" "$@"; }

TMP=$(run "mktemp -d /tmp/portico-smoke-XXXXXX")
cleanup() {
  run "xcrun simctl shutdown '$DEVICE_UDID' 2>/dev/null || true" >/dev/null 2>&1 || true
  run "rm -rf '$TMP'" >/dev/null 2>&1 || true
}
DEVICE_UDID=""
trap cleanup EXIT

# Find or create one simulator of the right kind.
UDID=$(run "xcrun simctl list devices available --json" | python3 -c "
import json,sys
data=json.load(sys.stdin)['devices']
want='$DEVICE_NAME'
for runtime, devices in data.items():
    for device in devices:
        if device.get('name') == want and device.get('isAvailable'):
            print(device['udid']); raise SystemExit
raise SystemExit(1)
" 2>/dev/null || true)

if [ -z "$UDID" ]; then
  say "BLOCKED" "no available $DEVICE_NAME simulator on the Mac"
  exit 0
fi
DEVICE_UDID="$UDID"
say "INFO" "simulator $DEVICE_NAME ($UDID)"

# Install, cold launch, screenshot.
if ! run "test -d '$APP'"; then
  say "FAIL" "app bundle not found on the Mac at $APP"
  exit 1
fi
run "xcrun simctl boot '$UDID'" >/dev/null 2>&1 || true
run "xcrun simctl bootstatus '$UDID' -b" >/dev/null 2>&1 || true
run "xcrun simctl terminate '$UDID' '$BUNDLE'" >/dev/null 2>&1 || true
run "xcrun simctl uninstall '$UDID' '$BUNDLE'" >/dev/null 2>&1 || true

if run "xcrun simctl install '$UDID' '$APP'"; then
  say "PASS" "install $SURFACE"
else
  say "FAIL" "install $SURFACE"
  STATUS=1
fi

if run "xcrun simctl launch '$UDID' '$BUNDLE'"; then
  say "PASS" "launch $SURFACE"
else
  say "FAIL" "launch $SURFACE"
  STATUS=1
fi

sleep 12
run "xcrun simctl io '$UDID' screenshot '$TMP/launch.png'" >/dev/null 2>&1 || true
mkdir -p "$OUT"
run "cat '$TMP/launch.png'" > "$OUT/launch.png" 2>/dev/null || true

BYTES=$(wc -c < "$OUT/launch.png" 2>/dev/null || echo 0)
if [ "$BYTES" -gt 20000 ]; then
  say "PASS" "launch screen painted ($BYTES byte capture)"
else
  say "FAIL" "launch capture is $BYTES bytes; suspect a blank or black screen"
  STATUS=1
fi

# A crash on launch leaves a log entry; a clean run does not.
CRASH=$(run "xcrun simctl spawn '$UDID' log show --last 2m --predicate 'process == \"Portico\" AND (eventMessage CONTAINS \"crash\" OR eventMessage CONTAINS \"Fatal\")' 2>/dev/null | tail -n 5" || true)
if [ -n "$CRASH" ]; then
  say "FAIL" "crash or fatal log lines: $(printf '%s' "$CRASH" | head -n 2 | tr '\n' ' ')"
  STATUS=1
else
  say "PASS" "no crash or fatal log lines in the last two minutes"
fi

say "BLOCKED" "interaction steps (sign in, browse, play, D-pad) need an XCUITest driver; ds-apple-core owns those flows"
say "INFO" "build $BUILD checkpoint ${CHECKPOINT:-none}; screenshot $OUT/launch.png"
exit "$STATUS"
