#!/usr/bin/env bash
# Builds only libass and FFmpeg subtitle/Opus decoding for AVPlayer overlays.
# Run on macOS with Xcode: third_party/apple-subtitles/build-apple.sh [--clean]
# Pinned archives, licences and corresponding source accompany every build.
ME_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$ME_ROOT/lib/common.sh"
ME_LOCK="$ME_ROOT/sources.lock.json"
ME_WORK="${APPLE_SUBTITLE_WORK_DIR:-$ME_ROOT/work}"
ME_OUT="${APPLE_SUBTITLE_OUT_DIR:-$ME_ROOT/out}"
ME_DOWNLOADS="$ME_WORK/downloads"; ME_TOOLS="$ME_WORK/tools"; ME_LOGS="$ME_WORK/logs"
ME_JOBS="${APPLE_SUBTITLE_JOBS:-4}"
export SOURCE_DATE_EPOCH
SOURCE_DATE_EPOCH=$(python3 -I -c 'import json,sys; print(json.load(open(sys.argv[1]))["sourceDateEpoch"])' "$ME_LOCK")

CLEAN=0
while (( $# )); do
    case "$1" in
        --clean) CLEAN=1; shift ;;
        -h|--help) sed -n '2,24p' "$0"; exit 0 ;;
        *) me_die "Unknown argument $1 (see --help)" ;;
    esac
done

PLATFORM=apple
NATIVE="$ME_ROOT/../../apps/native"

# ---------------------------------------------------------------------------------------------
# Fail early: wrong machine, missing or old Xcode, missing tools, deployment targets that no
# longer match the Xcode projects.

[[ "$(uname -s)" == Darwin ]] || me_die "build-apple.sh runs on a Mac."
command -v xcodebuild >/dev/null || me_die "Xcode is not installed (xcodebuild is missing)."
XCODE_VERSION=$(xcodebuild -version 2>/dev/null | sed -n 's/^Xcode //p')
[[ -n "$XCODE_VERSION" ]] || me_die "xcodebuild did not report a version. Run: sudo xcode-select -s /Applications/Xcode.app"
# Xcode's developer folder: the compilers and SDKs live here, and it is mapped to a neutral name.
DEVELOPER_DIR_PATH=$(xcode-select -p)
XCODE_MIN=$(me_lock tool xcode minimum)
python3 -I -c 'import sys; a=[int(x) for x in sys.argv[1].split(".")]; b=[int(x) for x in sys.argv[2].split(".")]; sys.exit(0 if a>=b else 1)' \
    "$XCODE_VERSION" "$XCODE_MIN" || me_die "Xcode $XCODE_VERSION is older than the required $XCODE_MIN."
for tool in xcrun lipo install_name_tool otool codesign plutil nm strip; do
    command -v "$tool" >/dev/null || xcrun -f "$tool" >/dev/null 2>&1 || me_die "$tool is missing (install Xcode's command line tools)."
done
for sdk in iphoneos iphonesimulator appletvos appletvsimulator; do
    xcrun --sdk "$sdk" --show-sdk-path >/dev/null 2>&1 || me_die "The $sdk SDK is missing from Xcode $XCODE_VERSION (Xcode > Settings > Components)."
done

deployment_target() {  # <ios|tvos> -> the highest deployment target in that Xcode project
    local key project
    [[ "$1" == ios ]] && key=IPHONEOS_DEPLOYMENT_TARGET || key=TVOS_DEPLOYMENT_TARGET
    project="$NATIVE/$1/Portico.xcodeproj/project.pbxproj"
    [[ -f "$project" ]] || me_die "$project not found."
    sed -n "s/.*$key = \([0-9.]*\);.*/\1/p" "$project" | sort -t. -k1,1n -k2,2n | tail -1
}
IOS_MIN=$(deployment_target ios)
TVOS_MIN=$(deployment_target tvos)
[[ "$IOS_MIN" == "$(me_lock tool apple-deployment ios)" && "$TVOS_MIN" == "$(me_lock tool apple-deployment tvos)" ]] \
    || me_die "The Xcode projects target iOS $IOS_MIN / tvOS $TVOS_MIN, sources.lock.json says $(me_lock tool apple-deployment ios) / $(me_lock tool apple-deployment tvos). Update the lock (and rebuild)."

