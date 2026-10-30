#!/bin/sh
# Runs a development server against a state directory of your choice, serving the
# web app this repository builds. Build both first:
#   (cd server && go build -o ../.scratch/portico-server ./cmd/server)
#   npm run build --workspace portico-web
# then: PORTICO_DEV_DIR=<a copy of some state> server/dev/run-demo.sh
set -e
ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)"
D="${PORTICO_DEV_DIR:?set PORTICO_DEV_DIR to a state directory (never a server you care about)}"
export PATH=/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin
export PORTICO_DATA_DIR="$D/state"
export PORTICO_BIND="${PORTICO_BIND:-0.0.0.0:32500}"
export PORTICO_WEB_DIR="${PORTICO_WEB_DIR:-$ROOT/web/dist}"
export PORTICO_BACKUP_DIR="$D/backups"
export PORTICO_BACKUP_KEY_DIR="$D/backup-keys"
export PORTICO_BACKUP_RECOVERY_KEY_DIR="$D/recovery-keys"
export PORTICO_RECORDINGS_DIR="$D/recordings"
export PORTICO_REQUEST_STANDARD_FOLDERS=true
# Verify provenance and every file before executing an auto-discovered bundle.
if [ -d "$ROOT/third_party/ffmpeg/bundle" ]; then
  python3 "$ROOT/scripts/verify-ffmpeg-manifest.py" "$ROOT/third_party/ffmpeg/bundle"
  export PORTICO_FFMPEG="${PORTICO_FFMPEG:-$ROOT/third_party/ffmpeg/bundle/bin/ffmpeg}"
  export PORTICO_FFPROBE="${PORTICO_FFPROBE:-$ROOT/third_party/ffmpeg/bundle/bin/ffprobe}"
fi
export PORTICO_FFMPEG="${PORTICO_FFMPEG:-$(command -v ffmpeg)}"
export PORTICO_FFPROBE="${PORTICO_FFPROBE:-$(command -v ffprobe)}"
export PORTICO_HOSTED_ORIGIN=https://web.getportico.tv
export PORTICO_HOSTED_KEY_ID=PwQmNdWoEa7mOhmv
export PORTICO_HOSTED_PUBLIC_KEY=Q0tcaHe-0we7kFV5t52op_meZ1ZH1Gp4SYBJ9vKXaV8
exec "$ROOT/.scratch/portico-server"
