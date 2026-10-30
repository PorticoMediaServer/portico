#!/usr/bin/env bash
# G09 environment and driver. Private Docker network only: real HTTP and HTTPS
# origins, a controlled DNS responder and the Portico server all live inside
# p2-65-g09. No host port 53/88/445, no host DNS change, no host mount.
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=../lib/common.sh
. "$here/../lib/common.sh"

usage() {
  cat >&2 <<'USAGE'
usage: run.sh --execute --portico-image IMG [--keep]
Runs the STRM origin fixture and the server-side checks.
USAGE
  exit 2
}

execute=0 image="" keep=0
while [ $# -gt 0 ]; do
  case "$1" in
    --execute) execute=1; shift ;;
    --portico-image) image="$2"; shift 2 ;;
    --keep) keep=1; shift ;;
    -h|--help) usage ;;
    *) usage ;;
  esac
done
[ "$execute" = 1 ] || { echo "PLAN: would run the G09 origin, DNS and STRM checks on a private network"; exit 0; }
[ -n "$image" ] || usage

fixture_guard
fixture_need docker openssl jq ffmpeg base64
fixture_secret_dir
fixture_playback_inputs
fixture_evidence_dir
scratch="${HOME}/astra-scratch/P2-65/disposable/g09"
mkdir -m 700 -p "$scratch/logs"
scratch="$(realpath "$scratch")"
out="$FIXTURE_EVIDENCE/g09"
mkdir -m 700 -p "$out"

# Refuse a retained fixture rather than resetting it. Cleanup is installed
# before any Docker mutation, so failed startup and interrupted checks retire it.
for object in p2-65-g09-origin p2-65-g09-origin-tls p2-65-g09-origin-bad p2-65-g09-dns p2-65-g09-server; do
  ! docker container inspect "$object" >/dev/null 2>&1 || fixture_refuse "retained container $object exists"
done
! docker volume inspect p2-65-g09-config >/dev/null 2>&1 || fixture_refuse "retained G09 config exists"
! docker network inspect p2-65-g09 >/dev/null 2>&1 || fixture_refuse "retained G09 network exists"
cleanup_owned() {
  if [ "$keep" != 1 ]; then
    docker rm -f p2-65-g09-origin p2-65-g09-origin-tls p2-65-g09-origin-bad p2-65-g09-dns p2-65-g09-server >/dev/null 2>&1 || true
    docker volume rm p2-65-g09-config >/dev/null 2>&1 || true
    docker network rm p2-65-g09 >/dev/null 2>&1 || true
  fi
}
trap cleanup_owned EXIT
trap 'exit 130' INT
trap 'exit 143' TERM HUP
network="$(fixture_network p2-65-g09 172.30.67.0/24)"
origin=p2-65-g09-origin
origin_tls=p2-65-g09-origin-tls
bad=p2-65-g09-origin-bad
resolver=p2-65-g09-dns
server=p2-65-g09-server
logs="$scratch/logs"

media="$scratch/media"
if [ ! -d "$media" ]; then "$here/make-media.sh" --out "$media"; fi

token_file="$FIXTURE_SECRETS/g09-control-token"
[ -f "$token_file" ] || fixture_secret g09-control-token 24 >/dev/null
[ -f "$FIXTURE_SECRETS/g09-owner-password" ] || fixture_secret g09-owner-password 24 >/dev/null

ca_key="$FIXTURE_SECRETS/g09-ca.key"
ca_pem="$FIXTURE_SECRETS/g09-ca.pem"
origin_key="$FIXTURE_SECRETS/g09-origin.key"
origin_pem="$FIXTURE_SECRETS/g09-origin.pem"
if [ ! -f "$origin_pem" ]; then
  (umask 077
   openssl req -x509 -newkey rsa:2048 -sha256 -days 3 -nodes \
     -keyout "$ca_key" -out "$ca_pem" -subj "/CN=P2-65 G09 fixture CA" >/dev/null 2>&1
   openssl req -newkey rsa:2048 -nodes -keyout "$origin_key" -out "$scratch/origin.csr" \
     -subj "/CN=media.portico.test" >/dev/null 2>&1
   printf 'subjectAltName=DNS:media.portico.test\nbasicConstraints=CA:FALSE\n' >"$scratch/ext.cnf"
   openssl x509 -req -in "$scratch/origin.csr" -CA "$ca_pem" -CAkey "$ca_key" \
     -CAcreateserial -out "$origin_pem" -days 3 -sha256 -extfile "$scratch/ext.cnf" >/dev/null 2>&1
  )
  chmod 600 "$ca_key" "$origin_key" "$ca_pem" "$origin_pem"
fi

zone="$scratch/zone.json"
if [ ! -f "$zone" ]; then
  printf '{"media.portico.test": "172.30.67.10"}\n' >"$zone"
  chmod 600 "$zone"
