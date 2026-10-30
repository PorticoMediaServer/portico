#!/usr/bin/env bash
# Builds the media engine for Android from the pinned sources in sources.lock.json.
#
#   ./build-android.sh                      # all four ABIs the app ships
#   ./build-android.sh --abis "arm64-v8a"   # a subset (an emulator build, say)
#   ./build-android.sh --clean              # forget earlier builds first
#
# Output (MEDIA_ENGINE_OUT_DIR, default ./out): out/android/
#   lib/<abi>/lib*.so   one stripped shared library per component, plain SONAMEs, 16 KB pages
#   include/mpv/*.h     libmpv's headers (the JNI bridge compiles against client.h)
#   meta/<abi>/         FFmpeg's configuration and component lists, mpv's feature list
#   notices/, source/   licence texts and the corresponding source bundle (collect-notices.sh)
#   manifest.json       what was built (verify.py checks it)
#
# Work files (sources, builds, tools; several GB) go to MEDIA_ENGINE_WORK_DIR, default ./work.
# Needs: Linux or macOS, the Android NDK named in the lock under $ANDROID_NDK_HOME or
# $ANDROID_HOME/ndk/, rustup, python3, perl, pkg-config, make, curl. Everything else is installed
# into the work directory at the pinned versions.
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/common.sh"

ABIS="arm64-v8a armeabi-v7a x86 x86_64"
CLEAN=0
while (( $# )); do
    case "$1" in
        --abis) ABIS="$2"; shift 2 ;;
        --clean) CLEAN=1; shift ;;
        -h|--help) sed -n '2,20p' "$0"; exit 0 ;;
        *) me_die "Unknown argument $1 (see --help)" ;;
    esac
done

PLATFORM=android
API=$(me_lock tool android-api version)
NDK_VERSION=$(me_lock tool android-ndk version)
NDK="${ANDROID_NDK_HOME:-${ANDROID_HOME:-$HOME/Android/Sdk}/ndk/$NDK_VERSION}"
[[ -f "$NDK/source.properties" ]] || me_die "Android NDK $NDK_VERSION not found at $NDK. Install it with the SDK manager (sdkmanager \"ndk;$NDK_VERSION\") or set ANDROID_NDK_HOME."
grep -q "Pkg.Revision = $NDK_VERSION" "$NDK/source.properties" || me_die "$NDK is not NDK $NDK_VERSION (the version the app is built with)."
case "$(uname -s)" in
    Linux) HOST_TAG=linux-x86_64 ;;
    Darwin) HOST_TAG=darwin-x86_64 ;;
    *) me_die "Build on Linux or macOS." ;;
esac
TC="$NDK/toolchains/llvm/prebuilt/$HOST_TAG"
SYSROOT="$TC/sysroot"
for abi in $ABIS; do
    case "$abi" in arm64-v8a|armeabi-v7a|x86|x86_64) ;; *) me_die "Unknown ABI $abi" ;; esac
done

me_check_disk 20
(( CLEAN )) && rm -rf "$ME_WORK/build/$PLATFORM" "$ME_WORK/prefix/$PLATFORM" "$ME_WORK/stamps/$PLATFORM" "$ME_OUT/$PLATFORM"
mkdir -p "$ME_LOGS" "$ME_WORK/stamps/$PLATFORM"
me_tools

# ---------------------------------------------------------------------------------------------
# Per-ABI toolchain settings.

