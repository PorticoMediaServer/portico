#!/usr/bin/env bash
# Run the G08 evidence: real Kerberos kinit, smbclient and both rclone SPN modes,
# the production child environment, DNS removal/re-pin and a server restart,
# then the current saved-source candidate API path.
#
# No host 53/88/445 bind and no host DNS change: every name and port lives on
# the private p2-65-g08 network.
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=../lib/common.sh
. "$here/../lib/common.sh"

usage() {
  cat >&2 <<'USAGE'
usage: run.sh --execute --portico-image IMG [--keep] [--no-cleanup]
Runs after provision.sh (it calls it when needed).
USAGE
  exit 2
}

execute=0 image="" keep=0 cleanup=1
while [ $# -gt 0 ]; do
  case "$1" in
    --execute) execute=1; shift ;;
    --portico-image) image="$2"; shift 2 ;;
    --keep) keep=1; shift ;;
    --no-cleanup) cleanup=0; shift ;;
    -h|--help) usage ;;
    *) usage ;;
  esac
done
[ "$execute" = 1 ] || { echo "PLAN: would run G08 Kerberos/SMB evidence on the private network"; exit 0; }
[ -n "$image" ] || usage

fixture_guard
fixture_need docker jq base64
fixture_secret_dir
fixture_evidence_dir
[ -f "$FIXTURE_SECRETS/g08-owner-password" ] || fixture_secret g08-owner-password 24 >/dev/null
out="$FIXTURE_EVIDENCE/g08"
mkdir -m 700 -p "$out"

for object in p2-65-g08-dc p2-65-g08-client p2-65-g08-server; do
  ! docker container inspect "$object" >/dev/null 2>&1 || fixture_refuse "retained container $object exists"
done
for object in p2-65-g08-ccache p2-65-g08-dcdata p2-65-g08-config; do
  ! docker volume inspect "$object" >/dev/null 2>&1 || fixture_refuse "retained volume $object exists"
done
! docker network inspect p2-65-g08 >/dev/null 2>&1 || fixture_refuse "retained G08 network exists"
cleanup_owned() {
  if [ "$cleanup" = 1 ] && [ "$keep" != 1 ]; then
    docker rm -f p2-65-g08-dc p2-65-g08-client p2-65-g08-server >/dev/null 2>&1 || true
    docker volume rm p2-65-g08-ccache p2-65-g08-dcdata p2-65-g08-config >/dev/null 2>&1 || true
    docker network rm p2-65-g08 >/dev/null 2>&1 || true
  fi
}
trap cleanup_owned EXIT
trap 'exit 130' INT
trap 'exit 143' TERM HUP
"$here/provision.sh" --execute --portico-image "$image"

dc=p2-65-g08-dc
client=p2-65-g08-client
server=p2-65-g08-server
rclone=/opt/portico/third_party/rclone/rclone
fixture_pass="$(cat "$FIXTURE_SECRETS/g08-fixture-password")"
obscured="$FIXTURE_SECRETS/g08-obscured.txt"
if [ ! -f "$obscured" ]; then
  docker exec "$client" "$rclone" obscure "$fixture_pass" >"$obscured"
  chmod 600 "$obscured"
fi

write_config() {
  local path="$1" host="$2" spn="$3" pass
  pass="$(cat "$obscured")"
  (umask 077
   {
     printf '[smbfixture]\n'
     printf 'type = smb\n'
     printf 'host = %s\n' "$host"
     printf 'port = 445\n'
     printf 'user = fixture\n'
     printf 'pass = %s\n' "$pass"
     printf 'domain = PORTICO\n'
     printf 'use_kerberos = true\n'
     if [ -n "$spn" ]; then printf 'spn = %s\n' "$spn"; fi
     printf 'idle_timeout = 5s\n'
   } >"$path"
  )
  chmod 600 "$path"
}
# The exact carrier Portico's pin rewrite produces: the selected numeric SMB
# address, with the original hostname preserved as the Kerberos SPN.
write_config "$FIXTURE_SECRETS/g08-rclone-pinned-implicit.conf" "172.30.65.10" "cifs/dc1.portico.test"
# An owner-set explicit SPN on the same numeric transport. The alias is
# registered on the DC machine account by entrypoint.sh.
write_config "$FIXTURE_SECRETS/g08-rclone-pinned-explicit.conf" "172.30.65.10" "cifs/smb-alias.portico.test"
# Hostname transport with rclone's own implicit cifs/<host> derivation: a
# control for the KDC and for the DNS-removal case, never the pinned carrier.
write_config "$FIXTURE_SECRETS/g08-rclone-hostname.conf" "dc1.portico.test" ""

