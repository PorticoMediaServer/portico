#!/bin/bash
# One owned foreground Docker job. All source/artifacts are per-job; no host installs.
set -eu
export CI=1
cd "$(dirname "$0")/../../.."
job="desktop-r29-$(date +%s)-$$"
image="portico/$job"
cleanup(){ docker rm -f "$job" >/dev/null 2>&1 || true; docker image rm "$image" >/dev/null 2>&1 || true; }
trap cleanup EXIT
# Root's remote wrapper holds the machine slot for this entire sequential check.
docker build --quiet --network=host --label portico=desktop-r29 --tag "$image" --file apps/desktop/test/native-linux.Dockerfile .
docker image inspect "$image" --format '{{.Id}} {{json .RepoDigests}} {{json .Config.Labels}}'
# Tauri generates permission schemas beside its manifest. Only this exact per-job
# archive is writable, with the runner uid so its final cleanup can remove every file.
mkdir -p "$TMPDIR/native-work/home"
scope="${1:-engine}"
case "$scope" in shell|engine|retirement|observed-audio) ;; *) exit 64 ;; esac
docker_user=(--user "$(id -u):$(id -g)")
case "$(docker info --format '{{json .SecurityOptions}}')" in *name=rootless*) docker_user=(--user 0:0) ;; esac
docker run --rm --name "$job" --network=host "${docker_user[@]}" --label portico=desktop-r29 --cpus=1 --memory=3g --memory-swap=3g \
  --env HOME=/work/home --env CARGO_HOME=/work/cargo --env RUSTUP_TOOLCHAIN=1.99.0 \
  --env "PORTICO_NATIVE_CHECK=$scope" \
  --mount "type=bind,src=$PWD,dst=/source" --mount "type=bind,src=$TMPDIR/native-work,dst=/work" "$image" bash -c '
set -eu
export TMPDIR=/work/tmp CARGO_TARGET_DIR=/work/cargo-target CARGO_BUILD_JOBS=1
mkdir -p "$TMPDIR"
rustc --version
cargo --version
pkg-config --modversion gtk+-3.0 webkit2gtk-4.1 mpv
ffmpeg -hide_banner -version
dpkg-query -W libmpv2 libmpv-dev libwebkit2gtk-4.1-dev
export PORTICO_TEST_MPV_LIBRARY=/usr/lib/x86_64-linux-gnu/libmpv.so.2
export PORTICO_TEST_PCM_FIXTURE=/work/owned-48000-stereo.wav PORTICO_TEST_VIDEO_FIXTURE=/work/owned-vfr-6.mkv PORTICO_TEST_WAVPACK_FIXTURE=/work/owned-48000-stereo.wv PORTICO_TEST_SUBTITLE_FIXTURE=/work/owned-default-subtitle.mkv
if [ "$PORTICO_NATIVE_CHECK" = retirement ]; then
  # Actual GTK object ownership, without a WebView, media engine or screen dump.
  xvfb-run -a -s "-screen 0 800x600x24" cargo test --quiet --locked --manifest-path apps/desktop/src-tauri/Cargo.toml --features shell --lib shell::surface::tests -- --test-threads=1
elif [ "$PORTICO_NATIVE_CHECK" = observed-audio ]; then
  python3 apps/desktop/test/native-fixtures.py
  cargo test --quiet --locked --manifest-path apps/desktop/src-tauri/storage/Cargo.toml --lib renderer_output_tests
  cargo test --quiet --locked --manifest-path apps/desktop/src-tauri/storage/Cargo.toml --lib boundary::tests
  cargo test --quiet --locked --manifest-path apps/desktop/src-tauri/storage/Cargo.toml --lib download_receipt::tests
  cargo test --quiet --locked --manifest-path apps/desktop/src-tauri/engine/Cargo.toml --lib mpv::local_source_tests -- --ignored --test-threads=1
elif [ "$PORTICO_NATIVE_CHECK" = engine ]; then
  python3 apps/desktop/test/native-fixtures.py
  cargo test --quiet --locked --manifest-path apps/desktop/src-tauri/storage/Cargo.toml --lib boundary::tests
  cargo test --quiet --locked --manifest-path apps/desktop/src-tauri/storage/Cargo.toml --lib download_receipt::tests
  cargo test --locked --manifest-path apps/desktop/src-tauri/engine/Cargo.toml --lib -- --test-threads=1
  cargo test --locked --manifest-path apps/desktop/src-tauri/engine/Cargo.toml --lib mpv::local_source_tests -- --ignored --test-threads=1
  cargo test --quiet --locked --manifest-path apps/desktop/src-tauri/engine/Cargo.toml --lib mpv::wavpack_tests -- --ignored --test-threads=1
  cargo test --quiet --locked --manifest-path apps/desktop/src-tauri/engine/Cargo.toml --lib mpv::pcm_original_tests -- --ignored --test-threads=1
else
  python3 apps/desktop/test/native-fixtures.py
  cargo test --locked --manifest-path apps/desktop/src-tauri/Cargo.toml --features shell --lib -- --test-threads=1
  cargo test --locked --manifest-path apps/desktop/src-tauri/Cargo.toml --features shell --lib mpv::local_source_tests -- --ignored --test-threads=1
fi
'
