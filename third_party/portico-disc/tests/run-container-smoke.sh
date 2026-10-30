#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
# Disposable runner-only source/helper check. No host packages or image builds.
set -euo pipefail
repo="$(cd "$(dirname "$0")/../../.." && pwd)"
: "${TMPDIR:?remote job TMPDIR is required}"
job_tmp="$(realpath "$TMPDIR")"
mkdir -p "$job_tmp/container-tmp"
container=""
cleanup() {
  if [[ -n "$container" ]]; then
    docker rm -f "$container" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT
trap 'exit 143' TERM HUP INT
# Resolve the real immutable public dependency image for this host proof and
# record it. This is not the canonical FFmpeg compiler/toolchain contract.
image="ubuntu:24.04"
docker pull "$image"
image_id="$(docker image inspect --format '{{.Id}}' "$image")"
docker image inspect --format '{{json .RepoDigests}}' "$image_id"
container="$(docker create --label portico=video-server-r29 --label portico.purpose=disc-host-source-check \
  --cpus=1 --memory=1g --pids-limit=256 --network=host \
  --mount "type=bind,source=$repo,target=/source,readonly" \
  --mount "type=bind,source=$job_tmp/container-tmp,target=/job" \
  --env TMPDIR=/job --env DEBIAN_FRONTEND=noninteractive --env JOB_UID="$(id -u)" --env JOB_GID="$(id -g)" \
  "$image_id" bash -c '
    set -euo pipefail
    trap "chown -R $JOB_UID:$JOB_GID /job" EXIT
    apt-get -o Acquire::Retries=0 -o Acquire::http::Timeout=15 -o Acquire::https::Timeout=15 update
    apt-get install --no-install-recommends -y build-essential ca-certificates git meson ninja-build pkg-config python3
    dpkg-query -W gcc meson ninja-build pkg-config git python3
    python3 /source/third_party/portico-disc/tests/qualify-pinned-host.py --work /job/disc-host --static
  ')"
set +e
docker start -a "$container"
attach_status=$?
set -e
container_status="$(docker inspect --format '{{.State.ExitCode}}' "$container")"
if [[ "$attach_status" -ne 0 && "$container_status" -eq 0 ]]; then
  exit "$attach_status"
fi
if [[ "$container_status" -ne 0 ]]; then
  exit "$container_status"
fi
# No image was built. Only this labelled container and its per-job bind scratch
# belong to the check; public dependency-image cache is not pruned here.
