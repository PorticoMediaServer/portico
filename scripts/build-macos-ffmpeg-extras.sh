#!/usr/bin/env bash
# Sourced by the private pinned homebridge recipe. Build additions from source;
# never brew-install a replacement FFmpeg or a binary dependency bundle.
set -euo pipefail
portico_lock="${CWD}/portico-macos-extras.lock.json"
portico_clone() {
 local name="$1" repo revision
 if declare -F portico_time_start >/dev/null; then portico_time_start "portico-$name"; fi
 repo=$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))[sys.argv[2]]["repository"])' "$portico_lock" "$name")
 revision=$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))[sys.argv[2]]["commit"])' "$portico_lock" "$name")
 if [ ! -d "$PACKAGES/portico-$name/.git" ]; then
  git init "$PACKAGES/portico-$name"
  git -C "$PACKAGES/portico-$name" remote add origin "$repo"
  git -C "$PACKAGES/portico-$name" fetch --depth=1 origin "$revision"
  git -C "$PACKAGES/portico-$name" checkout --detach FETCH_HEAD
 fi
 # Some reviewed SHA-1s name immutable annotated tag objects. Verify the
 # exact object, then compare HEAD with that object's peeled commit.
 [[ "$revision" =~ ^[0-9a-f]{40}$ ]] || return 1
 local peeled kind
 peeled=$(git -C "$PACKAGES/portico-$name" rev-parse --verify "$revision^{commit}") || return 1
 kind=$(git -C "$PACKAGES/portico-$name" cat-file -t "$revision") || return 1
 [[ "$kind" == commit || "$kind" == tag ]] || return 1
 [ "$(git -C "$PACKAGES/portico-$name" rev-parse HEAD)" = "$peeled" ] || return 1
 python3 - "$repo" "$revision" "$peeled" "$kind" "$PACKAGES/portico-$name-source.json" <<'PYSOURCE'
import json,pathlib,sys
repository,locked,commit,kind,destination=sys.argv[1:]
pathlib.Path(destination).write_text(json.dumps({'repository':repository,'lockedObject':locked,
    'objectType':kind,'checkedOutCommit':commit},indent=2)+'\n')
PYSOURCE
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
# The private pkg-config search path deliberately excludes Homebrew libraries.
# Supply only metadata for the exact compiler SDK's built-in system libraries.
python3 "$CWD/portico-macos-system-libs.py" --prefix "$WORKSPACE" \
 --receipt "$PACKAGES/portico-system-libraries.json"
source "$CWD/portico-build-macos-zvbi.sh"
if declare -F portico_time_start >/dev/null; then portico_time_start portico-libdovi; fi
python3 "$CWD/portico-build-macos-libdovi.py" build --recipe "$CWD" --prefix "$WORKSPACE" \
 --target "${PORTICO_FFMPEG_TARGET:?}" --tools "$CWD/../native-rust-tools"
# Match pinned Jellyfin builder/scripts.d/25-openssl.sh on native macOS.
# Static source-built TLS; sources/notices remain in the corresponding archive.
portico_clone openssl
git submodule update --init --recursive --depth=1
python3 - <<'PYOPENSSL'
from pathlib import Path
path = Path('Configure')
text = path.read_text()
lines = text.splitlines(keepends=True)
indices = [i for i, line in enumerate(lines) if line.startswith('my @disablables =')]
if len(indices) != 1 or not lines[indices[0]].rstrip().endswith('('):
    raise SystemExit('Pinned OpenSSL apps-disable boundary changed')
i = indices[0]
lines[i] = lines[i].rstrip('\n') + '"apps",\n'
path.write_text(''.join(lines))
PYOPENSSL
export CFLAGS="${CFLAGS:-} -fno-strict-aliasing"
./Configure threads zlib no-shared no-tests no-apps no-legacy no-ssl3 \
 enable-camellia enable-ec enable-srp --prefix="$WORKSPACE" --libdir=lib
make -j "${PORTICO_BUILD_JOBS:-4}" build_sw
make install_sw
# Use the same immutable component sources as the BtbN targets, with the
# pinned Jellyfin native Mac settings. SDK iconv/libxml2 match Jellyfin's
# explicit system-library exception; no Homebrew media library is used.
portico_clone xz
./autogen.sh --no-po4a --no-doxygen
./configure --prefix="$WORKSPACE" --disable-symbol-versions --disable-shared --enable-static --with-pic
make -j "${PORTICO_BUILD_JOBS:-2}"
make install
portico_clone openmpt
make -j "${PORTICO_BUILD_JOBS:-2}" CONFIG=clang PREFIX="$WORKSPACE" \
 CXXSTDLIB_PCLIBSPRIVATE="-lc++" VERBOSE=2 STATIC_LIB=1 SHARED_LIB=0 DYNLINK=0 \
 EXAMPLES=0 OPENMPT123=0 IN_OPENMPT=0 XMP_OPENMPT=0 DEBUG=0 OPTIMIZE=1 TEST=0 MODERN=1 \
 FORCE_DEPS=1 NO_MINIMP3=0 NO_ZLIB=0 NO_OGG=0 NO_VORBIS=0 NO_VORBISFILE=0 \
 NO_MPG123=1 NO_SDL2=1 NO_PULSEAUDIO=1 NO_SNDFILE=1 NO_PORTAUDIO=1 NO_PORTAUDIOCPP=1 NO_FLAC=1 all install
portico_clone chromaprint
portico_cmake -DBUILD_TOOLS=OFF -DBUILD_TESTS=OFF -DFFT_LIB=vdsp
# Apple clang uses libc++; the FFT backend is Apple's system Accelerate.
printf '%s\n' 'Libs.private: -framework Accelerate -lc++' 'Cflags.private: -DCHROMAPRINT_NODLL' >> "$WORKSPACE/lib/pkgconfig/libchromaprint.pc"
portico_clone srt
portico_cmake -DENABLE_SHARED=OFF -DENABLE_STATIC=ON -DENABLE_CXX_DEPS=ON \
 -DUSE_STATIC_LIBSTDCXX=ON -DENABLE_ENCRYPTION=ON -DENABLE_APPS=OFF \
 -DOPENSSL_ROOT_DIR="$WORKSPACE" -DOPENSSL_USE_STATIC_LIBS=TRUE
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
# The pinned libbluray always requires libudfread >= 1.2.0; it has no libudfread option.
portico_meson -Denable_docs=false -Denable_tools=false -Denable_examples=false -Dbdj_jar=disabled -Dfontconfig=enabled -Dfreetype=enabled -Dlibxml2=disabled
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
# Match the pinned BtbN static module selection; keep the shared build unused.
python3 "$CWD/portico-configure-macos-shaderc.py" --prefix "$WORKSPACE" \
 --receipt "$CWD/packages/portico-shaderc-static-link.json"
portico_clone placebo
git submodule update --init --recursive
portico_meson -Dvulkan=enabled -Dshaderc=enabled -Dglslang=disabled -Ddemos=false -Dtests=false -Dbench=false
# Every collected native source and build command is archived with the bundle.
