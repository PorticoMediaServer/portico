#!/usr/bin/env bash
# Build only against the separately verified ONNX Runtime SDK.
set -euo pipefail
if [ "$#" != 3 ]; then echo 'Usage: build-sonic-runtime.sh SDK BUILD OUTPUT' >&2; exit 2; fi
root="$(cd "$(dirname "$0")/.." && pwd)"
cmake -S "$root/third_party/portico-sonic" -B "$2" \
  -DORT_ROOT="$1" -DCMAKE_BUILD_TYPE=Release -DCMAKE_BUILD_WITH_INSTALL_RPATH=ON
cmake --build "$2" --parallel 2
install -m 755 "$2/portico-sonic" "$3"
