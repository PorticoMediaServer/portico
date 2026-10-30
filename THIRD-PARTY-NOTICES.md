# Third-party notices

Portico is licensed under the GPL, version 3 or later. It's built on a lot of other people's work, and those parts keep their own copyrights and licences. This file lists them.

The licence names below are an inventory, not a substitute for the licence texts themselves. If you distribute a Portico binary, keep the upstream notices and the matching source and build materials with it. Updates are identified by the versioned source files and lockfiles in this repository, never by an upstream project's moving default branch.

## Go server

`server/go.mod` and `server/go.sum` pin the Go modules. `apikit` is Portico's own module. Go itself, and the Go image-scaling code copied into Portico, use BSD-3-Clause terms.

| Module | Version | Licence |
| --- | --- | --- |
| golang.org/x/crypto | v0.56.0 | BSD-3-Clause |
| golang.org/x/image | v0.46.0 | BSD-3-Clause |
| golang.org/x/sys | v0.48.0 | BSD-3-Clause |
| golang.org/x/text | v0.42.0 | BSD-3-Clause |
| modernc.org/sqlite | v1.58.0 | BSD-3-Clause; SQLite engine public domain |
| modernc.org/libc | v1.75.6 | BSD-3-Clause; generated/ported code retains its source notices |
| modernc.org/mathutil | v1.7.1 | BSD-3-Clause |
| modernc.org/memory | v1.12.1 | BSD-3-Clause |
| rsc.io/pdf | v0.1.1 | BSD-3-Clause (Go Authors) |
| github.com/dustin/go-humanize | v1.0.1 | MIT |
| github.com/google/uuid | v1.6.0 | BSD-3-Clause |
| github.com/mattn/go-isatty | v0.0.24 | MIT |
| github.com/ncruces/go-strftime | v1.0.0 | MIT |
| github.com/remyoudompheng/bigfft | v0.0.0-20230129092748-24d4a6f8daec | BSD-3-Clause |
| gopkg.in/yaml.v3 | v3.0.1 | MIT and Apache-2.0, per file |
| github.com/bodgit/sevenzip | v1.6.5 | BSD-3-Clause |
| github.com/nwaples/rardecode/v2 | v2.4.5 | BSD-2-Clause |

The two archive readers power the comic reader: `rardecode` for CBR files and `sevenzip` for CB7 files. Both are direct requirements in `server/go.mod`, and official builds link them in using the `portico_rar7z` and `portico_cb7` tags from `scripts/release-build-tags.py`. CB7 also needs a supported platform and the bounded `archive7z` worker; the tag on its own never opens an archive inside the server process. Their licence texts are the packages' own unchanged `LICENSE` files.

The SQLite driver files copied into Portico keep their upstream notices in [server/internal/thirdparty/sqlite/LICENSE](server/internal/thirdparty/sqlite/LICENSE), with their history in [PORTICO.md](server/internal/thirdparty/sqlite/PORTICO.md). The image scaler's BSD notice is in [server/internal/imagework/SCALE_LICENSE](server/internal/imagework/SCALE_LICENSE).

## Web and build packages

The [npm dependency inventory](docs/dependency-licenses.md) lists every package and its declared licence from the root lockfile, including indirect, development and platform-specific packages. The main ones: React and React DOM, TanStack Router, Radix UI, QRCode and the Noble cryptography libraries are MIT, and hls.js is Apache-2.0. The build uses TypeScript (Apache-2.0), Vite (MIT) and platform-specific bundler binaries under their recorded licences.

Portico uses a patched hls.js, version `1.7.2-portico.1`, based on upstream `1.7.2` at `dd669ec53b601aa9fab7a2a78c913e25018cdf18`. The original archive, the patch, the build recipe, the [licence](third_party/hls.js/LICENSE) and the [provenance record](third_party/hls.js/provenance.json) are all in `third_party/hls.js`.

## Reader libraries

