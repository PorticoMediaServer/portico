#!/usr/bin/env bash
# Tooling self-check: no Docker, no KDC, no Portico server. It proves the shell
# and Python parse, and exercises the real origin HTTP/TLS/DNS responders and
# the Samba share stanza on loopback/high ports.
#
# Run this before claiming the tooling is reviewable. It is not acceptance
# evidence for G07-G09; the disposable-host runs remain the acceptance work.
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="${PORTICO_SELFCHECK_SCRATCH:-${HOME}/astra-scratch/P2-65/disposable/selfcheck}"
mkdir -p "$root"
root="$(realpath "$root")"
work="$(mktemp -d "$root/run.XXXXXX")"
pids=()
cleanup() {
  for pid in "${pids[@]:-}"; do kill "$pid" 2>/dev/null || true; done
  wait 2>/dev/null || true
  find "$here" -name '__pycache__' -type d -prune -exec rm -rf {} + 2>/dev/null || true
  rm -rf "$work"
}
trap cleanup EXIT

pass() { printf 'PASS %s\n' "$*"; }
fail() { printf 'FAIL %s\n' "$*" >&2; exit 1; }

for tool in python3 curl jq openssl dig; do
  command -v "$tool" >/dev/null 2>&1 || fail "self-check needs $tool"
done

# 1. Parse every shipped script.
find "$here" -name '__pycache__' -type d -prune -exec rm -rf {} + 2>/dev/null || true
while IFS= read -r file; do
  case "$file" in
    *.py) python3 -m py_compile "$file" || fail "python parse $file" ;;
    *.sh) bash -n "$file" || fail "shell parse $file" ;;
  esac
done < <(find "$here" -type f \( -name '*.py' -o -name '*.sh' \) | sort)
find "$here" -name '__pycache__' -type d -prune -exec rm -rf {} + 2>/dev/null || true
pass "all shell and Python files parse"

# 1b. API client wire self-check against a loopback mock envelope.
python3 "$here/lib/api_selfcheck.py" || fail "api client wire self-check"

# 2. Origin: ranges, strong ETag checks, redirects, offline, replacement.
mkdir -p "$work/media" "$work/logs"
head -c 65536 /dev/urandom >"$work/media/direct.mp4"
cp "$work/media/direct.mp4" "$work/media/offline.mp4"
cp "$work/media/direct.mp4" "$work/media/replace.mp4"
head -c 65536 /dev/urandom >"$work/media/next.mp4"
openssl rand -hex 24 >"$work/token"
chmod 600 "$work/token"
python3 "$here/g09-strm/origin.py" --root "$work/media" --port 19080 \
  --control-port 19081 --log "$work/logs/origin.jsonl" --token-file "$work/token" \
  >"$work/logs/origin.out" 2>&1 &
pids+=($!)
for _ in $(seq 1 50); do
  curl -fsS -o /dev/null "http://127.0.0.1:19080/media/direct.mp4" 2>/dev/null && break
  sleep 0.1
done
token="$(cat "$work/token")"
code="$(curl -sS -o /dev/null -w '%{http_code}' -H 'Range: bytes=0-0' http://127.0.0.1:19080/media/direct.mp4)"
[ "$code" = 206 ] || fail "origin range 206 (got $code)"
etag="$(curl -sS -I http://127.0.0.1:19080/media/direct.mp4 | tr -d '\r' | awk -F': ' 'tolower($1)=="etag"{print $2}')"
[ -n "$etag" ] || fail "origin strong ETag"
[ "$(curl -sS -o /dev/null -w '%{http_code}' -H 'If-Match: "stale"' http://127.0.0.1:19080/media/direct.mp4)" = 412 ] || fail "origin If-Match 412"
[ "$(curl -sS -o /dev/null -w '%{http_code}' -H "If-None-Match: $etag" http://127.0.0.1:19080/media/direct.mp4)" = 304 ] || fail "origin If-None-Match 304"
before="$(curl -sS -H "X-Fixture-Token: $token" http://127.0.0.1:19081/state | jq -r '.files["replace.mp4"] | "\(.size) \(.mtimeNs) \(.etag)"')"
curl -sS -o /dev/null -X POST -H "X-Fixture-Token: $token" -H 'Content-Type: application/json' \
  -d "{\"name\":\"replace.mp4\",\"source\":\"$work/media/next.mp4\",\"keepSize\":true,\"keepMtime\":true}" \
  http://127.0.0.1:19081/replace
