#!/usr/bin/env bash
# Build and start the isolated G08 environment: private network, real Samba AD
# DC (KDC + SMB), a client image with the pinned rclone and Kerberos tools, and
# a Portico server with no published host port. No host 53/88/445 bind and no
# host DNS change.
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=../lib/common.sh
. "$here/../lib/common.sh"

usage() {
  cat >&2 <<'USAGE'
usage: provision.sh --execute --portico-image IMG [--keep]
Builds the fixture images and starts: p2-65-g08-dc, p2-65-g08-client,
p2-65-g08-server on the private p2-65-g08 network. run.sh drives them.
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
[ "$execute" = 1 ] || { echo "PLAN: would build the G08 DC/client/server on a private network"; exit 0; }
[ -n "$image" ] || usage

fixture_guard
fixture_need docker jq
case "$(docker info --format '{{.CgroupVersion}}' 2>/dev/null || echo unknown)" in
  2) ;;
  *) fixture_refuse "cgroup v2 is required" ;;
esac
fixture_secret_dir
fixture_evidence_dir
scratch="${HOME}/astra-scratch/P2-65/disposable/g08"
mkdir -m 700 -p "$scratch"
scratch="$(realpath "$scratch")"

network="$(fixture_network p2-65-g08 172.30.65.0/24)"
dc=p2-65-g08-dc
client=p2-65-g08-client
server=p2-65-g08-server
driver=p2-65-g08-driver

admin_pass="$(fixture_secret g08-admin-password 24)"
fixture_pass="$(fixture_secret g08-fixture-password 24)"
spn_alias="${G08_SPN_ALIAS:-smb-alias.portico.test}"
env_file="$FIXTURE_SECRETS/g08-dc.env"
if [ ! -f "$env_file" ]; then
  (umask 077
   {
     printf 'REALM=%s\n' "${G08_REALM:-PORTICO.TEST}"
     printf 'DOMAIN=%s\n' "${G08_DOMAIN:-PORTICO}"
     printf 'ADMIN_PASS=%s\n' "$admin_pass"
     printf 'FIXTURE_PASS=%s\n' "$fixture_pass"
     printf 'SPN_ALIAS=%s\n' "$spn_alias"
   } >"$env_file")
  chmod 600 "$env_file"
fi

docker build -q -t p2-65-g08-dc:latest "$here" >/dev/null
docker build -q -t p2-65-g08-client:latest -f "$here/Dockerfile.client" \
  --build-arg "PORTICO_IMAGE=$image" "$here" >/dev/null
docker image inspect -f '{{.Id}}' p2-65-g08-dc:latest >"$scratch/dc-image-id.txt"
docker image inspect -f '{{.Id}}' p2-65-g08-client:latest >"$scratch/client-image-id.txt"
docker image inspect -f '{{.Id}}' "$image" >"$scratch/server-image-id.txt"

docker volume create p2-65-g08-ccache >/dev/null
docker volume create p2-65-g08-dcdata >/dev/null
docker volume create p2-65-g08-config >/dev/null

docker rm -f "$dc" >/dev/null 2>&1 || true
docker run -d --name "$dc" --hostname dc1.portico.test \
  --network "$network" --ip 172.30.65.10 \
  --env-file "$env_file" \
  -v p2-65-g08-dcdata:/var/lib/samba \
  --log-driver=local --log-opt max-size=8m --log-opt max-file=2 \
  p2-65-g08-dc:latest >/dev/null

end=$((SECONDS + 240))
until docker exec "$dc" test -f /var/lib/samba/private/sam.ldb \
  && docker exec "$dc" sh -c '(grep -qi ":01BD" /proc/net/tcp /proc/net/tcp6) && (grep -qi ":0058" /proc/net/tcp /proc/net/tcp6)'; do
  [ "$SECONDS" -lt "$end" ] || { docker logs --tail 60 "$dc" >&2 || true; fixture_refuse "fixture DC did not provision"; }
  sleep 2
done

docker cp "$dc:/etc/krb5.conf" "$FIXTURE_SECRETS/g08-krb5.conf"
chmod 600 "$FIXTURE_SECRETS/g08-krb5.conf"

docker rm -f "$client" >/dev/null 2>&1 || true
docker run -d --name "$client" --hostname fixture-client \
  --network "$network" --ip 172.30.65.20 --dns 172.30.65.10 \
  -v p2-65-g08-ccache:/ccache \
  -v "$FIXTURE_SECRETS/g08-krb5.conf:/etc/krb5.conf:ro" \
  -v "$FIXTURE_SECRETS:/fixture-secrets:ro" \
  --entrypoint /bin/sleep p2-65-g08-client:latest infinity >/dev/null

docker rm -f "$server" >/dev/null 2>&1 || true
docker run -d --name "$server" \
  --network "$network" --ip 172.30.65.30 --dns 172.30.65.10 \
  -e PUID="$(id -u)" -e PGID="$(id -g)" -e UMASK=0077 -e TZ=UTC \
  -v p2-65-g08-config:/config \
  -v p2-65-g08-ccache:/ccache \
  -v "$FIXTURE_SECRETS/g08-krb5.conf:/etc/krb5.conf:ro" \
  -v "$FIXTURE_SECRETS:/fixture-secrets:ro" \
  --log-driver=local --log-opt max-size=8m --log-opt max-file=2 \
  "$image" >/dev/null

end=$((SECONDS + 240))
until docker exec "$client" curl -fsS http://p2-65-g08-server:32500/v1/server >/dev/null 2>&1; do
  [ "$SECONDS" -lt "$end" ] || { docker logs --tail 60 "$server" >&2 || true; fixture_refuse "Portico fixture server did not become ready"; }
  sleep 2
done

jq -n --arg host "$FIXTURE_HOST" --arg network "$network" \
  --arg dc "$dc" --arg client "$client" --arg server "$server" \
  --arg dcImage "$(cat "$scratch/dc-image-id.txt")" \
  --arg clientImage "$(cat "$scratch/client-image-id.txt")" \
  --arg serverImage "$(cat "$scratch/server-image-id.txt")" \
  --arg realm "${G08_REALM:-PORTICO.TEST}" --arg spnAlias "$spn_alias" \
  --arg source "$(git -C "$here" rev-parse HEAD 2>/dev/null || echo unknown)" \
  --arg at "$(fixture_now)" \
  '{phase:"g08-provision",host:$host,network:$network,dc:$dc,client:$client,server:$server,
    dcImageId:$dcImage,clientImageId:$clientImage,serverImageId:$serverImage,
    realm:$realm,spnAlias:$spnAlias,sourceSha:$source,startedAt:$at}' \
  >"$scratch/provision.json"
chmod 600 "$scratch/provision.json"
printf 'G08 environment ready on private network %s\n' "$network"
