#!/usr/bin/env bash
# General admitted-job recipe. All installs/downloads stay in that job's TMPDIR;
# an optional caller-owned dependency cache may be supplied explicitly.
set -euo pipefail
# Distribution mode verifies the actual already-built bundle on its native
# architecture, using references exported once from the original checkpoint.
if [ "${1:-}" = --bundle ]; then
  if [ "$#" != 4 ]; then echo 'Usage: --bundle DIRECTORY REFERENCE TARGET' >&2; exit 2; fi
  root="$(cd "$(dirname "$0")/.." && pwd)"
  bundle="$(realpath "$2")"
  python3 "$root/scripts/check-sonic-bundle-conformance.py" --bundle "$bundle" --reference "$3" --target "$4"
  export PORTICO_SONIC_HELPER_TEST="$bundle/portico-sonic" PORTICO_SONIC_MODEL_TEST="$bundle/musicnn-mtt"
  (cd "$root/server" && GOMAXPROCS=1 GOMEMLIMIT=1200MiB GOFLAGS="-p=1 -trimpath" \
    go test -count=1 ./internal/sonic -run '^TestRuntimeActual(CPUAndCurrentVerifiedModel|CancellationRetiresHelperAndInput)$')
  exit
fi
: "${TMPDIR:?Run inside an admitted isolated build job}"
ulimit -c 0
cpu="${PORTICO_SONIC_TEST_CPU:-no-avx}"
case "$cpu" in no-avx|arm64) ;; *) echo "Unsupported conformance CPU" >&2; exit 2 ;; esac
cd "$(dirname "$0")/.."
export CI=1 OMP_NUM_THREADS=1 TF_NUM_INTRAOP_THREADS=1 TF_NUM_INTEROP_THREADS=1
export UV_CACHE_DIR="${PORTICO_SONIC_TEST_CACHE:-$TMPDIR/scoped-cache}/uv"
export UV_PYTHON_INSTALL_DIR="${PORTICO_SONIC_TEST_CACHE:-$TMPDIR/scoped-cache}/python"
work="$TMPDIR/sonic-cpu"
mkdir "$work"
export PORTICO_SONIC_TEST_WORK="$work"
python3 - <<'PY'
import hashlib, io, json, os, pathlib, urllib.request, zipfile
mem = {s.split(":")[0]: int(s.split()[1])*1024 for s in open("/proc/meminfo") if s.startswith("MemAvailable:")}
assert mem["MemAvailable"] >= 3*(1 << 30), "CPU/model job needs 3GiB actual headroom"
root = pathlib.Path(os.environ["PORTICO_SONIC_TEST_WORK"])
with urllib.request.urlopen("https://pypi.org/pypi/uv/0.9.3/json", timeout=20) as response:
    metadata = json.load(response)
assert metadata["info"]["version"] == "0.9.3"
asset = next(a for a in metadata["urls"] if a["filename"].endswith("py3-none-manylinux_2_17_x86_64.manylinux2014_x86_64.whl"))
with urllib.request.urlopen(asset["url"], timeout=60) as response:
    data = response.read((50 << 20)+1)
assert len(data) == asset["size"] and hashlib.sha256(data).hexdigest() == asset["digests"]["sha256"]
with zipfile.ZipFile(io.BytesIO(data)) as archive:
    member = next(i for i in archive.infolist() if i.filename.endswith("/scripts/uv"))
    assert 0 < member.file_size <= 128 << 20, (member.filename, member.file_size)
    (root / "uv").write_bytes(archive.read(member))
    (root / "uv").chmod(0o700)
