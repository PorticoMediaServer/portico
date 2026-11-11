#!/usr/bin/env bash
# Builds Portico's media tools: ffmpeg, ffprobe and the portico-disc helper.
#
# The source is jellyfin-ffmpeg at the tag in upstream.lock, built with jellyfin-ffmpeg's own
# builder. Portico lays a few things on top: four FFmpeg patches, four library recipes
# (recipes/), two patches that stop the disc libraries loading a decryption module, and the
# removal of FDK AAC. Nothing here signs or publishes anything.
#
#   build.sh <stage> <platform>
#
# Stages, in order. Each can be run alone; the slow ones keep their results in the project's
# container registry so that a later change does not repeat them.
#
#   prepare    unpack the pinned source and lay Portico's additions on it     (seconds)
#   check      everything that needs no compiler: patches apply, recipes parse,
#              the option list has what wanted/<platform>.txt asks for          (seconds)
#   sources    fetch every pinned library source and collect its licence text
#   compiler   upstream's compiler image, unchanged; built once per upstream version
#   libraries  every library, one saved step each (LIBRARY=name builds one alone)
#   helper     compile portico-disc against those libraries
#   configure  FFmpeg's configure step alone, checked against wanted/
#   compile    ffmpeg and ffprobe
#   package    the bundle, with its feature list checked against wanted/
set -euo pipefail
shopt -s globstar nullglob

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$HERE/../.." && pwd)"
STAGE="${1:?usage: build.sh <stage> <platform>}"
PLATFORM="${2:?usage: build.sh <stage> <platform>}"

case "$PLATFORM" in
    linux-x64) TARGET=linux64 ;;
    *) echo "build.sh: no build for $PLATFORM yet" >&2; exit 2 ;;
esac
VARIANT=gpl

WORK="${MEDIA_TOOLS_WORK:-$ROOT/.media-tools}/$PLATFORM"
SRC="$WORK/src"
BUILDER="$SRC/builder"
WANTED="$HERE/wanted/$PLATFORM.txt"
PATCHES=(portico-dovi-profile portico-dovi-join portico-bluray-playlist-minimum portico-x265-hdr10plus)

# Saved layers live in the project's container registry. They are build caches, never releases.
REGISTRY="${MEDIA_TOOLS_REGISTRY:-ghcr.io/porticomediaserver}"
COMPILER_IMAGE_NAME="$REGISTRY/build-cache-ffmpeg-compiler"
LIBRARY_CACHE="$REGISTRY/build-cache-ffmpeg-libraries:$TARGET"
LIBRARY_IMAGE="portico-media-tools-libraries:$TARGET"

say() { echo "== $*"; }
fail() { echo "build.sh: $*" >&2; exit 1; }
lock() { sed -n "s/^$1=//p" "$HERE/upstream.lock"; }
sha256_of() { sha256sum "$1" | cut -d' ' -f1; }
wanted() { awk -v kind="$1" '$1 == kind { print $2 }' "$WANTED"; }

need_prepared() { [[ -f "$BUILDER/generate.sh" ]] || fail "run 'build.sh prepare $PLATFORM' first"; }

