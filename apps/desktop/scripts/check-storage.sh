#!/bin/bash
# Headless native check. No GTK, audio device or WebView. Optional real-engine
# fixture tests remain explicit ignored tests, never qualified by this default run.
set -eu
cd "$(dirname "$0")/../../.."
crate="${1:-storage}"
case "$crate" in storage|engine) ;; *) exit 64 ;; esac
filter="${2:-}"
case "$filter" in *[!a-zA-Z0-9_:]*) exit 64 ;; esac
mkdir -p "$TMPDIR/storage-work"
job="desktop-r29-storage-$(date +%s)-$$"
cleanup(){ docker rm -f "$job" >/dev/null 2>&1 || true; }
trap cleanup EXIT
docker_user=(--user "$(id -u):$(id -g)")
case "$(docker info --format '{{json .SecurityOptions}}')" in
  *name=rootless*) docker_user=(--user 0:0) ;;
esac
docker run --rm --name "$job" --network=host "${docker_user[@]}" --label portico=desktop-r29 --cpus=1 --memory=1536m --memory-swap=1536m \
  --env CARGO_HOME=/work/cargo --env CARGO_TARGET_DIR=/work/target --env CARGO_BUILD_JOBS=1 --env TMPDIR=/work/tmp --env "PORTICO_CHECK_CRATE=$crate" --env "PORTICO_CHECK_FILTER=$filter" \
  --mount "type=bind,src=$PWD,dst=/source,readonly" --mount "type=bind,src=$TMPDIR/storage-work,dst=/work" \
  rust:1.99.0-slim-bookworm@sha256:2c3a22f0a5533ea2dd5a16627bc841228151faa2d4de2644ac9987e4a2f1f2fa \
  bash -c 'set -eu; mkdir -p "$TMPDIR"; cd /source; rustc --version; cargo test --locked --manifest-path "apps/desktop/src-tauri/$PORTICO_CHECK_CRATE/Cargo.toml" --lib "$PORTICO_CHECK_FILTER" -- --test-threads=1'
