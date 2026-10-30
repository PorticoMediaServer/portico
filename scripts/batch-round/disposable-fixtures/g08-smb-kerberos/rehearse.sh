#!/usr/bin/env bash
# Light KDC/SMB rehearsal on the authorized compute host.
#
# Two Kerberos auth cases only: the exact pinned carrier (numeric host with the
# original hostname SPN) and an explicit owner SPN alias. No Portico server, no
# load, no pressure, no host port. Resources: DC 512 MiB + client 256 MiB,
# one CPU each; the caller keeps at least 2 GiB MemAvailable for Hosted.
#
# Run on the authorized host with PORTICO_FIXTURE_DISPOSABLE=yes and a local
# qualified rclone 1.75.2 binary.
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=../lib/common.sh
. "$here/../lib/common.sh"

usage() {
  cat >&2 <<'USAGE'
usage: rehearse.sh --execute --rclone PATH [--scratch DIR] [--receipt FILE] [--keep]
Environment: PORTICO_FIXTURE_DISPOSABLE=yes
USAGE
  exit 2
}

execute=0 rclone_bin="" scratch="${HOME}/astra-scratch/P2-65/smb"
receipt="${HOME}/astra-scratch/P2-65/smb-rehearsal-receipt.json" keep=0
while [ $# -gt 0 ]; do
  case "$1" in
    --execute) execute=1; shift ;;
    --rclone) rclone_bin="$2"; shift 2 ;;
    --scratch) scratch="$2"; shift 2 ;;
    --receipt) receipt="$2"; shift 2 ;;
    --keep) keep=1; shift ;;
    -h|--help) usage ;;
    *) usage ;;
  esac
done
[ "$execute" = 1 ] || { echo "PLAN: would run two Kerberos auth cases on a private Docker network"; exit 0; }
[ -n "$rclone_bin" ] || usage
[ -f "$rclone_bin" ] || fixture_refuse "rclone binary not found"
[ "$(sha256sum "$rclone_bin" | cut -d' ' -f1)" = "ed7ab1f17876bb006c3df3949a3044959671afd9112362dacaa5dd1647735bc1" ] \
  || fixture_refuse "rclone binary is not the qualified 1.75.2 build"

export PORTICO_FIXTURE_SCRATCH="$scratch"
fixture_guard
fixture_need docker jq openssl base64 sha256sum
mkdir -m 700 -p "$scratch"
scratch="$(realpath "$scratch")"
case "$scratch" in *[!A-Za-z0-9/._-]*) fixture_refuse "scratch path may contain only safe characters" ;; esac
fixture_secret_dir

network=p2-65-smb-rehearsal
dc=p2-65-smb-dc-rehearsal
client=p2-65-smb-client-rehearsal
run_id="$(date -u +%Y%m%dT%H%M%SZ)-$$"
dc_tag="p2-65-rehearsal-dc:$run_id"
client_tag="p2-65-rehearsal-client:$run_id"
ip_dc=172.31.65.10
ip_client=172.31.65.20
mem_before_kb="$(awk '/MemAvailable/{print $2}' /proc/meminfo)"
[ "$mem_before_kb" -ge 2621440 ] || fixture_refuse "less than 2.5 GiB MemAvailable; preserve Hosted headroom"

base_present=0
docker image inspect debian:bookworm-slim >/dev/null 2>&1 && base_present=1
mem_min_kb="$mem_before_kb"
failures=()
cases_json='[]'

mkdir -m 700 -p "$scratch/ccache"
monitor_stop="$scratch/monitor.stop"
monitor_log="$scratch/memory.jsonl"
rm -f "$monitor_stop"
: >"$monitor_log"; chmod 600 "$monitor_log"
(
  while [ ! -f "$monitor_stop" ]; do
    avail="$(awk '/MemAvailable/{print $2}' /proc/meminfo)"
    printf '{"t":%s,"memAvailableKb":%s}\n' "$(date +%s)" "$avail" >>"$monitor_log"
    sleep 2
  done
) &
monitor_pid=$!