# ---------------------------------------------------------------------------------------------
prepare() {
    local archive="$WORK/upstream-$(lock sha256).tar.gz"
    mkdir -p "$WORK"
    if [[ ! -f "$archive" ]]; then
        say "fetching jellyfin-ffmpeg $(lock tag)"
        curl --fail --silent --show-error --location --retry 3 -o "$archive.part" "$(lock url)"
        mv "$archive.part" "$archive"
    fi
    [[ "$(sha256_of "$archive")" == "$(lock sha256)" ]] || fail "the upstream archive does not match upstream.lock"

    rm -rf "$SRC"
    mkdir -p "$SRC"
    tar -xzf "$archive" -C "$SRC" --strip-components=1

    # FDK AAC is not redistributable in a GPL program. The server uses FFmpeg's own AAC encoder.
    rm "$BUILDER/scripts.d/50-fdk-aac-stripped.sh"

    cp "$HERE"/recipes/*.sh "$BUILDER/scripts.d/"
    mkdir -p "$BUILDER/patches/portico"
    cp "$HERE"/patches/libbluray-no-decryption.patch "$HERE"/patches/libdvdread-no-decryption.patch "$BUILDER/patches/portico/"

    # libbluray is upstream's recipe; Portico only adds the patch that stops it loading a
    # decryption module. If upstream reshapes the recipe this refuses instead of guessing.
    local bluray="$BUILDER/scripts.d/50-libbluray.sh"
    [[ "$(grep -c '^    cd libbluray$' "$bluray")" == 1 ]] || fail "upstream's libbluray recipe changed shape"
    sed -i 's|^    cd libbluray$|    cd libbluray\n    patch -p1 < /patches/libbluray-no-decryption.patch|' "$bluray"
    cat >> "$bluray" <<'EOF'

ffbuild_dockerstage() {
    to_df "RUN --mount=src=${SELF},dst=/stage.sh --mount=src=patches/portico,dst=/patches run_stage /stage.sh"
}
EOF

    local name
    for name in "${PATCHES[@]}"; do
        cp "$HERE/$name.patch" "$SRC/debian/patches/$name.patch"
        echo "$name.patch" >> "$SRC/debian/patches/series"
    done
    say "prepared $SRC"
}

# Everything upstream's builder derives from the recipes without compiling: which recipes are
# switched on for this platform, and the options FFmpeg will be configured with.
plan() {
    need_prepared
    (
        cd "$BUILDER"
        set +eu
        source util/vars.sh "$TARGET" "$VARIANT"
        source "variants/${TARGET}-${VARIANT}.sh"
        export FFBUILD_PREFIX=/opt/ffbuild
        : > "$WORK/recipes.txt"
        get_output() {
            (
                SELF="$1"
                source "$1"
                if ffbuild_enabled; then ffbuild_$2 || exit 0; else ffbuild_un$2 || exit 0; fi
            )
        }
        for script in scripts.d/**/*.sh; do
            ( source "$script"; ffbuild_enabled ) && echo "$script" >> "$WORK/recipes.txt"
            FF_CONFIGURE+=" $(get_output "$script" configure)"
            FF_CFLAGS+=" $(get_output "$script" cflags)"
            FF_CXXFLAGS+=" $(get_output "$script" cxxflags)"
            FF_LDFLAGS+=" $(get_output "$script" ldflags)"
            FF_LDEXEFLAGS+=" $(get_output "$script" ldexeflags)"
            FF_LIBS+=" $(get_output "$script" libs)"
        done
        for name in FF_CONFIGURE FF_CFLAGS FF_CXXFLAGS FF_LDFLAGS FF_LDEXEFLAGS FF_LIBS; do
            printf '%s=%q\n' "$name" "$(xargs <<< "${!name}")"
        done > "$WORK/ffmpeg-options.sh"
    )
    source "$WORK/ffmpeg-options.sh"
    tr ' ' '\n' <<< "$FF_CONFIGURE" | grep . > "$WORK/configure-options.txt"
}

