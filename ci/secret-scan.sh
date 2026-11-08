#!/usr/bin/env bash
set -euo pipefail
scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT
tarball="$scratch/gitleaks.tar.gz"
sha256=551f6fc83ea457d62a0d98237cbad105af8d557003051f41f3e7ca7b3f2470eb
url=https://github.com/gitleaks/gitleaks/releases/download/v8.30.1/gitleaks_8.30.1_linux_x64.tar.gz
cache=${GITLEAKS_TARBALL_CACHE:-}
from_cache=false
if [[ -n "$cache" && -f "$cache" ]]; then
  from_cache=true
  cp -- "$cache" "$tarball"
else
  # Backoff spans about three minutes; a 504 from the release CDN is often short-lived.
  delays=(5 10 20 40 60 60)
  attempt=1
  while :; do
    if curl --fail --location --silent --show-error "$url" -o "$tarball"; then
      break
    fi
    if (( attempt > ${#delays[@]} )); then
      echo "gitleaks download failed after $attempt attempts" >&2
      exit 1
    fi
    delay=${delays[attempt - 1]}
    echo "gitleaks download attempt $attempt failed; retrying in ${delay}s" >&2
    sleep "$delay"
    attempt=$((attempt + 1))
  done
fi
# Every tarball is verified, cached or freshly downloaded.
if ! printf '%s  %s\n' "$sha256" "$tarball" | sha256sum --check --status; then
  if [[ "$from_cache" == true ]]; then
    rm -f -- "$cache"
    echo 'Cached gitleaks tarball failed sha256 verification; cache removed' >&2
  else
    echo 'Downloaded gitleaks tarball failed sha256 verification' >&2
  fi
  exit 1
fi
if [[ "$from_cache" == false && -n "$cache" ]]; then
  mkdir -p -- "$(dirname -- "$cache")"
  cp -- "$tarball" "$cache"
fi
tar -xzf "$tarball" -C "$scratch" gitleaks
mkdir "$scratch/snapshot"
git archive HEAD | tar -x -C "$scratch/snapshot"
# Snapshot only, comprehensive default rules extended by repository config. No findings printed.
(cd "$scratch/snapshot" && "$scratch/gitleaks" dir . --config .gitleaks.toml --redact --no-banner --report-path="$scratch/report.json") >"$scratch/log" 2>&1 || { echo 'Snapshot secret scan failed; findings withheld'; exit 1; }
