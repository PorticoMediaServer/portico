#!/usr/bin/env bash
set -euo pipefail
[[ $# -ge 1 && $# -le 2 ]] || { echo 'usage: verify-ffmpeg-bundle.sh BUNDLE [full|limited]' >&2; exit 2; }
# limited is retained for existing callers; target requirements always apply.
[[ ${2:-full} == full || ${2:-full} == limited ]] || exit 2
python3 "$(dirname "$0")/verify-ffmpeg-manifest.py" "$1"
if [[ "$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["target"])' "$1/toolchain-manifest.json")" == linux-* ]]; then
  python3 "$(dirname "$0")/verify-ffmpeg-abi.py" "$1" --report "$1/../abi-report-$(basename "$1").json"
fi
if [[ "$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["target"])' "$1/toolchain-manifest.json")" == macos-* ]]; then
  python3 "$(dirname "$0")/macos-engine-runtime.py" verify --bundle "$1" --target "$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["target"])' "$1/toolchain-manifest.json")" --report "$1/../macos-runtime-report.json"
fi
python3 "$(dirname "$0")/verify-ffmpeg-features.py" "$1" --report "$1/../feature-report-$(basename "$1").json"
python3 "$(dirname "$0")/verify-disc-bundle.py" "$1" --fixtures "${PORTICO_DISC_FIXTURES:?Set PORTICO_DISC_FIXTURES to the checked disc fixture manifest}" --fixture-sha256 "${PORTICO_DISC_FIXTURES_SHA256:?Set the checked disc fixture manifest SHA256}" --report "$1/../disc-report-$(basename "$1").json"
python3 "$(dirname "$0")/verify-ffmpeg-recipes.py" "$1" --font "${PORTICO_FFMPEG_TEST_FONT:-$(dirname "$0")/../packages/reader-core/assets/Literata[opsz,wght].ttf}" --scratch "${TMPDIR:?Set TMPDIR to job scratch on disk}" --report "$1/../runtime-report-$(basename "$1").json"
