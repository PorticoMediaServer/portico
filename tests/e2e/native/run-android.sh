#!/usr/bin/env bash
set -euo pipefail
: "${PORTICO_E2E_SERVER_URL:?Start from the per-run server harness}"
: "${RUNNER_TEMP:?Hosted runner scratch is required}"
repo_root="$(git rev-parse --show-toplevel)"
artifact_dir="$RUNNER_TEMP/portico-e2e-android"
mkdir -p "$artifact_dir"
port="${PORTICO_E2E_SERVER_URL##*:}"
export SERVER_ADDRESS="http://10.0.2.2:$port"
adb install -r "$repo_root/apps/native/android/app/build/outputs/apk/google/debug/app-google-debug.apk"
record_pid=''
finish_recording() {
  if [[ "$record_pid" =~ ^[0-9]+$ ]]; then
    timeout 15 adb shell "kill -INT $record_pid; while kill -0 $record_pid 2>/dev/null; do sleep 0.2; done" || true
    adb pull /sdcard/portico-e2e.mp4 "$flow_dir/video.mp4" || true
    adb shell rm /sdcard/portico-e2e.mp4 || true
  fi
  record_pid=''
}
trap finish_recording EXIT
run_flow() {
  local name="$1" flow="$2" result=0
  flow_dir="$artifact_dir/$name"
  mkdir -p "$flow_dir"
  record_pid="$(adb shell 'screenrecord /sdcard/portico-e2e.mp4 >/dev/null 2>&1 & echo $!' | tr -d '\r')"
  # Each flow starts from cleared app state, and has its own screenshots, log,
  # result and video. A failure does not hide the other independent journeys.
  (cd "$flow_dir" && maestro test --test-output-dir "$flow_dir/steps" --format junit --output "$flow_dir/junit.xml" \
    -e "SERVER_ADDRESS=$SERVER_ADDRESS" "$flow" 2>&1 | tee "$flow_dir/maestro.log") || result=$?
  finish_recording
  printf '%s: %s\n' "$name" "$result" >> "$artifact_dir/results.txt"
  return "$result"
}
failed=0
run_flow pilot "$repo_root/tests/e2e/native/pilot.yaml" || failed=1
run_flow browse-settings "$repo_root/tests/e2e/native/flows/browse-settings.yaml" || failed=1
run_flow subtitles "$repo_root/tests/e2e/native/flows/subtitles.yaml" || failed=1
if python3 "$repo_root/tests/e2e/native/prepare-profiles.py" 2>&1 | tee "$artifact_dir/profile-fixture.log"; then
  run_flow restricted-profile "$repo_root/tests/e2e/native/flows/restricted-profile.yaml" || failed=1
else
  printf '%s\n' 'restricted-profile: fixture provisioning failed' >> "$artifact_dir/results.txt"
  failed=1
fi
exit "$failed"
