#!/usr/bin/env bash
set -euo pipefail
# Native macOS builds, or BtbN's container cross builds. Never publishes.
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
TARGET="${1:?usage: build-ffmpeg.sh TARGET [WORK_DIRECTORY]}"
PHASE="${PORTICO_FFMPEG_PHASE:-full}"
case "$PHASE" in full) ;; working|compile|package) [[ "$TARGET" == linux-x64 ]] || { echo 'Split compile/package phases currently require linux-x64' >&2; exit 1; };; *) echo 'Unknown source build phase' >&2; exit 1;; esac
# The dedicated hardware lane names its on-disk scratch directory explicitly.
# CI and local packaging runs without one use the script-owned runtime
# directory, so the emitted archive stays on the path packaging reads.
if [[ -n "${PORTICO_LANE_SCRATCH:-}" ]]; then
  LANE_SCRATCH="$PORTICO_LANE_SCRATCH"
  [[ "$LANE_SCRATCH" == /*/.scratch/hardware-r29 ]] || { echo 'Use the lane scratch directory on disk' >&2; exit 1; }
  mkdir -p "$LANE_SCRATCH"
  LANE_SCRATCH="$(cd "$LANE_SCRATCH" && pwd -P)"
  case "$LANE_SCRATCH" in /tmp/*|/dev/shm/*) echo 'Lane scratch must be on disk' >&2; exit 1;; esac
  TEMP_ROOT="$LANE_SCRATCH"
else
  LANE_SCRATCH="$ROOT/runtime"
  mkdir -p "$LANE_SCRATCH"
  LANE_SCRATCH="$(cd "$LANE_SCRATCH" && pwd -P)"
  # Disposable tool caches only. The build output and its archive never move.
  TEMP_ROOT="${RUNNER_TEMP:-$LANE_SCRATCH}"
  mkdir -p "$TEMP_ROOT"
  TEMP_ROOT="$(cd "$TEMP_ROOT" && pwd -P)"
fi
export PORTICO_LANE_SCRATCH="$LANE_SCRATCH"
WORK="${2:-$LANE_SCRATCH/ffmpeg-build/$TARGET}"
[[ "$WORK" == /* ]] || WORK="$PWD/$WORK"
[[ "$WORK" == "$LANE_SCRATCH/"* ]] || { echo 'Build output must stay under the scratch directory' >&2; exit 1; }
mkdir -p "$WORK"
WORK="$(cd "$WORK" && pwd -P)"
[[ "$WORK" == "$LANE_SCRATCH/"* ]] || { echo 'Build output must stay under the scratch directory' >&2; exit 1; }
export TMPDIR="$TEMP_ROOT/tmp" GOTMPDIR="$TEMP_ROOT/go-tmp" XDG_CACHE_HOME="$TEMP_ROOT/cache" PIP_CACHE_DIR="$TEMP_ROOT/cache/pip"
export GOCACHE="$HOME/.cache/go-build"
mkdir -p "$TMPDIR" "$GOTMPDIR" "$XDG_CACHE_HOME" "$PIP_CACHE_DIR"
readlock() { python3 -c 'import json,sys;v=json.load(open(sys.argv[1]));print(v[sys.argv[2]][sys.argv[3]])' "$ROOT/third_party/ffmpeg/sources.lock.json" "$1" "$2"; }
python3 "$ROOT/scripts/check-ffmpeg-build-inputs.py" --output "$WORK/build-source-authority.json"
VERSION="$(readlock ffmpeg version)"
ARCHIVE="$WORK/jellyfin-ffmpeg-source.tar.gz"
SOURCE="$WORK/ffmpeg-$VERSION.tar.xz"
export SOURCE_DATE_EPOCH="$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["sourceDateEpoch"])' "$ROOT/third_party/ffmpeg/sources.lock.json")"
# The helper is owned by the video source lane. A job must name its checked
# source commit explicitly; an uncommitted or unrelated WIP file is not intake.
DISC_SOURCE="$ROOT/third_party/portico-disc/portico-disc.c"
DISC_HEADER="$ROOT/third_party/portico-disc/mpegts_dovi.h"
if [[ "${PORTICO_FFMPEG_SOURCE_ONLY:-0}" != 1 && "${PORTICO_FFMPEG_DEPS_ONLY:-0}" != 1 ]]; then
  [[ ${PORTICO_DISC_SOURCE_COMMIT:-} =~ ^[0-9a-f]{40}$ ]] || { echo 'Set PORTICO_DISC_SOURCE_COMMIT to the checked disc source commit' >&2; exit 1; }
  [[ "$PORTICO_DISC_SOURCE_COMMIT" == "$(readlock discHelper sourceCommit)" ]] || { echo 'Disc helper commit differs from the canonical checked source lock' >&2; exit 1; }
  [[ -f "$DISC_SOURCE" && ! -L "$DISC_SOURCE" ]] || { echo 'Checked disc source is missing' >&2; exit 1; }
  cp "$DISC_SOURCE" "$WORK/checked-portico-disc.c"
  cmp "$WORK/checked-portico-disc.c" "$DISC_SOURCE"
  [[ -f "$DISC_HEADER" && ! -L "$DISC_HEADER" ]] || { echo 'Checked disc PSI header is missing' >&2; exit 1; }
  cp "$DISC_HEADER" "$WORK/mpegts_dovi.h"
  cmp "$WORK/mpegts_dovi.h" "$DISC_HEADER"
  cp "$ROOT/LICENSE" "$WORK/checked-portico-disc.LICENSE"
  cmp "$WORK/checked-portico-disc.LICENSE" "$ROOT/LICENSE"
  [[ ${PORTICO_DISC_FIXTURES_SHA256:-} =~ ^[0-9a-f]{64}$ && -f ${PORTICO_DISC_FIXTURES:-} ]] || { echo 'Provide the checked disc fixture manifest and SHA256' >&2; exit 1; }
fi
if [[ ! -f "$ARCHIVE" ]]; then
  curl --fail --location --retry 3 "$(readlock ffmpeg source)" -o "$ARCHIVE.new"
  mv "$ARCHIVE.new" "$ARCHIVE"
fi
# Keep an earlier checked source receipt intact when a new post-series contract
# is introduced. Each source tree is extracted directly from the locked archive.
POST_SERIES_KEY="$(python3 -c 'import hashlib,json,sys;p=json.load(open(sys.argv[1]))["ffmpeg"].get("postSeriesPatches",[]);print("-"+hashlib.sha256(json.dumps(p,sort_keys=True).encode()).hexdigest()[:12] if p else "")' "$ROOT/third_party/ffmpeg/sources.lock.json")"
SOURCE="$WORK/ffmpeg-$VERSION$POST_SERIES_KEY.tar.xz"
PREPARED_SOURCE="$WORK/patched-source$POST_SERIES_KEY"
python3 "$ROOT/scripts/prepare-ffmpeg-source.py" "$PREPARED_SOURCE" --archive "$ARCHIVE"
python3 "$ROOT/scripts/archive-ffmpeg-source.py" "$PREPARED_SOURCE" "$SOURCE" "$SOURCE_DATE_EPOCH"
if [[ "${PORTICO_FFMPEG_SOURCE_ONLY:-0}" == 1 ]]; then echo "$SOURCE"; exit 0; fi
case "$TARGET" in
 macos-arm64|macos-x64)
  [[ "$(uname -s)" == Darwin ]]
  expected=arm64; [[ "$TARGET" == macos-x64 ]] && expected=x86_64
  [[ "$(uname -m)" == "$expected" ]] || { echo 'Use a native runner for this architecture' >&2;exit 1; }
  if [[ ! -d "$WORK/recipe/.git" && -d "$WORK/recipe/.git.bak" ]]; then mv "$WORK/recipe/.git.bak" "$WORK/recipe/.git"; fi
  [[ -d "$WORK/recipe/.git" ]] || git clone "$(readlock macosRecipe repository)" "$WORK/recipe"
  git -C "$WORK/recipe" checkout --detach "$(readlock macosRecipe commit)"
  for patch in homebridge-sourceforge-download.patch homebridge-gpl-only.patch homebridge-arm64-vpx.patch homebridge-private-python.patch homebridge-zix-build.patch homebridge-private-pkgconfig.patch; do
    if git -C "$WORK/recipe" apply --check "$ROOT/third_party/ffmpeg/patches/$patch"; then git -C "$WORK/recipe" apply "$ROOT/third_party/ffmpeg/patches/$patch"; else git -C "$WORK/recipe" apply --reverse --check "$ROOT/third_party/ffmpeg/patches/$patch"; fi
  done
  # Build tools and downloads are private to this job; never upgrade system Python.
  python3 -m venv "$WORK/python"
  "$WORK/python/bin/pip" install --disable-pip-version-check 'meson==1.11.0' 'ninja==1.11.1.4' 'setuptools==75.8.0'
  export PATH="$WORK/python/bin:$PATH"
  python3 "$ROOT/scripts/configure-ffmpeg-recipe.py" "$WORK/recipe" "$PREPARED_SOURCE" "$TARGET"
  # Seed the exact verified source consumed by the pinned recipe.
  mkdir -p "$WORK/recipe/packages";python3 "$ROOT/scripts/archive-ffmpeg-source.py" "$PREPARED_SOURCE" "$WORK/recipe/packages/FFmpeg-release-$VERSION.tar.gz" "$SOURCE_DATE_EPOCH"
  (cd "$WORK/recipe" && FFMPEG_VERSION="$VERSION" SKIPINSTALL=yes SKIPRAV1E=yes ./build-ffmpeg --build --enable-gpl)
  INPUT="$WORK/recipe/workspace"
  python3 "$ROOT/scripts/build-portico-disc.py" --source "$WORK/checked-portico-disc.c" --source-commit "$PORTICO_DISC_SOURCE_COMMIT" --prefix "$INPUT" --output "$INPUT/bin/portico-disc" --metadata "$INPUT/portico-disc-build.json" --target "$TARGET"
  cp "$WORK/checked-portico-disc.LICENSE" "$INPUT/portico-disc-LICENSE"
  mkdir -p "$INPUT/LICENSES"
  python3 - "$WORK/recipe/packages" "$INPUT/LICENSES" <<'PYLICENSE'
import pathlib,shutil,sys
for i,f in enumerate(sorted(pathlib.Path(sys.argv[1]).rglob("*"))):
 if f.is_file() and f.name.lower().startswith(("license","copying")):
  shutil.copy2(f,pathlib.Path(sys.argv[2])/(str(i)+"-"+f.name))
PYLICENSE
  ;;
 linux-x64|linux-arm64|windows-x64|windows-arm64)
  case "$TARGET" in linux-x64) native=linux64;;linux-arm64) native=linuxarm64;;windows-x64) native=win64;;windows-arm64) native=winarm64;;esac
  [[ -d "$WORK/recipe/.git" ]] || git clone "$(readlock btbn repository)" "$WORK/recipe"
  git -C "$WORK/recipe" checkout --detach "$(readlock btbn commit)"
  # These generated recipe files are owned by this build. Restore only the
  # patched files so a resumed job cannot accumulate overlapping edits.
  git -C "$WORK/recipe" restore --source="$(readlock btbn commit)" -- build.sh images/base-linux64/ct-ng-config images/base-linuxarm64/ct-ng-config scripts.d
  for patch in btbn-gnu-ncurses-mirror.patch btbn-verified-source.patch; do
    if git -C "$WORK/recipe" apply --check "$ROOT/third_party/ffmpeg/patches/$patch"; then git -C "$WORK/recipe" apply "$ROOT/third_party/ffmpeg/patches/$patch"; else git -C "$WORK/recipe" apply --reverse --check "$ROOT/third_party/ffmpeg/patches/$patch"; fi
  done
  if ! grep -q 'std::system_error in the Windows Graphics Capture' "$WORK/recipe/build.sh"; then "$ROOT/scripts/patch-btbn-winarm64-system-error.sh" "$WORK/recipe/build.sh"; fi
  if [[ "${PORTICO_FFMPEG_DEPS_ONLY:-0}" != 1 ]]; then
    mkdir -p "$WORK/recipe/portico-disc"
    cp "$WORK/checked-portico-disc.c" "$WORK/recipe/portico-disc/portico-disc.c"
    cp "$WORK/mpegts_dovi.h" "$WORK/recipe/portico-disc/mpegts_dovi.h"
    cp "$WORK/checked-portico-disc.LICENSE" "$WORK/recipe/portico-disc/LICENSE"
    cp "$ROOT/scripts/build-portico-disc.py" "$WORK/recipe/portico-disc/build-portico-disc.py"
  fi
  python3 "$ROOT/scripts/configure-ffmpeg-recipe.py" "$WORK/recipe" "$PREPARED_SOURCE" "$TARGET"
  if [[ "$TARGET" == linux-x64 ]]; then
    if [[ "$PHASE" == package ]]; then
      # Recheck exact source, recipe, dependency seal and both executable rounds
      # without restarting the compiler image retired after the compile phase.
      python3 "$ROOT/scripts/build-linux-ffmpeg.py" --work "$WORK" --source "$SOURCE" --verify-completed-only
    else
    python3 "$ROOT/scripts/build-linux-ffmpeg-deps.py" --recipe "$WORK/recipe" --work "$WORK/linux-deps"
    docker run --rm --name portico-hardware-r29-licenses --label portico.lane=hardware-r29 \
      --network none --cpus 1 --memory 512m --memory-swap 512m \
      -v "$WORK/linux-deps:/dependencies" -v "$ROOT/scripts/collect-ffmpeg-licenses.py:/collect.py:ro" \
      -e TMPDIR=/dependencies/tmp "$(readlock linuxSourceBuild compilerImage)" bash -c '
        set -euo pipefail
        portico_output_owner="$(stat -c "%u:%g" /dependencies)"
        portico_restore_owner() {
          portico_stage_status=$?
          trap - EXIT
          chown -hRP "$portico_output_owner" /dependencies
          exit "$portico_stage_status"
        }
        trap portico_restore_owner EXIT
        python3 /collect.py /dependencies'
    if [[ "${PORTICO_FFMPEG_DEPS_ONLY:-0}" == 1 ]]; then
      echo "Dependency source build completed: $WORK/linux-deps"
      exit 0
    fi
    # Build libdovi from its MAIN-qualified source/Cargo locks after the base
    # dependency job exits; never overlap two compiler pools.
    python3 "$ROOT/scripts/build-linux-libdovi.py" --work "$WORK"
    # The source-built binaries use the exact immutable dependency prefix.
    compile_options=()
    [[ "$PHASE" != working ]] || compile_options+=(--first-build-only)
    python3 "$ROOT/scripts/build-linux-ffmpeg.py" --work "$WORK" --source "$SOURCE" "${compile_options[@]}"
    fi
    INPUT="$WORK/linux-output"
    if [[ "$PHASE" == compile ]]; then
      echo "Repeated source compilation finished; bundle packaging and runtime qualification remain: $INPUT"
      exit 0
    fi
  else
    [[ "${PORTICO_FFMPEG_DEPS_ONLY:-0}" != 1 ]] || { echo 'Dependency-only phase currently supports linux-x64' >&2; exit 1; }
  cp "$SOURCE" "$WORK/recipe/portico-ffmpeg-source.tar.xz"
  (cd "$WORK/recipe" && export GIT_BRANCH_OVERRIDE="n$VERSION" && ./makeimage.sh "$native" gpl 8.1 && ./build.sh "$native" gpl 8.1)
  mkdir -p "$WORK/extracted"
  python3 - "$WORK/recipe/artifacts" "$WORK/extracted" <<'PY'
import pathlib,shutil,sys
files=[p for p in pathlib.Path(sys.argv[1]).rglob('*') if p.name.endswith(('.tar.xz','.zip'))]
if len(files)!=1:raise SystemExit('Expected exactly one build artifact')
shutil.unpack_archive(str(files[0]),sys.argv[2])
PY
  INPUT="$WORK/extracted"
  fi
  ;;
 *) echo 'Unknown target' >&2;exit 2;;
esac
# A test asset, not a dependency of the distributed server/player.
export PORTICO_FFMPEG_TEST_FONT="$ROOT/packages/reader-core/assets/Literata[opsz,wght].ttf"
mkdir -p "$INPUT/LICENSES/fixtures"
cp "$ROOT/packages/reader-core/assets/OFL.txt" "$INPUT/LICENSES/fixtures/Literata-OFL.txt"
mkdir -p "$WORK/provenance"
mkdir -p "$WORK/provenance/contract" "$WORK/provenance/scripts"
mkdir -p "$WORK/provenance/fixtures/subtitles"
cp "$PORTICO_FFMPEG_TEST_FONT" "$WORK/provenance/fixtures/subtitles/Literata.ttf"
cp "$ROOT/packages/reader-core/assets/OFL.txt" "$ROOT/packages/reader-core/assets/SOURCES.md" "$WORK/provenance/fixtures/subtitles/"
mkdir -p "$WORK/provenance/portico-disc"
cp "$WORK/checked-portico-disc.c" "$WORK/provenance/portico-disc/portico-disc.c"
cp "$WORK/mpegts_dovi.h" "$WORK/provenance/portico-disc/mpegts_dovi.h"
cp "$WORK/checked-portico-disc.LICENSE" "$WORK/provenance/portico-disc/LICENSE"
cp "$ROOT/third_party/portico-disc/"{NOTICE.md,contract.md,schema1.json,fixture-expectations.json} "$WORK/provenance/portico-disc/"
cp -al "$ROOT/fixtures/discs" "$WORK/provenance/fixtures/discs"
cp "$ROOT/third_party/ffmpeg/"*.json "$ROOT/third_party/ffmpeg/NOTICE.md" "$WORK/provenance/contract/"
cp "$WORK/build-source-authority.json" "$WORK/provenance/contract/"
cp -R "$ROOT/third_party/ffmpeg/patches" "$WORK/provenance/contract/"
cp "$ROOT/scripts/build-ffmpeg.sh" "$ROOT/scripts/normalize-ffmpeg-bundle.py" "$ROOT/scripts/patch-btbn-winarm64-system-error.sh" "$ROOT/scripts/prepare-ffmpeg-source.py" "$ROOT/scripts/archive-ffmpeg-source.py" "$ROOT/scripts/configure-ffmpeg-recipe.py" "$WORK/provenance/scripts/"
cp "$ROOT/scripts/build-portico-disc.py" "$ROOT/scripts/build-macos-ffmpeg-extras.sh" "$WORK/provenance/scripts/"
cp "$ROOT/scripts/verify-ffmpeg-bundle.sh" "$ROOT/scripts/verify-ffmpeg-manifest.py" "$ROOT/scripts/verify-ffmpeg-features.py" "$ROOT/scripts/verify-disc-bundle.py" "$WORK/provenance/scripts/"
cp "$ROOT/scripts/verify-ffmpeg-bluray.py" "$WORK/provenance/scripts/"
cp "$ROOT/scripts/verify-ffmpeg-recipes.py" "$ROOT/scripts/verify-hdr10plus-encode.py" "$WORK/provenance/scripts/"
cp "$ROOT/scripts/verify-ffmpeg-abi.py" "$WORK/provenance/scripts/"
cp "$ROOT/scripts/check-ffmpeg-build-inputs.py" "$WORK/provenance/scripts/"
cp "$ROOT/scripts/run-ffmpeg-runner-build.py" "$ROOT/scripts/repair-ffmpeg-runner-ownership.py" "$WORK/provenance/scripts/"
cp "$ARCHIVE" "$SOURCE" "$WORK/provenance/"
if [[ "$TARGET" == linux-x64 ]]; then
  mkdir -p "$WORK/provenance/linux-dependencies"
  cp -al "$WORK/linux-deps/sources" "$WORK/provenance/linux-dependencies/sources"
  cp -a "$WORK/linux-deps/licenses" "$WORK/provenance/linux-dependencies/licenses"
  cp "$WORK/linux-deps/"{dependency-inputs.json,stage-plan.json,prefix-files.json,compiler.txt,license-inventory.json} "$WORK/provenance/linux-dependencies/"
  cp "$WORK/repeat-build.json" "$WORK/provenance/"
  cp "$WORK/linux-build-inputs.json" "$WORK/provenance/"
  cp -a "$WORK/linux-build-inputs" "$WORK/provenance/"
  cp -a "$INPUT/compiler-source" "$WORK/provenance/"
  cp "$INPUT/compiler-linked-inputs.json" "$WORK/provenance/"
  cp "$ROOT/scripts/build-linux-ffmpeg-deps.py" "$ROOT/scripts/build-linux-ffmpeg.py" "$WORK/provenance/scripts/"
  cp "$ROOT/scripts/reuse-ffmpeg-protocol-objects.py" "$WORK/provenance/scripts/"
  cp "$ROOT/scripts/capture-ffmpeg-compiler.py" "$WORK/provenance/scripts/"
  cp "$ROOT/scripts/collect-ffmpeg-licenses.py" "$WORK/provenance/scripts/"
  cp "$ROOT/scripts/build-linux-libdovi.py" "$WORK/provenance/scripts/"
  mkdir -p "$WORK/provenance/libdovi"
  cp -a "$LANE_SCRATCH/libdovi/sources" "$WORK/provenance/libdovi/sources"
  cp "$WORK/libdovi/"{vendor-source.tar.xz,vendor-lock.json,fetch.sh,build.sh,contract.json,expected.json,compiler-and-licenses.json} "$WORK/provenance/libdovi/"
  cp -a "$WORK/libdovi/license-supplements" "$WORK/provenance/libdovi/"
  cp "$INPUT/libdovi-build.json" "$WORK/provenance/libdovi/"
  if [[ "$PHASE" != working ]]; then
    cp "$INPUT/dovi-profile-build.json" "$INPUT/dovi-video-build.json" "$WORK/provenance/libdovi/"
  fi
  cp -a "$ROOT/third_party/ffmpeg/tests" "$WORK/provenance/contract/"
  cp "$ROOT/third_party/ffmpeg/portico-dovi-profile.patch" "$WORK/provenance/contract/"
  cp "$ROOT/third_party/ffmpeg/portico-dovi-join.patch" "$WORK/provenance/contract/"
  cp "$ROOT/third_party/ffmpeg/portico-bluray-playlist-minimum.patch" "$ROOT/third_party/ffmpeg/portico-x265-hdr10plus.patch" "$WORK/provenance/contract/"
  cp "$ROOT/scripts/qualify-linux-ffmpeg-hdr.py" "$ROOT/scripts/qualify-hdr-video.py" "$ROOT/scripts/qualify-dovi-join.py" "$WORK/provenance/scripts/"
fi
# Include dependency sources and exact recipe, excluding only output binaries and git metadata.
tar -C "$WORK" --sort=name --mtime="@$SOURCE_DATE_EPOCH" --owner=0 --group=0 --numeric-owner --exclude=recipe/.git --exclude=recipe/workspace --exclude=recipe/artifacts --exclude=recipe/.cache/images -I 'xz -T1 -0' -cf "$WORK/corresponding-source.tar.xz" recipe provenance
normalize_options=()
archive_name="portico-ffmpeg-$TARGET.tar.xz"
if [[ "$PHASE" == working ]]; then
  normalize_options+=(--working)
  archive_name="portico-ffmpeg-$TARGET-working.tar.xz"
fi
python3 "$ROOT/scripts/normalize-ffmpeg-bundle.py" "$INPUT" "$TARGET" "$WORK/bundle" "$WORK/corresponding-source.tar.xz" "${normalize_options[@]}"
if [[ "$PHASE" == working ]]; then
  "$WORK/bundle/bin/ffmpeg" -version
  "$WORK/bundle/bin/ffprobe" -version
  echo 'First working source bundle; repeat and full target qualification remain.'
else
  "$ROOT/scripts/verify-ffmpeg-bundle.sh" "$WORK/bundle"
fi
# The payload includes the compressed corresponding-source archive. Keep this
# outer compression pass fast; the manifest seals every extracted file.
tar -C "$WORK/bundle" --sort=name --mtime="@$SOURCE_DATE_EPOCH" --owner=0 --group=0 --numeric-owner -I 'xz -T1 -0' -cf "$WORK/$archive_name" .
echo "$WORK/$archive_name"
