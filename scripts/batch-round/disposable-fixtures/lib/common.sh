#!/usr/bin/env bash
# Shared guard and scratch helpers for the disposable G07-G09 fixtures.
# Source this file; do not execute it. Bash 5, coreutils, no network.
#
# Outstanding run location is ~/astra-scratch/P2-65/ only (campaign rule). This
# file creates that tree at 0700 and keeps generated fixture secrets inside it.

set -euo pipefail
umask 077

fixture_refuse() {
  printf 'REFUSED: %s\n' "$*" >&2
  exit 2
}

# A fixture must never run on a shared campaign machine. The disposable host is
# named explicitly by the operator; this guard is not a substitute for care.
fixture_guard() {
  local host ips
  host="$(hostname | tr '[:upper:]' '[:lower:]')"
  ips="$(hostname -I 2>/dev/null || true)"
  case "$host" in
    mini-1*|mini1*|ehler-personal*|ehler-homelab-mini-1*) fixture_refuse "mini-1 is not a fixture host" ;;
    mini-2*|mini2*|ehler-work*|ehler-homelab-mini-2*) fixture_refuse "mini-2 Docker belongs to ECG and is never used by this fixture" ;;
    ecg-*|*-ecg*) fixture_refuse "ECG hosts are off limits" ;;
  esac
  case " $ips " in
    *" 100.120.50.24 "*) fixture_refuse "mini-1 Tailscale address detected" ;;
    *" 100.113.197.98 "*) fixture_refuse "mini-2 Tailscale address detected" ;;
  esac
  if [ "${PORTICO_FIXTURE_DISPOSABLE:-}" != "yes" ]; then
    fixture_refuse "set PORTICO_FIXTURE_DISPOSABLE=yes on a host you may destroy"
  fi
  # Presence is enough: CI=false must not turn a CI guard off.
  if [ "${CI+set}" = "set" ] || [ "${GITHUB_ACTIONS+set}" = "set" ]; then
    fixture_refuse "fixtures never run where CI variables are present"
  fi
  FIXTURE_HOST="$host"
  FIXTURE_ADDRESS="${ips%% *}"
  export FIXTURE_HOST FIXTURE_ADDRESS
}

fixture_repo_root() {
  # Empty when the fixture tree is copied to a disposable host outside Git.
  git -C "$(dirname "${BASH_SOURCE[0]}")/../../../.." rev-parse --show-toplevel 2>/dev/null || true
}

