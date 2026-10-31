# Installer qualification inputs

Installers consume an engine artifact independently of the server commit. They
never compile FFmpeg. `artifacts.lock.json` contains accepted pins and rejected
candidate evidence. An empty `engines` object means native installer tests are
blocked; a successful planner or control test does not qualify an installer.

Only qualification inputs are supported here. No final installers are uploaded.
Real engine releases and Portico release entries require the maintainer's
separate approval. Existing release signatures and verifiers are unchanged.

An accepted entry is keyed by target (`windows-x64`, `windows-arm64`,
`macos-arm64`) and records:

- `target`, `kind: portico-engine`, `purpose: qualification`, `engineSourceSHA`;
- `name: ci-intermediate-ffmpeg-TARGET-ENGINE_SHA-NOT-FOR-USE`;
- `archiveSHA256`, `bundleManifestSHA256`, `sourceArchiveSHA256`,
  `licensesSHA256` (SHA-256 of the sorted compact JSON map of licence paths to hashes);
- UTC `createdAt` and `expiresAt`, no more than 14 days apart;
- `origin`: `kind: github-actions`, `repository: PorticoMediaServer/portico`,
  numeric `runId`, `runAttempt`, and `artifactId`.

The exact Actions ZIP contains `README.txt`, `engine-receipt.json` and `bundle/`.
README is exactly `Qualification engine intermediate; NOT-FOR-USE; not a Portico release.`
with a final newline. The receipt records `schemaVersion: 1`, `name`, `target`,
`engineSourceSHA`, and `release: false`. The normalized bundle contains its
unchanged `toolchain-manifest.json`, executables, corresponding source archive,
contracts, provenance and licence texts. The source archive retains dependency
snapshots, recipes, patches and build scripts, including the checked disc source.
Full corresponding source and licence review precedes accepting a pin; intake
checks the known provenance inventory and exact hashes. Intake is not native
engine or compiler qualification.

Windows source inventory follows the generated target Dockerfile: every selected
dependency recipe and its exact command-hash source snapshot must be present.
The complete builder tree, including scripts disabled for that target, remains
in corresponding source; disabled stages do not require unused source downloads.
Before using those mounts, intake checks `source-build-receipt.json`, which must
be hashed by the externally pinned bundle manifest. It records schema version 1,
`release: false`, the target and pinned engine source SHA, `dockerfileSHA256`,
`sourceArchiveSHA256`, `configureScripts` (source-archive paths to SHA-256s), and
`recipeTreeSHA256`. The latter hashes the sorted compact JSON map of all recipe
files (`kind: file`, `sha256`, permission `mode`) and symlinks (`kind: symlink`,
`target`, permission `mode`), relative to `recipe/`; directories are omitted.
All eight engine/configuration/source-preparation scripts listed in intake are
required. Removing a stage and its source, or changing a script, cannot rewrite
the manifest-sealed build history. Intake never executes bundled build scripts.

Local consumption uses a copy of the exact locked Actions ZIP, with no GitHub
environment variables. From a dedicated worktree, with on-disk scratch:

```sh
python3 scripts/plan-packaging.py --context local --evidence /absolute/scratch/evidence
python3 scripts/fetch-engine.py --target windows-x64 --purpose qualification --archive /absolute/engine.zip --output /absolute/scratch/engine
python3 scripts/ci-unsigned-payload.py windows-x64 --context local --engine /absolute/scratch/engine --work /absolute/scratch/payload --evidence /absolute/scratch/evidence
```

The final command requires matching native OS/architecture, performs the existing
feature/disc/recipe gates, then builds the actual server/web and pinned rclone,
NSIS/portable ZIP or app/PKG/DMG. A local run never pretends to be GitHub Actions.
On shared build hosts, use the project's heavy-job lock for builds and hold a
separate slot for the Windows VM's entire powered lifetime. No install is implied
by construction; the receipt lists installation, sealed startup and OS signing as
unexercised. A Windows x64 VM does not qualify ARM64 execution.

The manual `compiler_probe: intel-mac-gmp` mode runs the reviewed Intel-only
GMP PIC assembly, upstream `make check`, and the unchanged Nettle `pkcs1-conv`
link on an Intel Mac. It skips installer jobs and retains only dependency
evidence/source for seven days. Passing that probe does not qualify an engine
or installer. Intel Mac remains an engine/compiler target only.

The local Windows x64 producer is separate from installer consumption:

```sh
python3 scripts/build-engine.py windows-x64 --state /absolute/owned/on-disk-state
python3 scripts/assemble-engine.py windows-x64 --raw /absolute/owned/on-disk-state/raw-ID/engine.zip --recipe /absolute/owned/on-disk-state/recipe --build-report /absolute/owned/on-disk-state/report.json --output /absolute/fresh/engine-construction
```

Run from a clean committed checkout; assembly must use that same source commit.
The producer acquires `portico-heavy D-engine` itself. Run long jobs in a
transient user service so a terminal restart cannot cancel them. State retains
source archives, incremental reports and an owned bounded BuildKit cache.
Independent dependency failures aggregate; failed prerequisites block their
descendants. The final compile uses the pinned recipe's exact expanded payload
inside that graph, with a fresh output export and unchanged GPLv3 configuration.
For a repeat-build control, `--rebuild-ffmpeg` forces the final engine stage to
compile again while retaining dependency caches. Each invocation retains a fresh
raw archive and report; compare the actual executable hashes from two fresh
compiles. A cache hit alone is not repeat-build acceptance.

The Dockerfile is hashed after final generation, copied to a content-addressed
read-only file outside the recipe tree, and re-hashed. Docker consumes that copy;
both the original and copy are checked around every build. Assembly seals its
captured hash, recipe tree,
configure-script hashes, source archive and engine commit. Compiler runtime
sources/notices come from the completed image used by the build and remain
required on cache hits. Normalization preserves the existing source-build
receipt in the manifest. Neither construction nor intake grants native engine,
ABI, repeat-build, installer or release acceptance; run the existing native gates
on matching hardware before proposing any accepted engine pin.

Notice collection by filename is a convenience and is not an exhaustive legal
inventory. The complete corresponding source ships alongside the collected texts.