check() {
    need_prepared
    local problems=0 name file
    problem() { echo "check: $*" >&2; problems=$((problems + 1)); }

    say "patches apply in order"
    rm -rf "$WORK/patch-check"
    mkdir -p "$WORK/patch-check"
    tar -C "$SRC" --exclude=./builder --exclude=./debian --exclude=./msys2 -cf - . | tar -C "$WORK/patch-check" -xf -
    while read -r name; do
        [[ -z "$name" ]] && continue
        if ! patch -d "$WORK/patch-check" -p1 -s -N < "$SRC/debian/patches/$name" > "$WORK/patch-check.log" 2>&1; then
            problem "patch does not apply: $name"; sed 's/^/    /' "$WORK/patch-check.log" >&2
            break
        fi
    done < "$SRC/debian/patches/series"
    rm -rf "$WORK/patch-check" "$WORK/patch-check.log"

    say "recipes parse"
    for file in "$BUILDER"/scripts.d/**/*.sh "$BUILDER"/variants/*.sh "$BUILDER"/*.sh "$HERE/build.sh"; do
        bash -n "$file" || problem "shell syntax: ${file#"$SRC"/}"
    done

    say "the build plan has Portico's recipes and no FDK AAC"
    ( cd "$BUILDER" && GITHUB_REPOSITORY=local/portico ./generate.sh "$TARGET" "$VARIANT" ) || problem "upstream's generator failed"
    for file in "$HERE"/recipes/*.sh; do
        name="$(basename "$file" .sh)"
        grep -q "AS layer-$name\$" "$BUILDER/Dockerfile" || problem "the build plan has no step for $name"
    done
    grep -qi 'fdk' "$BUILDER/Dockerfile" && problem "the build plan still builds FDK AAC"

    say "FFmpeg's option list has everything wanted and nothing refused"
    plan
    for name in $(wanted configure); do
        grep -qxF -- "$name" "$WORK/configure-options.txt" || problem "wanted option is not in the plan: $name"
    done
    for name in $(wanted refuse-configure); do
        grep -qxF -- "$name" "$WORK/configure-options.txt" && problem "refused option is in the plan: $name"
    done
    grep -qxF -- --enable-gpl "$WORK/configure-options.txt" && grep -qxF -- --enable-version3 "$WORK/configure-options.txt" ||
        problem "the build is not GPL version 3"

    say "every library in the plan has a licence row"
    while read -r file; do
        name="$(basename "$file" .sh)"
        awk -F' *[|] *' -v n="$name" '$1 == n { found = 1 } END { exit !found }' "$HERE/licences.txt" ||
            problem "no row in licences.txt for $name"
    done < "$WORK/recipes.txt"
    awk -F' *[|] *' '!/^#/ && NF && tolower($3) ~ /non-?free|proprietary/ { print; bad = 1 } END { exit bad }' "$HERE/licences.txt" ||
        problem "licences.txt lists something that may not be redistributed"

    say "the disc helper's source is present"
    for file in portico-disc.c mpegts_dovi.h; do
        [[ -f "$ROOT/third_party/portico-disc/$file" ]] || problem "missing third_party/portico-disc/$file"
    done

    [[ $problems == 0 ]] || fail "$problems problem(s); nothing was built"
    say "checks passed for $PLATFORM: $(wc -l < "$WORK/recipes.txt") recipes, $(wc -l < "$WORK/configure-options.txt") FFmpeg options"
}

# ---------------------------------------------------------------------------------------------
# Fetches every pinned library source before any compiler starts, so a dead link or a patch that
# no longer fits is found in minutes. The same fetch gives the licence texts for the bundle and
# the library half of the source that the GPL requires Portico to offer.
fetch_git() {
    local repo="$1" ref="$2" dest="$3"
    rm -rf "$dest"
    git init -q "$dest"
    git -C "$dest" remote add origin "$repo"
    if ! git -C "$dest" fetch -q --depth=1 origin "$ref" 2>/dev/null; then
        # Some hosts refuse a fetch by commit; take the history without file contents instead.
        rm -rf "$dest"
        git clone -q --filter=blob:none --no-checkout "$repo" "$dest" || return 1
        git -C "$dest" -c advice.detachedHead=false checkout -q "$ref" || return 1
        return 0
    fi
    git -C "$dest" -c advice.detachedHead=false checkout -q FETCH_HEAD
}

sources() {
    need_prepared
    plan
    local out="$WORK/library-sources" texts="$WORK/licences" problems=0 script name
    rm -rf "$out" "$texts"
    mkdir -p "$out" "$texts"
    : > "$WORK/library-versions.txt"
    while read -r script; do
        name="$(basename "$script" .sh)"
        # A recipe names its sources in these variables; a recipe with none builds nothing.
        local pins
        pins="$(cd "$BUILDER" && set +eu && source util/vars.sh "$TARGET" "$VARIANT" && source "$script" &&
            printf '%s|%s\n' "${SCRIPT_REPO:-}" "${SCRIPT_COMMIT:-${SCRIPT_REV:-}}" "${SCRIPT_REPO2:-}" "${SCRIPT_COMMIT2:-}" \
                "${SCRIPT_REPO3:-}" "${SCRIPT_COMMIT3:-}" "${SCRIPT_URL:-}" "${SCRIPT_SHA256:-}")"
        local n=0 repo ref
        while IFS='|' read -r repo ref; do
            [[ -z "$repo" ]] && continue
            n=$((n + 1))
            local dest="$out/$name/$n"
            mkdir -p "$out/$name"
            say "$name: $repo $ref"
            echo "$name|$repo|$ref" >> "$WORK/library-versions.txt"
            if [[ "$repo" == *.tar.* ]]; then
                mkdir -p "$dest"
                curl --fail --silent --show-error --location --retry 3 -o "$dest/source.tar" "$repo" &&
                    [[ "$(sha256_of "$dest/source.tar")" == "$ref" ]] && tar -xf "$dest/source.tar" -C "$dest" --strip-components=1 &&
                    rm "$dest/source.tar" || { echo "sources: could not fetch $name from $repo" >&2; problems=$((problems + 1)); continue; }
            elif [[ "$repo" == *svn* ]]; then
                svn export -q "$repo@$ref" "$dest" || { echo "sources: could not fetch $name from $repo" >&2; problems=$((problems + 1)); continue; }
            else
                local tries=0
                until fetch_git "$repo" "$ref" "$dest"; do
                    tries=$((tries + 1))
                    [[ $tries -ge 3 ]] && break
                    sleep 10
                done
                [[ -d "$dest/.git" ]] || { echo "sources: could not fetch $name from $repo at $ref" >&2; problems=$((problems + 1)); continue; }
                if [[ -f "$dest/.gitmodules" ]]; then
                    git -C "$dest" submodule update -q --init --recursive --depth=1 ||
                        echo "sources: note: some sub-projects of $name were not fetched" >&2
                fi
            fi
            mkdir -p "$texts/$name"
            local text
            for text in "$dest"/LICEN[CS]E* "$dest"/COPYING* "$dest"/COPYRIGHT* "$dest"/Copyright* "$dest"/NOTICE* "$dest"/LICENSES/* "$dest"/[Ll]icense*; do
                [[ -f "$text" ]] && cp "$text" "$texts/$name/$n-$(basename "$text")"
            done
            # A few sources keep their licence in a header or the README; licences.txt names the file.
            text="$(awk -F' *[|] *' -v n="$name" '$1 == n { print $5 }' "$HERE/licences.txt")"
            [[ -n "$text" && -f "$dest/$text" ]] && cp "$dest/$text" "$texts/$name/$n-$(basename "$text")"
        done <<< "$pins"
        if [[ $n -gt 0 ]] && ! compgen -G "$texts/$name/*" > /dev/null; then
            echo "sources: no licence text found in the source of $name" >&2; problems=$((problems + 1))
        fi
    done < "$WORK/recipes.txt"

    say "Portico's library patches fit the pinned sources"
    patch --dry-run -s -p1 -d "$out/50-libbluray/1" < "$HERE/patches/libbluray-no-decryption.patch" ||
        { echo "sources: libbluray-no-decryption.patch does not apply" >&2; problems=$((problems + 1)); }
    patch --dry-run -s -p1 -d "$out/50-portico-dvd/1" < "$HERE/patches/libdvdread-no-decryption.patch" ||
        { echo "sources: libdvdread-no-decryption.patch does not apply" >&2; problems=$((problems + 1)); }

    # libdovi is Rust: its crates are sources too, and each carries its own licence text.
    if command -v cargo > /dev/null && [[ -d "$out/50-portico-libdovi/1/dolby_vision" ]]; then
        ( cd "$out/50-portico-libdovi/1/dolby_vision" && cargo vendor -q --locked ../crates > /dev/null ) ||
            { echo "sources: could not fetch libdovi's crates" >&2; problems=$((problems + 1)); }
        local crate
        for crate in "$out"/50-portico-libdovi/1/crates/*/; do
            for text in "$crate"LICEN[CS]E* "$crate"COPYING* "$crate"NOTICE*; do
                [[ -f "$text" ]] && cp "$text" "$texts/50-portico-libdovi/crate-$(basename "$crate")-$(basename "$text")"
            done
        done
    fi

    [[ $problems == 0 ]] || fail "$problems source problem(s)"
    tar -C "$WORK" --exclude=.git -cf - library-sources | zstd -q -T0 -3 -o "$WORK/library-sources-$PLATFORM.tar.zst" -f
    say "sources fetched: $(wc -l < "$WORK/library-versions.txt") pins, $(du -sh "$WORK/library-sources-$PLATFORM.tar.zst" | cut -f1) archive"
}

