#!/usr/bin/env bash
set -euo pipefail
out=$1; size=$2; profile=$3; stage=$4
if [ "$profile" = standard ]; then
  exec bash research/p0-s/run.sh "$out" "$size" "$profile" "$stage"
fi
exec sudo systemd-run --unit="p0-s-$stage" --wait --pipe --property=MemoryMax=8G --property=MemorySwapMax=0 --property=CPUQuota=200% --property=CPUQuotaPeriodSec=100ms --property=AllowedCPUs=0-3 --working-directory="$PWD" /usr/bin/env CHECKPOINT_MS="${CHECKPOINT_MS:-50}" TARGET_DDL="${TARGET_DDL:-full-dabf44.sql}" RUNNER_TEMP="$RUNNER_TEMP" MODE="${MODE:-churn}" GITHUB_SHA="$GITHUB_SHA" GITHUB_RUN_ID="$GITHUB_RUN_ID" /bin/bash research/p0-s/run.sh "$out" "$size" "$profile" "$stage"
