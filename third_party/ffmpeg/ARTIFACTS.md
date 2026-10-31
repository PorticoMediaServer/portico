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
