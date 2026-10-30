#!/usr/bin/env bash
# Orchestrate the G07 phases. Each phase starts a fresh bounded container on a
# private Docker network with a container-private tmpfs and private volumes,
# keeps one private server state volume and library, samples at 100 ms, drives
# real conversions, then asserts the named gates.
#
# Phases: memory-2g memory-4g memory-8g parent-2g floor-1g disk-1g spill-fail
#         restart-after-spill
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=../lib/common.sh
. "$here/../lib/common.sh"

usage() {
  cat >&2 <<'USAGE'
usage: run.sh --execute --image IMG [options]
  --scratch DIR        default ~/astra-scratch/P2-65/disposable/g07
  --media DIR          existing generated media directory
  --phases a,b         default: all eight phases
  --sessions 3         2-4 conversions per phase
  --seconds 120        driving window per phase
  --port 32500         loopback-only server API port for the host driver
  --keep-state         keep the private server state volume after the run
USAGE
  exit 2
}

execute=0 image="" scratch="${HOME}/astra-scratch/P2-65/disposable/g07"
media="" phases="memory-2g,memory-4g,memory-8g,parent-2g,floor-1g,disk-1g,spill-fail,restart-after-spill"
sessions=3 seconds=120 port=32500 keep_state=0
while [ $# -gt 0 ]; do
  case "$1" in
    --execute) execute=1; shift ;;
    --image) image="$2"; shift 2 ;;
    --scratch) scratch="$2"; shift 2 ;;
    --media) media="$2"; shift 2 ;;
    --phases) phases="$2"; shift 2 ;;
    --sessions) sessions="$2"; shift 2 ;;
    --seconds) seconds="$2"; shift 2 ;;
    --port) port="$2"; shift 2 ;;
    --keep-state) keep_state=1; shift ;;
    -h|--help) usage ;;
    *) usage ;;
  esac
done
[ "$execute" = 1 ] || { echo "PLAN: would run phases $phases against a disposable host"; exit 0; }
[ -n "$image" ] || usage
[ "$sessions" -ge 2 ] && [ "$sessions" -le 4 ] || { echo "--sessions must be 2-4" >&2; exit 2; }

