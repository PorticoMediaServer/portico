#!/usr/bin/env bash
# Plays a set of generated files through the Android media engine on a connected device or a
# running emulator, without the app: proof that every library loads (SONAMEs, dependencies,
# 16 KB pages, libc++), and that FFmpeg, dav1d, libass's dependencies, uchardet + iconv and
# OpenSSL work on the device's own C library.
#
#   third_party/media-engine/smoke/android-smoke.sh            # the device's primary ABI
#   ANDROID_SERIAL=emulator-5554 third_party/media-engine/smoke/android-smoke.sh --abi x86_64
#
# Needs: adb with exactly one device (or ANDROID_SERIAL), a finished build-android.sh for that
# ABI, and ffmpeg on this machine to make the test files (any build with libx264, libx265,
# libsvtav1 or libaom, libvpx and libopus). Leaves nothing on the device.
source "$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/lib/common.sh"

ABI=""
while (( $# )); do
    case "$1" in
        --abi) ABI="$2"; shift 2 ;;
        *) me_die "Unknown argument $1" ;;
    esac
done
ADB="${ADB:-$(command -v adb || echo "${ANDROID_HOME:-$HOME/Android/Sdk}/platform-tools/adb")}"
[[ -x "$ADB" ]] || me_die "adb not found."
"$ADB" get-state >/dev/null 2>&1 || me_die "No device or emulator is connected (adb get-state)."
[[ -n "$ABI" ]] || ABI=$("$ADB" shell getprop ro.product.cpu.abi | tr -d '\r')
command -v ffmpeg >/dev/null || me_die "ffmpeg is needed on this machine to make the test files."

NDK_VERSION=$(me_lock tool android-ndk version)
NDK="${ANDROID_NDK_HOME:-${ANDROID_HOME:-$HOME/Android/Sdk}/ndk/$NDK_VERSION}"
case "$(uname -s)" in Linux) TC="$NDK/toolchains/llvm/prebuilt/linux-x86_64" ;; *) TC="$NDK/toolchains/llvm/prebuilt/darwin-x86_64" ;; esac
case "$ABI" in
    arm64-v8a) CLANG=aarch64-linux-android24-clang; TRIPLE=aarch64-linux-android ;;
    armeabi-v7a) CLANG=armv7a-linux-androideabi24-clang; TRIPLE=arm-linux-androideabi ;;
    x86) CLANG=i686-linux-android24-clang; TRIPLE=i686-linux-android ;;
    x86_64) CLANG=x86_64-linux-android24-clang; TRIPLE=x86_64-linux-android ;;
    *) me_die "Unknown ABI $ABI" ;;
esac
LIBS="$ME_OUT/android/lib/$ABI"
[[ -f "$LIBS/libmpv.so" ]] || me_die "No build for $ABI in $LIBS (run build-android.sh --abis $ABI)."

SMOKE="$ME_WORK/smoke-$ABI"
MEDIA="$ME_WORK/smoke-media"
rm -rf "$SMOKE"; mkdir -p "$SMOKE" "$MEDIA"
"$TC/bin/$CLANG" -O2 -Wall -Werror -I"$ME_OUT/android/include" "$ME_ROOT/smoke/mpv-smoke.c" \
    -L"$LIBS" -lmpv -Wl,-z,max-page-size=16384 -o "$SMOKE/mpv-smoke"

# --- test files (made once) ----------------------------------------------------------------
make_media() {
    local f="$MEDIA/$1"; shift
    [[ -f "$f" ]] && return
    ffmpeg -hide_banner -loglevel error -y "$@" "$f.tmp.${f##*.}" && mv "$f.tmp.${f##*.}" "$f"
}
cat > "$MEDIA/styled.ass" <<'EOF'
[Script Info]
ScriptType: v4.00+
PlayResX: 640
PlayResY: 360

[V4+ Styles]
Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding
Style: Default,sans-serif,28,&H00FFFFFF,&H000000FF,&H00000000,&H64000000,0,0,0,0,100,100,0,0,1,2,0,2,10,10,10,1

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
Dialogue: 0,0:00:00.00,0:00:02.50,Default,,0,0,0,,{\b1}Portico styled line
EOF
printf '1\r\n00:00:00,000 --> 00:00:02,500\r\n\xcf\xf0\xe8\xe2\xe5\xf2, \xec\xe8\xf0! \xdd\xf2\xee \xf1\xf3\xe1\xf2\xe8\xf2\xf0\xfb \xe2 \xea\xee\xe4\xe8\xf0\xee\xe2\xea\xe5 Windows-1251.\r\n\r\n' > "$MEDIA/cyrillic-cp1251.srt"
SRC=(-f lavfi -i "testsrc2=size=640x360:rate=24:duration=3" -f lavfi -i "sine=frequency=440:duration=3")
make_media h264-aac-ass.mkv "${SRC[@]}" -i "$MEDIA/styled.ass" -map 0 -map 1 -map 2 -c:v libx264 -preset ultrafast -c:a aac -c:s ass
make_media hevc-main10.mp4 "${SRC[@]}" -c:v libx265 -preset ultrafast -pix_fmt yuv420p10le -x265-params log-level=error -tag:v hvc1 -c:a aac
if ffmpeg -hide_banner -encoders 2>/dev/null | grep -q libsvtav1; then
    make_media av1-opus.mkv "${SRC[@]}" -c:v libsvtav1 -preset 12 -c:a libopus
else
    make_media av1-opus.mkv "${SRC[@]}" -c:v libaom-av1 -cpu-used 8 -c:a libopus
