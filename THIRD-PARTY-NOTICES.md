# Third-party notices

Portico Media Server is GPL-3.0-or-later. Components below retain their own
copyrights and licences. Licence names here are an inventory, not replacement
licence texts. Preserve the upstream notices and corresponding source/build
materials when distributing a binary. Versioned source files and lockfiles,
not a moving upstream default branch, identify an update.

## Go server

`server/go.mod` and `server/go.sum` pin the modules. `apikit` is Portico's own
module. Go itself and copied Go image-scaling code use BSD-3-Clause terms.

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

The two archive readers are the bounded comic readers: `rardecode` for CBR and
`sevenzip` for the CB7 worker. Both are pinned in `server/go.mod` as direct
requirements and are linked into official builds, which set `portico_rar7z` and
`portico_cb7` from `scripts/release-build-tags.py`. CB7 still requires a
supported platform and the bounded `archive7z` worker; the tag alone never opens
an in-process reader. Their licence texts are the packages' own unchanged
`LICENSE` files, not rewritten here.

The maintained SQLite driver's copied files retain upstream notices in
[server/internal/thirdparty/sqlite/LICENSE](server/internal/thirdparty/sqlite/LICENSE)
and provenance in [PORTICO.md](server/internal/thirdparty/sqlite/PORTICO.md).
The image scaler's BSD notice is
[server/internal/imagework/SCALE_LICENSE](server/internal/imagework/SCALE_LICENSE).

## Browser and build packages

The [npm dependency inventory](docs/dependency-licenses.md) lists every package
version and declared licence in the root and standalone web lockfiles, including
transitive, development and platform-optional packages. React/React DOM,
TanStack Router, Radix UI, QRCode and Noble cryptography are MIT; hls.js is
Apache-2.0. Build dependencies include TypeScript (Apache-2.0), Vite (MIT), and
platform-dependent bundler binaries under their recorded licences.

The patched hls.js build is `1.7.2-portico.1`, based on upstream `1.7.2` at
`dd669ec53b601aa9fab7a2a78c913e25018cdf18`. Its archive, patch, build recipe,
[licence](third_party/hls.js/LICENSE) and
[provenance](third_party/hls.js/provenance.json) are retained in `third_party/hls.js`.

## Reader libraries

The Reader dependencies are pinned in `packages/reader-core/package.json` and the
root lockfile. Full upstream licence texts and their hashes are retained in
[third_party/reader](third_party/reader/provenance.json) and copied into release
payloads under `licenses/reader`.

