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

The native macOS libdovi stage uses the same checked source archive, both Cargo
locks, C ABI header hash and shared crate/Rust notice collector as the other
platforms. `native-rust-tools.lock.json`, sealed through the source lock, selects
Rust compiler/Cargo/standard-library components and matching library source
from one exact official distribution manifest, plus the exact cargo-c crate and
its Cargo.lock. Tools install in a private prefix outside the source recipe;
complete libdovi and cargo-c registry vendors, original archives/locks and Rust
library source stay in corresponding source. Both builds run locked and offline
after source preparation. The receipt records the actual native compiler/target,
tool executable hashes and linked-runtime archive inventory; these build records
do not themselves establish native media or final engine acceptance.

Mac's required ZVBI uses the same Git commit as the existing pinned BtbN ZVBI
stage. A private source-built GNU gettext0.26 supplies its autoreconf tools;
the exact official source archive SHA-256 is in the sealed Mac extras lock.
No gettext runtime is added to the engine, and ZVBI retains static/PIC builds
with X, DVB, BKTR, NLS and proxy support disabled as in the checked BtbN recipe.
The complete tool/library sources and actual tool receipt remain in the source
archive. The source lock's Meson version now matches the shared1.12.1 wheel and
media-engine pin; a producer control refuses drift among those inputs.
The real wheel installation control is explicitly enabled in hosted tool jobs;
the general Python fixture suite stays offline.

Linux x64 libdovi also uses this private pinned Rust/cargo-c implementation,
not the preserved C compiler image's nightly Cargo setup. Its complete cargo-c
vendor, original lock/archive and matching Rust library source are retained
with a checked per-file inventory. The preserved C compiler digest stays the
same. Cargo runs with a private home and explicit target; target-only RUSTFLAGS
select the cross-linker while host build scripts/proc-macros use native cc.
Before libdovi, a real locked/offline fixture builds a host build script, host
proc-macro and target executable, records both linker invocations and executes
the binary. This control and the actual compiler hashes are receipts, not final
engine, ABI, hardware or compiler-source qualification. The dependency-only
workflow proves this boundary without rebuilding FFmpeg for custody alone.

Mac normalization carries only the already-selected Vulkan loader, MoltenVK
and their ICD. Loader aliases are copied as regular bytes; all non-system
Mach-O loads resolve to the contained selected libraries and all architectures
must exactly match the target. Owned install names/rpaths and the ICD library
path become bundle-relative before hashes and the engine manifest are sealed.
macos-runtime-build.json records original and rewritten hashes/load commands;
the native verifier independently rechecks the resulting closure. A native
fixture deletes its original prefix and executes from an unrelated directory
with loader/Vulkan overrides unset. This proves relocation of real Mach-O
libraries, not a physical Vulkan device or automatic driver discovery in every
application layout. App signing/notarisation must follow these rewrites; a later
signature changes sealed bytes and belongs in the app's reviewed seal flow.
This engine stage uses no signing identity, account or credential.

The pinned MoltenVK generic Mac target can produce a universal arm64/x86_64
dylib. For that selected library only, the packer extracts exactly the engine
target's native slice before rewriting load paths and sealing. It records the
original universal hash/architecture set and the selected slice hash. A missing
target, unknown architecture, wrong-only library or mixed Vulkan loader still
refuses; the final verifier requires one exact native architecture everywhere.
A real two-slice native fixture executes the selected chain after deleting its
original prefix. This adds no source, feature, tool or signing identity.

The Mac shaderc stage selects the existing shaderc_combined pkg-config module,
as the pinned BtbN stage does, and includes its system libc++ link dependency.
It retains the original combined module unchanged, records both metadata hashes
and the actual pkg-config flags in corresponding source, and adds no shared
shaderc runtime. The real Mac runtime audit must still show no external shaderc
load; metadata controls alone are not a library compilation or engine proof.

Mac source checkout retains the exact reviewed Git object SHA. When that object
is an annotated tag, HEAD must equal the commit peeled from that exact object;
no floating tag name or fallback is used. Corresponding source records the
original object, object type, checked-out commit and repository. A different
existing checkout is refused. This changes no source pin or selected bytes.

Mac extras execute in an unconditional subshell: their strict options, build
directory and tool environment do not leak into Homebridge's remaining code,
and their nonzero status still stops the parent. The final FFmpeg download is
replaced with the already checked, post-series patched source archive, copied
read-only under its SHA-256 filename. Its receipt binds the archive to the exact
source lock; absence or mutation refuses before any upstream download. The
recipe, receipt, original prepared archive and checking script remain in full
corresponding source. A hosted control runs the actual pinned download function
against an inert prepared-source sentinel and proves there is no fallback.

Failed Mac packing can retain `engine-prepack-<target>-<sourceSHA>-NOT-FOR-USE`
for **one day**. This is an opaque tar of the compiled prefix and complete
corresponding-source archive, plus the checked contract inputs. Its separate
receipt records the exact run/source/target, original prefix, file inventory,
source/input hashes and tar hash/size. Contained file symlinks and contained framework directory aliases (including
Versions/Current and Resources) are stored relative without dereferencing. External
links, cycles, other directory aliases and special files are refused. Capture is atomic and rechecks
inputs after writing. It runs only after all three tools and corresponding source
exist; a capture failure never hides the original build failure. This recovery
input is unqualified, unreleased and not ready for publication. It is not an
engine intake artifact. A future repacker must verify the exact artifact and
receipt hashes and restore safely before running normal packing and native gates;
this slice does not add a repacker or waive those gates.