me_check_disk 5
(( CLEAN )) && rm -rf "$ME_WORK/build/$PLATFORM" "$ME_WORK/prefix/$PLATFORM" "$ME_WORK/stamps/$PLATFORM" "$ME_WORK/frameworks" "$ME_OUT/$PLATFORM"
mkdir -p "$ME_LOGS" "$ME_WORK/stamps/$PLATFORM"
# No Rust, GPU renderer, TLS or video decoding toolchain is needed.
python3 -m venv "$ME_TOOLS/venv"
"$ME_TOOLS/venv/bin/pip" install --quiet --disable-pip-version-check "meson==$(me_lock tool meson version)" "ninja==$(me_lock tool ninja version)"
export PATH="$ME_TOOLS/venv/bin:$PATH"
    nasm_version=""; nasm_version=$(me_lock tool nasm version)
    if [[ ! -x "$ME_TOOLS/nasm/bin/nasm" ]] || ! "$ME_TOOLS/nasm/bin/nasm" -v | grep -q "version $nasm_version "; then
        me_say "Building nasm $nasm_version"
        me_fetch "$(me_lock tool nasm file)" "$(me_lock tool nasm url)" "$(me_lock tool nasm sha256)"
        rm -rf "$ME_TOOLS/nasm-src"; mkdir -p "$ME_TOOLS/nasm-src"
        tar -xf "$ME_DOWNLOADS/$(me_lock tool nasm file)" -C "$ME_TOOLS/nasm-src" --strip-components=1
        ( cd "$ME_TOOLS/nasm-src" && ./configure --prefix="$ME_TOOLS/nasm" >/dev/null \
            && make -j"$ME_JOBS" nasm ndisasm >/dev/null && mkdir -p "$ME_TOOLS/nasm/bin" \
            && cp nasm ndisasm "$ME_TOOLS/nasm/bin/" ) || me_die "nasm did not build."
        rm -rf "$ME_TOOLS/nasm-src"
    fi
    export PATH="$ME_TOOLS/nasm/bin:$PATH"


command -v pkg-config >/dev/null || me_die "pkg-config is required."

# ---------------------------------------------------------------------------------------------
# Per-target toolchain settings. A target is one slice and one architecture.

TARGETS="ios:arm64 ios-simulator:arm64 ios-simulator:x86_64 tvos:arm64 tvos-simulator:arm64"
SLICES="ios ios-simulator tvos tvos-simulator"