# ---------------------------------------------------------------------------------------------
BUILDX=portico-media-tools

builder() {
    docker buildx inspect "$BUILDX" > /dev/null 2>&1 ||
        docker buildx create --name "$BUILDX" --driver docker-container --driver-opt network=host \
            --driver-opt env.BUILDKIT_STEP_LOG_MAX_SIZE=-1 --driver-opt env.BUILDKIT_STEP_LOG_MAX_SPEED=-1 > /dev/null
}

# The compiler image is upstream's, unchanged, so its name is the hash of upstream's files for it.
compiler_ref() {
    need_prepared
    local key
    key="$(cd "$BUILDER/images" && find base "base-$TARGET" -type f -print0 | sort -z | xargs -0 sha256sum | sha256sum | cut -c1-16)"
    echo "$COMPILER_IMAGE_NAME:$TARGET-$key"
}

compiler() {
    local ref
    ref="$(compiler_ref)"
    if docker manifest inspect "$ref" > /dev/null 2>&1; then
        say "the compiler image is already saved: $ref"
        return
    fi
    say "building the compiler image (upstream's, unchanged): $ref"
    builder
    docker container inspect portico-media-tools-registry > /dev/null 2>&1 ||
        docker run --rm -d -p 127.0.0.1:5000:5000 --name portico-media-tools-registry registry:2 > /dev/null
    local local_root=127.0.0.1:5000/local
    docker buildx build --builder "$BUILDX" --progress=plain --push --tag "$local_root/base:latest" "$BUILDER/images/base"
    docker buildx build --builder "$BUILDX" --progress=plain --push --tag "$ref" \
        --build-arg GH_REPO="$local_root" "$BUILDER/images/base-$TARGET"
    docker container stop portico-media-tools-registry > /dev/null
    say "saved $ref"
}

