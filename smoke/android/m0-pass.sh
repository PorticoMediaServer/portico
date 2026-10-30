#!/usr/bin/env bash
# One authorised M0 Android core pass on the pinned 119 artifact.
#
#   smoke/android/m0-pass.sh --out /path/scratch/phone-119 [--serial emulator-5554] [--drawn-size]
#
# Run ONLY after the named Portico_Phone_API36 owner has confirmed stopped and wiped. The script
# boots the named AVD itself, installs the pinned APK over cleared data, runs the core flows and the
# owner Settings capture, then stops the emulator, wipes its data and proves adb is empty. It never
# creates a second instance and never touches CertTest or any PIN.
#
# The display override (393x852 at density 160) is opt-in with --drawn-size. Default runs at the
# AVD's own metrics, so a run can separate the override from the app when Home stalls.
set -uo pipefail

# Resolve the harness root from this file, not the caller's cwd, before anything else happens.
SELF="$(readlink -f "${BASH_SOURCE[0]}")"
SHELL_DIR="$(cd "$(dirname "$SELF")/../.." && pwd)"
[ -f "$SHELL_DIR/smoke/android/flows.py" ] || {
  echo "FAIL cannot resolve the harness root from $SELF" >&2; exit 1; }

ARTIFACT=/home/justin/projects/Portico/.artifacts/release-r40/native-119112b3/portico-google-debug-x86_64-119112b3.apk
PIN=119112b3
EXPECTED_SHA=c0efe442aadebab6b8be938ca0cb950f06e3a44ea0a0ca603f5427b61d4c31ea
AVD=Portico_Phone_API36
SERIAL=emulator-5554
OUT=""
CREDS=/home/justin/projects/Portico/.private/demo-testing/active-credentials.json
SERVER=https://demo.getportico.tv
PLAYBACK=yes
OVERRIDE=no

while [ $# -gt 0 ]; do
  case "$1" in
    --out) OUT="$2"; shift 2 ;;
    --serial) SERIAL="$2"; shift 2 ;;
    --creds) CREDS="$2"; shift 2 ;;
    --server) SERVER="$2"; shift 2 ;;
    --skip-playback) PLAYBACK=no; shift ;;
    --native-display) OVERRIDE=no; shift ;;
    --drawn-size) OVERRIDE=yes; shift ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done
[ -n "$OUT" ] || { echo "usage: $0 --out DIR [--serial emulator-5554] [--drawn-size] [--skip-playback]" >&2; exit 2; }
mkdir -p "$OUT"
LOG="$OUT/pass.log"
say() { printf '%s %s\n' "$1" "$2" | tee -a "$LOG"; }

# 0. The artifact must be the pinned one.
[ -f "$ARTIFACT" ] || { say FAIL "artifact missing: $ARTIFACT"; exit 1; }
ACTUAL_SHA=$(sha256sum "$ARTIFACT" | cut -d' ' -f1)
[ "$ACTUAL_SHA" = "$EXPECTED_SHA" ] || { say FAIL "artifact hash $ACTUAL_SHA does not match the pin"; exit 1; }
say PASS "artifact $PIN verified ($(stat -c %s "$ARTIFACT") bytes)"

# 1. Never start a second instance, in any state.
if adb devices | awk 'NR>1 && NF' | grep -q .; then
  say FAIL "a device or emulator is already attached; this script only boots the named AVD from stopped"
  adb devices | awk 'NR>1 && NF {print "  attached: " $0}' | tee -a "$LOG"
  exit 75
fi
if pgrep -f "qemu-system.*-avd $AVD" >/dev/null 2>&1 || pgrep -f "emulator.*-avd $AVD" >/dev/null 2>&1; then
  say FAIL "an emulator process for $AVD is already running; not starting a second instance"
  pgrep -af "emulator.*-avd $AVD" | tee -a "$LOG"
  exit 75
fi
if ls "$HOME/.android/avd/$AVD.avd/"*.lock >/dev/null 2>&1; then
  say FAIL "$AVD still holds lock files; the owner has not finished wiping it"
  ls "$HOME/.android/avd/$AVD.avd/"*.lock | tee -a "$LOG"
  exit 75
fi