| Component | Pinned source | Licence |
| --- | --- | --- |
| Mozilla Foundation PDF.js (`pdfjs-dist`) | [6.4.299](https://github.com/mozilla/pdf.js/tree/v6.4.299) | [Apache-2.0](third_party/reader/pdfjs-LICENSE) |
| John Factotum Foliate.js | [78914aef4466eb960965702401634c2cb348e9b1](https://github.com/johnfactotum/foliate-js/tree/78914aef4466eb960965702401634c2cb348e9b1) | [MIT](third_party/reader/foliate-LICENSE) |
| Gildas Lormeau zip.js | 2.24.0 | [BSD-3-Clause](third_party/reader/zipjs-LICENSE) |
| Cure53 DOMPurify | 3.4.17 | [Apache-2.0](third_party/reader/dompurify-LICENSE), selected from the upstream Apache-2.0 OR MPL-2.0 choice |

The official Literata variable font is pinned to
[google/fonts 4e5f06dbb274a27ebe71ed54ea706b3ee40eabd9](https://github.com/google/fonts/tree/4e5f06dbb274a27ebe71ed54ea706b3ee40eabd9/ofl/literata).
Its unchanged [SIL Open Font License 1.1](packages/reader-core/assets/OFL.txt),
metadata and source receipt are in `packages/reader-core/assets`; the full licence
also travels in `licenses/reader/literata-OFL.txt`. The font SHA-256 is
`b41138c9373112f32abb589cc22e8674b06ed4048b0c513be922bdd26f274440`.

The necessary text-layer CSS subset in `packages/reader-core/src/pdf/adapter.ts`
is adapted from Mozilla PDF.js 6.4.299 under Apache-2.0 and retains its source
attribution. Portico supplies its own viewer chrome.

## Browser subtitle renderer and fonts

The unmodified `@jellyfin/libass-wasm` 4.2.4 archive and served files are in
`third_party/libass-wasm`. Source is
[JavascriptSubtitlesOctopus at the pinned revision](https://github.com/jellyfin/JavascriptSubtitlesOctopus/tree/2d4ec9b16a639f23733cd32982e09e0eb757f092).
The worker is WebAssembly; the JavaScript wrapper and worker glue are MIT.
The worker contains:

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

Full per-file copyrights and licence texts are in
[COPYRIGHT](third_party/libass-wasm/COPYRIGHT), with wrapper
[LICENSE](third_party/libass-wasm/LICENSE) and font
[OFL-1.1.txt](third_party/libass-wasm/OFL-1.1.txt). The web build copies these
beside the served renderer. `python3 -I third_party/libass-wasm/verify.py` checks
its bytes against the stored archive. The corresponding source must include
its pinned library submodules and build recipe; an upstream top-level archive
alone is not a complete source bundle.

| Distributed font/art | Licence and local notice |
| --- | --- |
| Manrope web interface font | OFL-1.1, `web/src/kit/fonts/OFL.txt` |
| Liberation Sans 2.00.5 subtitle default | OFL-1.1, `third_party/libass-wasm/OFL-1.1.txt` |
| Noto Sans Arabic, Hebrew, Thai, Devanagari, Bengali, Tamil | OFL-1.1, `third_party/subtitle-fonts/OFL-1.1.txt` |
| Noto Sans CJK JP, decoder fallback and browser subtitles (served by the server) | OFL-1.1, `server/internal/mediaexec/fonts/NotoCJK-LICENSE` |
| Decoder DejaVu fonts | Bitstream Vera/Arev font terms, DejaVu changes public domain; `server/internal/mediaexec/fonts/LICENSE` |
| Lucide icons | ISC, `packages/design/LICENSE-LUCIDE` |

Font file origins and digests are in each folder's provenance file. The
browser fallback set is not a claim of coverage for every script.

## Server rclone

Portico ships rclone **1.75.2** (MIT), upstream source revision
`be8d6044eacb843b74d335db0c5fbe09222031fe`, as a separate executable.
[The lock](third_party/rclone/bundle.lock.json) pins all six Linux, macOS and
Windows amd64/arm64 archives, their signed checksums, source and vendor archives.
The build authenticates the release with upstream key
`FBF737ECE9F8AB18604BD2AC93935E02FF3B54FA` and verifies every extracted byte.
[The MIT licence](third_party/rclone/LICENSE) and
[dependency notices](third_party/rclone/DEPENDENCY-NOTICES.txt) travel with every
payload, installer and image. The latter preserves the vendor module inventory
and all 341 upstream licence/copyright/notice files (a platform-inclusive superset);
the upstream binary archives carry README/manual and git history, not a separate
complete module notices file. All of those archive files are retained too.
The lock records the corresponding source and vendor URLs and SHA-256 digests.

The custom-program minimum is **1.70.0**: `--max-connections` first appears there;
`--vfs-read-chunk-streams` requires 1.68.0 and `--vfs-cache-min-free-space` 1.64.0.
The pinned help/flag audit and release changelog establish that the remaining
read-only serve, range, cache, retry, logging, pacing and environment-credential
options predate 1.70.0. See [the bundle contract](third_party/rclone/README.md).
Build provenance hashes authenticate distributed artifacts; custom programs are
checked only by running `version` for at most five seconds, without file approval.

## Server FFmpeg and FFprobe

The server invokes these as separate processes. The qualified server build is
FFmpeg **8.1.2**, component release `ffmpeg-8.1.2-portico.1`, pinned in
[third_party/ffmpeg/sources.lock.json](third_party/ffmpeg/sources.lock.json).
The Linux/Windows recipe is BtbN revision
`48576f197ad1c2afb2e0b8efe204919a1afbff54`; macOS is the Homebridge recipe at
`4920fab9d6ac56999ce7e961b240659b4da078a5`, with the recorded Portico patches.
Configuration requires `--enable-gpl --enable-version3` and forbids `nonfree`
and libfdk-aac. The resulting FFmpeg build is GPL-3.0-or-later. Its compiled
codec/filter dependencies retain their individual notices.

The qualified bundle's `licenses` directory and corresponding source archive
are the inventory for the actual platform build, including external libraries
chosen by that recipe. Package creation carries this bundle intact. Consult
[third_party/ffmpeg/NOTICE.md](third_party/ffmpeg/NOTICE.md). This checkout has
recipes rather than the completed platform bundles; it cannot establish the
complete dependency list of a release artifact that has not been inspected.
Do not substitute a PATH binary and describe it as the qualified build.

## Native media engine (separate client repository)

The phone, tablet and television apps carry a second player engine, mpv, built
by Portico itself from pinned source in the client repository
(`third_party/media-engine/`, every source URL and SHA-256 in its
`sources.lock.json`). It is not embedded in the Go server. The same recipe
builds Android (Android, Android TV, Fire TV) and Apple (iPhone, iPad, Apple
TV), each library as its own replaceable dynamic library (`.so` on Android, a
framework on Apple). The whole engine is LGPL-2.1-or-later: FFmpeg is
configured without `--enable-gpl`, `--enable-version3` or `--enable-nonfree`,
and mpv is built with `-Dgpl=false`. FFmpeg is the same version the server pins.

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

zlib and, on Android, the C++ runtime are the platform's own. The engines the
apps used before (MPVKit on Apple, media-kit's libmpv on Android) are no longer
used, and nothing from them ships: no GnuTLS, Nettle, GMP, mbedTLS, libbluray,
libsmbclient or uavs3d (`verify.py` fails a build that contains any of them).

The client repository's `collect-notices.sh` produces the full licence texts
and the corresponding source bundle a release must carry, and each app shows
the engine's components in Settings › About › Open-source licenses. This server
notice summarises; it does not prove that a particular native release carries
its notices and source.

## Container and release tools

The Dockerfile fetches Debian packages for bubblewrap (LGPL-2.0-or-later), tini
(MIT), tzdata (public-domain/BSD notices), CA certificates (Mozilla certificates
plus Debian packaging notices), libva (MIT), Mesa VA drivers (MIT and per-file
permissive notices), and on amd64 Intel media/i965 drivers (MIT and their
bundled notices). Runtime Debian libraries and their transitive dependencies
retain notices under `/usr/share/doc/<package>/copyright`; the image SBOM is the
artifact inventory. Node build images include Node.js (MIT plus bundled notices)
and npm (Artistic-2.0 plus dependencies); Go build images carry Go (BSD-3-Clause).
They are build stages, not copied wholesale into the runtime image.

Release tools include Python cryptography 49.0.0 (Apache-2.0 or BSD-3-Clause,
with bundled dependencies), nFPM 2.43.4 (MIT), NSIS (zlib/libpng licence for core,
with component notices), Meson 1.7.0 (Apache-2.0), Ninja (Apache-2.0), and
setuptools (MIT). Compiler, OS packaging tools and GitHub workflow actions are
build infrastructure; their upstream distributions retain their notices.
Their use is not a claim that their code is bundled into the Portico server.

## Sonic music analysis

The bundled musicnn MTT model is by Jordi Pons and Xavier Serra and is
redistributed under the author's ISC licence, included unchanged with the
model. It was trained on MagnaTagATune (Law et al., 2009). The model is converted
from the pinned original weights; no Essentia models are included.

ONNX Runtime 1.20.1 is distributed under MIT. Its original LICENSE and
ThirdPartyNotices.txt accompany every Sonic bundle. The portico-sonic helper
remains GPLv3; its corresponding source and build instructions are included.
The platform C and C++ runtime remains a system dependency, as for FFmpeg.

## External services

TMDB and TheTVDB application credentials identify Portico and are intentionally
distributed, not server-owner secrets. The UI carries provider attribution;
these services' data/API terms are separate from software licences. Owners can
supply their own server-wide credentials. MusicBrainz, AniList and configured
subtitle services have their own service/data terms.

The Cast receiver loads Google's hosted Cast Application Framework script at
runtime under Google's SDK terms. It is not copied into this repository and is
not GPL licensed by Portico. Service access and Apple store/distribution rights
must be checked separately from this software inventory.
