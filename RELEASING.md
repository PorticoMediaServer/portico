# How Portico is released

This explains how official Portico builds are made, so you can see what you're installing. It isn't permission to publish your own builds under the Portico name; see [TRADEMARKS.md](TRADEMARKS.md).

The release workflow is `.github/workflows/release.yml`. Until v0.1.0 its releases are internal builds, numbered 0.0.N and marked NOT FOR USE.

Only the maintainer publishes official releases. The step-by-step checklist, approvals, rollback steps and credentials are kept in a private repository.

## Versions and build numbers

Until v0.1.0, every product (the server, the apps on every platform and the desktop app) shares one version. Internal releases are numbered 0.0.N; the first public release will be the v0.1.0 beta. A server and an app are only promised to work together at the same version.

- The person who starts a release chooses the version. `scripts/release-version.py` refuses anything that isn't 0.0.N, or that isn't higher than every earlier release.
- The build number is the release commit's position on `main` (`git rev-list --count`). It is the same for every product in a release, and it only rises.
- The source tree carries `0.0.0` placeholders. Only a release build stamps the real version:
  - server: `buildinfo.Version`;
  - Android: `-PporticoVersionName` and `-PporticoVersionCode`;
  - Apple: `MARKETING_VERSION` and `CURRENT_PROJECT_VERSION`;
  - desktop and Vega: `release-version.py stamp`.
- A build made by hand reports 0.0.0, never a real version.

## How a release is made

1. **Start.** The maintainer runs the **Release** workflow on `main` with a version (`0.0.N`).
   - It builds everything from that one commit.
   - The server-next default merge suite runs over the full module as a gate, both with default tags and with the CBR/CB7 capability tags. Separate media-tier and hardware qualification is not claimed by this gate. The macOS server build also runs it on macOS. Cross-compiled builds run no tests of their own. A separate nightly run checks for data races.
   - A pull request that changes the release machinery runs the same builds as a proof: version 0.0.0, with no signing, no seal and no draft.
2. **Seal.** Two steps wait for the maintainer's approval in the `release-seal` environment, the only place the release key is available. That's two approvals per release:
   - **The server builds**, one approval for every platform at once. Each build's own `manifest.json` and `manifest.sig` are signed. Not built yet: internal builds skip it.
   - **The inventory**, after everything is built. `release-manifest.json` and `.sig` list exactly the files of the release. The inventory never makes a server build trusted: each build is trusted only through its own sealed manifest.
3. **Draft.** The workflow ends at a draft GitHub release that names its commit. Publishing it creates the tag, and `release-published.yml` then pushes the attached container image to `ghcr.io/porticomediaserver/portico-server`. Nothing is pushed or published before the maintainer publishes.

**Internal builds (0.0.N)** are unsealed:
- Their files say `UNSEALED-NOT-FOR-USE`.
- Their update feed lists no build, so no server offers one as an update.
- Their gate runs server-next's default tier with the media tools the release ships (below), disc tests included.
- The server doesn't install updates today. When an updater exists, it must verify the sealed manifest.

**Signing:**
- Android: one Portico app signing key, shared by GitHub downloads and Google Play.
- Apple: App Store Connect automatic signing for iPhone, iPad and Apple TV, and Developer ID plus notarisation for the macOS server app, its installer package and its disk image.
- Each signing step skips, and the file names say so, when its credentials aren't configured.

## The media tools (FFmpeg)

The server runs three programs beside it: `ffmpeg`, `ffprobe` and `portico-disc`, Portico's Blu-ray and DVD reader. A release builds them from its own commit (`.github/workflows/media-tools.yml`, stage `bundle`, called by the release workflow) with `third_party/ffmpeg/build.sh`:

- **What they are.** FFmpeg from jellyfin-ffmpeg at the tag in `third_party/ffmpeg/upstream.lock`, built with jellyfin-ffmpeg's own builder, plus Portico's four FFmpeg patches, four library recipes and two patches that stop the disc libraries loading a decryption module. FDK AAC is removed, and the build refuses `--enable-nonfree`. The result is GPL-3.0-or-later. Nothing in it can decrypt a commercial disc.
- **Where.** Every server build (Linux x64 and ARM64, Windows x64, macOS on Apple Silicon) and the container image. The macOS app is code-signed and notarised with the tools inside it.
- **What travels with them.** Each server package has `third_party/ffmpeg/` whole: the three programs, `NOTICE.md`, `LICENSES/` (the GPL text and every built-in library's own licence text) and `manifest.json` (every library, its licence and exact source, and each file's SHA-256). The release builder refuses a bundle whose files differ from its manifest. Linux packages and the container image also carry music analysis (Sonic) in `third_party/ffmpeg/bin`: the pinned bundle from `third_party/portico-sonic/runtime.lock.json`, checked against its own manifest (`scripts/sonic-bundle.py`), with its licences and the helper's source.
- **The source offer.** Every release that carries them attaches their complete source: `jellyfin-ffmpeg-8.1.3-1-source.tar.gz` (the tagged source, its builder included), `portico-media-tools-<platform>-library-sources.tar.zst` (every library's source as built), and this repository's `third_party/ffmpeg` and `third_party/portico-disc` at the release commit (the patches, recipes, helper source and the build script). `build.sh prepare <platform>` lays them out the way they were built.
- **Changing FFmpeg** means changing `upstream.lock` (and the patches if they stop applying), then proving the build with `media-tools.yml` before a release takes it.

## The desktop app's bundled mpv

The Windows installer and the macOS app carry mpv, so they play files the app's web view can't (the CI's `installers` job builds them). Before a desktop release ships:

- **Licence and source.** mpv is GPL-2.0-or-later. The release must include mpv's licence text and a written offer of its source (or the exact source beside the release), covering mpv and every library bundled with it: on macOS that is everything in `Portico.app/Contents/Resources/mpv/lib`, MoltenVK included.
- **Not Portico's FFmpeg.** The bundled mpv uses the FFmpeg its package came with (Chocolatey on Windows, Homebrew on macOS), not Portico's own qualified FFmpeg build. Its codecs and fixes can differ from the server's.
- **Versions aren't pinned yet.** The job installs whatever Chocolatey and Homebrew offer that day (seen on 18 November 2026: mpv 0.41.0 from Chocolatey, and 0.41.0 or 0.42.0 from Homebrew), so two proof builds can carry different mpv. A release must pin the versions, or at least record them.
- **On macOS**, mpv draws through Vulkan, which a Mac only has through MoltenVK. The app ships MoltenVK and its manifest in `mpv/lib`, and sets `VK_DRIVER_FILES` (and `VK_ICD_FILENAMES`) when it starts mpv. Signing the app must cover those libraries too. The CI only proves that mpv's picture output opens on that driver: GitHub's macOS runners crash MoltenVK at the first frame, Homebrew's own mpv included. A picture on a real Mac still needs checking by hand.

## What a release builds

A Portico release is built from one commit on `main`; publishing its draft creates the `v0.0.N` tag. The release workflow builds the media tools, runs the server and web tests again, and produces:

- A Windows x64 installer, plus a portable ZIP (no Windows ARM64 build yet)
- An Apple Silicon macOS PKG, and a DMG that contains it
- Linux x64 and ARM64 `.tar.gz` bundles, DEB files and RPM files
- A Linux amd64/arm64 container image on GitHub Container Registry

Each server binary is built from an isolated copy of `server-next/` (`github.com/PorticoMediaServer/portico/server-next`, command `./cmd/portico-server`) and sealed with a signed manifest by [the release builder](scripts/build-server-release.py). The verifier checks its linked build identity and the Hosted Services root the binary uses. Every shipped build uses the same Go build tags from `scripts/release-build-tags.py`: `release`, plus the `portico_rar7z` and `portico_cb7` reader tags. The release is built and tested with those tags, and the verifier rejects any binary built with a different set.

The workflow then writes SHA-256 checksums, an SBOM and a small update manifest, and leaves a draft GitHub release; build provenance is written when it is published. A final signed inventory covers every finished archive and installer, including the web files.

## Download links

GitHub's `/releases/latest` link is the permanent download link for people. The release asset names don't include a version, and `portico-update-manifest.json` is the stable location for software that checks for updates.

Releases are marked as GitHub's "latest" so that link works. The release notes still describe each build as a preview. Being "latest" doesn't mean Portico has reached a stable release. Automatic updates stay off until packages are signed and update rollback has been tested.

## What the signatures prove

- `build-server-release.py` needs an Ed25519 private key plus a separately pinned public key that matches it. It refuses missing or unsafe key files, and never makes up a signing identity.
- On macOS, the sealed manifest records the final binary hashes after code signing.
- `seal-release-artifacts.py` signs the finished artifact inventory.

These signatures are separate from Windows Authenticode, Apple notarization and installer verification. They don't by themselves show that the production signing keys are held securely, or that you've set up a trusted installer key of your own.

## Container image

The release's container image is the two Linux packages' contents, media tools included, on the runtime base in the `Dockerfile` (its `unsealed` stage), which installs the runtime packages listed there. The full inventory is in [THIRD-PARTY-NOTICES.md](THIRD-PARTY-NOTICES.md).

Passing the release tests doesn't prove a build works on physical Apple or Android devices, or with a particular GPU.


## API compatibility evidence

Before the first stable 1.0 tag, complete the [actual-tool qualification matrix](docs/integration-qualification.md). Freeze
the reviewed producer schema/corpus with its exact client parser pin. Later
releases must pass both released-parser/current-producer and
current-parser/released-producer directions, including error/precondition,
continuation/resync, offline receipt and Cast projection coverage. Current
contract regeneration and synthetic unknown-field tests alone are insufficient.
No 1.0 compatibility baseline or qualified smaller automation vocabulary exists
yet; do not label development recordings or source research as release acceptance.