set_abi() {
    ABI="$1"
    case "$ABI" in
        arm64-v8a)   TRIPLE=aarch64-linux-android;    CLANG=aarch64-linux-android$API;     CPU_FAMILY=aarch64; CPU=armv8-a;  FF_ARCH=aarch64; FF_CPU=armv8-a; OSSL=android-arm64;  RUST=aarch64-linux-android ;;
        armeabi-v7a) TRIPLE=arm-linux-androideabi;    CLANG=armv7a-linux-androideabi$API;  CPU_FAMILY=arm;     CPU=armv7;    FF_ARCH=arm;     FF_CPU=armv7-a; OSSL=android-arm;    RUST=armv7-linux-androideabi ;;
        x86)         TRIPLE=i686-linux-android;       CLANG=i686-linux-android$API;        CPU_FAMILY=x86;     CPU=i686;     FF_ARCH=x86;     FF_CPU=i686;    OSSL=android-x86;    RUST=i686-linux-android ;;
        x86_64)      TRIPLE=x86_64-linux-android;     CLANG=x86_64-linux-android$API;      CPU_FAMILY=x86_64;  CPU=x86_64;   FF_ARCH=x86_64;  FF_CPU=x86-64;  OSSL=android-x86_64; RUST=x86_64-linux-android ;;
    esac
    PREFIX="$ME_WORK/prefix/$PLATFORM/$ABI"
    BUILD="$ME_WORK/build/$PLATFORM/$ABI"
    mkdir -p "$PREFIX/lib/pkgconfig" "$PREFIX/include" "$BUILD"
    export CC="$TC/bin/$CLANG-clang" CXX="$TC/bin/$CLANG-clang++"
    export AR="$TC/bin/llvm-ar" NM="$TC/bin/llvm-nm" RANLIB="$TC/bin/llvm-ranlib" STRIP="$TC/bin/llvm-strip"
    export PKG_CONFIG_LIBDIR="$PREFIX/lib/pkgconfig" PKG_CONFIG_PATH="" PKG_CONFIG_SYSROOT_DIR=""
    # 16 KB pages (Google Play requires it for 64-bit; harmless on 32-bit), only the libraries
    # actually used are recorded as dependencies, and a build id for crash symbolication.
    LDFLAGS_ABI="-Wl,-z,max-page-size=16384 -Wl,-z,common-page-size=16384 -Wl,--as-needed -Wl,--build-id=sha1 -Wl,-z,relro -Wl,-z,now"
    # No build-machine folders in the libraries (lib/common.sh, "Neutral paths").
    PATH_MAP="$(me_path_map_cflags "$NDK")"
    CFLAGS_ABI="-fPIC -fstack-protector-strong -D_FORTIFY_SOURCE=2 $PATH_MAP"
    [[ "$ABI" == armeabi-v7a ]] && CFLAGS_ABI="$CFLAGS_ABI -mfpu=neon -mthumb"
    ASM_OK=1
    # 32-bit x86 assembly in these projects is not position-independent; Android refuses text
    # relocations, so the x86 (old emulator) build uses C only.
    [[ "$ABI" == x86 ]] && ASM_OK=0
    write_cross_file
    write_zlib_pc
}

meson_list() { local out="" a; for a in "$@"; do out+="'$a', "; done; printf '[%s]' "${out%, }"; }

write_cross_file() {
    # shellcheck disable=SC2086
    cat > "$BUILD/cross.ini" <<EOF
[binaries]
c = '$CC'
cpp = '$CXX'
ar = '$AR'
nm = '$NM'
strip = '$STRIP'
ranlib = '$RANLIB'
pkg-config = '$(command -v pkg-config)'
nasm = '$(command -v nasm)'

[built-in options]
c_args = $(meson_list $CFLAGS_ABI "-I$PREFIX/include")
cpp_args = $(meson_list $CFLAGS_ABI "-I$PREFIX/include")
c_link_args = $(meson_list $LDFLAGS_ABI "-L$PREFIX/lib")
cpp_link_args = $(meson_list $LDFLAGS_ABI "-L$PREFIX/lib")

[properties]
needs_exe_wrapper = true
pkg_config_libdir = '$PREFIX/lib/pkgconfig'

[host_machine]
system = 'android'
cpu_family = '$CPU_FAMILY'
cpu = '$CPU'
endian = 'little'
EOF
}

# zlib is the NDK's own (libz.so is a public Android library); a .pc file lets pkg-config users find it.
write_zlib_pc() {
    local version
    version=$(sed -n 's/^#define ZLIB_VERSION "\([0-9.]*\).*/\1/p' "$SYSROOT/usr/include/zlib.h" | head -1)
    cat > "$PREFIX/lib/pkgconfig/zlib.pc" <<EOF
Name: zlib
Description: zlib from the Android NDK sysroot
Version: ${version:-1.3}
Libs: -lz
Cflags:
EOF
}

meson_build() {
    local name="$1"; shift
    local dir="$BUILD/$name"
    rm -rf "$dir"
    meson setup "$dir" "$ME_WORK/src/$name" --cross-file "$BUILD/cross.ini" \
        --prefix "$PREFIX" --libdir lib --buildtype release --default-library shared \
        --wrap-mode nodownload "$@"
    # mpv writes its configure command line and config folder into config.h.
    [[ -f "$dir/config.h" ]] && me_neutralize "$NDK" "$dir/config.h"
    ninja -C "$dir" -j "$ME_JOBS"
    ninja -C "$dir" install
}

