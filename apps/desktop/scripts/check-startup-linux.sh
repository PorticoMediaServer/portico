#!/bin/bash
# Actual app startup/run/close on an isolated Linux display, without user state.
set -eu
export CI=1
cd "$(dirname "$0")/../../.."
job="desktop-r29-startup-$(date +%s)-$$"
image="portico/$job"
cleanup(){ docker rm -f "$job" >/dev/null 2>&1 || true; docker image rm "$image" >/dev/null 2>&1 || true; }
trap cleanup EXIT
python3 - <<'PREFLIGHT'
from pathlib import Path
import shutil
available=int(next(line.split()[1] for line in Path('/proc/meminfo').read_text().splitlines() if line.startswith('MemAvailable:')))*1024
if available<4*1024**3 or shutil.disk_usage('.').free<20*1024**3:raise SystemExit(75)
PREFLIGHT
docker build --quiet --network=host --label portico=desktop-r29 --tag "$image" --file apps/desktop/test/native-linux.Dockerfile .
mkdir -p "$TMPDIR/startup-work/home"
docker_user=(--user "$(id -u):$(id -g)")
case "$(docker info --format '{{json .SecurityOptions}}')" in *name=rootless*) docker_user=(--user 0:0) ;; esac
docker run --rm --name "$job" --network=host "${docker_user[@]}" --label portico=desktop-r29 \
  --cpus=1 --memory=3g --memory-swap=3g --shm-size=256m \
  --env HOME=/work/home --env CARGO_HOME=/work/cargo --env RUSTUP_TOOLCHAIN=1.99.0 \
  --mount "type=bind,src=$PWD,dst=/source" --mount "type=bind,src=$TMPDIR/startup-work,dst=/work" "$image" bash -c '
set -eu
export TMPDIR=/work/tmp CARGO_TARGET_DIR=/work/target CARGO_BUILD_JOBS=1
mkdir -p "$TMPDIR"
cd /source
dpkg-query -W gstreamer1.0-plugins-base gstreamer1.0-plugins-good gstreamer1.0-gl libegl1 libgl1-mesa-dri libglx-mesa0 at-spi2-core
cargo build --quiet --locked --manifest-path apps/desktop/src-tauri/Cargo.toml --features custom-protocol --bin portico-desktop
ulimit -c 0
unset WEBKIT_DISABLE_DMABUF_RENDERER WEBKIT_DISABLE_COMPOSITING_MODE
export LIBGL_ALWAYS_SOFTWARE=1 PORTICO_SPIKE_CLOSE=1
for mode in unavailable native; do
  export PORTICO_SPIKE_DATA="/work/data-$mode" PORTICO_SPIKE_REPORT="/work/startup-$mode.json"
  if [ "$mode" = unavailable ]; then export PORTICO_MPV_LIBRARY=/work/intentionally-missing.so
  else export PORTICO_MPV_LIBRARY=/usr/lib/x86_64-linux-gnu/libmpv.so.2; fi
  if timeout 45 xvfb-run -a -s "-screen 0 1440x900x24" dbus-run-session -- /work/target/debug/portico-desktop > "/work/app-$mode.log" 2>&1; then
    python3 apps/desktop/test/native-startup.py "$mode"
  else
    result=$?;printf "Linux startup failed mode=%s exit=%s\n" "$mode" "$result"
    tail -n 15 "/work/app-$mode.log"
    if [ -f "/work/startup-$mode.native.log" ]; then tail -n 10 "/work/startup-$mode.native.log"; fi
    exit "$result"
  fi
done
# Mandatory native origin/protocol/receipt guards; no full suite or device run.
cargo test --quiet --locked --manifest-path apps/desktop/src-tauri/Cargo.toml --features custom-protocol --lib boundary::tests -- --test-threads=1
cargo test --quiet --locked --manifest-path apps/desktop/src-tauri/Cargo.toml --features custom-protocol --lib download_commands::tests -- --test-threads=1
cargo test --quiet --locked --manifest-path apps/desktop/src-tauri/Cargo.toml --features custom-protocol --lib shutdown::tests -- --test-threads=1
python3 apps/desktop/test/native-fixtures.py
export PORTICO_TEST_MPV_LIBRARY=/usr/lib/x86_64-linux-gnu/libmpv.so.2 PORTICO_TEST_PCM_FIXTURE=/work/owned-48000-stereo.wav
cargo test --quiet --locked --manifest-path apps/desktop/src-tauri/Cargo.toml --features custom-protocol --lib constrained_local_source_plays_pcm_and_never_follows_network_playlist -- --ignored --test-threads=1
cargo test --quiet --locked --manifest-path apps/desktop/src-tauri/Cargo.toml --features custom-protocol --lib native_http_progressive_plays_but_manifest_contents_never_open_another_resource -- --ignored --test-threads=1
'