cleanup() {
  touch "$monitor_stop" 2>/dev/null || true
  [ -z "${monitor_pid:-}" ] || wait "$monitor_pid" 2>/dev/null || true
  docker rm -f "$dc" "$client" >/dev/null 2>&1 || true
  docker volume rm -f p2-65-rehearsal-dcdata >/dev/null 2>&1 || true
  if [ "${keep:-0}" != 1 ]; then docker network rm "$network" >/dev/null 2>&1 || true; fi
  docker image rm -f "$dc_tag" "$client_tag" >/dev/null 2>&1 || true
  if [ "$base_present" = 0 ] && [ "$keep" != 1 ]; then
    docker image rm -f debian:bookworm-slim >/dev/null 2>&1 || true
  fi
  if [ "$keep" != 1 ]; then
    rm -rf "$scratch/build-client" "$scratch/ccache"
  fi
}
trap cleanup EXIT INT TERM HUP

if docker network inspect "$network" >/dev/null 2>&1; then
  docker network rm "$network" >/dev/null 2>&1 || fixture_refuse "a leftover rehearsal network could not be removed"
fi
docker network create --driver bridge --subnet 172.31.65.0/24 "$network" >/dev/null

# DC image: Debian + Samba AD DC.
nice -n 19 docker build --network=host -q -t "$dc_tag" "$here" >/dev/null
# Client image: Debian + Kerberos tools + the already-qualified local rclone.
mkdir -m 700 -p "$scratch/build-client"
cp "$rclone_bin" "$scratch/build-client/rclone"
cp "$here/Dockerfile.client-light" "$scratch/build-client/Dockerfile"
nice -n 19 docker build --network=host -q -t "$client_tag" "$scratch/build-client" >/dev/null

# Samba's default password quality needs more than one character class.
# Generated throwaway values only, 0600 under the scratch secrets directory.
samba_password() {
  local name="$1" prefix="$2" path="$FIXTURE_SECRETS/$1"
  if [ ! -f "$path" ]; then
    (umask 077; printf '%s%s\n' "$prefix" "$(openssl rand -hex 16)" >"$path")
  fi
  chmod 0600 "$path"
  cat "$path"
}
admin_pass="$(samba_password rehearsal-admin-password 'Aa1!')"
fixture_pass="$(samba_password rehearsal-fixture-password 'Bb2!')"
obscured="$FIXTURE_SECRETS/rehearsal-obscured.txt"
if [ ! -f "$obscured" ]; then
  # Obscure locally with the same qualified rclone, no container yet.
  (umask 077; "$rclone_bin" obscure "$fixture_pass" >"$obscured")
fi

env_file="$FIXTURE_SECRETS/rehearsal-dc.env"
(umask 077
 {
   printf 'REALM=PORTICO.TEST\n'
   printf 'DOMAIN=PORTICO\n'
   printf 'ADMIN_PASS=%s\n' "$admin_pass"
   printf 'FIXTURE_PASS=%s\n' "$fixture_pass"
   printf 'SPN_ALIAS=smb-alias.portico.test\n'
 } >"$env_file")
chmod 600 "$env_file"

docker volume create p2-65-rehearsal-dcdata >/dev/null
docker run -d --name "$dc" --hostname dc1.portico.test \
  --network "$network" --ip "$ip_dc" \
  --memory=512m --memory-swap=512m --cpus=1 --pids-limit=256 \
  --cap-add SYS_ADMIN \
  -v p2-65-rehearsal-dcdata:/var/lib/samba \
  --env-file "$env_file" \
  --log-driver=local --log-opt max-size=4m --log-opt max-file=2 \
  "$dc_tag" >/dev/null

end=$((SECONDS + 240))
until docker exec "$dc" test -s /var/lib/samba/private/sam.ldb \
  && docker exec "$dc" sh -c '(grep -qi ":01BD" /proc/net/tcp /proc/net/tcp6) && (grep -qi ":0058" /proc/net/tcp /proc/net/tcp6)'; do
  [ "$SECONDS" -lt "$end" ] || { docker logs --tail 60 "$dc" >&2 || true; failures+=("dc-not-ready"); break; }
  [ "$(docker inspect -f '{{.State.Running}}' "$dc")" = true ] || break
  sleep 2
