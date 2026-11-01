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
   - The full server suite runs once, as a gate, the same way the merge checks run it. The macOS server build also runs it on macOS. Cross-compiled builds run no tests of their own. A separate nightly run checks for data races.
   - A pull request that changes the release machinery runs the same builds as a proof: version 0.0.0, with no signing, no seal and no draft.
2. **Seal.** Two steps wait for the maintainer's approval in the `release-seal` environment, the only place the release key is available. That's two approvals per release:
   - **The server builds**, one approval for every platform at once. Each build's own `manifest.json` and `manifest.sig` are signed. This step arrives with the qualified FFmpeg toolchain; internal builds skip it.
   - **The inventory**, after everything is built. `release-manifest.json` and `.sig` list exactly the files of the release. The inventory never makes a server build trusted: each build is trusted only through its own sealed manifest.
3. **Draft.** The workflow ends at a draft GitHub release that names its commit. Publishing it creates the tag, and `release-published.yml` then pushes the attached container image to `ghcr.io/porticomediaserver/portico-server`. Nothing is pushed or published before the maintainer publishes.

**Internal builds (0.0.N)** carry a development FFmpeg engine: upstream jellyfin-ffmpeg 8.1.3-1, pinned by hash in `scripts/development-engine.py`, with its source attached.
- That makes every server build unsealed.
- Its files say `UNSEALED-NOT-FOR-USE-ENGINE`.
- Its update feed lists no build, so no server offers it as an update.
- Its gate skips the few tests that need features only the qualified engine has (libsoxr for music conversion): `ci/quarantine.json` entries scoped to `engine: development`, each with a reason and an expiry. A qualified-engine release runs every test.
- Sealed releases need the qualified toolchain below.
- The server doesn't install updates today. When an updater exists, it must verify the sealed manifest.

**Signing:**
- Android: one Portico app signing key, shared by GitHub downloads and Google Play.
- Apple: App Store Connect automatic signing for iPhone, iPad and Apple TV, and Developer ID plus notarisation for the macOS server app, its installer package and its disk image.
- Each signing step skips, and the file names say so, when its credentials aren't configured.

## FFmpeg is released separately

Portico and its FFmpeg build are released on their own schedules.

- **A normal Portico release** reuses the currently approved FFmpeg build.
- **FFmpeg only changes** when I pick and test a new stable upstream version and start the **Qualified FFmpeg toolchain** workflow by hand. That workflow builds and checks FFmpeg, then publishes it as its own GitHub release, along with its source. The release is named after the `releaseTag` in `third_party/ffmpeg/sources.lock.json`.

## What a release builds

A Portico release starts from a reviewed `vMAJOR.MINOR.PATCH` Git tag. The release workflow runs the server and web tests again, downloads the exact pinned FFmpeg build, and produces:

- Windows x64 and ARM64 installers, plus portable ZIPs
- An Apple Silicon macOS PKG, and a DMG that contains it
- Linux x64 and ARM64 `.tar.gz` bundles, DEB files and RPM files
- A Linux amd64/arm64 container image on GitHub Container Registry

Each server binary is built from an isolated copy of the source and sealed with a signed manifest (`scripts/build-server-release.py`; see [release signing](server/api/release-signing.md)). Every shipped build uses the same Go build tags from `scripts/release-build-tags.py`: `release`, plus the `portico_rar7z` and `portico_cb7` reader tags. The release is built and tested with those tags, and the verifier rejects any binary built with a different set.

The workflow then writes SHA-256 checksums, an SBOM, build provenance and a small update manifest, and publishes the GitHub release. A final signed inventory covers every finished archive and installer, including the web files.

## Download links

GitHub's `/releases/latest` link is the permanent download link for people. The release asset names don't include a version, and `portico-update-manifest.json` is the stable location for software that checks for updates.

Releases are marked as GitHub's "latest" so that link works. The release notes still describe each build as a preview. Being "latest" doesn't mean Portico has reached a stable release. Automatic updates stay off until packages are signed and update rollback has been tested.

## What the signatures prove

- `build-server-release.py` needs an Ed25519 private key plus a separately pinned public key that matches it. It refuses missing or unsafe key files, and never makes up a signing identity.
- On macOS, the sealed manifest records the final binary hashes after code signing.
- `seal-release-artifacts.py` signs the finished artifact inventory.

These signatures are separate from Windows Authenticode, Apple notarization and installer verification. They don't by themselves show that the production signing keys are held securely, or that you've set up a trusted installer key of your own.

## Container image

The Docker build uses the same pinned FFmpeg build. It fetches npm and Go dependencies, copies in the bundled subtitle files and installs the runtime packages listed in the `Dockerfile`. The full inventory is in [THIRD-PARTY-NOTICES.md](THIRD-PARTY-NOTICES.md).

Passing the release tests doesn't prove a build works on physical Apple or Android devices, or with a particular GPU.
