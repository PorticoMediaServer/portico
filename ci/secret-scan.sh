#!/usr/bin/env bash
set -euo pipefail
scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT
tarball="$scratch/gitleaks.tar.gz"
sha256=551f6fc83ea457d62a0d98237cbad105af8d557003051f41f3e7ca7b3f2470eb
url=${GITLEAKS_TARBALL_URL:-https://github.com/gitleaks/gitleaks/releases/download/v8.30.1/gitleaks_8.30.1_linux_x64.tar.gz}
cache=${GITLEAKS_TARBALL_CACHE:-}
from_cache=false
tarball_ready=false
if [[ -n "$cache" && -f "$cache" ]]; then
  from_cache=true
  cp -- "$cache" "$tarball"
  tarball_ready=true
else
  # Backoff spans about three minutes; a 504 from the release CDN is often short-lived.
  delays=(5 10 20 40 60 60)
  attempt=1
  while :; do
    if curl --fail --location --silent --show-error "$url" -o "$tarball"; then
      tarball_ready=true
      break
    fi
    if (( attempt > ${#delays[@]} )); then
      echo "gitleaks download failed after $attempt attempts" >&2
      break
    fi
    delay=${delays[attempt - 1]}
    echo "gitleaks download attempt $attempt failed; retrying in ${delay}s" >&2
    sleep "$delay"
    attempt=$((attempt + 1))
  done
fi
# Used only after the download window above is exhausted. The fallback module comes
# from the public proxy and is verified against sum.golang.org; a tarball that downloads
# but fails its pinned sha256 stays a hard failure and never falls back.
go_fallback() {
  echo 'gitleaks tarball unavailable after retries; using verified Go module fallback' >&2
  local pinned actual metadata module_version module_sum installed
  if ! command -v go >/dev/null 2>&1; then
    echo 'gitleaks fallback needs Go on PATH; add actions/setup-go with go-version-file: server/go.mod before ci/secret-scan.sh' >&2
    exit 1
  fi
  pinned=go$(awk '$1 == "go" { print $2; exit }' server/go.mod) || {
    echo 'gitleaks fallback cannot read the pinned Go version from server/go.mod' >&2
    exit 1
  }
  actual=$(go env GOVERSION)
  if [[ "$actual" != "$pinned" ]]; then
    echo "gitleaks fallback needs the pinned Go toolchain $pinned from server/go.mod, but go env GOVERSION reported ${actual:-nothing}" >&2
    exit 1
  fi
  (
    unset GOFLAGS GONOSUMDB GONOSUMCHECK GOPRIVATE GONOPROXY GOINSECURE
    export GOSUMDB=sum.golang.org
    export GOPROXY=https://proxy.golang.org
    export GOTOOLCHAIN=local
    export GOWORK=off
    export GOBIN="$scratch/gobin"
    mkdir -p -- "$GOBIN"
    cd -- "$scratch"
    if ! metadata=$(go mod download -json github.com/zricethezav/gitleaks/v8@v8.30.1); then
      echo 'gitleaks module download failed' >&2
      exit 1
    fi
    module_version=$(printf '%s' "$metadata" | python3 -c 'import json, sys; print(json.load(sys.stdin).get("Version", ""))') || {
      echo 'gitleaks module metadata was not valid JSON' >&2
      exit 1
    }
    module_sum=$(printf '%s' "$metadata" | python3 -c 'import json, sys; print(json.load(sys.stdin).get("Sum", ""))') || {
      echo 'gitleaks module metadata was not valid JSON' >&2
      exit 1
    }
    echo "gitleaks module version: ${module_version:-missing}" >&2
    echo "gitleaks module sum: ${module_sum:-missing}" >&2
    if [[ "$module_version" != v8.30.1 || -z "$module_sum" ]]; then
      echo 'gitleaks module is not the expected sumdb-verified v8.30.1 release' >&2
      exit 1
    fi
    go install github.com/zricethezav/gitleaks/v8@v8.30.1
    installed=$("$scratch/gobin/gitleaks" version)
    echo "gitleaks fallback binary version: $installed" >&2
    if ! grep -Eq '(^|[^0-9.])v?8\.30\.1([^0-9.]|$)' <<<"$installed"; then
      # go install cannot stamp gitleaks' ldflags version, so fall back to the module
      # version the toolchain recorded when it built this exact binary.
      built=$(go version -m "$scratch/gobin/gitleaks" | awk '$2 == "github.com/zricethezav/gitleaks/v8" { print $3; exit }')
      echo "gitleaks fallback binary module version: ${built:-missing}" >&2
      if [[ "$built" != v8.30.1 ]]; then
        echo "gitleaks fallback binary is not the pinned v8.30.1 module (version '$installed', module '${built:-nothing}')" >&2
        exit 1
      fi
    fi
  )
}
if [[ "$tarball_ready" == true ]]; then
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
  gitleaks_bin="$scratch/gitleaks"
else
  go_fallback
  gitleaks_bin="$scratch/gobin/gitleaks"
fi
mkdir "$scratch/snapshot"
git archive HEAD | tar -x -C "$scratch/snapshot"
# Snapshot only, comprehensive default rules extended by repository config. No findings printed.
(cd "$scratch/snapshot" && "$gitleaks_bin" dir . --config .gitleaks.toml --redact --no-banner --report-path="$scratch/report.json") >"$scratch/log" 2>&1 || { echo 'Snapshot secret scan failed; findings withheld'; exit 1; }