set_target() {
    SLICE="${1%%:*}"; ARCH="${1##*:}"
    case "$SLICE" in
        ios)            SDK=iphoneos;         MIN=$IOS_MIN;  TRIPLE="$ARCH-apple-ios$MIN";            SUBSYSTEM=ios ;;
        ios-simulator)  SDK=iphonesimulator;  MIN=$IOS_MIN;  TRIPLE="$ARCH-apple-ios$MIN-simulator";  SUBSYSTEM=ios-simulator ;;
        tvos)           SDK=appletvos;        MIN=$TVOS_MIN; TRIPLE="$ARCH-apple-tvos$MIN";           SUBSYSTEM=tvos ;;
        tvos-simulator) SDK=appletvsimulator; MIN=$TVOS_MIN; TRIPLE="$ARCH-apple-tvos$MIN-simulator"; SUBSYSTEM=tvos-simulator ;;
    esac
    [[ "$ARCH" == arm64 ]] && { CPU_FAMILY=aarch64; FF_ARCH=aarch64; } \
                           || { CPU_FAMILY=x86_64;  FF_ARCH=x86_64; }
    SYSROOT=$(xcrun --sdk "$SDK" --show-sdk-path)
    TAG="$SLICE-$ARCH"
    PREFIX="$ME_WORK/prefix/$PLATFORM/$TAG"
    BUILD="$ME_WORK/build/$PLATFORM/$TAG"
    mkdir -p "$PREFIX/lib/pkgconfig" "$PREFIX/include" "$PREFIX/Frameworks" "$BUILD"
    export CC="$(xcrun --sdk "$SDK" -f clang)" CXX="$(xcrun --sdk "$SDK" -f clang++)"
    export AR="$(xcrun --sdk "$SDK" -f ar)" RANLIB="$(xcrun --sdk "$SDK" -f ranlib)"
    export NM="$(xcrun --sdk "$SDK" -f nm)" STRIP="$(xcrun --sdk "$SDK" -f strip)"
    export SDKROOT="$SYSROOT"
    export PKG_CONFIG_LIBDIR="$PREFIX/lib/pkgconfig" PKG_CONFIG_PATH="" PKG_CONFIG_SYSROOT_DIR=""
    # Deployment targets for the selected Apple toolchain.
    unset IPHONEOS_DEPLOYMENT_TARGET TVOS_DEPLOYMENT_TARGET
    [[ "$SLICE" == ios* ]] && export IPHONEOS_DEPLOYMENT_TARGET="$MIN" || export TVOS_DEPLOYMENT_TARGET="$MIN"
    # Joined forms keep target and SDK arguments together through configure scripts.
    [[ "$SYSROOT" != *" "* ]] || me_die "The SDK path contains a space: $SYSROOT"
    # No build-machine folders in the libraries (lib/common.sh, "Neutral paths").
    CFLAGS_T="--target=$TRIPLE -isysroot$SYSROOT -fPIC -fstack-protector-strong $(me_path_map_cflags "$DEVELOPER_DIR_PATH")"
    # Room for the install names the frameworks get later; only libraries actually used are linked.
    LDFLAGS_T="--target=$TRIPLE -isysroot$SYSROOT -Wl,-headerpad_max_install_names -Wl,-dead_strip_dylibs"
    write_cross_file
    printf "[binaries]\nc = '%s'\ncpp = '%s'\n[built-in options]\nc_args = ['--target=$(uname -m)-apple-macosx14.0', '-isysroot%s']\nc_link_args = ['--target=$(uname -m)-apple-macosx14.0', '-isysroot%s']\ncpp_args = ['--target=$(uname -m)-apple-macosx14.0', '-isysroot%s']\ncpp_link_args = ['--target=$(uname -m)-apple-macosx14.0', '-isysroot%s']\n" "$(xcrun --sdk macosx -f clang)" "$(xcrun --sdk macosx -f clang++)" "$(xcrun --sdk macosx --show-sdk-path)" "$(xcrun --sdk macosx --show-sdk-path)" "$(xcrun --sdk macosx --show-sdk-path)" "$(xcrun --sdk macosx --show-sdk-path)" > "$BUILD/native.ini"
}

meson_list() { local out="" a; for a in "$@"; do out+="'$a', "; done; printf '[%s]' "${out%, }"; }

write_cross_file() {
    # shellcheck disable=SC2086
    cat > "$BUILD/cross.ini" <<EOF
[binaries]
c = '$CC'
cpp = '$CXX'
objc = '$CC'
objcpp = '$CXX'
ar = '$AR'
nm = '$NM'
strip = '$STRIP'
ranlib = '$RANLIB'
pkg-config = '$(command -v pkg-config)'
nasm = '$(command -v nasm)'

[built-in options]
c_args = $(meson_list $CFLAGS_T "-I$PREFIX/include")
cpp_args = $(meson_list $CFLAGS_T "-I$PREFIX/include")
objc_args = $(meson_list $CFLAGS_T "-I$PREFIX/include")
c_link_args = $(meson_list $LDFLAGS_T "-L$PREFIX/lib")
cpp_link_args = $(meson_list $LDFLAGS_T "-L$PREFIX/lib")
objc_link_args = $(meson_list $LDFLAGS_T "-L$PREFIX/lib")

[properties]
needs_exe_wrapper = true
pkg_config_libdir = '$PREFIX/lib/pkgconfig'

[host_machine]
system = 'darwin'
subsystem = '$SUBSYSTEM'
kernel = 'xnu'
cpu_family = '$CPU_FAMILY'
cpu = '$ARCH'
endian = 'little'
EOF
}

meson_build() {
    local name="$1"; shift
    local dir="$BUILD/$name"
    rm -rf "$dir"
    CC_FOR_BUILD="$(xcrun --sdk macosx -f clang)" CXX_FOR_BUILD="$(xcrun --sdk macosx -f clang++)" meson setup "$dir" "$ME_WORK/src/$name" --cross-file "$BUILD/cross.ini" --native-file "$BUILD/native.ini" \
        --prefix "$PREFIX" --libdir lib --buildtype release --default-library shared \
        --wrap-mode nodownload "$@"
    # Generated configuration headers must not retain build-machine paths.
    [[ -f "$dir/config.h" ]] && me_neutralize "$DEVELOPER_DIR_PATH" "$dir/config.h"
    ninja -C "$dir" -j "$ME_JOBS"
    ninja -C "$dir" install
}

