# Android media-engine recipe helpers. Sourced by build-android.sh, never run.

set -euo pipefail

ME_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ME_LOCK="$ME_ROOT/sources.lock.json"
ME_WORK="${MEDIA_ENGINE_WORK_DIR:-$ME_ROOT/work}"
ME_OUT="${MEDIA_ENGINE_OUT_DIR:-$ME_ROOT/out}"
ME_JOBS="${MEDIA_ENGINE_JOBS:-8}"
ME_DOWNLOADS="$ME_WORK/downloads"
ME_TOOLS="$ME_WORK/tools"
ME_LOGS="$ME_WORK/logs"

me_say() { printf '\033[1m[media-engine]\033[0m %s\n' "$*" >&2; }
me_die() { printf '\033[1;31m[media-engine] error:\033[0m %s\n' "$*" >&2; exit 1; }

# ---------------------------------------------------------------------------------------------
# Neutral paths. A release library must not carry the build machine's folders (the work
# directory, the person's home folder, where the toolchain is installed): they say nothing useful
# to anyone else and make two builds of the same sources differ. Three things put them there:
#
#   1. File names the compiler writes into the code (__FILE__ in asserts, debug info). Every C,
#      C++ and Objective-C compile gets -ffile-prefix-map, -fdebug-prefix-map and
#      -fmacro-prefix-map (me_path_map_cflags); Rust gets --remap-path-prefix (me_rust_remap_flags).
#   2. Configuration strings a library reports about itself (FFmpeg's av*_configuration(), mpv's
#      "Configuration:" line and config folder, OpenSSL's "compiler:" line and module folders).
#      These are rewritten in the generated header before anything is compiled (me_neutralize).
#   3. Run paths: there are none in the Android libraries.
#
# verify.py fails a build in which any library still contains the work directory, the home
# folder, or a /home/ or /Users/ path.
ME_NEUTRAL_WORK=/portico-media-engine
ME_NEUTRAL_TOOLCHAIN=/toolchain

# A folder as the compiler may see it: as given, and with symbolic links resolved.
me_path_forms() {
    local real
    printf '%s\n' "$1"
    real=$(cd "$1" 2>/dev/null && pwd -P) || return 0
    [[ "$real" == "$1" ]] || printf '%s\n' "$real"
}

# "from=to" pairs for the compilers: the work directory and the toolchain folder (the NDK, or
# another configured toolchain folder). They never contain each other, so the order does not matter.
me_path_map_pairs() {  # <toolchain dir>
    local form
    mkdir -p "$ME_WORK"
    while read -r form; do printf '%s=%s\n' "$form" "$ME_NEUTRAL_WORK"; done < <(me_path_forms "$ME_WORK")
    [[ -n "${1:-}" ]] && while read -r form; do printf '%s=%s\n' "$form" "$ME_NEUTRAL_TOOLCHAIN"; done < <(me_path_forms "$1")
    return 0
}

me_path_map_cflags() {  # <toolchain dir> -> flags for every C/C++/Objective-C compile
    local pair out=""
    while read -r pair; do
        # The flags travel through word-split variables (CFLAGS, configure lines): a folder with a
        # space cannot be named this way. verify.py then reports any such path that is left.
        [[ "$pair" == *[[:space:]]* ]] && { me_say "Not mapping a folder with a space in its name: ${pair%%=*}"; continue; }
        out+="-ffile-prefix-map=$pair -fdebug-prefix-map=$pair -fmacro-prefix-map=$pair "
    done < <(me_path_map_pairs "$1")
    printf '%s' "${out% }"
}

# rustc applies the last matching remapping, so the work directory (which may sit inside the home
# folder) comes after the home folder's cargo and rustup directories.
me_rust_remap_flags() {  # -> one flag per line
    local home="${HOME:-}" form
    [[ -n "$home" ]] && printf '%s\n' "--remap-path-prefix=$home/.cargo=/cargo" "--remap-path-prefix=$home/.rustup=/rustup"
    while read -r form; do printf '%s\n' "--remap-path-prefix=$form=$ME_NEUTRAL_WORK"; done < <(me_path_forms "$ME_WORK")
}