build_plan() {
    local ref
    ref="$(compiler_ref)"
    ( cd "$BUILDER" && GITHUB_REPOSITORY=local/portico ./generate.sh "$TARGET" "$VARIANT" )
    sed -i "1s|.*|FROM $ref AS base|" "$BUILDER/Dockerfile"
}

# Builds the libraries one saved step at a time. Whatever was finished is saved to the registry
# even when a later library fails, so a failure never costs more than the library that failed.
# LIBRARY=<recipe name> builds that one library alone (and what it needs), saving nothing.
libraries() {
    need_prepared
    builder
    build_plan
    local steps=() good=() failed="" step
    mapfile -t steps < <(sed -n 's/^FROM [^ ]* AS \(layer-[^ ]*\)$/\1/p' "$BUILDER/Dockerfile")
    if [[ -n "${LIBRARY:-}" ]]; then
        step="$(printf '%s\n' "${steps[@]}" | grep -E -- "-${LIBRARY}\$" | head -1)"
        [[ -n "$step" ]] || fail "no step for library '$LIBRARY'"
        say "building $step alone"
        docker buildx build --builder "$BUILDX" --progress=plain --cache-from "type=registry,ref=$LIBRARY_CACHE" \
            --target "$step" "$BUILDER" || fail "LIBRARY FAILED: $step (its own output is just above)"
        say "$step built"
        return
    fi
    for step in "${steps[@]}"; do
        say "step $step"
        if docker buildx build --builder "$BUILDX" --progress=plain --cache-from "type=registry,ref=$LIBRARY_CACHE" \
            --target "$step" "$BUILDER" > "$WORK/step.log" 2>&1; then
            good+=("$step")
            grep -E '^#[0-9]+ (DONE|CACHED)' "$WORK/step.log" | tail -1 || true
        else
            failed="$step"
            cat "$WORK/step.log"
            break
        fi
    done

    # One empty step that depends on every finished one: saving it saves them all.
    {
        cat "$BUILDER/Dockerfile"
        echo "FROM base AS portico-saved"
        printf 'RUN'
        for step in "${good[@]}"; do printf ' --mount=from=%s,src=/opt/ffbuild,dst=/mnt/%s' "$step" "$step"; done
        echo ' true'
    } > "$BUILDER/Dockerfile.saved"
    if [[ ${#good[@]} -gt 0 && "${MEDIA_TOOLS_SAVE:-1}" == 1 ]]; then
        say "saving ${#good[@]} finished steps to $LIBRARY_CACHE"
        docker buildx build --builder "$BUILDX" --file "$BUILDER/Dockerfile.saved" --target portico-saved \
            --cache-from "type=registry,ref=$LIBRARY_CACHE" --cache-to "type=registry,ref=$LIBRARY_CACHE,mode=max,image-manifest=true,oci-mediatypes=true" \
            "$BUILDER" > "$WORK/save.log" 2>&1 || { tail -30 "$WORK/save.log"; fail "could not save the finished steps"; }
    fi
    [[ -z "$failed" ]] || fail "LIBRARY FAILED: $failed (its own output is just above; ${#good[@]} finished steps were saved)"
    say "all ${#good[@]} steps built"
}

# The image later stages run in: every library, taken from the saved steps.
library_image() {
    docker image inspect "$LIBRARY_IMAGE" > /dev/null 2>&1 && return
    need_prepared
    builder
    build_plan
    say "loading the library image from the saved steps"
    docker buildx build --builder "$BUILDX" --cache-from "type=registry,ref=$LIBRARY_CACHE" \
        --load --tag "$LIBRARY_IMAGE" "$BUILDER" > "$WORK/load.log" 2>&1 || { tail -40 "$WORK/load.log"; fail "could not load the library image"; }
}

in_image() {
    docker run --rm -i -u "$(id -u):$(id -g)" -e HOME=/tmp "$@"
}

# ---------------------------------------------------------------------------------------------
helper() {
    library_image
    mkdir -p "$WORK/out"
    in_image -v "$ROOT/third_party/portico-disc:/disc:ro" -v "$WORK/out:/out" "$LIBRARY_IMAGE" bash -e <<'EOF'
modules="libbluray dvdnav dvdread libudfread"
libs="$(pkg-config --static --libs $modules)"
case "$libs" in *dvdcss*|*aacs*|*bdplus*) echo "a decryption library is in the link line: $libs" >&2; exit 1 ;; esac
$CC $CFLAGS -std=c11 -O2 -Wall -Wextra -Werror -D_FILE_OFFSET_BITS=64 -fPIE $(pkg-config --cflags $modules) \
    /disc/portico-disc.c -o /out/portico-disc $LDFLAGS -pie $libs
EOF
    local disc
    for disc in bluray-two-clip-bdmv bluray-udf250.iso dvd-two-title-video-ts; do
        "$WORK/out/portico-disc" probe "$ROOT/fixtures/discs/$disc" > "$WORK/out/probe.json" ||
            fail "the helper could not read fixtures/discs/$disc"
        say "helper read $disc: $(head -c 120 "$WORK/out/probe.json")"
    done
}

# Maps a wanted line to the name FFmpeg's configure step writes when the component is on.
configured_name() {
    local kind="$1" name="${2^^}"
    # A few components are listed by FFmpeg under one name and configured under another.
    case "$kind $2" in
        "decoder cc_dec") name=CCAPTION ;;
        "decoder libzvbi_teletextdec") name=LIBZVBI_TELETEXT ;;
        "demuxer mpeg") name=MPEGPS ;;
        "demuxer lavfi") echo CONFIG_LAVFI_INDEV; return ;;
        "muxer s16le") name=PCM_S16LE ;;
        "muxer f32le") name=PCM_F32LE ;;
        "protocol srt") name=LIBSRT ;;
    esac
    case "$kind" in
        decoder | encoder | demuxer | muxer | filter | protocol | bsf) echo "CONFIG_${name}_${kind^^}" ;;
        hwaccel) echo "CONFIG_${name}" ;;
    esac
}

