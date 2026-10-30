#!/usr/bin/env bash
# Sourced by the private pinned homebridge recipe. Build additions from source;
# never brew-install a replacement FFmpeg or a binary dependency bundle.
set -euo pipefail
portico_lock="${CWD}/portico-macos-extras.lock.json"
portico_clone() {
 local name="$1" repo revision
 repo=$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))[sys.argv[2]]["repository"])' "$portico_lock" "$name")
 revision=$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))[sys.argv[2]]["commit"])' "$portico_lock" "$name")
 if [ ! -d "$PACKAGES/portico-$name/.git" ]; then
  git init "$PACKAGES/portico-$name"
  git -C "$PACKAGES/portico-$name" remote add origin "$repo"
  git -C "$PACKAGES/portico-$name" fetch --depth=1 origin "$revision"
  git -C "$PACKAGES/portico-$name" checkout --detach FETCH_HEAD
 fi
 [ "$(git -C "$PACKAGES/portico-$name" rev-parse HEAD)" = "$revision" ]
 cd "$PACKAGES/portico-$name"
}
portico_cmake() {
 cmake -S . -B portico-build -DCMAKE_BUILD_TYPE=Release -DCMAKE_INSTALL_PREFIX="$WORKSPACE" -DCMAKE_INSTALL_LIBDIR=lib -DBUILD_SHARED_LIBS=OFF "$@"
 cmake --build portico-build --parallel "${PORTICO_BUILD_JOBS:-4}"
 cmake --install portico-build
}
portico_meson() {
 meson setup portico-build --prefix="$WORKSPACE" --libdir=lib --buildtype=release --default-library=static "$@"
 ninja -C portico-build -j "${PORTICO_BUILD_JOBS:-4}"
 ninja -C portico-build install
}
portico_disc_patch() {
 local patch_file="${CWD}/portico-no-decryption/$1"
 python3 - "$CWD/portico-ffmpeg-sources.lock.json" "$patch_file" <<'PYPATCH'
import hashlib,json,pathlib,sys
patch=pathlib.Path(sys.argv[2])
entries=json.load(open(sys.argv[1]))['linuxSourceBuild']['sourcePatches']
expected=next(x['sha256'] for x in entries if pathlib.Path(x['path']).name==patch.name)
if hashlib.sha256(patch.read_bytes()).hexdigest()!=expected:raise SystemExit('Disc patch checksum changed')
PYPATCH
 if patch --batch --fuzz=0 --dry-run -p1 < "$patch_file"; then
  patch --batch --fuzz=0 -p1 < "$patch_file"
 else
  patch --batch --fuzz=0 --reverse --dry-run -p1 < "$patch_file"
 fi
}
export PKG_CONFIG_PATH="$WORKSPACE/lib/pkgconfig:${PKG_CONFIG_PATH:-}"
portico_clone soxr
portico_cmake -DWITH_OPENMP=OFF -DBUILD_TESTS=OFF -DBUILD_EXAMPLES=OFF -DCMAKE_POLICY_VERSION_MINIMUM=3.5
portico_clone fontconfig
portico_meson -Dtests=disabled -Dtools=disabled -Ddoc=disabled -Dxml-backend=libxml2
portico_clone dvdread
portico_disc_patch libdvdread-no-decryption.patch
portico_meson -Dlibdvdcss=disabled
portico_clone dvdnav
portico_meson
portico_clone udfread
portico_meson -Denable_examples=false
portico_clone bluray
portico_disc_patch libbluray-no-decryption.patch
portico_meson -Denable_docs=false -Denable_tools=false -Denable_examples=false -Dbdj_jar=disabled -Dfontconfig=enabled -Dfreetype=enabled -Dlibxml2=disabled -Dlibudfread=enabled
portico_clone vulkanHeaders
portico_cmake -DVULKAN_HEADERS_ENABLE_TESTS=OFF
portico_clone vulkanLoader
portico_cmake -DBUILD_TESTS=OFF -DBUILD_WSI_XCB_SUPPORT=OFF -DBUILD_WSI_XLIB_SUPPORT=OFF -DBUILD_WSI_WAYLAND_SUPPORT=OFF -DUPDATE_DEPS=OFF -DBUILD_SHARED_LIBS=ON
portico_clone moltenVK
git submodule update --init --recursive
./fetchDependencies --macos
make -j "${PORTICO_BUILD_JOBS:-4}" macos
mkdir -p "$WORKSPACE/lib" "$WORKSPACE/share/vulkan/icd.d"
cp Package/Release/MoltenVK/dylib/macOS/libMoltenVK.dylib "$WORKSPACE/lib/"
cp Package/Release/MoltenVK/dylib/macOS/MoltenVK_icd.json "$WORKSPACE/share/vulkan/icd.d/"
portico_clone shaderc
python3 utils/git-sync-deps
portico_cmake -DSHADERC_SKIP_TESTS=ON -DSHADERC_SKIP_EXAMPLES=ON -DSHADERC_SKIP_COPYRIGHT_CHECK=ON
portico_clone placebo
git submodule update --init --recursive
portico_meson -Dvulkan=enabled -Dshaderc=enabled -Dglslang=disabled -Ddemos=false -Dtests=false -Dbench=false
# Every collected native source and build command is archived with the bundle.