# ---------------------------------------------------------------------------------------------
# Components, in dependency order. Each runs in a subshell with its output in the log.

build_openssl() {
    cd "$ME_WORK/src/openssl"
    make distclean >/dev/null 2>&1 || true
    # OpenSSL's Android targets find the compiler through ANDROID_NDK_ROOT and PATH; its
    # shared libraries are already named libcrypto.so and libssl.so with matching SONAMEs.
    PATH="$TC/bin:$PATH" ANDROID_NDK_ROOT="$NDK" CC=clang CXX=clang++ \
        ./Configure "$OSSL" -D__ANDROID_API__="$API" --prefix="$PREFIX" --libdir=lib \
        --openssldir=/dev/null/portico-no-openssl-dir \
        $(me_openssl_common_flags) $( (( ASM_OK )) || echo no-asm ) \
        $CFLAGS_ABI $LDFLAGS_ABI
    # Engines and modules are not built (no-engine, no-module); their folders must not name the
    # work directory either. The "compiler:" line OpenSSL_version() reports is made neutral.
    local dirs=(ENGINESDIR=/dev/null/portico-no-engines MODULESDIR=/dev/null/portico-no-modules)
    PATH="$TC/bin:$PATH" make "${dirs[@]}" crypto/buildinf.h
    me_neutralize "$NDK" crypto/buildinf.h
    PATH="$TC/bin:$PATH" make -j"$ME_JOBS" "${dirs[@]}" build_libs
    PATH="$TC/bin:$PATH" make "${dirs[@]}" install_dev
}

build_libiconv() {
    cd "$ME_WORK/src/libiconv"
    make distclean >/dev/null 2>&1 || true
    ./configure --host="$TRIPLE" --prefix="$PREFIX" --libdir="$PREFIX/lib" --enable-shared --disable-static \
        --disable-nls --disable-rpath --enable-extra-encodings=no \
        CFLAGS="$CFLAGS_ABI -O2" LDFLAGS="$LDFLAGS_ABI"
    # -avoid-version: Android loads libraries by their plain name (libiconv.so, no .so.N).
    # Only the library is built (lib/localcharset.h first builds libcharset into lib/); the GPL
    # iconv program in src/ is never compiled.
    make lib/localcharset.h LDFLAGS="$LDFLAGS_ABI -avoid-version"
    make -j"$ME_JOBS" -C lib all LDFLAGS="$LDFLAGS_ABI -avoid-version"
    make -C lib install-lib libdir="$PREFIX/lib" includedir="$PREFIX/include" LDFLAGS="$LDFLAGS_ABI -avoid-version"
    install -m 644 include/iconv.h.inst "$PREFIX/include/iconv.h"
    # libcharset is compiled into libiconv; only its header is needed.
    rm -f "$PREFIX"/lib/libcharset.so* "$PREFIX"/lib/libcharset.la "$PREFIX"/lib/libiconv.la
}

build_fribidi() { meson_build fribidi $(me_fribidi_common_flags); }

build_freetype() { meson_build freetype $(me_freetype_common_flags); }

build_harfbuzz() { meson_build harfbuzz $(me_harfbuzz_common_flags); }

build_libunibreak() {
    cd "$ME_WORK/src/libunibreak"
    make distclean >/dev/null 2>&1 || true
    ./configure --host="$TRIPLE" --prefix="$PREFIX" --libdir="$PREFIX/lib" --enable-shared --disable-static \
        CFLAGS="$CFLAGS_ABI -O2" LDFLAGS="$LDFLAGS_ABI"
    make -j"$ME_JOBS" LDFLAGS="$LDFLAGS_ABI -avoid-version"
    make install LDFLAGS="$LDFLAGS_ABI -avoid-version"
    rm -f "$PREFIX"/lib/libunibreak.la
}

build_libass() {
    local asm=disabled
    (( ASM_OK )) && [[ "$ABI" != armeabi-v7a ]] && asm=enabled
    meson_build libass $(me_libass_common_flags) -Dcoretext=disabled -Drequire-system-font-provider=false -Dasm=$asm
}

build_dav1d() {
    local asm=true; (( ASM_OK )) || asm=false
    meson_build dav1d $(me_dav1d_common_flags) -Denable_asm=$asm
}

build_lcms2() { meson_build lcms2 $(me_lcms2_common_flags) -Dversionedlibs=false; }

