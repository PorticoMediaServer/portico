# Portico media engine

This folder builds the Android [mpv](https://mpv.io) player engine and every library it uses
from pinned source, as separate dynamic libraries. The retired Apple mpv recipe is no longer
a build or verification target. Native iOS/tvOS app archives remain in the release workflow;
they use the native Apple player and their independent dependencies.

## Why we build it ourselves (licensing in plain words)

Portico is GPLv3 and Justin owns all of its copyright, so his own code can go on the app
stores. Third-party GPL code cannot, and LGPL code can only when it is used the way the LGPL
expects: as **separate, replaceable dynamic libraries**, with the licence texts and the
**corresponding source** shipped with the app. The engines used before did not fit that well:

- Apple used MPVKit, whose FFmpeg is LGPL **version 3** (forced by GMP) and **statically
  linked**, and which bundles GnuTLS, Nettle, GMP, libbluray and uavs3d.
- Android used media-kit's `libmpv.so`: FFmpeg 6.0 under LGPL version 3 with mbedTLS, all linked
  statically into one library.

This build is **LGPL 2.1 or later** throughout (FFmpeg with no `--enable-gpl`,
`--enable-version3` or `--enable-nonfree`; mpv with `-Dgpl=false`), every library is its own
`.so`, and `collect-notices.sh` produces the notices and the source bundle a release
must carry. FFmpeg is the **same version the Portico server pins**, so both ends decode alike.

## What is built

| Component | Version | Licence | Why |
|---|---|---|---|
| FFmpeg | 8.1.2 (= server pin) | LGPL-2.1-or-later | Demuxing, decoding, parsing, network reads. Every decoder, demuxer, parser and bitstream filter; no encoders, muxers or devices; a small set of filters (the ones mpv inserts itself: deinterlacing, rotation, flips). Hardware decoding: MediaCodec. |
| OpenSSL | 3.5.9 (LTS) | Apache-2.0 | TLS for FFmpeg's `https` (FFmpeg accepts OpenSSL 3 without `version3` when GPL is off). Client-side algorithms only. |
| mpv | 0.41.0 | LGPL-2.1-or-later (`-Dgpl=false`) | The player core, `libmpv`. Android draws with `vo=gpu` (OpenGL ES). |
| libass | 0.17.5 | ISC | Styled (ASS/SSA) subtitles. Fonts: the app's own fonts folder on Android (with a patch that falls back letter by letter to any loaded font, see Patches). |
| FreeType | 2.15.0 | FTL | Font rasterising for libass. |
| HarfBuzz | 14.6.0 | MIT | Text shaping for libass. |
| FriBidi | 1.0.17 | LGPL-2.1-or-later | Right-to-left text for libass. |
| libunibreak | 8.0 | Zlib | Unicode line breaking for libass (CJK text without spaces wraps correctly). |
| dav1d | 1.5.4 | BSD-2-Clause | AV1 software decoding. |
| libplacebo | 7.360.1 | LGPL-2.1-or-later | mpv's renderer (a required dependency of mpv 0.41): scaling, colour, HDR tone mapping. |
| Little CMS | 2.19.1 | MIT | Colour management (ICC) for libplacebo and mpv. |
| libdovi | 3.4.0 | MIT | Dolby Vision metadata for libplacebo (`vo=gpu-next`). Rust, built with cargo-c. |
| uchardet | 0.0.8 | LGPL-2.1-or-later (tri-licence) | Detects the character set of subtitles that are not UTF-8. |
| libiconv | 1.19 | LGPL-2.1-or-later | **Android only.** Converts those subtitles; Android's own iconv starts at API 28, the app supports API 24. Apple's C library has iconv. |

Every source URL, SHA-256 and tool version is in `sources.lock.json`. zlib is the platform's
own (the NDK's `libz.so`). The C++ runtime on Android is the NDK's
`libc++_shared.so`, which React Native already packages; the engine does not ship a copy.

**Dropped** compared with the old engines, and why: GnuTLS, Nettle, GMP, mbedTLS (OpenSSL does
TLS); libbluray, libsmbclient, libxml2/DASH, uavs3d, Lua, LuaJIT, JavaScript (the app plays
files and HLS from its server, never discs, SMB shares, DASH or scripts); every encoder and
muxer (a player encodes nothing; S/PDIF passthrough is not used, sound is decoded to PCM).
`verify.py` fails a build that contains any of GnuTLS, Nettle, GMP, mbedTLS, x264, x265,
libbluray, libsmbclient or fdk-aac.

## Layout

```
sources.lock.json      every pinned source and tool
lib/common.sh          the shared recipe (FFmpeg configuration, mpv/libplacebo/libass options, …)
build-android.sh       Android: four ABIs, one .so per library
patches/               changes to upstream sources (see below)
verify.py              checks a finished build (licences, linking, contents, manifest)
manifest/*.json        the recorded dependency graphs verify.py compares a build with
smoke/                 android-smoke.sh: plays generated files through the engine on a device or emulator
check-ffmpeg-pin.py    fails when FFmpeg differs from the server's pin
collect-notices.sh     licence notices and the corresponding source bundle
work/  out/            build products (not in Git)
```

## Android (Ubuntu or macOS)

Prerequisites: the Android SDK with NDK **27.1.12297006** (the app's `ndkVersion`; `sdkmanager
"ndk;27.1.12297006"`), `rustup`, `python3` (with `venv`), `perl`, `pkg-config`, `make`, `curl`,
a C compiler for the build tools (nasm, cargo-c). No root access is needed: meson, ninja, cmake
(pip, in a venv), nasm (from source) and cargo-c (`cargo install --locked`) are installed at
their pinned versions into `work/tools`, and the four Android Rust targets with `rustup target
add`.

```bash
third_party/media-engine/build-android.sh                 # all four ABIs: ~30 min the first time on 12 cores, ~3 GB of work files
third_party/media-engine/build-android.sh --abis x86_64   # one emulator architecture
MEDIA_ENGINE_JOBS=4 MEDIA_ENGINE_WORK_DIR=/big/disk/work third_party/media-engine/build-android.sh
```

The output is `out/android/`: `lib/<abi>/*.so` (twenty libraries per ABI, stripped, plain
SONAMEs such as `libavcodec.so`, 16 KB page alignment), `include/mpv/`, `meta/<abi>/` (FFmpeg's
configuration and component list, mpv's features), `notices/`, `source/`, `manifest.json`.
`apps/native/android/portico-mpv/build.gradle` packages it; `-PporticoLibmpvDir=` points it
elsewhere. Each component is rebuilt only when its pin, its recipe or an earlier component
changes; `--clean` starts over.

### Smoke test on a device or emulator

`smoke/android-smoke.sh [--abi x86_64]` compiles a small libmpv program (`smoke/mpv-smoke.c`),
pushes it with the libraries to the connected device or emulator (`ANDROID_SERIAL` to choose),
makes test files with this machine's `ffmpeg`, and plays each to the end with no video or audio
output: H.264/AAC with ASS subtitles in MKV, HEVC Main 10 in MP4, AV1 (dav1d) with Opus, VP9,
MPEG-2/AC-3 in TS, E-AC-3, FLAC, HLS, a Windows-1251 SRT that must come out as UTF-8, and TLS
(refused without trusted roots, accepted with them). It proves the libraries load and decode on
the device's own C library; MediaCodec, drawing and the app are tested in the app.

## Apple recipe retirement

The Apple mpv build script and Swift package were retired when native Apple playback moved
to AVPlayer. This directory no longer offers an Apple manifest, MoltenVK/glslang pins, a
MoltenVK mpv patch, or Apple verify/notices modes. The release workflow still installs Pods
and archives both native Xcode workspaces. Their current independent dependencies are
maintained with those apps, including `third_party/apple-subtitles`.

Previously built frameworks, source bundles, notices and frozen checkpoint products remain
retained artifacts; this source cleanup does not delete or repackage them. The historical
recipe remains available in Git for reproducing those older artifacts.

## Verify

`verify.py android` runs at the end of each build; run it again any time. It reads ELF
files directly (no other tools) and checks: FFmpeg's licence is "LGPL version 2.1 or
later" and its configuration has no gpl/version3/nonfree; mpv is `gpl=false`; no forbidden
library is linked or embedded (dependency names, symbols and tell-tale strings); exactly the
expected libraries exist, each names itself correctly and contains no copy of another (FFmpeg
and OpenSSL are not inside libmpv); every dependency is one of ours or an allowed system
library; plain SONAMEs, 16 KB alignment, stripped; the decoders, demuxers, parsers,
protocols, filters and hardware decoders the app relies on are present; the dependency graph
matches `manifest/<platform>.json`; the notices and source bundle exist; no library contains
the work directory, the home folder or any `/home/` or `/Users/` path. `--update-expected` re-records the manifest after an
intended change; `--work` names the work directory when it is not the default.

## Neutral build paths

The libraries carry no folder of the machine that built them, wherever the work directory is.
Every C, C++ and Objective-C compile gets `-ffile-prefix-map`, `-fdebug-prefix-map` and
`-fmacro-prefix-map` from the work directory to `/portico-media-engine` and from the toolchain
(the NDK) to `/toolchain`, so file names in asserts and debug
information are neutral; libdovi's Rust code gets `--remap-path-prefix` the same way. The
configuration strings the libraries report about themselves (FFmpeg's `av*_configuration()`,
mpv's configure line and config folder, OpenSSL's `compiler:` line and module folders) are
rewritten in their generated headers before compiling. Android libraries have no run paths. `verify.py`
fails a build if any path is left.

## Releasing: notices and corresponding source

`collect-notices.sh` (run by the build scripts) writes `out/<platform>/notices/` and
`out/<platform>/source/portico-media-engine-<platform>-source-r<revision>.tar` (the source
archives exactly as pinned, libdovi's vendored Rust crates, this recipe and its patches) with
its SHA-256. The notices go into the Android app automatically:
`assets/media-engine/THIRD-PARTY-NOTICES.txt`. **Each release must publish the source bundle** next to the
app (for example as a release asset); the notice text says it is published with each release.

## Updating a component

1. Change its `version`, `url`, `file` and `sha256` in `sources.lock.json` (download it and run
   `sha256sum`), bump `revision`.
2. FFmpeg: change it only together with the server
   (`main-server/third_party/ffmpeg/sources.lock.json`), then run
   `./check-ffmpeg-pin.py <server>/third_party/ffmpeg/sources.lock.json`.
3. libplacebo: also update `subsources` to the submodule commits of the new tag
   (`https://api.github.com/repos/haasn/libplacebo/contents/3rdparty?ref=<tag>`).
4. Check that the remaining patches still apply (the build stops if not).
5. Rebuild Android at the next Release checkpoint; `verify.py android` must pass. If the dependency graph changed on purpose,
   review it and run `verify.py <platform> --update-expected`.
6. A new tool version: change it in `tools`; the scripts reinstall it.

## Patches

- `patches/libass/0001-fall-back-to-any-loaded-font-with-the-glyph.patch`: libass looks for a
  face that draws a missing character through the system font provider, and Android has none in
  libass. With this, when neither the requested family nor the default family has the character,
  any loaded font that does (the app's fonts folder, fonts attached to the file) is used before
  the default font file, the closest weight and slant first. A line that mixes scripts (Arabic and
  Latin, say) then draws every letter. Apple's CoreText provider answers first, so nothing changes
  there. Written for Portico; ISC like libass.

- `patches/libdovi/0001-build-the-library-without-benchmark-dependencies.patch`: removes
  libdovi's benchmark-only dependency (criterion) and the 61 lock entries only it needed, so the
  vendored Rust crates in the source bundle are 19 MB instead of 190 MB (mostly Windows import
  libraries). Every remaining crate keeps the version and checksum upstream's `Cargo.lock` pins.

## Certificate trust

Android mpv never connects to the server itself: the engine gives it `http://127.0.0.1:<port>/...` and
the in-app relay (`PorticoMpvRelay`, Android) fetches from the server with the app's
own HTTP client (OkHttp), so certificates are checked exactly as for every other
request, including an owner's own certificate for their domain, and plain HTTP keeps working.
mpv's protocols are limited to `file,http,tcp,crypto` (no `https`, no `tls`), and `tls-verify`
is set to `yes`: should mpv ever be handed an HTTPS address, it verifies the certificate and
refuses rather than trusting it unchecked, as mpv does by default. No root-certificate bundle is
shipped: it would be a second, unmaintained trust store beside the system's.