ffmpeg_build() {
    local stop_after_configure="$1"
    library_image
    plan
    source "$WORK/ffmpeg-options.sh"
    rm -rf "$WORK/ffbuild"
    mkdir -p "$WORK/ffbuild/ffmpeg"
    tar -C "$SRC" --exclude=./builder --exclude=./msys2 -cf - . | tar -C "$WORK/ffbuild/ffmpeg" -xf -
    cat > "$WORK/ffbuild/run.sh" <<EOF
set -e
cd /ffbuild/ffmpeg
ln -s /ffbuild/ffmpeg/debian/patches patches
quilt push -a > /ffbuild/patches.log
./configure --prefix=/ffbuild/prefix \$FFBUILD_TARGET_FLAGS --extra-version=Portico \\
    --extra-cflags='$FF_CFLAGS' --extra-cxxflags='$FF_CXXFLAGS' --extra-ldflags='$FF_LDFLAGS' \\
    --extra-ldexeflags='$FF_LDEXEFLAGS' --extra-libs='$FF_LIBS' $FF_CONFIGURE ||
    { tail -60 ffbuild/config.log; exit 1; }
[[ "$stop_after_configure" == 1 ]] && exit 0
make -j\$(nproc)
make install
EOF
    in_image -v "$WORK/ffbuild:/ffbuild" "$LIBRARY_IMAGE" bash /ffbuild/run.sh

    say "FFmpeg's configure step switched on everything wanted"
    local kind name macro missing=0
    while read -r kind name _; do
        macro="$(configured_name "$kind" "$name")"
        [[ -z "$macro" ]] && continue
        grep -qsE "^#define $macro 1\$" "$WORK/ffbuild/ffmpeg/config.h" "$WORK/ffbuild/ffmpeg/config_components.h" ||
            { echo "configure: wanted but switched off: $kind $name" >&2; missing=$((missing + 1)); }
    done < <(grep -Ev '^(#|$)' "$WANTED")
    [[ $missing == 0 ]] || fail "$missing wanted feature(s) are switched off"
}