kerberos_env='KRB5CCNAME=FILE:/ccache/krb5cc KRB5_CONFIG=/etc/krb5.conf'
phase_a="$out/phase-a.txt"
: >"$phase_a"; chmod 600 "$phase_a"

run_client() {
  # run_client <label> <shell command>; stderr/stdout is redacted to path/status.
  local label="$1" command="$2"
  local rc=0
  docker exec "$client" sh -c "export $kerberos_env; $command" >>"$phase_a" 2>&1 || rc=$?
  printf 'RESULT %s rc=%s\n' "$label" "$rc" >>"$phase_a"
  return "$rc"
}

set +e
docker exec -i "$client" sh -c "export $kerberos_env; kinit fixture" \
  <"$FIXTURE_SECRETS/g08-fixture-password" >>"$phase_a" 2>&1
printf 'RESULT kinit rc=%s\n' "$?" >>"$phase_a"
run_client klist 'klist'
run_client smbclient-kerberos 'smbclient //dc1.portico.test/media -k -c ls'
run_client rclone-hostname-implicit-lsjson "$rclone lsjson smbfixture:media --config /fixture-secrets/g08-rclone-hostname.conf --contimeout 5s --retries 1 --low-level-retries 1"
run_client rclone-pinned-implicit-lsjson "$rclone lsjson smbfixture:media --config /fixture-secrets/g08-rclone-pinned-implicit.conf --contimeout 5s --retries 1 --low-level-retries 1"
run_client rclone-pinned-explicit-lsjson "$rclone lsjson smbfixture:media --config /fixture-secrets/g08-rclone-pinned-explicit.conf --contimeout 5s --retries 1 --low-level-retries 1"
run_client rclone-pinned-implicit-cat "$rclone cat smbfixture:media/fixture.txt --config /fixture-secrets/g08-rclone-pinned-implicit.conf"

# Numeric-dial trace: keep the raw connect destination lines (address and port),
# never config bytes. No sed reduction of the address.
docker exec "$client" sh -c "export $kerberos_env; strace -f -e trace=connect -o /tmp/g08.trace $rclone lsjson smbfixture:media --config /fixture-secrets/g08-rclone-pinned-explicit.conf --contimeout 5s --retries 1 >/dev/null 2>&1" || true
docker exec "$client" sh -c 'grep -E "sin6?_port=htons\(445\)" /tmp/g08.trace' \
  | grep -E 'sin_addr=inet_addr\("172\.30\.65\.10"\)|sin6_addr=inet_pton\(AF_INET6' \
  | sort -u >"$out/connect-445.txt" || true
chmod 600 "$out/connect-445.txt"

# Production child environment: the server's own rclone, env -i like the launch
# path. The ccache must be at the default location because the child environment
# does not carry KRB5CCNAME.
uid="$(docker exec "$server" sh -c 'id -u')"
docker exec "$server" sh -c "mkdir -p /config/home && chown $uid:$uid /config/home" || true
docker exec "$client" base64 -w0 /ccache/krb5cc >"$FIXTURE_SECRETS/g08-ccache.b64"
chmod 600 "$FIXTURE_SECRETS/g08-ccache.b64"
docker exec -i -u "$uid" "$server" sh -c "base64 -d > /tmp/krb5cc_$uid && chmod 600 /tmp/krb5cc_$uid" \
  <"$FIXTURE_SECRETS/g08-ccache.b64"
phase_b="$out/phase-b.txt"
: >"$phase_b"; chmod 600 "$phase_b"
set +e
docker exec -u "$uid" "$server" env -i PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
  HOME=/config/home TMPDIR=/config/home \
  "$rclone" lsjson smbfixture:media --config /fixture-secrets/g08-rclone-pinned-implicit.conf \
  --contimeout 5s --retries 1 --low-level-retries 1 >>"$phase_b" 2>&1
printf 'RESULT production-child-default-ccache rc=%s\n' "$?" >>"$phase_b"
docker exec -u "$uid" "$server" env -i PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
  HOME=/config/home TMPDIR=/config/home KRB5CCNAME=FILE:/tmp/krb5cc_$uid \
  "$rclone" lsjson smbfixture:media --config /fixture-secrets/g08-rclone-pinned-implicit.conf \
  --contimeout 5s --retries 1 --low-level-retries 1 >>"$phase_b" 2>&1
printf 'RESULT production-child-explicit-ccache rc=%s\n' "$?" >>"$phase_b"

# DNS removal and re-add, driven inside the DC; the password travels on stdin.
admin_pass="$(cat "$FIXTURE_SECRETS/g08-admin-password")"
dns_phase="$out/phase-dns.txt"
: >"$dns_phase"; chmod 600 "$dns_phase"
printf '%s\n' "$admin_pass" | docker exec -i "$dc" sh -c \
  'read -r p; printf "%s\n" "$p" | samba-tool dns delete 127.0.0.1 portico.test dc1 A 172.30.65.10 -U administrator' \
  >>"$dns_phase" 2>&1