The book and comic reader's dependencies are pinned in `packages/reader-core/package.json` and the root lockfile. Their full licence texts and hashes are in [third_party/reader](third_party/reader/provenance.json), and release builds copy them into `licenses/reader`.

| Component | Pinned source | Licence |
| --- | --- | --- |
| Mozilla Foundation PDF.js (`pdfjs-dist`) | [6.4.299](https://github.com/mozilla/pdf.js/tree/v6.4.299) | [Apache-2.0](third_party/reader/pdfjs-LICENSE) |
| John Factotum Foliate.js | [78914aef4466eb960965702401634c2cb348e9b1](https://github.com/johnfactotum/foliate-js/tree/78914aef4466eb960965702401634c2cb348e9b1) | [MIT](third_party/reader/foliate-LICENSE) |
| Gildas Lormeau zip.js | 2.24.0 | [BSD-3-Clause](third_party/reader/zipjs-LICENSE) |
| Cure53 DOMPurify | 3.4.17 | [Apache-2.0](third_party/reader/dompurify-LICENSE), selected from the upstream Apache-2.0 OR MPL-2.0 choice |

The Literata variable font is pinned to [google/fonts 4e5f06dbb274a27ebe71ed54ea706b3ee40eabd9](https://github.com/google/fonts/tree/4e5f06dbb274a27ebe71ed54ea706b3ee40eabd9/ofl/literata). Its unchanged [SIL Open Font License 1.1](packages/reader-core/assets/OFL.txt), metadata and source receipt are in `packages/reader-core/assets`, and the licence also ships as `licenses/reader/literata-OFL.txt`. The font's SHA-256 is `b41138c9373112f32abb589cc22e8674b06ed4048b0c513be922bdd26f274440`.

The small piece of text-layer CSS in `packages/reader-core/src/pdf/adapter.ts` is adapted from Mozilla PDF.js 6.4.299 under Apache-2.0 and keeps its attribution. The rest of the viewer is Portico's own.

## Subtitles in the browser, and fonts

The browser subtitle renderer is the unmodified `@jellyfin/libass-wasm` 4.2.4, kept in `third_party/libass-wasm`. Its source is [JavascriptSubtitlesOctopus at the pinned revision](https://github.com/jellyfin/JavascriptSubtitlesOctopus/tree/2d4ec9b16a639f23733cd32982e09e0eb757f092). The renderer itself is WebAssembly, and its JavaScript wrapper and worker code are MIT. Inside it:

| Component | Licence |
| --- | --- |
| libass | ISC |
| FreeType | FTL or GPL-2.0-or-later; the FTL option is recorded in provenance |
| HarfBuzz | MIT-style |
| FriBidi | LGPL-2.1-or-later |
| Fontconfig | MIT-style and NTP |
| Expat, Brotli | MIT/Expat |
| zlib | Zlib |
| Small parts listed in COPYRIGHT | BSD-2-Clause, BSL-1.0, NTP and public domain |

The full per-file copyrights and licence texts are in [COPYRIGHT](third_party/libass-wasm/COPYRIGHT), along with the wrapper's [LICENSE](third_party/libass-wasm/LICENSE) and the font's [OFL-1.1.txt](third_party/libass-wasm/OFL-1.1.txt). The web build copies these next to the renderer it serves. `python3 -I third_party/libass-wasm/verify.py` checks the files against the stored archive. A complete source bundle needs the pinned library submodules and the build recipe; the upstream top-level archive alone isn't enough.

Fonts and icons Portico ships:

| Distributed font/art | Licence and local notice |
| --- | --- |
| Manrope web interface font | OFL-1.1, `web/src/kit/fonts/OFL.txt` |
| Liberation Sans 2.00.5 subtitle default | OFL-1.1, `third_party/libass-wasm/OFL-1.1.txt` |
| Noto Sans Arabic, Hebrew, Thai, Devanagari, Bengali, Tamil | OFL-1.1, `third_party/subtitle-fonts/OFL-1.1.txt` |
| Noto Sans CJK JP, decoder fallback and browser subtitles (served by the server) | OFL-1.1, `server/internal/mediaexec/fonts/NotoCJK-LICENSE` |
| Decoder DejaVu fonts | Bitstream Vera/Arev font terms, DejaVu changes public domain; `server/internal/mediaexec/fonts/LICENSE` |
| Lucide icons | ISC, `packages/design/LICENSE-LUCIDE` |

Each folder's provenance file records where its fonts came from and their hashes. The browser's fallback fonts don't cover every script.

## rclone, for remote storage

Portico ships rclone **1.75.2** (MIT), upstream source revision `be8d6044eacb843b74d335db0c5fbe09222031fe`, as a separate program.

- [The lock file](third_party/rclone/bundle.lock.json) pins all six Linux, macOS and Windows amd64/arm64 archives, their signed checksums, and the source and vendor archives.
- The build checks the release against upstream key `FBF737ECE9F8AB18604BD2AC93935E02FF3B54FA` and verifies every extracted byte.
- [The MIT licence](third_party/rclone/LICENSE) and [dependency notices](third_party/rclone/DEPENDENCY-NOTICES.txt) ship with every payload, installer and image. The dependency notices keep the vendored module list and all 341 upstream licence, copyright and notice files, covering every platform. The upstream binary archives include a README, manual and Git history but no complete module notices file of their own, so all of those archive files are kept too.
- The lock file records the source and vendor URLs and their SHA-256 hashes.

If you point Portico at your own rclone, it needs at least **1.70.0**, the first version with `--max-connections`. (`--vfs-read-chunk-streams` needs 1.68.0, and `--vfs-cache-min-free-space` needs 1.64.0.) The pinned flag audit and the release changelog show that the other read-only serve, range, cache, retry, logging, pacing and credential options Portico uses are all older than 1.70.0. See [the bundle contract](third_party/rclone/README.md). Portico verifies the rclone it ships through build provenance hashes. A custom rclone is only checked by running `version`, with a five-second limit, and isn't approved file by file.

## FFmpeg and FFprobe on the server

The server runs these as separate programs. The qualified server build is FFmpeg **8.1.2**, component release `ffmpeg-8.1.2-portico.1`, pinned in [third_party/ffmpeg/sources.lock.json](third_party/ffmpeg/sources.lock.json).

- Linux and Windows builds use the BtbN recipe at revision `48576f197ad1c2afb2e0b8efe204919a1afbff54`. macOS uses the Homebridge recipe at `4920fab9d6ac56999ce7e961b240659b4da078a5`. Both apply the recorded Portico patches.
- The build must use `--enable-gpl --enable-version3`, and can't use `nonfree` or libfdk-aac. The result is GPL-3.0-or-later. The codecs and filters compiled into it keep their own notices.

Each qualified bundle's `licenses` folder and source archive are the real inventory for that platform's build, including the external libraries its recipe pulls in. Packaging carries the bundle over unchanged. See [third_party/ffmpeg/NOTICE.md](third_party/ffmpeg/NOTICE.md). This repository holds the recipes, not the finished bundles, so it can't list every dependency of a release that hasn't been inspected. Don't swap in an FFmpeg from your PATH and call it the qualified build.

## The media engine in the apps

The phone, tablet and TV apps use a second player engine, mpv, which Portico builds from pinned source in `third_party/media-engine/`. Every source URL and SHA-256 is in its `sources.lock.json`. It isn't part of the Go server.

The same recipe builds for Android (phones, Android TV and Fire TV) and Apple (iPhone, iPad and Apple TV). Each library is its own replaceable dynamic library: a `.so` on Android, a framework on Apple. The whole engine is LGPL-2.1-or-later. FFmpeg is configured without `--enable-gpl`, `--enable-version3` or `--enable-nonfree`, and mpv is built with `-Dgpl=false`. It uses the same FFmpeg version as the server.

| Component | Version | Licence |
| --- | --- | --- |
| FFmpeg (decoders, demuxers, parsers; no encoders or muxers) | 8.1.2 | LGPL-2.1-or-later |
| OpenSSL | 3.5.9 | Apache-2.0 |
| mpv (libmpv) | 0.41.0 | LGPL-2.1-or-later |
| libass | 0.17.5 | ISC |
| FreeType | 2.15.0 | FTL |
| HarfBuzz | 14.6.0 | MIT |
| FriBidi | 1.0.17 | LGPL-2.1-or-later |
| libunibreak | 8.0 | Zlib |
| dav1d | 1.5.4 | BSD-2-Clause |
| libplacebo | 7.360.1 | LGPL-2.1-or-later |
| Little CMS 2 | 2.19.1 | MIT |
| libdovi | 3.4.0 | MIT |
| uchardet | 0.0.8 | LGPL-2.1-or-later (tri-licence) |
| libiconv (Android only) | 1.19 | LGPL-2.1-or-later |
| MoltenVK (Apple only; Khronos release build) | 1.4.2 | Apache-2.0 |
| glslang (Apple only; linked into libplacebo) | 16.6.0 | BSD-3-Clause and others |

zlib, and the C++ runtime on Android, come from the platform. The engines the apps used before (MPVKit on Apple, media-kit's libmpv on Android) are gone, and nothing from them ships. That means no GnuTLS, Nettle, GMP, mbedTLS, libbluray, libsmbclient or uavs3d, and `verify.py` fails any build that contains them.

`third_party/media-engine/collect-notices.sh` produces the full licence texts and the matching source bundle each app release has to carry. Each app also lists the engine's components under Settings › About › Open-source licenses. This section is a summary. It doesn't prove that any particular app release carries its notices and source.

## Container image and release tools

The Dockerfile installs these Debian packages:

- bubblewrap (LGPL-2.0-or-later)
- tini (MIT)
- tzdata (public domain and BSD notices)
- CA certificates (Mozilla's certificates plus Debian packaging notices)
- libva (MIT)
- Mesa VA drivers (MIT and per-file permissive notices)
- on amd64, the Intel media and i965 drivers (MIT and their bundled notices)

Debian runtime libraries and their dependencies keep their notices in `/usr/share/doc/<package>/copyright`, and the image's SBOM is the full inventory. The Node build stages include Node.js (MIT, plus bundled notices) and npm (Artistic-2.0, plus its dependencies), and the Go build stages include Go (BSD-3-Clause). Those build stages aren't copied wholesale into the runtime image.

Release tooling includes:

- Python cryptography 49.0.0 (Apache-2.0 or BSD-3-Clause, with bundled dependencies)
- nFPM 2.43.4 (MIT)
- NSIS (zlib/libpng licence for the core, with component notices)
- Meson 1.7.0 (Apache-2.0)
- Ninja (Apache-2.0)
- setuptools (MIT)

Compilers, OS packaging tools and GitHub Actions are build infrastructure. Their own distributions carry their notices, and using them doesn't mean their code is bundled into Portico.

## Sonic music analysis

The bundled musicnn MTT model is by Jordi Pons and Xavier Serra, redistributed under the author's ISC licence, which is included unchanged with the model. It was trained on MagnaTagATune (Law et al., 2009). The model is converted from the pinned original weights, and no Essentia models are included.

ONNX Runtime 1.20.1 is MIT licensed, and its original LICENSE and ThirdPartyNotices.txt ship with every Sonic bundle. The portico-sonic helper is GPLv3, and its source and build instructions are included. Like FFmpeg, it relies on the platform's C and C++ runtime.

## Online services

Portico ships with its own TMDB and TheTVDB application credentials. They identify Portico and are meant to be public; they aren't secrets belonging to server owners. The app shows each provider's attribution, and their data and API terms are separate from any software licence. Owners can supply their own credentials for the whole server. MusicBrainz, AniList and any subtitle services you configure have their own terms.

The Cast receiver loads Google's hosted Cast Application Framework script when it runs, under Google's SDK terms. It isn't copied into this repository, and Portico doesn't license it under the GPL. Access to these services, and Apple's store and distribution rights, are separate from this software inventory.