configure() { ffmpeg_build 1; }
compile() { ffmpeg_build 0; }

# ---------------------------------------------------------------------------------------------
package() {
    local bundle="$WORK/bundle" tool
    [[ -x "$WORK/ffbuild/prefix/bin/ffmpeg" && -x "$WORK/out/portico-disc" ]] || fail "run the compile and helper stages first"
    rm -rf "$bundle"
    mkdir -p "$bundle/bin" "$bundle/LICENSES"
    cp "$WORK/ffbuild/prefix/bin/ffmpeg" "$WORK/ffbuild/prefix/bin/ffprobe" "$WORK/out/portico-disc" "$bundle/bin/"
    cp "$SRC/COPYING.GPLv3" "$bundle/LICENSES/GPL-3.0.txt"
    [[ -d "$WORK/licences" ]] && cp -R "$WORK/licences/." "$bundle/LICENSES/"

    say "the programs start and need nothing beyond the operating system"
    "$bundle/bin/ffmpeg" -hide_banner -version | head -1
    "$bundle/bin/ffprobe" -hide_banner -version | head -1
    for tool in ffmpeg ffprobe portico-disc; do
        if ldd "$bundle/bin/$tool" | grep -vE '^\s*(linux-vdso|/lib64/ld-linux|lib(c|m|dl|pthread|rt|mvec|gcc_s)\.so)' | grep -q .; then
            ldd "$bundle/bin/$tool" >&2
            fail "$tool needs a library that is not part of the operating system"
        fi
    done

    say "the feature list has everything wanted"
    local ffmpeg="$bundle/bin/ffmpeg" kind name where missing=0
    declare -A listing
    for kind in decoders encoders demuxers muxers filters protocols bsfs hwaccels devices; do
        listing[$kind]="$("$ffmpeg" -hide_banner "-$kind" 2> /dev/null)"
    done
    "$ffmpeg" -hide_banner -buildconf | tr -s ' ' '\n' > "$WORK/built-options.txt"
    has() { grep -qE "(^|[ ,])$2([ ,]|\$)" <<< "${listing[$1]}"; }
    while read -r kind name where; do
        case "$kind" in
            configure) grep -qxF -- "$name" "$WORK/built-options.txt" || { echo "package: not built with $name" >&2; missing=$((missing + 1)); } ;;
            refuse-configure) grep -qxF -- "$name" "$WORK/built-options.txt" && { echo "package: built with refused $name" >&2; missing=$((missing + 1)); } ;;
            decoder | encoder | muxer | filter | protocol | bsf | hwaccel) has "${kind}s" "$name" || { echo "package: missing $kind $name" >&2; missing=$((missing + 1)); } ;;
            demuxer) has demuxers "$name" || has devices "$name" || { echo "package: missing $kind $name" >&2; missing=$((missing + 1)); } ;;
            option) "$ffmpeg" -hide_banner -h "$name" 2> /dev/null | grep -qE "^\s+-$where\s" || { echo "package: $name has no option $where" >&2; missing=$((missing + 1)); } ;;
        esac
    done < <(grep -Ev '^(#|$)' "$WANTED")
    [[ $missing == 0 ]] || fail "$missing wanted feature(s) are missing from the bundle"

    cat > "$bundle/NOTICE.md" <<EOF