build_libdovi() {
    cd "$ME_WORK/src/libdovi/dolby_vision"
    local upper; upper=$(echo "$RUST" | tr 'a-z-' 'A-Z_')
    export "CARGO_TARGET_${upper}_LINKER=$CC"
    export "CC_${RUST//-/_}=$CC" "AR_${RUST//-/_}=$AR"
    export CARGO_HOME="$ME_WORK/cargo-home"
    # Flags separated by 0x1f, so a folder with a space in its name stays one argument.
    local flags=() f
    for f in $LDFLAGS_ABI; do flags+=("-Clink-arg=$f"); done
    while read -r f; do flags+=("$f"); done < <(me_rust_remap_flags)
    unset RUSTFLAGS
    export CARGO_ENCODED_RUSTFLAGS="$(IFS=$'\x1f'; printf '%s' "${flags[*]}")"
    rustup target list --installed | grep -qx "$RUST" || rustup target add "$RUST"
    cargo cinstall --profile release-deploy --locked --offline --target "$RUST" --library-type cdylib \
        --prefix "$PREFIX" --libdir "$PREFIX/lib" --target-dir "$BUILD/libdovi" -j "$ME_JOBS"
}

build_uchardet() {
    local dir="$BUILD/uchardet"
    rm -rf "$dir"
    cmake -S "$ME_WORK/src/uchardet" -B "$dir" -G Ninja \
        -DCMAKE_TOOLCHAIN_FILE="$NDK/build/cmake/android.toolchain.cmake" \
        -DANDROID_ABI="$ABI" -DANDROID_PLATFORM="android-$API" -DANDROID_STL=c++_shared \
        -DANDROID_SUPPORT_FLEXIBLE_PAGE_SIZES=ON \
        -DCMAKE_BUILD_TYPE=Release -DCMAKE_INSTALL_PREFIX="$PREFIX" -DCMAKE_INSTALL_LIBDIR=lib \
        -DCMAKE_C_FLAGS="$PATH_MAP" -DCMAKE_CXX_FLAGS="$PATH_MAP" \
        -DCMAKE_SHARED_LINKER_FLAGS="$LDFLAGS_ABI" -DCMAKE_POLICY_VERSION_MINIMUM=3.5 \
        -DBUILD_BINARY=OFF -DBUILD_STATIC=OFF -DBUILD_SHARED_LIBS=ON
    cmake --build "$dir" -j "$ME_JOBS"
    cmake --install "$dir"
}

build_libplacebo() {
    meson_build libplacebo $(me_libplacebo_common_flags) -Dvulkan=disabled -Dopengl=enabled -Dglslang=disabled
}

build_ffmpeg() {
    local dir="$BUILD/ffmpeg" asm=()
    rm -rf "$dir"; mkdir -p "$dir"; cd "$dir"
    if (( ASM_OK )); then
        [[ "$ABI" == armeabi-v7a ]] && asm=(--enable-neon --enable-thumb)
        [[ "$ABI" == x86_64 ]] && asm=(--x86asmexe=nasm)
    else
        asm=(--disable-asm)
    fi
    "$ME_WORK/src/ffmpeg/configure" \
        --prefix="$PREFIX" --libdir="$PREFIX/lib" \
        --target-os=android --enable-cross-compile --arch="$FF_ARCH" --cpu="$FF_CPU" \
        --cc="$CC" --cxx="$CXX" --ar="$AR" --nm="$NM" --ranlib="$RANLIB" --strip="$STRIP" \
        --pkg-config=pkg-config --host-cc=cc \
        --extra-cflags="$CFLAGS_ABI -I$PREFIX/include" --extra-ldflags="$LDFLAGS_ABI -L$PREFIX/lib" \
        $(me_ffmpeg_common_flags) \
        --enable-jni --enable-mediacodec \
        ${asm[@]+"${asm[@]}"}
    # av*_configuration() reports this command line: neutral folders only.
    me_neutralize "$NDK" config.h
    make -j"$ME_JOBS"
    make install
    mkdir -p "$META"
    cp config.h "$META/ffmpeg-config.h"
    cp config_components.h "$META/ffmpeg-config_components.h"
}

build_mpv() {
    meson_build mpv $(me_mpv_common_flags) \
        -Daudiotrack=enabled -Daaudio=disabled -Dopensles=disabled \
        -Dgl=enabled -Degl-android=enabled -Dplain-gl=enabled -Dvulkan=disabled \
        -Dandroid-media-ndk=enabled
    mkdir -p "$META"
    cp "$BUILD/mpv/config.h" "$META/mpv-config.h"
}