done

# Preserve the DC's own provision log for the receipt before anything else.
docker logs "$dc" >"$scratch/dc.log" 2>&1 || true
if ! docker exec "$dc" sh -c '(grep -qi ":01BD" /proc/net/tcp /proc/net/tcp6) && (grep -qi ":0058" /proc/net/tcp /proc/net/tcp6)'; then
  jq -n --arg host "$(hostname)" --arg runId "$run_id" --arg at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    --rawfile log "$scratch/dc.log" \
    '{runId:$runId,host:$host,finishedAt:$at,result:"FAIL",
      failure:"fixture-dc-provision",dcLogTail:($log|split("\n")|.[-40:]|join("\n"))}' \
    >"$receipt"; chmod 600 "$receipt"
  printf 'rehearsal FAIL: fixture DC did not provision; receipt %s\n' "$receipt"
  exit 1
fi

docker run -d --name "$client" \
  --network "container:$dc" \
  --memory=256m --memory-swap=256m --cpus=1 --pids-limit=64 \
  -v "$FIXTURE_SECRETS:/secrets:ro" -v "$scratch/ccache:/ccache" \
  --log-driver=local --log-opt max-size=4m --log-opt max-file=2 \
  "$client_tag" >/dev/null

# Grab the DC-generated krb5.conf for the client (realm/KDC settings).
(umask 077; cat >"$FIXTURE_SECRETS/krb5.conf" <<KRB
[libdefaults]
 default_realm = PORTICO.TEST
 dns_lookup_kdc = false
 dns_lookup_realm = false
[realms]
 PORTICO.TEST = {
  kdc = $ip_dc
  admin_server = $ip_dc
 }
[domain_realm]
 .portico.test = PORTICO.TEST
 portico.test = PORTICO.TEST
KRB
)
chmod 600 "$FIXTURE_SECRETS/krb5.conf"
docker cp "$FIXTURE_SECRETS/krb5.conf" "$client:/etc/krb5.conf"
docker exec "$client" sh -c "printf '%s dc1.portico.test smb-alias.portico.test\\n' '$ip_dc' >>/etc/hosts"

write_conf() {
  local path="$1" spn="$2" pass
  pass="$(cat "$obscured")"
  (umask 077
   {
     printf '[smbfixture]\ntype = smb\nhost = %s\nport = 445\nuser = fixture\npass = %s\ndomain = PORTICO\nuse_kerberos = true\n' "$ip_dc" "$pass"
     [ -z "$spn" ] || printf 'spn = %s\n' "$spn"
     printf 'idle_timeout = 5s\n'
   } >"$path")
}
write_conf "$FIXTURE_SECRETS/pinned-implicit.conf" "cifs/dc1.portico.test"
write_conf "$FIXTURE_SECRETS/pinned-explicit.conf" "cifs/smb-alias.portico.test"

kerberos_env='KRB5CCNAME=FILE:/ccache/krb5cc KRB5_CONFIG=/etc/krb5.conf'
klist_out=""
if docker exec -i "$client" sh -c "export $kerberos_env; kinit fixture" \
    <"$FIXTURE_SECRETS/rehearsal-fixture-password" >"$scratch/kinit.out" 2>&1; then
  klist_out="$(docker exec "$client" sh -c "export $kerberos_env; klist" 2>&1 || true)"
else
  failures+=("kinit")
fi

# Kerberos SMB control (hostname, not the pinned carrier).
smbclient_rc=0
docker exec "$client" sh -c "export $kerberos_env; smbclient //dc1.portico.test/media --use-kerberos=required --no-pass -c ls" \
  >"$scratch/smbclient.out" 2>&1 || smbclient_rc=$?
[ "$smbclient_rc" = 0 ] || failures+=("smbclient-kerberos")