# Rewrites generated files (configuration headers) so they name neutral folders instead of this
# machine's: the work directory, the recipe folder, the toolchain folder, then the home folder.
me_neutralize() {  # <toolchain dir> <file>...
    local toolchain="$1"; shift
    python3 -I - "$ME_WORK" "$ME_ROOT" "$toolchain" "${HOME:-}" "$ME_NEUTRAL_WORK" "$ME_NEUTRAL_TOOLCHAIN" "$@" <<'EOF'
import os, sys
work, root, toolchain, home, neutral_work, neutral_toolchain, *files = sys.argv[1:]
pairs = []
def add(path, to):
    if not path:
        return
    for form in {path, os.path.realpath(path)}:
        pairs.append((form.rstrip("/"), to))
add(work, neutral_work)
add(root, "/portico-media-engine-recipe")
add(toolchain, neutral_toolchain)
add(home, "~")
pairs.sort(key=lambda p: len(p[0]), reverse=True)  # the longest (most specific) folder first
import re
def neutral(text):
    for old, new in pairs:
        if old:
            text = text.replace(old, new)
    return text
def char_array(match):
    # OpenSSL's buildinf.h spells its "compiler:" line as an array of single characters.
    chars = re.findall(r"'(\\.|[^\\'])'", match.group(2))
    flags = "".join(c[1] if c.startswith("\\") else c for c in chars if c != "\\0")
    body = "".join(("\n    " if i % 16 == 0 else "") + "'" + ("\\" + c if c in "\\'" else c) + "'," for i, c in enumerate(neutral(flags)))
    return match.group(1) + body + "\n'\\0'\n" + match.group(3)
for name in files:
    with open(name, encoding="utf-8", errors="surrogateescape") as f:
        text = f.read()
    text = re.sub(r"(compiler_flags\[\] = \{)(.*?)(\};)", char_array, text, flags=re.S)
    text = neutral(text)
    with open(name, "w", encoding="utf-8", errors="surrogateescape") as f:
        f.write(text)
EOF
}

# Free space check: the builds are large; stop early rather than half way through.
me_check_disk() {
    local need_gb="${1:-20}" free_kb
    mkdir -p "$ME_WORK"
    free_kb=$(df -Pk "$ME_WORK" | awk 'NR==2 {print $4}')
    (( free_kb / 1048576 >= need_gb )) || me_die "Less than ${need_gb} GB free under $ME_WORK."
}

# lock <component> <field> -> prints the value from sources.lock.json (empty when absent)
me_lock() {
    python3 -I - "$ME_LOCK" "$@" <<'EOF'
import json, sys
data = json.load(open(sys.argv[1]))
node = data["tools"] if sys.argv[2] == "tool" else data["components"]
keys = sys.argv[3:] if sys.argv[2] == "tool" else sys.argv[2:]
for key in keys:
    node = node.get(key, {}) if isinstance(node, dict) else {}
print(node if isinstance(node, str) else ("" if node == {} else json.dumps(node)))
EOF
}

# Components of a platform, in lock order.
me_components() {
    python3 -I - "$ME_LOCK" "$1" <<'EOF'
import json, sys
for name, entry in json.load(open(sys.argv[1]))["components"].items():
    if sys.argv[2] in entry["platforms"]:
        print(name)
EOF
}

me_sha256() {
    if command -v sha256sum >/dev/null; then sha256sum "$1" | cut -d' ' -f1; else shasum -a 256 "$1" | cut -d' ' -f1; fi
}

# fetch <file> <url> <sha256>: download once into the downloads folder, then always verify.
me_fetch() {
    local file="$1" url="$2" sum="$3" path="$ME_DOWNLOADS/$1"
    mkdir -p "$ME_DOWNLOADS"
    if [[ ! -f "$path" ]]; then
        me_say "Downloading $file"
        curl -fL --retry 3 --connect-timeout 30 -o "$path.part" "$url" || { rm -f "$path.part"; me_die "Could not download $url"; }
        mv "$path.part" "$path"
    fi
    local actual; actual=$(me_sha256 "$path")
    [[ "$actual" == "$sum" ]] || me_die "$file does not match its pinned SHA-256 (expected $sum, got $actual). Delete it to download again."
}

# Fetches and verifies every source (and sub-source) a platform needs, before anything is built.
me_fetch_all() {
    local platform="$1" name
    python3 -I - "$ME_LOCK" "$platform" <<'EOF' > "$ME_WORK/fetch-list.txt"
import json, sys
data = json.load(open(sys.argv[1]))
for name, entry in data["components"].items():
    if sys.argv[2] not in entry["platforms"]:
        continue
    print(entry["file"], entry["url"], entry["sha256"])
    for sub in entry.get("subsources", {}).values():
        print(sub["file"], sub["url"], sub["sha256"])
EOF
    while read -r file url sum; do me_fetch "$file" "$url" "$sum"; done < "$ME_WORK/fetch-list.txt"
}