# ---------------------------------------------------------------------------------------------
# Components, in dependency order.

build_fribidi() { meson_build fribidi $(me_fribidi_common_flags); }

build_freetype() { meson_build freetype $(me_freetype_common_flags); }

build_harfbuzz() { meson_build harfbuzz $(me_harfbuzz_common_flags); }

build_libunibreak() {
    cd "$ME_WORK/src/libunibreak"
    make distclean >/dev/null 2>&1 || true
    printf '#!/bin/sh\nexec "%s" "--target=%s" "-isysroot%s" "$@"\n' "$CC" "$TRIPLE" "$SYSROOT" > "$BUILD/cc-unibreak"
    chmod +x "$BUILD/cc-unibreak"
    ./configure --host="${ARCH/arm64/aarch64}-apple-darwin" --prefix="$PREFIX" --libdir="$PREFIX/lib" \
        --enable-shared --disable-static CC="$BUILD/cc-unibreak" CFLAGS="$CFLAGS_T -O2" LDFLAGS="$LDFLAGS_T"
    make -j"$ME_JOBS"
    make install
    rm -f "$PREFIX"/lib/libunibreak.la
}

build_libass() {
    meson_build libass $(me_libass_common_flags) -Dcoretext=enabled -Drequire-system-font-provider=true -Dasm=enabled
}

build_ffmpeg() {
    local dir="$BUILD/ffmpeg" asm=()
    rm -rf "$dir"; mkdir -p "$dir"; cd "$dir"
    [[ "$ARCH" == x86_64 ]] && asm=(--x86asmexe=nasm)
    "$ME_WORK/src/ffmpeg/configure" \
        --prefix="$PREFIX" --libdir="$PREFIX/lib" \
        --target-os=darwin --enable-cross-compile --arch="$FF_ARCH" \
        --cc="$CC" --cxx="$CXX" --ar="$AR" --nm="$NM" --ranlib="$RANLIB" --strip="$STRIP" \
        --pkg-config=pkg-config --host-cc="$(xcrun --sdk macosx -f clang)" \
        --host-cflags="-isysroot$(xcrun --sdk macosx --show-sdk-path)" --host-ldflags="-isysroot$(xcrun --sdk macosx --show-sdk-path)" \
        --extra-cflags="$CFLAGS_T -I$PREFIX/include" --extra-ldflags="$LDFLAGS_T -L$PREFIX/lib" \
        --install-name-dir='@rpath' \
        --disable-autodetect --disable-everything --disable-programs --disable-doc --disable-network \
        --enable-gpl --enable-version3 --enable-shared --disable-static --disable-avdevice --disable-avfilter --disable-swscale \
        --enable-avformat --enable-avcodec --enable-avutil --enable-swresample \
        --enable-decoder=pgssub,dvdsub,dvbsub,opus --enable-demuxer=sup,vobsub,matroska,ogg \
        --enable-parser=dvdsub,dvbsub,opus --enable-protocol=file --disable-videotoolbox --disable-audiotoolbox --disable-asm \
        ${asm[@]+"${asm[@]}"}
    # av*_configuration() reports this command line: neutral folders only.
    me_neutralize "$DEVELOPER_DIR_PATH" config.h
    make -j"$ME_JOBS"
    make install
    mkdir -p "$ME_OUT/$PLATFORM/meta/$TAG"
    cp config.h "$ME_OUT/$PLATFORM/meta/$TAG/ffmpeg-config.h"
    cp config_components.h "$ME_OUT/$PLATFORM/meta/$TAG/ffmpeg-config_components.h"
    cp "$ME_WORK/src/ffmpeg/portico-source-receipt.json" "$ME_OUT/$PLATFORM/meta/$TAG/"
}

