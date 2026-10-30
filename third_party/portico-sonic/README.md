`portico-sonic` is an out-of-process CPU inference helper. Build it against the
verified ONNX Runtime 1.20.1 CPU package using `-DORT_ROOT=/absolute/package`.
The ONNX Runtime MIT licence and third-party notices must accompany its shared
library in a distribution. The original model's ISC licence accompanies the
model. TensorFlow, Python and the converter are build-time tools only.

The helper loads one verified musicnn model, accepts float32LE 187x96 log-mel
patches on stdin and writes `PSNC0001` followed by one 200+50 float32LE result
per patch. It checks exact model metadata, tensor shapes, finite values and
bounded patch count. It receives no recording path, network URL, database or
profile identity. The server confines it through mediaexec, supplies a fixed
model descriptor and owns cancellation/resource limits.

No architecture or availability claim follows from source compilation. The
server's actual load/inference check must succeed, including on x64 without
AVX2 and ARM64. A missing library/model or failed child is an explicit Sonic
unavailable state; the server keeps working and genre discovery remains usable.

Runtime source/licence: https://github.com/microsoft/onnxruntime/tree/v1.20.1

The offline source build uses these separate inputs:

1. Stage the exact original publisher wheel with
   `scripts/prepare-musicnn-model.py CHECKPOINT --wheel WHEEL`.
2. In CPython3.11.11 with `third_party/musicnn/requirements-export.txt`, run
   `scripts/export-musicnn-onnx.py CHECKPOINT MODEL_DIRECTORY`. Conversion checks
   numeric parity against the original saved graph and refuses to publish a
   model whose bytes differ from the checked model lock.
3. Stage the original runtime SDK with
   `scripts/prepare-sonic-runtime.py SDK --archive ARCHIVE --target linux-x64`.
4. Configure/build this directory using that absolute SDK path, then install the
   helper. The deployment closure also needs the verified runtime shared library,
   its original MIT licence and third-party notices, plus the converted model
   directory and its original ISC licence. Compilation alone is insufficient.

The server resolves the helper beside FFmpeg and the model in its sibling
`musicnn-mtt` directory. Explicit installation paths use `PORTICO_SONIC_HELPER`
and `PORTICO_SONIC_MODEL_DIRECTORY`; extra runtime libraries use
`PORTICO_SONIC_LIBRARIES`. These operator paths are never recording inputs.
Missing installations leave ordinary playback and genre discovery available.

The original Linux AArch64 1.20.1 SDK is also pinned and accepted by the staging
tool with `--target linux-aarch64`. Its bounded archive, original MIT licence,
third-party notices and AArch64 ELF identity have been checked. This is a
dependency-source qualification, not ARM helper compilation or actual inference
qualification. ARM inference and x86 CPUs without AVX2 still require their real
out-of-process conformance runs; no installation becomes ready from this pin.

The admitted general-job recipe `scripts/run-sonic-cpu-conformance.sh` stages
those exact inputs and compares three real helper outputs against the native
reference. Its default is pre-AVX Nehalem emulation. Set
`PORTICO_SONIC_TEST_CPU=arm64` for a job-local cross-compiled helper and Cortex-A53
emulation using the verified AArch64 SDK. The cross compiler and sysroot are
extracted only into the job, with no host installation. An emulated pass does
not qualify physical ARM hardware or an installed server/runtime closure.

Conformance jobs supply `PORTICO_SONIC_HELPER_TEST` and
`PORTICO_SONIC_MODEL_TEST` for actual supervised inference/cancellation tests.
The media-tier `TestSonicActualConfinedCueDecodeAndSingleAllocation` additionally
uses the real FFmpeg range decoder. It tests source/decoder/model composition;
it is separate from the owner/job publication and discovery contracts.

## Linux distribution

The manual `sonic-toolchain.yml` workflow exports the original model once, then
builds the helper natively on Linux x64 and ARM64. Each extracted bundle runs
against outputs of the original TensorFlow checkpoint and through the supervised
Go runtime before publication. `runtime.lock.json` names the non-latest release.

The reviewed model export is itself published once per pin: the workflow first
tries `sonic-bundle.py model-fetch`, which verifies and stages the pinned
`musicnn-mtt-<version>.tar.xz` from the model release named by
`third_party/musicnn/model.lock.json` `releaseTag`. Only when that release does
not exist yet does the job export the original wheel and publish it, never
replacing an existing pin. Later runs therefore reuse exact reviewed bytes
instead of installing TensorFlow, and the published bundles cannot drift from
the committed model pin.

`build-sonic-runtime.sh SDK BUILD OUTPUT` uses only the SDK already verified by
`prepare-sonic-runtime.py`. CMake sets `$ORIGIN/lib` for the packaged SDK library.
`sonic-bundle.py` reads the actual ELF NEEDED closure and packages its ONNX
libraries/aliases. The OS C/C++ runtime remains external; every observed system
library is recorded. No SDK headers or converter/training packages ship.

The bundle contains the helper, `lib/`, `musicnn-mtt/{model.onnx,model.json,LICENSE.md}`,
original ONNX MIT licence/notices, the Portico GPLv3 licence and corresponding
helper source/build recipe. Sonic's manifest is verified separately before its
files are composed beside `bin/ffmpeg`; the original FFmpeg manifest is never
changed. Both inventories and the exact installed union are then checked.

Missing optional Sonic inputs in a development/server build leave the existing
unavailable state. Linux release and Docker jobs explicitly require their pinned
Sonic bundle. macOS and Windows packaging are later work.

A byte-different export that passes original-checkpoint parity is retained as a
build artifact before the source pin check. It cannot be packaged or loaded
until its exact byte pin is reviewed and committed. A later manual workflow can
set `modelRunId` to reuse those exact bytes: only an ancestor MAIN run of this
same repository/workflow with byte-identical exporter/original-weight inputs
is accepted. This avoids re-exporting a model while updating its source pin.

The reviewed export is published once per pin, under
`third_party/musicnn/model.lock.json` `releaseTag`, as a non-latest release
holding `musicnn-mtt-<version>.tar.xz` and `checksums.sha256`. The archive
contains only the model trio, the original-checkpoint conformance reference and
the pin itself. `sonic-bundle.py model-fetch` verifies and atomically stages it;
`model-package` re-creates it only from already-verified bytes; `model-verify`
checks a staged directory. Later workflow runs reuse that published export
instead of installing TensorFlow again, and a missing release still exports the
original wheel exactly as before. The export is an input pin, not an
installation, and it enables no inference on its own.