print("Verified job-local uv 0.9.3 ready")
PY
mkdir "$work/qemu-download" "$work/qemu"
(cd "$work/qemu-download" && apt-get download -qq qemu-user-static qemu-user)
for package in "$work"/qemu-download/*.deb; do dpkg-deb -x "$package" "$work/qemu"; done
qemu_arch=x86_64
if test "$cpu" = arm64; then qemu_arch=aarch64; fi
qemu_binary="$work/qemu/usr/bin/qemu-$qemu_arch-static"
if ! test -x "$qemu_binary"; then qemu_binary="$work/qemu/usr/bin/qemu-$qemu_arch"; fi
"$qemu_binary" --version
"$work/uv" venv --quiet --python 3.11.11 "$work/venv"
"$work/uv" pip install --quiet --no-progress --python "$work/venv/bin/python" -r third_party/musicnn/requirements-export.txt
python3 - <<'PY'
import json, os, pathlib, urllib.request
root = pathlib.Path(os.environ["PORTICO_SONIC_TEST_WORK"])
model = json.load(open("third_party/musicnn/model.lock.json"))["distribution"]
runtime = json.load(open("third_party/portico-sonic/runtime.lock.json"))["packages"]["linux-x64"]
for name, record in [("model.whl", model), ("runtime.tgz", runtime)]:
    with urllib.request.urlopen(record["url"], timeout=90) as response:
        data = response.read((100 << 20)+1)
    assert len(data) <= 100 << 20
    (root / name).write_bytes(data)
PY
python3 scripts/prepare-musicnn-model.py "$work/checkpoint" --wheel "$work/model.whl"
python3 scripts/prepare-sonic-runtime.py "$work/sdk" --archive "$work/runtime.tgz" --target linux-x64
"$work/venv/bin/python" scripts/export-musicnn-onnx.py "$work/checkpoint" "$work/model"
c++ -std=c++17 -O2 -Wall -Wextra -Werror -fno-fast-math -ffp-contract=off \
  -I"$work/sdk/include" third_party/portico-sonic/main.cpp -L"$work/sdk/lib" \
  -Wl,-rpath,"$work/sdk/lib" -lonnxruntime -o "$work/portico-sonic"
if test "$cpu" = arm64; then
  python3 - <<'PYARM'
import json, os, pathlib, urllib.request
root = pathlib.Path(os.environ["PORTICO_SONIC_TEST_WORK"])
record = json.load(open("third_party/portico-sonic/runtime.lock.json"))["packages"]["linux-aarch64"]
with urllib.request.urlopen(record["url"], timeout=90) as response:
    data = response.read((100 << 20)+1)
assert len(data) == record["bytes"]
(root / "arm-runtime.tgz").write_bytes(data)
PYARM
  python3 scripts/prepare-sonic-runtime.py "$work/arm-sdk" --archive "$work/arm-runtime.tgz" --target linux-aarch64
  mkdir "$work/cross-download" "$work/cross"
  # Use the distribution's selected cross compiler, extracted only into this
  # admitted job. No package is installed and no host configuration changes.
  major=$(apt-cache depends g++-aarch64-linux-gnu | sed -nE 's/.*Depends: g\+\+-([0-9]+)-aarch64-linux-gnu$/\1/p' | head -n 1)
  test -n "$major"
  (cd "$work/cross-download" && apt-get download -qq \
    "g++-$major-aarch64-linux-gnu" "gcc-$major-aarch64-linux-gnu" "cpp-$major-aarch64-linux-gnu" \
    "libgcc-$major-dev-arm64-cross" "libstdc++-$major-dev-arm64-cross" \
    binutils-aarch64-linux-gnu libc6-dev-arm64-cross libc6-arm64-cross \
    libstdc++6-arm64-cross libgcc-s1-arm64-cross linux-libc-dev-arm64-cross)
  for package in "$work"/cross-download/*.deb; do dpkg-deb -x "$package" "$work/cross"; done
  LD_LIBRARY_PATH="$work/cross/usr/lib/x86_64-linux-gnu:$work/cross/usr/lib" \
  "$work/cross/usr/bin/aarch64-linux-gnu-g++-$major" --sysroot="$work/cross" \
    -B"$work/cross/usr/lib/gcc-cross/aarch64-linux-gnu/$major/" -B"$work/cross/usr/bin/" \
    -idirafter "$work/cross/usr/aarch64-linux-gnu/include" \
    -std=c++17 -O2 -Wall -Wextra -Werror -fno-fast-math -ffp-contract=off \
    -I"$work/arm-sdk/include" third_party/portico-sonic/main.cpp \
    -L"$work/cross/usr/aarch64-linux-gnu/lib" -L"$work/arm-sdk/lib" \
    -Wl,-rpath,"$work/arm-sdk/lib" -lonnxruntime -o "$work/portico-sonic-arm64"
  python3 scripts/check-sonic-cpu-baseline.py --helper "$work/portico-sonic" \
    --model "$work/model/model.onnx" --qemu "$qemu_binary" \
    --arm-helper "$work/portico-sonic-arm64" --sysroot "$work/cross/usr/aarch64-linux-gnu"
else
  python3 scripts/check-sonic-cpu-baseline.py --helper "$work/portico-sonic" \
    --model "$work/model/model.onnx" --qemu "$qemu_binary"
fi
python3 -m unittest discover -s scripts -p test_prepare_sonic_runtime.py

# Exercise the actual server aggregation/normalization and cancellation boundary.
export PORTICO_SONIC_HELPER_TEST="$work/portico-sonic" PORTICO_SONIC_MODEL_TEST="$work/model"
(cd server && GOTOOLCHAIN=go1.26.10 GOMAXPROCS=1 GOMEMLIMIT=1200MiB GOTMPDIR="$TMPDIR" GOFLAGS="-p=1 -trimpath" \
  go test -count=1 ./internal/sonic -run "^TestRuntimeActual(CPUAndCurrentVerifiedModel|CancellationRetiresHelperAndInput)$")