printf 'RESULT dns-delete rc=%s\n' "$?" >>"$dns_phase"
docker exec "$client" getent hosts dc1.portico.test >>"$dns_phase" 2>&1
printf 'RESULT resolve-after-delete rc=%s\n' "$?" >>"$dns_phase"
docker exec "$client" sh -c "export $kerberos_env; $rclone lsjson smbfixture:media --config /fixture-secrets/g08-rclone-hostname.conf --contimeout 3s --retries 1" >>"$dns_phase" 2>&1
printf 'RESULT rclone-after-delete rc=%s\n' "$?" >>"$dns_phase"
printf '%s\n' "$admin_pass" | docker exec -i "$dc" sh -c \
  'read -r p; printf "%s\n" "$p" | samba-tool dns add 127.0.0.1 portico.test dc1 A 172.30.65.10 -U administrator' \
  >>"$dns_phase" 2>&1
printf 'RESULT dns-add rc=%s\n' "$?" >>"$dns_phase"
run_client rclone-after-repin "$rclone lsjson smbfixture:media --config /fixture-secrets/g08-rclone-pinned-implicit.conf --contimeout 5s --retries 1"

# Portico restart keeps the fixture environment usable.
docker restart "$server" >/dev/null
end=$((SECONDS + 180))
until docker exec "$client" curl -fsS http://p2-65-g08-server:32500/v1/server >/dev/null 2>&1; do
  [ "$SECONDS" -lt "$end" ] || { docker logs --tail 40 "$server" >&2 || true; fixture_refuse "server did not return after restart"; }
  sleep 2
done
printf 'RESULT portico-restart rc=0\n' >>"$phase_a"
set -e

# Current saved-source candidate path, driven inside the private network.
docker run --rm --network p2-65-g08 \
  -v "$here/..:/fixtures:ro" \
  -v "$FIXTURE_SECRETS:/secrets:ro" \
  -v "$FIXTURE_EVIDENCE:/evidence" \
  -e G08_OBSCURED_PASS=/secrets/g08-obscured.txt \
  python:3.13-slim python3 /fixtures/g08-smb-kerberos/check.py --execute \
    --base http://p2-65-g08-server:32500 --state /evidence/g08-token.json \
    --password-file /secrets/g08-owner-password --out /evidence/g08-candidate.json \
  >"$out/candidate.txt" 2>&1
chmod 600 "$out/candidate.txt"

# Redacted child-config evidence: host/SPN/use_kerberos keys only, never pass.
grep -E '^(host|spn|use_kerberos|port|user|domain) *=' "$FIXTURE_SECRETS/g08-rclone-pinned-explicit.conf" \
  >"$out/child-config-redacted.txt"
chmod 600 "$out/child-config-redacted.txt"

jq -n --arg phase "g08-run" --arg host "$FIXTURE_HOST" \
  --arg at "$(fixture_now)" \
  --rawfile phaseA "$phase_a" --rawfile phaseB "$phase_b" --rawfile dns "$dns_phase" \
  --rawfile candidate "$out/candidate.txt" \
  --arg source "$(git -C "$here" rev-parse HEAD 2>/dev/null || echo unknown)" \
  '{phase:$phase,host:$host,sourceSha:$source,finishedAt:$at,
    phaseA:$phaseA, phaseB:$phaseB, dnsPhase:$dns, candidate:$candidate}' \
  >"$out/run.json"
chmod 600 "$out/run.json"

printf 'G08 evidence under %s\n' "$out"

# Raw outcomes remain evidence; nonzero required phases cannot become a pass.
python3 - "$out" <<'PYCODE'
import json,pathlib,re,sys
root=pathlib.Path(sys.argv[1]); failures=[]
for file in ('phase-a.txt','phase-b.txt','phase-dns.txt'):
    outcomes=re.findall(r'^RESULT ([a-z0-9-]+) rc=(\d+)$',(root/file).read_text(),re.M)
    if not outcomes: failures.append(file+': no outcomes')
    for name,rc in outcomes:
        expected_failure=name in ('resolve-after-delete','rclone-after-delete')
        if (int(rc)==0)==expected_failure: failures.append(name+': rc='+rc)
if not (root/'connect-445.txt').read_text().strip(): failures.append('no numeric445 trace')
report={'passed':not failures,'failures':failures,'basis':'real KDC, pinned carrier, default child ccache and DNS repin'}
with (root/'gate.json').open('x') as out: json.dump(report,out)
(root/'gate.json').chmod(0o600)
print(json.dumps(report))
raise SystemExit(1 if failures else 0)
PYCODE