build_core() {
    "$CC" $CFLAGS_T -O2 -dynamiclib "$ME_ROOT/core/PorticoSubtitleCore.c" -I"$PREFIX/include" \
      -L"$PREFIX/lib" -lass -lavformat -lavcodec -lavutil -o "$PREFIX/lib/libporticosubtitle.dylib" \
      $LDFLAGS_T -Wl,-install_name,@rpath/libporticosubtitle.dylib
}
ORDER="fribidi freetype harfbuzz libunibreak libass ffmpeg"
for name in $ORDER; do
    me_components "$PLATFORM" | grep -qx "$name" || me_die "Component $name is not in sources.lock.json for $PLATFORM."
done
for name in $(me_components "$PLATFORM"); do
    [[ " $ORDER " == *" $name "* ]] || me_die "sources.lock.json names $name for $PLATFORM, but this script does not build it."
done

me_say "Checking sources"
for name in $ORDER; do me_fetch "$(me_lock "$name" file)" "$(me_lock "$name" url)" "$(me_lock "$name" sha256)"; done
mkdir -p "$ME_WORK/src"

component_key() {
    local name="$1" previous="$2"
    {
        echo "$previous"; echo "$XCODE_VERSION"
        python3 -I -c 'import json,sys; print(json.dumps(json.load(open(sys.argv[1]))["components"][sys.argv[2]], sort_keys=True))' "$ME_LOCK" "$name"
        declare -f "build_$name" set_target write_cross_file meson_build
        declare -f me_path_forms me_path_map_pairs me_path_map_cflags me_neutralize
        declare -f me_libass_common_flags me_harfbuzz_common_flags me_freetype_common_flags me_fribidi_common_flags
        if [[ "$name" == ffmpeg ]]; then
            cat "$ME_ROOT/jellyfin-ffmpeg.source.lock.json" "$ME_ROOT/prepare-ffmpeg-source.py"
        fi
    } | shasum -a 256 | cut -d' ' -f1
}

EXTRACTED=" "
prepare_source() {
    local name="$1"
    [[ "$EXTRACTED" == *" $name "* ]] && return
    if [[ "$name" == ffmpeg ]]; then
        rm -rf "$ME_WORK/src/ffmpeg"
        # The checked preparer verifies the archive and every one of the 100 patches,
        # applies them in exact order with --fuzz=0 and writes the source receipt.
        python3 -I "$ME_ROOT/prepare-ffmpeg-source.py" "$ME_WORK/src/ffmpeg" \
            --lock "$ME_ROOT/jellyfin-ffmpeg.source.lock.json" \
            --archive "$ME_DOWNLOADS/$(me_lock ffmpeg file)"
    else
        me_extract "$name"
    fi
    EXTRACTED+="$name "
}

for target in $TARGETS; do
    set_target "$target"
    key=""
    for name in $ORDER; do
        key=$(component_key "$name" "$key")
        stamp="$ME_WORK/stamps/$PLATFORM/$TAG-$name"
        if [[ -f "$stamp" && "$(cat "$stamp")" == "$key" ]]; then
            me_say "$TAG: $name is up to date"
            continue
        fi
        rm -f "$stamp"
        prepare_source "$name"
        me_check_disk 10
        me_say "$TAG: building $name"
        log="$ME_LOGS/$PLATFORM-$TAG-$name.log"
        set +e
        ( set -euo pipefail -x; "build_$name" ) > "$log" 2>&1
        status=$?
        set -e
        if (( status != 0 )); then
            tail -40 "$log" >&2
            me_die "$name failed for $TAG. Full log: $log"
        fi
        echo "$key" > "$stamp"
    done
    build_core
done

# ---------------------------------------------------------------------------------------------
# Frameworks.

me_write_build_info "$PLATFORM" "$ME_OUT/$PLATFORM/build-info.json"
python3 "$ME_ROOT/notices.py" "$ME_LOCK" "$ME_DOWNLOADS" "$ME_OUT/$PLATFORM"

# library stem -> component (for the version and the notice)
STEMS="avcodec:ffmpeg avformat:ffmpeg avutil:ffmpeg swresample:ffmpeg porticosubtitle:ffmpeg ass:libass freetype:freetype harfbuzz:harfbuzz fribidi:fribidi unibreak:libunibreak"
REVISION=$(python3 -I -c 'import json,sys; print(json.load(open(sys.argv[1]))["revision"])' "$ME_LOCK")
FW="$ME_WORK/frameworks"
XC="$ME_OUT/$PLATFORM/xcframeworks"
rm -rf "$FW" "$XC"; mkdir -p "$FW" "$XC"