run_case() {
  local name="$1" conf="$2" spn="$3"
  local rc=0 tmp trace numeric output
  tmp="$scratch/$name.out"
  trace="$scratch/$name.trace"
  docker exec "$client" sh -c "export $kerberos_env; strace -f -e trace=connect -o /tmp/$name.trace \
    /usr/local/bin/rclone lsjson smbfixture:media --config /secrets/$conf --contimeout 5s --retries 1 --low-level-retries 1" \
    >"$tmp" 2>&1 || rc=$?
  docker exec "$client" cat "/tmp/$name.trace" >"$trace" 2>/dev/null || true
  numeric="$(grep -E "sin6?_port=htons\\(445\\)" "$trace" 2>/dev/null \
    | grep -E "inet_addr\\(\"$ip_dc\"\\)|inet_pton\\(AF_INET6" | sort -u || true)"
  if [ "$rc" = 0 ]; then
    # A real read too, not only a listing.
    docker exec "$client" sh -c "export $kerberos_env; /usr/local/bin/rclone cat smbfixture:media/fixture.txt --config /secrets/$conf" \
      >>"$tmp" 2>&1 || rc=$?
  else
    failures+=("$name")
  fi
  [ -n "$numeric" ] || failures+=("$name-no-numeric-connect")
  cases_json="$(jq --arg name "$name" --arg conf "$conf" --arg spn "$spn" \
    --argjson rc "$rc" --rawfile out "$tmp" --arg trace "$numeric" \
    '. + [{name:$name,config:$conf,spn:$spn,rc:$rc,outputTail:($out|split("\n")|.[-12:]|join("\n")),numericDial:($trace|split("\n")|map(select(length>0)))}]' \
    <<<"$cases_json")"
}
run_case pinned-implicit pinned-implicit.conf "cifs/dc1.portico.test"
run_case pinned-explicit pinned-explicit.conf "cifs/smb-alias.portico.test"

touch "$monitor_stop"; wait "$monitor_pid" 2>/dev/null || true
mem_after_kb="$(awk '/MemAvailable/{print $2}' /proc/meminfo)"
mem_min_kb="$(jq -s 'map(.memAvailableKb)|min' "$monitor_log" 2>/dev/null || true)"
case "$mem_min_kb" in ""|null) mem_min_kb="$mem_after_kb" ;; esac
[ "$mem_min_kb" -ge 2097152 ] || failures+=("hosted-headroom-below-2gib")

result="PASS"
[ "${#failures[@]}" = 0 ] || result="FAIL"

jq -n --arg host "$(hostname)" --arg runId "$run_id" \
  --arg started "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  --arg dcImage "$(docker image inspect -f '{{.Id}}' "$dc_tag")" \
  --arg clientImage "$(docker image inspect -f '{{.Id}}' "$client_tag")" \
  --argjson memBefore "$mem_before_kb" --argjson memMin "$mem_min_kb" --argjson memAfter "$mem_after_kb" \
  --argjson cases "$cases_json" --argjson failures "$(printf '%s\n' "${failures[@]:-}" | jq -R . | jq -s 'map(select(length>0))')" \
  --arg klist "$klist_out" --argjson smbclientRc "$smbclient_rc" --arg result "$result" \
  --arg rcloneSha "$(sha256sum "$rclone_bin" | cut -d' ' -f1)" \
  '{runId:$runId,host:$host,startedAt:$started,result:$result, networkBasis:"client shares the private DC network namespace; numeric TCP and real KDC auth only, not cross-bridge or DNS repin acceptance",
    limits:{dc:"512MiB/1cpu",client:"256MiB/1cpu",totalMemory:"768MiB"},
    images:{dc:$dcImage,client:$clientImage},rcloneSha256:$rcloneSha,
    memAvailableKb:{before:$memBefore,min:$memMin,after:$memAfter},
    kerberos:{klist:$klist,smbclientRc:$smbclientRc},cases:$cases,failures:$failures}' \
  >"$receipt.part"
mv "$receipt.part" "$receipt"
chmod 600 "$receipt"

printf 'rehearsal %s; receipt %s\n' "$result" "$receipt"
[ "$result" = PASS ] || exit 1
