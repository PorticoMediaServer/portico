#!/usr/bin/env bash
set -euo pipefail
if [[ $# -lt 2 ]]; then echo "usage: $0 BUNDLE OS-ARCH [--execute-version]" >&2; exit 2; fi
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BUNDLE="$1"; TARGET="$2"; shift 2
exec python3 "$ROOT/scripts/rclone-bundle.py" verify "$BUNDLE" --target "$TARGET" "$@"