# The real (not symlinked) dylib of a library in a prefix.
dylib_of() {  # <prefix> <stem>
    local f
    for f in "$1/lib/lib$2".*dylib "$1/lib/lib$2.dylib"; do
        [[ -f "$f" && ! -L "$f" ]] && { echo "$f"; return; }
    done
    me_die "lib$2 was not built in $1"
}

stem_of_install_name() {  # an install name of one of our libraries -> its stem, or nothing
    local base; base=$(basename "$1")
    base=${base#lib}; base=${base%%.*}
    for pair in $STEMS; do [[ "${pair%%:*}" == "$base" ]] && { echo "$base"; return; }; done
    return 0
}

platform_names() {  # <slice> -> CFBundleSupportedPlatforms value and UIDeviceFamily
    case "$1" in
        ios) echo "iPhoneOS 1,2" ;; ios-simulator) echo "iPhoneSimulator 1,2" ;;
        tvos) echo "AppleTVOS 3" ;; tvos-simulator) echo "AppleTVSimulator 3" ;;
    esac
}

write_info_plist() {  # <framework dir> <name> <version> <slice>
    local fw="$1" name="$2" version="$3" slice="$4" min plat family
    read -r plat family <<< "$(platform_names "$slice")"
    [[ "$slice" == ios* ]] && min=$IOS_MIN || min=$TVOS_MIN
    python3 -I - "$fw/Info.plist" "$name" "$version" "$REVISION" "$plat" "$family" "$min" <<'EOF'
import plistlib, sys, re
path, name, version, revision, plat, family, minimum = sys.argv[1:]
short = ".".join((re.findall(r"\d+", version) + ["0", "0"])[:3])
plistlib.dump({
    "CFBundleDevelopmentRegion": "en",
    "CFBundleExecutable": name,
    "CFBundleIdentifier": "tv.getportico.apple-subtitles." + name.lower(),
    "CFBundleInfoDictionaryVersion": "6.0",
    "CFBundleName": name,
    "CFBundlePackageType": "FMWK",
    "CFBundleShortVersionString": short,
    "CFBundleVersion": revision,
    "CFBundleSupportedPlatforms": [plat],
    "MinimumOSVersion": minimum,
    "UIDeviceFamily": [int(x) for x in family.split(",")],
}, open(path, "wb"))
EOF
}

# The privacy manifest Apple requires of SDKs that use "required reason" APIs. The binary's
# imported symbols decide what is declared; an API with no reason that fits these libraries
# stops the build so a person decides, rather than a reason being guessed.
write_privacy_manifest() {  # <framework dir> <binary>
    local fw="$1" binary="$2"
    xcrun nm -u -arch all "$binary" 2>/dev/null | sed 's/^ *//' | sort -u > "$ME_WORK/imports.txt"
    python3 -I - "$ME_WORK/imports.txt" "$fw/PrivacyInfo.xcprivacy" "$binary" <<'EOF'
import plistlib, sys, re
imports = set(open(sys.argv[1]).read().split())
categories = {
    # category: (symbols, reason code, why it fits)
    "NSPrivacyAccessedAPICategoryFileTimestamp": (
        r"^_(stat|fstat|fstatat|lstat|getattrlist|getattrlistbulk|fgetattrlist|getattrlistat)(\$INODE64)?$",
        "C617.1"),   # metadata of files inside the app's container (downloads, subtitles, fonts)
    "NSPrivacyAccessedAPICategorySystemBootTime": (r"^_mach_absolute_time$", "35F9.1"),  # elapsed time for playback timing
}
undecided = [s for s in imports if re.match(r"^_(statfs|statvfs|fstatfs|fstatvfs)(\$INODE64)?$", s)]
if undecided:
    sys.exit(f"{sys.argv[3]} uses disk-space APIs ({', '.join(sorted(undecided))}); decide the reason and add it to build-apple.sh.")
accessed = []
for category, (pattern, reason) in categories.items():
    if any(re.match(pattern, s) for s in imports):
        accessed.append({"NSPrivacyAccessedAPIType": category, "NSPrivacyAccessedAPITypeReasons": [reason]})
plistlib.dump({
    "NSPrivacyTracking": False,
    "NSPrivacyTrackingDomains": [],
    "NSPrivacyCollectedDataTypes": [],
    "NSPrivacyAccessedAPITypes": accessed,
}, open(sys.argv[2], "wb"))
EOF
}

