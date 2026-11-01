#!/usr/bin/env bash
set -euo pipefail
: "${PORTICO_E2E_SERVER_URL:?Start from the per-run server harness}"
: "${RUNNER_TEMP:?Hosted runner scratch is required}"
artifact_dir="$RUNNER_TEMP/portico-e2e-android-tv"
mkdir -p "$artifact_dir"
port="${PORTICO_E2E_SERVER_URL##*:}"
export SERVER_ADDRESS="http://10.0.2.2:$port"
adb install -r apps/native/android/app/build/outputs/apk/google/debug/app-google-debug.apk
adb shell screenrecord /sdcard/portico-e2e-tv.mp4 &
record_pid=$!
finish() {
  adb shell pkill -INT screenrecord || true
  wait "$record_pid" || true
  adb pull /sdcard/portico-e2e-tv.mp4 "$artifact_dir/video.mp4" || true
  adb shell rm /sdcard/portico-e2e-tv.mp4 || true
}
trap finish EXIT
maestro test --test-output-dir "$artifact_dir/steps" --format junit --output "$artifact_dir/junit.xml" \
  -e "SERVER_ADDRESS=$SERVER_ADDRESS" tests/e2e/native/android-tv-pilot.yaml 2>&1 | tee "$artifact_dir/maestro.log"
