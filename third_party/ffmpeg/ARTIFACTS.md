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
All nine engine/configuration/source-preparation scripts listed in intake are
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

Windows rav1e source preparation checks its original upstream Cargo.lock and
replaces it with the committed resolution pinned in `sources.lock.json`. Both
locks and their hashes remain in the source snapshot, together with every
vendored registry source and licence. Compilation uses `--locked --offline`;
changing the resolution requires a deliberate reviewed lock update.


## Preserved compiler inputs

`compiler-images.lock.json` records exact upstream and Portico mirror OCI indexes
for Linux x64, Windows x64 and Linux ARM64, including every platform descriptor,
BtbN's producing source commit and the job that published the exact digests.
The Linux x64 source builder pulls only its recorded Portico mirror; it has no
upstream/latest fallback. Windows and Linux ARM64 mirror entries are retained
for their corresponding hosted compiler adapters; their current cold source
builds are not silently changed by adding these entries.

The separate compiler-custody workflow inspects installed metadata without
compiling media. Only its main-branch manual preservation job can write packages.
Skopeo copies all manifests with preserve-digests, and the receiver rechecks the
index and each child manifest. Existing differently hashed destination tags are
refused. If registry preservation fails, an exact, checked OCI-layout archive is
retained as NOT-FOR-USE evidence for attachment to the engine release and a
reviewed transport pin; the code never substitutes another image. Copying an
index does not grant corresponding-source, compiler or engine qualification.
The copy utility's actual version is recorded; it cannot change accepted bytes.

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

The hosted Windows producer requests --repeat-compiles: both terminal FFmpeg
and portico-disc compiles force BuildKit's portico-ffmpeg stage to execute,
while compiler/dependency layers remain reusable. Two fresh export directories
must have the same actual three executable hashes and sealed source archive;
the first export and source are rechecked after round two. repeat-build.json
survives the raw package and bundle seal, and the producer retains the exact
compiler image, consumed Dockerfile and both compile-log hashes. This does not
grant compiler/runtime-source, native, installer or release acceptance. Source
controls are transport/refusal fixtures; actual repeated compilation must be
proved by the next naturally required hosted Windows build, without restarting
an otherwise usable development run solely for this receipt.

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


## Hosted cold toolchain runs

`ffmpeg-toolchain.yml` starts Windows x64, Linux x64/ARM64 and native macOS
ARM64 builds in parallel, with an optional Intel Mac attempt. Mac uses macos-15
(or macos-15-intel). Windows ARM64 is deferred. The initial workflow deliberately
measures cold builds before adding cross-job checkpoint caching. Its branch-only
push trigger bootstrapped the first run and is now removed. New runs use manual dispatch.
There is no publisher or release pin in this slice.

The explicit GitHub context checks the repository, workflow, hosted runner,
branch, exact checkout SHA and run/attempt. It does not call portico-heavy or
claim a local host is GitHub. Retained NOT-FOR-USE artifacts include cold phase
and total timings, resource observations, source/compiler receipts, failure logs
and complete constructed bundles with corresponding source and licences. Native
jobs consume only their exact run/target transfer and execute existing gates on
matching Windows/Linux/Mac hardware. Construction and successful native checks
still do not grant compiler, repeat-build, installer or release acceptance.

A later reviewed slice adds a main-only manual publisher, immutable prerelease
assets with latest=false and an exact asset/hash release intake path. Its generated
intake-lock change receives separate review. Existing qualification expiry and
NOT-FOR-USE policy remain unchanged. mini-1 is reserved for the Windows VM; the
already-running r7 engine is the sole exception and is not interrupted.


All Meson-using engine targets share scripts/requirements-ffmpeg-meson.txt:
Meson1.12.1's universal wheel is installed with require-hashes, only-binary and
no-deps. BtbN base images consume that same file; the immutable Linux compiler
uses an isolated build-tool environment for dependency compilation. macOS uses
its existing private Python environment. Receipts record the requirement/wheel
hashes and actual installed version; wrong-version receipts refuse. Neither the
compiler mirror nor any engine is relabelled by the tool installation.

Cold retries sample filesystem disk pressure every five seconds (short spikes
may be missed) and retain dependency/stage timings. These measurements do not
exist retroactively for the first cold run. A separate read-only PR-context pull
probe checks access to every preserved compiler using the actual PR token and
reviewed main pins. It does not execute PR code with registry credentials.
Windows recipe construction takes its Rust and cargo-c versions from
`third_party/media-engine/sources.lock.json`, currently 1.99.0 and 0.10.26.
The base installs those exact versions, uses Cargo's locked tool resolution,
refuses a different installed version, and retains the resulting tool receipt
with compiler source custody. The rustup bootstrap itself remains a separate
acceptance hold; this version alignment is not a claim of fully pinned bootstrap
inputs. It applies to the next required build, without restarting a running one.
The toolchain's `controlsOnly` dispatch exercises source/refusal tests and skips
both construction and native qualification; it cannot produce an engine.

ARM64 Linux's BtbN recipe also builds the required libdovi from the existing
checked archive, both Cargo locks and complete offline vendor. It uses the
shared C ABI hash and crate/Rust notice collector, preserves its runtime receipt
and notice tree through the static packaging variant, and selects the same
repository Rust/cargo-c versions as Windows. The standalone Linux x64 builder
and Windows default selection remain separate. This functional dependency
addition does not establish complete ARM compiler-source or native media
acceptance; those remain explicit qualification gates.