notice_for() {  # <framework dir> <component>
    local dest="$1/NOTICE.txt" f
    {
        echo "$2 $(me_lock "$2" version) — $(me_lock "$2" licence)"
        echo "Source: $(me_lock "$2" url)"
        echo "All third-party notices for Portico Apple subtitles: THIRD-PARTY-NOTICES.txt in Libporticosubtitle.framework."
        echo
        find "$ME_OUT/$PLATFORM/notices/$2" -type f | sort | while read -r f; do
            echo "--- ${f#"$ME_OUT/$PLATFORM/notices/"} ---"; cat "$f"; echo
        done
    } > "$dest"
}

for slice in $SLICES; do
    for pair in $STEMS; do
        stem=${pair%%:*}; component=${pair##*:}
        name="Lib$stem"
        fw="$FW/$slice/$name.framework"
        rm -rf "$fw"; mkdir -p "$fw"
        inputs=()
        for target in $TARGETS; do
            [[ "${target%%:*}" == "$slice" ]] || continue
            inputs+=("$(dylib_of "$ME_WORK/prefix/$PLATFORM/$slice-${target##*:}" "$stem")")
        done
        lipo -create "${inputs[@]}" -output "$fw/$name"
        # Its own name, and every reference to another of our libraries, as @rpath frameworks.
        install_name_tool -id "@rpath/$name.framework/$name" "$fw/$name"
        otool -L "$fw/$name" | tail -n +2 | awk '{print $1}' | while read -r dep; do
            other=$(stem_of_install_name "$dep")
            [[ -n "$other" && "$other" != "$stem" ]] && install_name_tool -change "$dep" "@rpath/Lib$other.framework/Lib$other" "$fw/$name"
            true
        done
        # No build-machine paths stay behind.
        otool -l "$fw/$name" | awk '/cmd LC_RPATH/ {getline; getline; print $2}' | sort -u | while read -r rp; do
            install_name_tool -delete_rpath "$rp" "$fw/$name"
        done
        links=$(otool -L "$fw/$name" | awk '/^[[:space:]]/')
        [[ "$links" != *"$ME_WORK"* ]] || me_die "$name still refers to the build directory: $links"
        # -S as well: the debug map's object-file paths (N_OSO) name the build folders.
        strip -x -S "$fw/$name"
        if [[ "$stem" == porticosubtitle ]]; then
            mkdir -p "$fw/Headers" "$fw/Modules"
            cp "$ME_ROOT/core/PorticoSubtitleCore.h" "$fw/Headers/"
            printf 'framework module Libporticosubtitle { header "PorticoSubtitleCore.h" export * }\n' > "$fw/Modules/module.modulemap"
            cp "$ME_OUT/$PLATFORM/notices/THIRD-PARTY-NOTICES.txt" "$fw/"
        fi
        write_info_plist "$fw" "$name" "$(me_lock "$component" version)" "$slice"
        write_privacy_manifest "$fw" "$fw/$name"
        notice_for "$fw" "$component"
        codesign --force --sign - --timestamp=none "$fw/$name" >/dev/null
    done

done

for pair in $STEMS; do
    stem=${pair%%:*}
    name="Lib$stem"
    args=()
    for slice in $SLICES; do args+=(-framework "$FW/$slice/$name.framework"); done
    xcodebuild -create-xcframework "${args[@]}" -output "$XC/$name.xcframework" >/dev/null \
        || me_die "Could not create $name.xcframework."
done

# Where the local Swift package (apple/Package.swift) finds them.
rm -rf "$ME_ROOT/apple/Frameworks"; mkdir -p "$ME_ROOT/apple/Frameworks"
cp -R "$XC"/*.xcframework "$ME_ROOT/apple/Frameworks/"

python3 -I "$ME_ROOT/verify.py" "$ME_OUT/$PLATFORM"
me_say "Done. Open the Xcode workspaces; the PorticoSubtitleEngine package now resolves from apple/Frameworks."