# Fetch the configured server stamp before starting any device. An unreadable stamp is
# incomplete evidence, so leave Release's device untouched rather than running unpinned.
STAMP=$(curl --fail --silent --show-error --max-time 15 "$SERVER/v1/system" | python3 -c '
import json, sys
try:
    data = json.load(sys.stdin)
    version, server_id = data.get("version"), data.get("id")
    if not isinstance(version, str) or not version.strip() or not isinstance(server_id, str) or not server_id.strip():
        raise ValueError("missing version or id")
    print(json.dumps({"version": version, "id": server_id}, sort_keys=True))
except (ValueError, AttributeError):
    sys.exit(1)
') || { say FAIL "configured server stamp unavailable before UI; no device started"; exit 1; }
say INFO "server before UI: $STAMP"

say INFO "booting $AVD"
nohup "$HOME/Android/Sdk/emulator/emulator" -avd "$AVD" -no-window -no-audio -no-boot-anim \
  -gpu swiftshader_indirect -no-snapshot -memory 2048 -cores 4 > "$OUT/emulator.log" 2>&1 &
adb wait-for-device >/dev/null 2>&1
for _ in $(seq 1 60); do
  [ "$(adb shell getprop sys.boot_completed 2>/dev/null | tr -d '\r')" = "1" ] && break
  sleep 5
done
[ "$(adb shell getprop sys.boot_completed 2>/dev/null | tr -d '\r')" = "1" ] || { say FAIL "the AVD did not finish booting"; exit 1; }
if [ "$OVERRIDE" = "yes" ]; then
  adb shell wm size 393x852 >/dev/null 2>&1
  adb shell wm density 160 >/dev/null 2>&1
fi
DISPLAY_SIZE=$(adb shell wm size | tr -d '\r')
DISPLAY_DENSITY=$(adb shell wm density | tr -d '\r')
say INFO "display actual: $DISPLAY_SIZE / $DISPLAY_DENSITY (override=$OVERRIDE)"


cleanup() {
  adb shell wm size reset >/dev/null 2>&1 || true
  adb shell wm density reset >/dev/null 2>&1 || true
  adb emu kill >/dev/null 2>&1 || true
  sleep 8
  local avd_dir="$HOME/.android/avd/$AVD.avd"
  rm -f "$avd_dir/userdata-qemu.img" "$avd_dir/userdata-qemu.img.qcow2" "$avd_dir/cache.img" \
        "$avd_dir/cache.img.qcow2" "$avd_dir/encryptionkey.img.qcow2" 2>/dev/null || true
  rm -rf "$avd_dir/snapshots" "$avd_dir/tmpAdbCmds" 2>/dev/null || true
  rm -rf "$avd_dir"/*.lock 2>/dev/null || true
  local devices locks
  devices=$(adb devices | awk 'NR>1 && NF' | wc -l)
  locks=$(ls "$avd_dir/"*.lock 2>/dev/null | wc -l)
  say INFO "stopped and wiped; adb devices=$devices; locks=$locks; AVD size=$(du -sh "$avd_dir" 2>/dev/null | cut -f1)"
}
trap cleanup EXIT

# 2. Core flows.
FLOW_ARGS=(--serial "$SERIAL" --apk "$ARTIFACT" --mode phone --out "$OUT/core" --creds "$CREDS" --server "$SERVER" --build "$PIN" --display "$DISPLAY_SIZE $DISPLAY_DENSITY")
[ "$PLAYBACK" = "no" ] && FLOW_ARGS+=(--skip-playback)
python3 "$SHELL_DIR/smoke/android/flows.py" "${FLOW_ARGS[@]}" >> "$LOG" 2>&1
CORE=$?
say "$([ $CORE -eq 0 ] && echo PASS || echo FAIL)" "core flows exit=$CORE"

# 3. Owner Settings evidence: row order, Troubleshooting pair, Storage and Integrations when drawn.
STAMP_AFTER=$({ curl -sS --max-time 15 "$SERVER/v1/system" || true; } | python3 -c "import json,sys;
try:
 d=json.load(sys.stdin); print(d.get('version'), d.get('id'))
except Exception: print('unreadable')" 2>/dev/null)
say INFO "server after core flows: $STAMP_AFTER"
python3 "$SHELL_DIR/smoke/android/settings-capture.py" --serial "$SERIAL" --apk "$ARTIFACT" \
  --out "$OUT/settings" --creds "$CREDS" --server "$SERVER" --build "$PIN" >> "$LOG" 2>&1
SETTINGS=$?
say "$([ $SETTINGS -eq 0 ] && echo PASS || echo FAIL)" "settings capture exit=$SETTINGS"

say INFO "evidence under $OUT (core, settings, pass.log)"
exit $(( CORE != 0 || SETTINGS != 0 ))
