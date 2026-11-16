#!/usr/bin/env bash
# The compile check for the Roku app: BrighterScript's checker, used on plain BrightScript and the
# component XML. It needs node and the network (npx fetches the checker at the version named here).
set -euo pipefail
cd "$(dirname "$0")"
exec npx --yes --package brighterscript@0.69.13 bsc --project bsconfig.json
