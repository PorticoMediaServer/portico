#!/bin/bash
# Linux-hosted Windows target check. No VM, execution, HDR or signing claim.
set -eu
export CI=1
cd "$(dirname "$0")/../../.."
job="desktop-r29-windows-check-$(date +%s)-$$"
image="portico/$job"
cleanup(){ docker rm -f "$job" >/dev/null 2>&1 || true; docker image rm "$image" >/dev/null 2>&1 || true; }
trap cleanup EXIT
# Bound actual disk/RAM before adding the SDK/compiler layers, not just the slot.
python3 - <<'PRECHECK'
from pathlib import Path
import shutil
memory=int(next(line.split()[1] for line in Path('/proc/meminfo').read_text().splitlines() if line.startswith('MemAvailable:')))*1024
if memory<4*1024**3 or shutil.disk_usage('.').free<28*1024**3:raise SystemExit(75)
PRECHECK
# The committed PNG is the source; generate the required Windows resource format
# with the already pinned Tauri CLI, without modifying any source pixels/styles.
node apps/desktop/node_modules/@tauri-apps/cli/tauri.js icon apps/desktop/src-tauri/icons/icon.png --output "$TMPDIR/windows-icons"
cp "$TMPDIR/windows-icons/icon.ico" apps/desktop/src-tauri/icons/icon.ico
docker build --quiet --network=host --label portico=desktop-r29 --tag "$image" --file apps/desktop/test/native-windows-check.Dockerfile .
mkdir -p "$TMPDIR/windows-check/home"
docker_user=(--user "$(id -u):$(id -g)")
case "$(docker info --format '{{json .SecurityOptions}}')" in *name=rootless*) docker_user=(--user 0:0) ;; esac
docker run --rm --name "$job" --network=host "${docker_user[@]}" --label portico=desktop-r29 \
  --cpus=1 --memory=3g --memory-swap=3g --env HOME=/work/home --env CARGO_HOME=/work/cargo \
  --env CARGO_TARGET_DIR=/work/target --env CARGO_BUILD_JOBS=1 --env TMPDIR=/work/tmp \
  --mount "type=bind,src=$PWD,dst=/source" --mount "type=bind,src=$TMPDIR/windows-check,dst=/work" "$image" bash -c '
set -eu
mkdir -p "$TMPDIR"
cd /source
rustc --version
cargo check --quiet --locked --manifest-path apps/desktop/src-tauri/Cargo.toml --features custom-protocol --target x86_64-pc-windows-gnu
printf "%s\n" "Windows GNU target source check only; no Windows execution/output/HDR acceptance"
'