# The campaign scratch root. Everything this fixture creates on the host lives
# below it, except Docker-managed volumes and the private Docker network.
fixture_scratch_root() {
  local repo root
  repo="$(fixture_repo_root)"
  root="${PORTICO_FIXTURE_SCRATCH:-$HOME/astra-scratch/P2-65/disposable}"
  mkdir -p "$root"
  chmod 0700 "$root"
  if [ -n "$repo" ]; then
    case "$(realpath "$root")" in
      "$(realpath "$repo")"/*) fixture_refuse "scratch root must not be inside the repository" ;;
    esac
  fi
  if [ -L "$root" ]; then fixture_refuse "scratch root must not be a symlink"; fi
  FIXTURE_SCRATCH="$(realpath "$root")"
  export FIXTURE_SCRATCH
}

# Generated fixture secrets only (throwaway KDC/SMB passwords, private CA keys).
# Never real credentials.
fixture_secret_dir() {
  local dir
  fixture_scratch_root
  dir="${FIXTURE_SECRETS_DIR:-$FIXTURE_SCRATCH/secrets}"
  mkdir -p "$dir"
  chmod 0700 "$dir"
  case "$(realpath "$dir")" in
    "$FIXTURE_SCRATCH"/*) ;;
    *) fixture_refuse "secret directory must stay under $FIXTURE_SCRATCH" ;;
  esac
  if [ -L "$dir" ]; then fixture_refuse "secret directory must not be a symlink"; fi
  FIXTURE_SECRETS="$dir"
  export FIXTURE_SECRETS
}

fixture_evidence_dir() {
  local dir
  fixture_scratch_root
  dir="${FIXTURE_EVIDENCE:-$FIXTURE_SCRATCH/evidence/$(date -u +%Y%m%dT%H%M%SZ)}"
  mkdir -p "$dir"
  chmod 0700 "$dir"
  FIXTURE_EVIDENCE="$dir"
  export FIXTURE_EVIDENCE
}

fixture_need() {
  local missing=0 tool
  for tool in "$@"; do
    command -v "$tool" >/dev/null 2>&1 || { printf 'MISSING: %s\n' "$tool" >&2; missing=1; }
  done
  [ "$missing" = 0 ] || fixture_refuse "install the missing prerequisites"
}

fixture_secret() {
  # fixture_secret <name> <bytes>: create once at 0600, print the value.
  local name="$1" bytes="${2:-32}" path
  path="$FIXTURE_SECRETS/$name"
  if [ ! -f "$path" ]; then
    umask 077
    openssl rand -hex "$bytes" >"$path"
    chmod 0600 "$path"
  fi
  if [ -L "$path" ] || [ "$(stat -c %a "$path")" != "600" ]; then
    fixture_refuse "secret $name is not a private regular 0600 file"
  fi
  cat "$path"
}

fixture_receipt() {
  # fixture_receipt <path> <json>: write private 0600 JSON, never overwrite.
  local path="$1" body="$2"
  if [ -e "$path" ]; then fixture_refuse "refusing to overwrite receipt $path"; fi
  (umask 077; printf '%s\n' "$body" >"$path")
  chmod 0600 "$path"
}

fixture_now() { date -u +%Y-%m-%dT%H:%M:%SZ; }

fixture_run_id() {
  printf '%s-%s' "$(fixture_now | tr -d ':-')" "$$"
}

# A fixture-private Docker network. No host network, no host port 53/88/445 and
# no host DNS change. Returns the network name.
fixture_network() {
  local name="$1" subnet="$2"
  if ! docker network inspect "$name" >/dev/null 2>&1; then
    docker network create --driver bridge --subnet "$subnet" "$name" >/dev/null
  fi
  printf '%s' "$name"
}

# Playback fixtures use the qualified native runtime and a captured capability
# document; never fabricate a codec profile to force a desired plan.
fixture_playback_inputs() {
  [ -n "${PORTICO_FIXTURE_NODE:-}" ] && [ -n "${PORTICO_FIXTURE_CAPABILITIES:-}" ] \
    || fixture_refuse "set PORTICO_FIXTURE_NODE and PORTICO_FIXTURE_CAPABILITIES"
  FIXTURE_NODE="$(realpath "$PORTICO_FIXTURE_NODE")"
  [ "$("$FIXTURE_NODE" -p process.versions.node)" = 24.21.0 ] || fixture_refuse "Node24.21.0 required"
  [ ! -L "$PORTICO_FIXTURE_CAPABILITIES" ] && [ -f "$PORTICO_FIXTURE_CAPABILITIES" ] \
    && [ "$(stat -c %a "$PORTICO_FIXTURE_CAPABILITIES")" = 600 ] \
    && [ "$(stat -c %u "$PORTICO_FIXTURE_CAPABILITIES")" = "$(id -u)" ] \
    || fixture_refuse "captured capabilities must be owned0600"
  local saved="$FIXTURE_SECRETS/fixture-capabilities.json"
  if [ -e "$saved" ]; then
    cmp -s "$PORTICO_FIXTURE_CAPABILITIES" "$saved" || fixture_refuse "different retained capability document"
  else
    cp "$PORTICO_FIXTURE_CAPABILITIES" "$saved"
    chmod 600 "$saved"
  fi
  export PORTICO_FIXTURE_CAPABILITIES="$saved" PORTICO_FIXTURE_NODE="$FIXTURE_NODE"
  FIXTURE_REPO="$(fixture_repo_root)"
  [ -n "$FIXTURE_REPO" ] || fixture_refuse "playback fixtures need the complete source checkout"
  export FIXTURE_NODE FIXTURE_REPO
}