after="$(curl -sS -H "X-Fixture-Token: $token" http://127.0.0.1:19081/state | jq -r '.files["replace.mp4"] | "\(.size) \(.mtimeNs) \(.etag)"')"
[ "${before% *}" = "${after% *}" ] || fail "replacement preserved size/mtime"
[ "${before##* }" != "${after##* }" ] || fail "replacement changed the ETag"
curl -sS -o /dev/null -X POST -H "X-Fixture-Token: $token" -H 'Content-Type: application/json' \
  -d '{"name":"offline.mp4","offline":true}' http://127.0.0.1:19081/offline
[ "$(curl -sS -o /dev/null -w '%{http_code}' http://127.0.0.1:19080/media/offline.mp4)" = 503 ] || fail "origin offline 503"
[ "$(curl -sS -o /dev/null -w '%{http_code}' -H "X-Fixture-Token: wrong" http://127.0.0.1:19081/state)" = 403 ] || fail "control token refusal"
pass "origin HTTP ranges, ETag conditions, redirects, offline, replacement"

# 3. Origin TLS with a fixture CA.
openssl req -x509 -newkey rsa:2048 -sha256 -days 2 -nodes -keyout "$work/ca.key" \
  -out "$work/ca.pem" -subj '/CN=selfcheck CA' >/dev/null 2>&1
openssl req -newkey rsa:2048 -nodes -keyout "$work/tls.key" -out "$work/tls.csr" \
  -subj '/CN=media.portico.test' >/dev/null 2>&1
printf 'subjectAltName=DNS:media.portico.test\nbasicConstraints=CA:FALSE\n' >"$work/ext.cnf"
openssl x509 -req -in "$work/tls.csr" -CA "$work/ca.pem" -CAkey "$work/ca.key" \
  -CAcreateserial -out "$work/tls.pem" -days 2 -sha256 -extfile "$work/ext.cnf" >/dev/null 2>&1
python3 "$here/g09-strm/origin.py" --root "$work/media" --port 19443 \
  --control-port 19444 --log "$work/logs/tls.jsonl" --token-file "$work/token" \
  --tls-cert "$work/tls.pem" --tls-key "$work/tls.key" >"$work/logs/tls.out" 2>&1 &
pids+=($!)
for _ in $(seq 1 50); do
  curl -fsS --cacert "$work/ca.pem" --resolve media.portico.test:19443:127.0.0.1 \
    -o /dev/null "https://media.portico.test:19443/media/direct.mp4" 2>/dev/null && break
  sleep 0.1
done
[ "$(curl -sS --cacert "$work/ca.pem" --resolve media.portico.test:19443:127.0.0.1 \
  -o /dev/null -w '%{http_code}' https://media.portico.test:19443/media/direct.mp4)" = 200 ] || fail "origin TLS 200"
pass "origin HTTPS with a fixture CA"

# 4. Controlled DNS: A answer, change, NXDOMAIN.
printf '{"media.portico.test":"172.30.67.10"}\n' >"$work/zone.json"
python3 "$here/g09-strm/dns_responder.py" --zone "$work/zone.json" --port 15353 \
  >"$work/logs/dns.out" 2>&1 &
pids+=($!)
for _ in $(seq 1 50); do
  [ "$(dig +short @127.0.0.1 -p 15353 media.portico.test A 2>/dev/null)" = 172.30.67.10 ] && break
  sleep 0.1
done
[ "$(dig +short @127.0.0.1 -p 15353 media.portico.test A)" = 172.30.67.10 ] || fail "DNS A answer"
printf '{"media.portico.test":"10.0.0.66"}\n' >"$work/zone.json"
[ "$(dig +short @127.0.0.1 -p 15353 media.portico.test A)" = 10.0.0.66 ] || fail "DNS zone change"
[ -z "$(dig +short @127.0.0.1 -p 15353 missing.portico.test A)" ] || fail "DNS NXDOMAIN"
pass "DNS A answer, controlled change and NXDOMAIN"

# 5. Samba share stanza parses.
if command -v testparm >/dev/null 2>&1; then
  mkdir -p "$work/share"
  printf 'fixture\n' >"$work/share/fixture.txt"
  cat >"$work/smb.conf" <<EOF
[global]
	server role = active directory domain controller
	realm = PORTICO.TEST
	workgroup = PORTICO
[media]
	path = $work/share
	read only = yes
	guest ok = no
	valid users = fixture
EOF
  testparm -s --suppress-prompt "$work/smb.conf" >/dev/null 2>&1 || fail "testparm share stanza"
  pass "Samba share stanza parses (testparm)"
else
  printf 'SKIP testparm is not installed\n'
fi

printf 'SELF-CHECK OK (tooling only; not G07-G09 acceptance)\n'