fi

start_origin() {
  local name="$1" ip="$2" port="$3" control="$4" log="$5" extra=("${@:6}")
  docker rm -f "$name" >/dev/null 2>&1 || true
  docker run -d --name "$name" --network "$network" --ip "$ip" \
    --entrypoint python3 -v "$here/..:/fixtures:ro" -v "$media:/media" \
    -v "$logs:/logs" -v "$FIXTURE_SECRETS:/secrets:ro" \
    python:3.13-slim "/fixtures/g09-strm/origin.py" --root /media --port "$port" \
    --control-port "$control" --log "/logs/$log" --token-file /secrets/g09-control-token \
    "${extra[@]}" >/dev/null
}

start_origin "$origin" 172.30.67.10 8080 8081 origin.jsonl
start_origin "$origin_tls" 172.30.67.12 8443 8444 origin-tls.jsonl \
  --tls-cert /secrets/g09-origin.pem --tls-key /secrets/g09-origin.key
start_origin "$bad" 172.30.67.11 8080 8082 bad-origin.jsonl

docker rm -f "$resolver" >/dev/null 2>&1 || true
docker run -d --name "$resolver" --network "$network" --ip 172.30.67.53 \
  --entrypoint python3 -v "$here/..:/fixtures:ro" -v "$scratch:/zone:ro" \
  python:3.13-slim /fixtures/g09-strm/dns_responder.py --zone /zone/zone.json --port 53 >/dev/null

docker volume create p2-65-g09-config >/dev/null
docker rm -f "$server" >/dev/null 2>&1 || true
docker run -d --name "$server" --network "$network" --ip 172.30.67.20 \
  --dns 172.30.67.53 \
  -e PUID="$(id -u)" -e PGID="$(id -g)" -e UMASK=0077 -e TZ=UTC \
  -e SSL_CERT_FILE=/fixture-ca/ca.pem \
  -v p2-65-g09-config:/config \
  -v "$media:/media:ro" \
  -v "$FIXTURE_SECRETS/g09-ca.pem:/fixture-ca/ca.pem:ro" \
  --log-driver=local --log-opt max-size=8m --log-opt max-file=2 \
  "$image" >/dev/null

wait_ready() {
  local end=$((SECONDS + 240))
  until docker run --rm --network "$network" python:3.13-slim python3 -c \
    "import urllib.request; urllib.request.urlopen('http://p2-65-g09-server:32500/v1/server', timeout=3)" >/dev/null 2>&1; do
    [ "$SECONDS" -lt "$end" ] || { docker logs --tail 60 "$server" >&2 || true; fixture_refuse "Portico fixture server did not become ready"; }
    sleep 2
  done
}
wait_ready

set +e
docker run --rm --network "$network" \
  -v "$FIXTURE_NODE:/fixture-node:ro" -v "$FIXTURE_REPO:/fixture-source:ro" \
  -e PORTICO_FIXTURE_NODE=/fixture-node -e PORTICO_FIXTURE_CAPABILITIES=/secrets/fixture-capabilities.json \
  -e PORTICO_FIXTURE_SESSION_ADAPTER=/fixture-source/scripts/batch-round/disposable-fixtures/lib/session-adapter.ts \
  -v "$here/..:/fixtures:ro" -v "$media:/media" -v "$scratch:/scratch" \
  -v "$FIXTURE_SECRETS:/secrets:ro" -v "$out:/evidence" \
  -e G09_CONTROL_TOKEN=/secrets/g09-control-token \
  python:3.13-slim python3 /fixtures/g09-strm/check.py --execute \
    --base http://p2-65-g09-server:32500 --state /evidence/g09-token.json \
    --password-file /secrets/g09-owner-password \
    --control http://172.30.67.10:8081 --bad-control http://172.30.67.11:8082 \
    --origin-log /scratch/logs/origin.jsonl --bad-log /scratch/logs/bad-origin.jsonl \
    --origin-tls-log /scratch/logs/origin-tls.jsonl \
    --zone /scratch/zone.json --media /media --server-ip 172.30.67.20 \
    --out /evidence/g09-receipt.json
check_rc=$?
set -e

jq -n --arg host "$FIXTURE_HOST" --arg network "$network" --arg at "$(fixture_now)" \
  --arg source "$(git -C "$here" rev-parse HEAD 2>/dev/null || echo unknown)" \
  --argjson checkRc "$check_rc" \
  --rawfile receipt "$out/g09-receipt.json" \
  '{phase:"g09-run",host:$host,network:$network,sourceSha:$source,finishedAt:$at,
    checkExit:$checkRc,receipt:$receipt}' >"$out/run.json"
chmod 600 "$out/run.json"

printf 'G09 receipts under %s (check exit %s)\n' "$out" "$check_rc"
exit "$check_rc"