fi
make_media vp9-opus.webm "${SRC[@]}" -c:v libvpx-vp9 -deadline realtime -cpu-used 8 -c:a libopus
make_media mpeg2-ac3.ts "${SRC[@]}" -c:v mpeg2video -c:a ac3
make_media eac3-flac.mka -f lavfi -i "sine=frequency=330:duration=3" -f lavfi -i "sine=frequency=550:duration=3" -map 0 -map 1 -c:a:0 eac3 -c:a:1 flac
if [[ ! -f "$MEDIA/hls/index.m3u8" ]]; then
    mkdir -p "$MEDIA/hls"
    ffmpeg -hide_banner -loglevel error -y "${SRC[@]}" -c:v libx264 -preset ultrafast -c:a aac -f hls -hls_time 1 \
        -hls_playlist_type vod "$MEDIA/hls/index.m3u8"
fi

# --- push and run ----------------------------------------------------------------------------
DEVICE_DIR=/data/local/tmp/portico-media-engine
"$ADB" shell rm -rf "$DEVICE_DIR" >/dev/null
"$ADB" shell mkdir -p "$DEVICE_DIR/media" >/dev/null
"$ADB" push -q "$LIBS"/*.so "$SMOKE/mpv-smoke" "$DEVICE_DIR/" >/dev/null
"$ADB" push -q "$TC/sysroot/usr/lib/$TRIPLE/libc++_shared.so" "$DEVICE_DIR/" >/dev/null
"$ADB" push -q "$MEDIA"/*.mkv "$MEDIA"/*.mp4 "$MEDIA"/*.webm "$MEDIA"/*.ts "$MEDIA"/*.mka "$MEDIA"/*.srt "$MEDIA/hls" "$DEVICE_DIR/media/" >/dev/null
"$ADB" shell chmod 755 "$DEVICE_DIR/mpv-smoke"

PASSED=0; FAILED=0
run_case() {  # <name> <regex>... -- <file> [options...]: every regex must match the output
    local name="$1"; shift
    local patterns=()
    while [[ "$1" != "--" ]]; do patterns+=("$1"); shift; done
    shift
    local out ok=1 pattern
    out=$("$ADB" shell "cd $DEVICE_DIR && LD_LIBRARY_PATH=$DEVICE_DIR ./mpv-smoke $*" 2>&1 | tr -d '\r') || true
    for pattern in "${patterns[@]}"; do grep -qE -- "$pattern" <<< "$out" || { ok=0; echo "  missing: $pattern"; }; done
    if (( ok )); then
        PASSED=$((PASSED + 1)); echo "PASS $name"
    else
        FAILED=$((FAILED + 1)); echo "FAIL $name"; sed 's/^/    /' <<< "$out" | tail -25
    fi
    echo "$out" > "$SMOKE/$name.log"
}

echo "Device: $("$ADB" shell getprop ro.product.model | tr -d '\r'), Android $("$ADB" shell getprop ro.build.version.release | tr -d '\r'), ABI $ABI"
run_case h264-aac-ass "^result eof" "^video .*264" "^audio .*AAC" "^sub-text Portico styled line" "^ffmpeg 8\.1\.2" -- media/h264-aac-ass.mkv sid=1
run_case hevc-main10 "^result eof" "^video .*(HEVC|265)" -- media/hevc-main10.mp4
run_case av1-dav1d-opus "^result eof" "^video .*AV1" "^audio .*Opus" "^playing .*\"decoder\":\"libdav1d\"" -- media/av1-opus.mkv
run_case vp9-opus "^result eof" "^video .*VP9" -- media/vp9-opus.webm
run_case mpeg2-ac3-ts "^result eof" "^video .*MPEG-2" "^audio .*(AC-3|ATSC)" -- media/mpeg2-ac3.ts
run_case eac3 "^result eof" "^audio .*E-AC-3" -- media/eac3-flac.mka aid=1
run_case flac "^result eof" "^audio .*FLAC" -- media/eac3-flac.mka aid=2
run_case hls "^result eof" "^format hls" -- media/hls/index.m3u8
run_case cp1251-subtitles "^result eof" "^sub-text Привет, мир! Это субтитры в кодировке Windows-1251\." -- media/hevc-main10.mp4 sub-files=media/cyrillic-cp1251.srt sid=1
# TLS through OpenSSL with verification on (as in the app): with no trusted roots given, an
# HTTPS server must be refused at the certificate check; with this machine's root bundle, the
# same server passes it (and then is not a media file). Needs network on the device; an IP
# address, as an emulator's shell often has no DNS.
if "$ADB" shell "ping -c 1 -W 3 1.1.1.1" >/dev/null 2>&1; then
    run_case tls-verify-refuses "certificate verify failed|error:0A000086" "^result error" -- https://1.1.1.1/ network-timeout=10
    for bundle in /etc/ssl/certs/ca-certificates.crt /etc/ssl/cert.pem; do
        [[ -f "$bundle" ]] || continue
        "$ADB" push -q "$bundle" "$DEVICE_DIR/roots.pem" >/dev/null
        run_case tls-verify-accepts "^result error unrecognized file format" -- https://1.1.1.1/ network-timeout=10 tls-ca-file=$DEVICE_DIR/roots.pem
        break
    done
else
    echo "SKIP tls-verify (the device has no network)"
fi
"$ADB" shell rm -rf "$DEVICE_DIR"
echo "$PASSED passed, $FAILED failed. Logs: $SMOKE/"
(( FAILED == 0 ))
