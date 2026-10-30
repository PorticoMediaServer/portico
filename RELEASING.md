# How Portico is released

This explains how official Portico builds are made, so you can see what you're installing. It isn't permission to publish your own builds under the Portico name; see [TRADEMARKS.md](TRADEMARKS.md).

The release workflows are still being moved into this repository, so there are no releases here yet. This page describes how they work.

Only the maintainer publishes official releases. The step-by-step checklist, approvals, rollback steps and credentials are kept in a private repository.

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
