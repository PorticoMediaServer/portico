#!/usr/bin/env bash
set -euo pipefail
: "${PORTICO_E2E_SERVER_URL:?Start from the per-run server harness}"
: "${RUNNER_TEMP:?Hosted runner scratch is required}"
artifact_dir="$RUNNER_TEMP/portico-e2e-android"
mkdir -p "$artifact_dir"
port="${PORTICO_E2E_SERVER_URL##*:}"
export SERVER_ADDRESS="http://10.0.2.2:$port"
export E2E_ARTIFACTS="$artifact_dir"
adb install -r apps/native/android/app/build/outputs/apk/google/debug/app-google-debug.apk
record_pid="$(adb shell 'screenrecord /sdcard/portico-e2e.mp4 >/dev/null 2>&1 & echo $!' | tr -d '\r')"
finish() {
  if [[ "$record_pid" =~ ^[0-9]+$ ]]; then
    timeout 15 adb shell "kill -INT $record_pid; while kill -0 $record_pid 2>/dev/null; do sleep 0.2; done" || true
    adb pull /sdcard/portico-e2e.mp4 "$artifact_dir/video.mp4" || true
    adb shell rm /sdcard/portico-e2e.mp4 || true
  fi
}
trap finish EXIT
maestro test --format junit --output "$artifact_dir/junit.xml" tests/e2e/native/pilot.yaml 2>&1 | tee "$artifact_dir/maestro.log"
