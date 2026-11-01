#!/usr/bin/env bash
set -euo pipefail
: "${PORTICO_E2E_SERVER_URL:?Start from the per-run server harness}"
: "${RUNNER_TEMP:?Hosted runner scratch is required}"
: "${PORTICO_TVOS_APP:?Simulator build is required}"
artifact_dir="$RUNNER_TEMP/portico-e2e-tvos"
mkdir -p "$artifact_dir"
export SERVER_ADDRESS="$PORTICO_E2E_SERVER_URL"
export E2E_ARTIFACTS="$artifact_dir"
xcrun simctl install booted "$PORTICO_TVOS_APP"
xcrun simctl io booted recordVideo --codec h264 "$artifact_dir/video.mp4" >"$artifact_dir/recording.log" 2>&1 &
record_pid=$!
finish() {
  kill -INT "$record_pid" 2>/dev/null || true
  wait "$record_pid" 2>/dev/null || true
}
trap finish EXIT
maestro --platform tvos test --test-output-dir "$artifact_dir/steps" --format junit --output "$artifact_dir/junit.xml" \
  -e "SERVER_ADDRESS=$SERVER_ADDRESS" -e "E2E_ARTIFACTS=$E2E_ARTIFACTS" tests/e2e/native/tvos-pilot.yaml 2>&1 | tee "$artifact_dir/maestro.log"