fixture_guard
fixture_need docker curl jq ffmpeg ffprobe python3 openssl stat nsenter
fixture_secret_dir
fixture_playback_inputs
fixture_evidence_dir
mkdir -m 700 -p "$scratch"
scratch="$(realpath "$scratch")"
case "$scratch" in
  "$FIXTURE_SCRATCH"/*) ;;
  *) fixture_refuse "phase scratch must stay under $FIXTURE_SCRATCH" ;;
esac
token_file="$FIXTURE_SECRETS/g07-token.json"
password_file="$FIXTURE_SECRETS/g07-owner-password"
[ -f "$password_file" ] || fixture_secret g07-owner-password 24 >/dev/null

media="${media:-$scratch/media}"
if [ ! -d "$media" ]; then "$here/media.sh" --out "$media"; fi
base="http://127.0.0.1:$port"

# Refuse a pre-existing listener/container rather than retiring someone else's fixture.
! docker container inspect portico-g07 >/dev/null 2>&1 || fixture_refuse "retained G07 container exists"
active_phase=""
# The fixture network is private to this run; no host network, no host DNS.
cleanup_fixture() {
  if [ -n "$active_phase" ]; then
    touch "$active_phase/stop-1" "$active_phase/stop-2" "$active_phase/stop-3"
    if [ -f "$active_phase/log/cleanup.sh" ]; then "$active_phase/log/cleanup.sh" || true;
    else docker rm -f portico-g07 >/dev/null 2>&1 || true; fi
  fi
  docker network rm p2-65-g07 >/dev/null 2>&1 || true
  if [ "$keep_state" != 1 ]; then
    docker volume rm -f portico-g07-config >/dev/null 2>&1 || true
  fi
}
trap cleanup_fixture EXIT

bootstrap() {
  if [ ! -f "$token_file" ]; then
    python3 "$here/../lib/api.py" bootstrap --base "$base" --token-file "$token_file" \
      --password-file "$password_file" --username fixture-owner --name "G07 fixture owner"
    return
  fi
  POC_FIXTURE_LIB="$here/../lib" python3 - "$base" "$token_file" <<'PY'
import os, sys
sys.path.insert(0, os.environ["POC_FIXTURE_LIB"])
import api
base, path = sys.argv[1], sys.argv[2]
client = api.Api.load(base, path)
try:
    client.request("GET", "/v1/server")
except api.ApiError as err:
    if err.status != 401:
        raise
    client.refresh()
    client.save_state(path)
PY
}

ensure_owner() {
  if [ -f "$token_file" ]; then
    if bootstrap; then return; fi
    # A stale token cannot belong to a freshly created fixture server.
    rm -f "$token_file"
  fi
  bootstrap
}

start_sampler() {
  local phase_dir="$1" n="$2" memdir="$3" diskdir="$4" sessions_csv="$5"
  local container_pid
  container_pid="$(docker inspect -f '{{.State.Pid}}' portico-g07)"
  python3 "$here/sample.py" --execute --pid "$container_pid" --base "$base" \
    --token-file "$token_file" --session-ids "$sessions_csv" \
    --memory-dir "$memdir" --disk-dir "$diskdir" --interval 0.1 --seconds 1800 \
    --stop-file "$phase_dir/stop-$n" --out "$phase_dir/samples-$n.jsonl" >"$phase_dir/sampler-$n.log" 2>&1 &
  echo $!
}

stop_sampler() {
  local stop="$1" pid="$2"
  touch "$stop"
  wait "$pid" 2>/dev/null || true
}

run_drive() {
  local fixture_file="$1" out="$2"; shift 2
  python3 "$here/drive.py" --execute --base "$base" --token-file "$token_file" \
    --fixture "$fixture_file" --sessions "$sessions" --seconds "$seconds" \
    --out "$out" "$@"
}

run_phase() {
  local phase="$1" memory tmpfs disk parent drive_extra=()
  local phase_dir="$scratch/$phase"
  case "$phase" in
    memory-2g) memory=2g; tmpfs=512; disk=0; parent=0 ;;
    memory-4g) memory=4g; tmpfs=512; disk=0; parent=0 ;;
    memory-8g) memory=8g; tmpfs=512; disk=0; parent=0 ;;
    parent-2g) memory=8g; tmpfs=512; disk=0; parent=2g ;;
    floor-1g) memory=1g; tmpfs=512; disk=0; parent=0 ;;
    disk-1g) memory=2g; tmpfs=0; disk=1024; parent=0 ;;
    spill-fail) memory=4g; tmpfs=512; disk=256; parent=0
      drive_extra=(--seek-every 6) ;;
    restart-after-spill) memory=4g; tmpfs=512; disk=0; parent=0
      drive_extra=(--spill-trigger "$phase_dir/spill" --kill-after-spill-ms 80) ;;
    *) echo "unknown phase $phase" >&2; return 2 ;;
  esac
  mkdir -m 700 -p "$phase_dir"
  printf 'phase %s: memory=%s tmpfs=%s disk=%s parent=%s\n' \
    "$phase" "$memory" "$tmpfs" "$disk" "$parent"

  active_phase="$phase_dir"
  set +e
  "$here/run-container.sh" --execute --image "$image" --state "$phase_dir" --media "$media" \
    --phase "$phase" --memory "$memory" --tmpfs-mib "$tmpfs" --disk-mib "$disk" \
    --disk-mode "$( [ "$disk" != 0 ] && echo loop || echo none )" \
    --parent-memory "$parent" --port "$port" --name portico-g07
  local start_rc=$?
  set -e
  if [ "$start_rc" = 4 ]; then
    local reason
    reason="$(cat "$phase_dir/log/blocked.txt" 2>/dev/null || echo "phase prerequisites are not met")"
    jq -n --arg phase "$phase" --arg reason "$reason" \
      '{phase:$phase,result:"BLOCKED-ENV",reason:$reason}' >"$phase_dir/assert.json"
    chmod 600 "$phase_dir/assert.json"
    return 0
  fi
  [ "$start_rc" = 0 ] || fixture_refuse "container start failed for $phase"

  ensure_owner
  local memory_path=""
  if [ "$tmpfs" != 0 ]; then memory_path=/memory-transcode; fi

  local sentinel="$phase_dir/sentinel.txt"
  printf 'unowned sentinel %s\n' "$(fixture_now)" >"$sentinel"
  chmod 600 "$sentinel"
  local sentinel_sha
  sentinel_sha="$(sha256sum "$sentinel" | cut -d' ' -f1)"

  local settings='{"conversionMode":"all","transcodeLocation":"memory","memoryCap":"2gb","temporaryDirectory":"/transcode","sessionStorageMiB":4096,"maxConcurrentSessions":4,"maxHardwareSessions":0,"maxSoftwareSessions":4}'
  case "$phase" in
    disk-1g) settings='{"conversionMode":"all","transcodeLocation":"disk","memoryCap":"2gb","temporaryDirectory":"/transcode","sessionStorageMiB":4096,"maxConcurrentSessions":4,"maxHardwareSessions":0,"maxSoftwareSessions":4}' ;;
    floor-1g) settings='{"conversionMode":"all","transcodeLocation":"memory","memoryCap":"4gb","temporaryDirectory":"/transcode","sessionStorageMiB":4096,"maxConcurrentSessions":4,"maxHardwareSessions":0,"maxSoftwareSessions":4}' ;;
  esac
  python3 "$here/prepare.py" --execute --base "$base" --token-file "$token_file" \
    --media-root /media --library-name "G07 fixture movies" --settings "$settings" \
    --out "$phase_dir/fixture.json"

  local sampler_pid
  sampler_pid="$(start_sampler "$phase_dir" 1 "$memory_path" /transcode "")"
  local drive_rc=0
  set +e
  run_drive "$phase_dir/fixture.json" "$phase_dir/drive-1.json" "${drive_extra[@]}"
  drive_rc=$?
  set -e
  stop_sampler "$phase_dir/stop-1" "$sampler_pid"
  [ "$drive_rc" = 0 ] || echo "drive exited $drive_rc for $phase" >&2

  local samples_arg="$phase_dir/samples.jsonl" drive_arg="$phase_dir/drive.json"
  local restart_args=()

  if [ "$phase" = spill-fail ]; then
    # The 256 MiB fixed filesystem is the ENOSPC fault point: the spill copy
    # exhausts it naturally. Nothing on the host is pre-filled.
    local sampler2
    sampler2="$(start_sampler "$phase_dir" 2 "$memory_path" /transcode "")"
    run_drive "$phase_dir/fixture.json" "$phase_dir/drive-2.json" --seconds 45
    stop_sampler "$phase_dir/stop-2" "$sampler2"
    cat "$phase_dir/samples-1.jsonl" "$phase_dir/samples-2.jsonl" >"$samples_arg"
  elif [ "$phase" = restart-after-spill ]; then
    local pre_session
    pre_session="$(jq -r '.sessions[0].id // empty' "$phase_dir/drive-1.json")"
    docker kill portico-g07 >/dev/null 2>&1 || true
    docker start portico-g07 >/dev/null
    local end=$((SECONDS + 180))
    until curl -fsS "$base/v1/server" >/dev/null 2>&1; do
      [ "$SECONDS" -lt "$end" ] || fixture_refuse "server did not return after restart"
      sleep 2
    done
    local branch
    branch="$(POC_FIXTURE_LIB="$here/../lib" python3 - "$base" "$token_file" "$pre_session" <<'PY'
import json, os, sys
sys.path.insert(0, os.environ["POC_FIXTURE_LIB"])
import api
base, token, session = sys.argv[1], sys.argv[2], sys.argv[3]
client = api.Api.load(base, token)
out = {"branch": "unknown", "spillsAfterRestart": None}
try:
    view = api.get_session(client, session)
    if view.get("state") in ("playing", "paused") and not view.get("end"):
        out["branch"] = "session-survived"
        out["generation"] = view.get("presentation", {}).get("generation")
    else:
        out["branch"] = "session-ended-disk-followup"
        out["endReason"] = (view.get("end") or {}).get("reason")
except api.ApiError as err:
    if err.status == 404:
        out["branch"] = "session-ended-disk-followup"
    else:
        out["branch"] = "read-failed"
        out["status"] = err.status
try:
    out["spillsAfterRestart"] = int(api.diagnostics(client).get("memorySpills", 0))
except api.ApiError:
    pass
print(json.dumps(out))
PY
)"
    local sampler3
    sampler3="$(start_sampler "$phase_dir" 3 "$memory_path" /transcode "")"
    settings='{"conversionMode":"all","transcodeLocation":"disk","memoryCap":"quarter","temporaryDirectory":"/transcode","sessionStorageMiB":4096,"maxConcurrentSessions":4,"maxHardwareSessions":0,"maxSoftwareSessions":4}'
    python3 "$here/prepare.py" --execute --base "$base" --token-file "$token_file" \
      --media-root /media --library-name "G07 fixture movies" --settings "$settings" \
      --out "$phase_dir/fixture-disk.json"
    run_drive "$phase_dir/fixture-disk.json" "$phase_dir/drive-3.json" --seconds 45
    stop_sampler "$phase_dir/stop-3" "$sampler3"
    cat "$phase_dir/samples-1.jsonl" "$phase_dir/samples-3.jsonl" >"$samples_arg"
    local follow_bytes
    follow_bytes="$(jq -s 'map(select(.diskDir)) | last | .diskDir.bytes // 0' "$samples_arg")"
    jq --argjson b "$branch" --argjson bytes "${follow_bytes:-0}" \
      '.branch = $b.branch | .spillsAfterRestart = $b.spillsAfterRestart | .followupDiskBytes = $bytes' \
      <<<"$branch" >"$phase_dir/restart.json"
    cp "$phase_dir/drive-1.json" "$drive_arg"
    restart_args=(--restart "$phase_dir/restart.json" --allow-restart)
  else
    cp "$phase_dir/samples-1.jsonl" "$samples_arg"
    cp "$phase_dir/drive-1.json" "$drive_arg"
  fi

  set +e
  python3 "$here/assert.py" --samples "$samples_arg" --drive "$drive_arg" \
    --container "$phase_dir/log/container.json" --phase "$phase" \
    --sentinel "$sentinel" --sentinel-sha "$sentinel_sha" \
    --expected-tmpfs-mib "$tmpfs" "${restart_args[@]}" \
    --out "$phase_dir/assert.json"
  local assert_rc=$?
  set -e
  printf 'phase %s assert exit %s\n' "$phase" "$assert_rc"

  "$phase_dir/log/cleanup.sh"
  active_phase=""
  return "$assert_rc"
}

IFS=',' read -r -a phase_list <<<"$phases"
for phase in "${phase_list[@]}"; do
  run_phase "$phase"
done
printf 'G07 phases complete; evidence under %s\n' "$FIXTURE_EVIDENCE"
