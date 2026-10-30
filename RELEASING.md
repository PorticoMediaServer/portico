# Public release process

This is a public description of how official Portico builds are produced. It
is not an invitation to publish unofficial builds under Portico's name; see
`TRADEMARKS.md`.

## Two separate release decisions

FFmpeg and Portico are deliberately released independently. A normal Portico
release reuses the currently approved FFmpeg toolchain. FFmpeg changes only
after Justin selects and tests a stable upstream version and manually starts
the **Qualified FFmpeg toolchain** workflow, which builds, verifies and
publishes the toolchain as its own GitHub Release, named by the `releaseTag`
in `third_party/ffmpeg/sources.lock.json`, together with its corresponding
source.

An application release starts from a reviewed `vMAJOR.MINOR.PATCH` Git tag.
The release workflow reruns server and web tests, downloads the exact pinned
FFmpeg component release, and builds:

- Windows x64 and ARM64 installers plus portable ZIPs;
- an Apple Silicon macOS PKG and a DMG carrying it;
- distribution-neutral Linux x64 and ARM64 `.tar.gz` bundles, DEB files, and RPM files; and
- a Linux amd64/arm64 image in GitHub Container Registry.

Each server binary is built from an isolated source snapshot and sealed with a
signed manifest (`scripts/build-server-release.py`; see
`server/api/release-signing.md`). Every shipped build takes its Go build tags
from `scripts/release-build-tags.py` (`release` plus the `portico_rar7z` and
`portico_cb7` reader capabilities); the same list is used to build and test the
release revision, the verifier refuses a manifest whose binary carries a
different set, and the container image uses the same list. The release workflow then writes SHA-256
checksums, an SBOM, build provenance, and a small update manifest before
publishing the GitHub Release. A final signed artifact inventory authenticates
the complete archives/installers after packaging, including web assets. The release body describes a development preview. The workflow sets
`prerelease: false` and `make_latest: true` so GitHub’s latest-release link works;
this does not mean the software has reached stable quality.

GitHub's `/releases/latest` URL is the permanent human-facing download link.
The versionless asset names and `portico-update-manifest.json` are the stable
machine-facing locations. Automatic installation remains disabled until
packages are signed and the update client has passed rollback testing.

Only a maintainer may create official releases. The operational checklist,
approval records, rollback steps, and service credentials are maintained in
Portico's private internal repository.

## What the signatures verify

`build-server-release.py` requires an injected Ed25519 private key and a matching
independently pinned public key; it refuses missing or unsafe key files and does
not invent a signing identity. macOS sealing records final binary digests after
code signing. `seal-release-artifacts.py` signs the finished artifact inventory.
These are distinct from Windows Authenticode, Apple notarization and installer
verification. The workflow does not establish that production signer custody or
an owner's independently trusted installer key has been provisioned.

The Docker build consumes the same pinned FFmpeg bundles through its named
`ffmpeg` build context. Its build steps fetch npm/Go dependencies, copy the
vendored browser subtitle assets, and install the runtime packages listed in
`Dockerfile`. See [third-party notices](THIRD-PARTY-NOTICES.md) for the inventory.
The build's test gates do not establish physical Apple/Android/GPU acceptance.