# Portico media tools ($PLATFORM)

ffmpeg and ffprobe are FFmpeg built from jellyfin-ffmpeg $(lock tag) with Portico's patches.
portico-disc is Portico's reader for Blu-ray and DVD folders and images. All three are
licensed under the GNU General Public License, version 3 or later (LICENSES/GPL-3.0.txt).
manifest.json lists every library built into them, with its licence and where its licence
text is in the LICENSES folder. The complete source for these exact programs is offered with
every Portico release that carries them. None of these programs can decrypt a commercial disc.
EOF

    python3 - "$bundle" "$PLATFORM" "$(lock tag)" "$HERE/licences.txt" "$WORK/library-versions.txt" <<'EOF'
import hashlib, json, pathlib, sys
bundle, platform, tag, rows, versions = pathlib.Path(sys.argv[1]), sys.argv[2], sys.argv[3], sys.argv[4], pathlib.Path(sys.argv[5])
pins = {}
if versions.is_file():
    for line in versions.read_text().splitlines():
        recipe, source, version = line.split('|')
        pins.setdefault(recipe, []).append({'source': source, 'version': version})
libraries = []
for line in pathlib.Path(rows).read_text().splitlines():
    if not line.strip() or line.startswith('#'):
        continue
    recipe, name, licence, use = [part.strip() for part in line.split('|')][:4]  # a fifth column names the licence text
    if use.startswith('build only'):
        continue
    texts = sorted(str(p.relative_to(bundle)) for p in (bundle / 'LICENSES' / recipe).glob('*') if p.is_file())
    libraries.append({'name': name, 'licence': licence, 'use': use, 'sources': pins.get(recipe, []), 'licenceTexts': texts})
files = {str(p.relative_to(bundle)): hashlib.sha256(p.read_bytes()).hexdigest()
         for p in sorted(bundle.rglob('*')) if p.is_file()}
manifest = {'schemaVersion': 2, 'target': platform, 'ffmpeg': 'jellyfin-ffmpeg ' + tag + ' with Portico patches',
            'licence': 'GPL-3.0-or-later', 'licenceText': 'LICENSES/GPL-3.0.txt', 'libraries': libraries, 'files': files}
(bundle / 'manifest.json').write_text(json.dumps(manifest, indent=2) + '\n')
EOF
    tar -C "$bundle" -cJf "$WORK/portico-media-tools-$PLATFORM.tar.xz" .
    say "bundle: $WORK/portico-media-tools-$PLATFORM.tar.xz ($(du -sh "$WORK/portico-media-tools-$PLATFORM.tar.xz" | cut -f1))"
}

case "$STAGE" in
    prepare) prepare ;;
    check) check ;;
    sources) sources ;;
    compiler) compiler ;;
    libraries) libraries ;;
    helper) helper ;;
    configure) configure ;;
    compile) compile ;;
    package) package ;;
    *) fail "unknown stage: $STAGE" ;;
esac