# extract <component> -> fresh source tree at $ME_WORK/src/<component>, patches applied.
me_extract() {
    local name="$1" file dest
    file=$(me_lock "$name" file)
    dest="$ME_WORK/src/$name"
    rm -rf "$dest"; mkdir -p "$dest"
    tar -xf "$ME_DOWNLOADS/$file" -C "$dest" --strip-components=1
    # Sub-sources (git submodules upstream) go where the upstream tree expects them.
    python3 -I - "$ME_LOCK" "$name" <<'EOF' > "$ME_WORK/sub-list.txt"
import json, sys
entry = json.load(open(sys.argv[1]))["components"][sys.argv[2]]
for path, sub in entry.get("subsources", {}).items():
    print(path, sub["file"])
EOF
    local path subfile
    while read -r path subfile; do
        rm -rf "${dest:?}/$path"; mkdir -p "$dest/$path"
        tar -xf "$ME_DOWNLOADS/$subfile" -C "$dest/$path" --strip-components=1
    done < "$ME_WORK/sub-list.txt"
    local patch
    for patch in "$ME_ROOT/patches/$name"/*.patch; do
        [[ -f "$patch" ]] || continue
        me_say "Patching $name: $(basename "$patch")"
        patch -d "$dest" -p1 --forward --quiet < "$patch" || me_die "Patch $(basename "$patch") does not apply to $name."
    done
}

# libdovi's Rust dependencies, exactly as its Cargo.lock pins them (cargo checks each crate's
# checksum), kept beside the source so the build itself runs offline. A source bundle carries
# them as downloads/libdovi-vendored-crates.tar, which is used instead of the network.
me_vendor_libdovi() {
    local src="$ME_WORK/src/libdovi"
    if [[ -f "$ME_DOWNLOADS/libdovi-vendored-crates.tar" ]]; then
        tar -xf "$ME_DOWNLOADS/libdovi-vendored-crates.tar" -C "$src"
    else
        ( cd "$src/dolby_vision" && CARGO_HOME="$ME_WORK/cargo-home" \
            cargo vendor --locked --versioned-dirs --quiet "$src/vendor" > /dev/null ) \
            || me_die "Could not fetch libdovi's pinned Rust dependencies."
    fi
    mkdir -p "$src/dolby_vision/.cargo"
    cat > "$src/dolby_vision/.cargo/config.toml" <<EOF
[source.crates-io]
replace-with = "vendored-sources"

[source.vendored-sources]
directory = "$src/vendor"
EOF
}

# Tools pinned in the lock, installed under the work directory: meson, ninja and cmake (pip, in a
# venv), nasm (from its pinned source), cargo-c (cargo install --locked). Rust itself comes from
# rustup and must report the pinned version.
me_tools() {
    local venv="$ME_TOOLS/venv"
    local meson ninja cmake
    meson=$(me_lock tool meson version); ninja=$(me_lock tool ninja version); cmake=$(me_lock tool cmake version)
    if [[ ! -x "$venv/bin/meson" ]] || [[ "$("$venv/bin/meson" --version)" != "$meson" ]] \
        || [[ "$("$venv/bin/cmake" --version 2>/dev/null | head -1)" != "cmake version $cmake" ]] \
        || ! "$venv/bin/ninja" --version 2>/dev/null | grep -q "^$ninja"; then
        me_say "Installing meson $meson, ninja $ninja, cmake $cmake"
        python3 -m venv "$venv"
        "$venv/bin/pip" install --quiet --disable-pip-version-check "meson==$meson" "ninja==$ninja" "cmake==$cmake"
    fi
    export PATH="$venv/bin:$PATH"

    local nasm_version; nasm_version=$(me_lock tool nasm version)
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

    local rust_version; rust_version=$(me_lock tool rust version)
    command -v rustc >/dev/null || me_die "Rust is not installed. Install rustup, then: rustup toolchain install $rust_version"
    if [[ "$(rustc --version | cut -d' ' -f2)" != "$rust_version" ]]; then
        if rustup run "$rust_version" rustc --version >/dev/null 2>&1; then
            export RUSTUP_TOOLCHAIN="$rust_version"
        else
            me_die "rustc $(rustc --version | cut -d' ' -f2) is not the pinned $rust_version. Run: rustup toolchain install $rust_version"
        fi
    fi
    local cargo_c; cargo_c=$(me_lock tool cargo-c version)
    if ! "$ME_TOOLS/cargo-c/bin/cargo-cbuild" --version 2>/dev/null | grep -q "^cargo-c $cargo_c"; then
        me_say "Installing cargo-c $cargo_c (a few minutes, once)"
        cargo install --quiet --locked --root "$ME_TOOLS/cargo-c" cargo-c --version "$cargo_c" -j "$ME_JOBS" \
            || me_die "cargo-c did not install."
    fi
    export PATH="$ME_TOOLS/cargo-c/bin:$PATH"
    command -v pkg-config >/dev/null || me_die "pkg-config is missing (Ubuntu: apt install pkgconf; Mac: brew install pkgconf)."
    command -v perl >/dev/null || me_die "perl is missing (OpenSSL's build needs it)."
}

# ---------------------------------------------------------------------------------------------
# The shared recipe.

# FFmpeg: LGPL 2.1 (no gpl, version3 or nonfree), shared libraries, a player's subset: every
# decoder, demuxer, parser and bitstream filter; no encoders, muxers or devices; network
# protocols for HTTP(S) and HLS; TLS through OpenSSL. Nothing is autodetected: each external
# library is named, so a stray library on the build machine can never end up linked.
# Filters: only those mpv inserts by itself (deinterlacing, rotation, flips) and the plumbing
# around them; the engines set no user filters. (All filters would add about 3.5 MB.)
me_ffmpeg_common_flags() {
    printf '%s\n' \
        --disable-gpl --disable-version3 --disable-nonfree \
        --disable-autodetect \
        --enable-shared --disable-static --enable-pic \
        --disable-programs --disable-doc --disable-debug \
        --disable-avdevice --disable-devices \
        --disable-encoders --disable-muxers \
        --enable-decoders --enable-demuxers --enable-parsers --enable-bsfs \
        --disable-filters \
        --enable-filter=bwdif,yadif,w3fdif,rotate,transpose,hflip,vflip,crop,pad,format,scale,null,anull,aformat,aresample,setpts,asetpts,fps,hwdownload,hwupload,hwmap \
        --enable-swresample --enable-swscale --enable-avfilter \
        --enable-network \
        --disable-protocols \
        --enable-protocol=file,http,https,tcp,tls,crypto,data,async,cache,subfile,httpproxy \
        --enable-openssl \
        --enable-libdav1d \
        --enable-zlib \
        --enable-pthreads \
        --disable-iconv --disable-bzlib --disable-lzma --disable-libxml2 --disable-sdl2 \
        --disable-xlib --disable-libxcb --disable-vulkan --disable-securetransport \
        --enable-hwaccels \
        --enable-optimizations --enable-runtime-cpudetect
}

# OpenSSL: a shared libcrypto and libssl for a TLS client. No engines or providers loaded from
# disk, no legacy provider, no tests, docs or programs, and none of the algorithms and protocols
# an HTTPS client never negotiates (national and obsolete ciphers, SRP, SCTP, QUIC,
# CMS, timestamping, certificate transparency, SLH-DSA). TLS 1.2 and 1.3 with every mainstream
# cipher suite, including the hybrid post-quantum key exchange, stay.
me_openssl_common_flags() {
    printf '%s\n' shared no-tests no-docs no-apps no-module no-engine no-dynamic-engine no-legacy \
        no-comp no-zlib no-ssl3 no-weak-ssl-ciphers no-ui-console \
        no-idea no-camellia no-seed no-aria no-sm2 no-sm3 no-sm4 no-whirlpool no-mdc2 no-rc2 no-rc4 \
        no-rc5 no-md4 no-bf no-cast no-srp no-sctp no-quic no-ct no-cms no-ts no-slh-dsa
}

me_mpv_common_flags() {
    printf '%s\n' \
        -Dgpl=false -Dlibmpv=true -Dcplayer=false -Dtests=false -Dfuzzers=false \
        -Dbuild-date=false -Dmanpage-build=disabled -Dhtml-build=disabled -Dpdf-build=disabled \
        -Dlua=disabled -Djavascript=disabled -Dcplugins=disabled \
        -Dlibarchive=disabled -Dlibbluray=disabled -Ddvdnav=disabled -Dcdda=disabled -Ddvbin=disabled \
        -Drubberband=disabled -Dvapoursynth=disabled -Dzimg=disabled -Djpeg=disabled -Dsdl2-audio=disabled \
        -Dsdl2-video=disabled -Dsdl2-gamepad=disabled -Dlibavdevice=disabled -Dshaderc=disabled \
        -Dspirv-cross=disabled -Dsixel=disabled -Dcaca=disabled -Dopenal=disabled -Djack=disabled \
        -Dpipewire=disabled -Dpulse=disabled -Dalsa=disabled -Dsndio=disabled -Doss-audio=disabled \
        -Dx11=disabled -Dwayland=disabled -Ddrm=disabled -Dgbm=disabled -Dvaapi=disabled -Dvdpau=disabled \
        -Dcuda-hwaccel=disabled -Dcuda-interop=disabled \
        -Dlcms2=enabled -Duchardet=enabled -Diconv=enabled -Dzlib=enabled
}

me_libplacebo_common_flags() {
    printf '%s\n' -Ddemos=false -Dtests=false -Dbench=false -Dfuzz=false -Dd3d11=disabled \
        -Dlcms=enabled -Ddovi=enabled -Dlibdovi=enabled -Dxxhash=disabled -Dunwind=disabled \
        -Dshaderc=disabled
}

me_libass_common_flags() {
    printf '%s\n' -Dtest=disabled -Dcompare=disabled -Dprofile=disabled -Dfuzz=disabled \
        -Dcheckasm=disabled -Dfontconfig=disabled -Ddirectwrite=disabled -Dlibunibreak=enabled
}

me_harfbuzz_common_flags() {
    printf '%s\n' -Dglib=disabled -Dgobject=disabled -Dcairo=disabled -Dchafa=disabled -Dicu=disabled \
        -Dgraphite2=disabled -Dfreetype=disabled -Dfontations=disabled -Dtests=disabled \
        -Dintrospection=disabled -Ddocs=disabled -Dutilities=disabled -Dbenchmark=disabled \
        -Dsubset=disabled -Draster=disabled -Dvector=disabled -Dgpu=disabled -Dgpu_demo=disabled \
        -Dwasm=disabled -Dpng=disabled -Dzlib=disabled -Dcoretext=disabled -Dwith_libstdcxx=false
}

me_freetype_common_flags() {
    printf '%s\n' -Dzlib=system -Dbzip2=disabled -Dpng=disabled -Dharfbuzz=disabled \
        -Dbrotli=disabled -Derror_strings=false -Dtests=disabled
}

me_fribidi_common_flags() {
    printf '%s\n' -Ddocs=false -Dbin=false -Dtests=false
}

me_dav1d_common_flags() {
    printf '%s\n' -Denable_tools=false -Denable_tests=false -Denable_examples=false -Denable_docs=false
}

me_lcms2_common_flags() {
    printf '%s\n' -Dtests=disabled -Dutils=false -Djpeg=disabled -Dtiff=disabled -Dfastfloat=false -Dthreaded=false
}

# Writes a short record of how a platform was built: versions from the lock, tool versions seen.
me_write_build_info() {
    local platform="$1" dest="$2"
    python3 -I - "$ME_LOCK" "$platform" "$dest" <<'EOF'
import json, sys, subprocess, platform as host, datetime
lock = json.load(open(sys.argv[1]))
def run(*cmd):
    try:
        return subprocess.run(cmd, capture_output=True, text=True, check=True).stdout.splitlines()[0].strip()
    except Exception:
        return None
info = {
    "platform": sys.argv[2],
    "lockRevision": lock["revision"],
    "components": {k: v["version"] for k, v in lock["components"].items() if sys.argv[2] in v["platforms"]},
    "toolsSeen": {
        "meson": run("meson", "--version"), "ninja": run("ninja", "--version"),
        "cmake": run("cmake", "--version"), "nasm": run("nasm", "-v"),
        "rustc": run("rustc", "--version"), "cargo-c": run("cargo-cbuild", "--version"),
        "pkg-config": run("pkg-config", "--version"), "perl": run("perl", "-e", "print $^V"),
    },
    "buildHost": host.platform(),
}
json.dump(info, open(sys.argv[3], "w"), indent=2, sort_keys=True)
EOF
}