# ---------------------------------------------------------------------------------------------

ORDER="openssl libiconv fribidi freetype harfbuzz libunibreak libass dav1d lcms2 libdovi uchardet libplacebo ffmpeg mpv"
for name in $ORDER; do
    me_components "$PLATFORM" | grep -qx "$name" || me_die "Component $name is not in sources.lock.json for $PLATFORM."
done
for name in $(me_components "$PLATFORM"); do
    [[ " $ORDER " == *" $name "* ]] || me_die "sources.lock.json names $name for $PLATFORM, but this script does not build it."
done

me_say "Checking sources"
me_fetch_all "$PLATFORM"
mkdir -p "$ME_WORK/src"

# The key of a component's build: its lock entry, its build function, the shared recipe and its
# patches, chained to the previous component's key, so a change rebuilds it and what follows.
component_key() {
    local name="$1" previous="$2"
    {
        echo "$previous"
        python3 -I -c 'import json,sys; print(json.dumps(json.load(open(sys.argv[1]))["components"][sys.argv[2]], sort_keys=True))' "$ME_LOCK" "$name"
        declare -f "build_$name" set_abi write_cross_file meson_build
        declare -f me_path_forms me_path_map_pairs me_path_map_cflags me_rust_remap_flags me_neutralize
        declare -f me_ffmpeg_common_flags me_openssl_common_flags me_mpv_common_flags me_libplacebo_common_flags \
            me_libass_common_flags me_harfbuzz_common_flags me_freetype_common_flags me_fribidi_common_flags \
            me_dav1d_common_flags me_lcms2_common_flags
        cat "$ME_ROOT/patches/$name"/*.patch 2>/dev/null || true
    } | if command -v sha256sum >/dev/null; then sha256sum; else shasum -a 256; fi | cut -d' ' -f1
}

EXTRACTED=" "
prepare_source() {
    local name="$1"
    [[ "$EXTRACTED" == *" $name "* ]] && return
    me_extract "$name"
    [[ "$name" == libdovi ]] && me_vendor_libdovi
    EXTRACTED+="$name "
}

for abi in $ABIS; do
    set_abi "$abi"
    META="$ME_OUT/$PLATFORM/meta/$ABI"
    key=""
    for name in $ORDER; do
        key=$(component_key "$name" "$key")
        stamp="$ME_WORK/stamps/$PLATFORM/$ABI-$name"
        if [[ -f "$stamp" && "$(cat "$stamp")" == "$key" ]]; then
            me_say "$ABI: $name is up to date"
            continue
        fi
        rm -f "$stamp"
        prepare_source "$name"
        me_check_disk 10
        me_say "$ABI: building $name"
        log="$ME_LOGS/$PLATFORM-$ABI-$name.log"
        # Not inside an "if": bash would then ignore errexit within the subshell.
        set +e
        ( set -euo pipefail -x; "build_$name" ) > "$log" 2>&1
        status=$?
        set -e
        if (( status != 0 )); then
            tail -40 "$log" >&2
            me_die "$name failed for $ABI. Full log: $log"
        fi
        echo "$key" > "$stamp"
    done

    # Stage: one stripped library per component, nothing else.
    me_say "$ABI: staging libraries"
    dest="$ME_OUT/$PLATFORM/lib/$ABI"
    rm -rf "$dest"; mkdir -p "$dest"
    for lib in "$PREFIX"/lib/*.so; do
        [[ -L "$lib" ]] && continue
        "$STRIP" --strip-unneeded -o "$dest/$(basename "$lib")" "$lib"
    done
    if [[ ! -d "$ME_OUT/$PLATFORM/include/mpv" ]]; then
        mkdir -p "$ME_OUT/$PLATFORM/include"
        cp -R "$PREFIX/include/mpv" "$ME_OUT/$PLATFORM/include/"
    fi
done

me_write_build_info "$PLATFORM" "$ME_OUT/$PLATFORM/build-info.json"
"$ME_ROOT/collect-notices.sh" "$PLATFORM"
python3 -I "$ME_ROOT/verify.py" "$PLATFORM" --out "$ME_OUT" --work "$ME_WORK" --ndk "$NDK" --abis "$ABIS" --write-manifest
me_say "Done: $ME_OUT/$PLATFORM"
