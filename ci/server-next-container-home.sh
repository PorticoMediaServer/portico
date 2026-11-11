#!/usr/bin/env bash
# The server in a container on its own bridge network ("lan", 172.30.0.0/24) with a published
# port. A client on that network is the household's side; a client on another network ("wan",
# 172.31.0.0/24) reaches the published port through the host and arrives with its own address,
# as a visitor from outside does. Each signs in, and the owner's sessions list says home or away.
set -euo pipefail
server=$1
image=debian:12-slim
curl_image=curlimages/curl:8.11.0
device() { printf '{"installationId":"install-%027d","name":"%s"}' "$1" "$2"; }

docker network create --subnet 172.30.0.0/24 lan >/dev/null
docker network create --subnet 172.31.0.0/24 wan >/dev/null
docker run -d --name portico --network lan --ip 172.30.0.2 -p 32500:32500 \
  -e PORTICO_TMDB_ORIGIN=http://127.0.0.1:9 -v "$server:/portico-server:ro" \
  "$image" /portico-server -data /data -listen 0.0.0.0:32500 >/dev/null
for _ in $(seq 60); do
  curl -sf http://127.0.0.1:32500/v1/health/live >/dev/null && break
  sleep 1
done
code=$(docker exec portico cat /data/setup-code.txt | tr -d '\r\n')
api=http://127.0.0.1:32500
setup=$(curl -sf -X POST "$api/v1/setup/owner" -H 'Content-Type: application/json' \
  -d "{\"setupCode\":\"$code\",\"username\":\"owner\",\"password\":\"correct horse\",\"device\":$(device 1 host)}")
profile=$(jq -r '.profiles[0].id' <<<"$setup")
token=$(curl -sf -X POST "$api/v1/profiles/$profile/select" -H "Authorization: Bearer $(jq -r '.session.accessToken' <<<"$setup")" \
  -H 'Content-Type: application/json' -d '{}' | jq -r '.accessToken')

# The same sign-in from the household's network and from outside.
sign_in() { # network, address of the server from there, installation number, device name
  docker run --rm --network "$1" "$curl_image" -sf -X POST "http://$2:32500/v1/auth/sign-in" \
    -H 'Content-Type: application/json' \
    -d "{\"username\":\"owner\",\"password\":\"correct horse\",\"device\":$(device "$3" "$4")}" >/dev/null
}
sign_in lan 172.30.0.2 2 lan-device
sign_in wan 172.31.0.1 3 outside-device

where() { # device name: home or away, as the owner's sessions list says
  curl -sf "$api/v1/me/sessions" -H "Authorization: Bearer $token" | jq -r --arg d "$1" '[.items[] | select(.deviceName == $d)][0].network'
}
check() { # device, want
  got=$(where "$1")
  echo "$1: $got"
  [ "$got" = "$2" ] || { echo "::error::$1 counts as $got, want $2"; exit 1; }
}

echo '--- while the server is guessing'
check lan-device away
check outside-device away
warning=$(curl -sf "$api/v1/admin/settings/network" -H "Authorization: Bearer $token" | jq -r '[.warnings[].code] | index("container_no_home") != null')
[ "$warning" = true ] || { echo '::error::the owner is not told that nothing counts as at home'; exit 1; }

echo '--- after the owner lists the lan as home'
curl -sf -X PUT "$api/v1/admin/settings/network" -H "Authorization: Bearer $token" -H 'Content-Type: application/json' \
  -d '{"homeNetworks":["172.30.0.0/24"],"trustedProxies":[]}' >/dev/null
check lan-device home
check outside-device away
