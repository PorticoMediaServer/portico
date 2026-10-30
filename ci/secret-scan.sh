#!/usr/bin/env bash
set -euo pipefail
scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT
curl --fail --location --retry 3 https://github.com/gitleaks/gitleaks/releases/download/v8.30.1/gitleaks_8.30.1_linux_x64.tar.gz -o "$scratch/gitleaks.tar.gz"
echo "551f6fc83ea457d62a0d98237cbad105af8d557003051f41f3e7ca7b3f2470eb  $scratch/gitleaks.tar.gz" | sha256sum --check
tar -xzf "$scratch/gitleaks.tar.gz" -C "$scratch" gitleaks
mkdir "$scratch/snapshot"
git archive HEAD | tar -x -C "$scratch/snapshot"
# Snapshot only, comprehensive default rules extended by repository config. No findings printed.
"$scratch/gitleaks" dir "$scratch/snapshot" --config .gitleaks.toml --redact --no-banner --report-path="$scratch/report.json" >"$scratch/log" 2>&1 || { echo 'Snapshot secret scan failed; findings withheld'; exit 1; }
