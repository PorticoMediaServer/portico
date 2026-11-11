#!/usr/bin/env bash
# The server in a container on its own bridge network ("lan", 172.30.0.0/24) with a published
# port. Three devices sign in, and the owner's sessions list says home or away:
#   - lan-device, on that network, the household's side;
#   - outside-device, in a network namespace joined to the host by a veth pair (10.99.0.0/24),
#     reaches the published port the way another machine does and keeps its own address;
#   - relayed-device, on another Docker network ("wan", 172.31.0.0/24), reaches the published
#     port through the host, and Docker relays it so it arrives from the gateway, 172.30.0.1.
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

# The same sign-in from the household's network, through Docker's relay, and from outside.
sign_in() { # network, address of the server from there, installation number, device name
  docker run --rm --network "$1" "$curl_image" -sf -X POST "http://$2:32500/v1/auth/sign-in" \
    -H 'Content-Type: application/json' \
    -d "{\"username\":\"owner\",\"password\":\"correct horse\",\"device\":$(device "$3" "$4")}" >/dev/null
}
sign_in lan 172.30.0.2 2 lan-device
sign_in wan 172.31.0.1 3 relayed-device
sudo ip netns add outside
sudo ip link add portico-out type veth peer name portico-in
sudo ip link set portico-in netns outside
sudo ip addr add 10.99.0.1/24 dev portico-out
sudo ip link set portico-out up
sudo ip netns exec outside ip addr add 10.99.0.2/24 dev portico-in
sudo ip netns exec outside ip link set portico-in up
sudo ip netns exec outside ip link set lo up
sudo ip netns exec outside ip route add default via 10.99.0.1
sudo ip netns exec outside curl -sf --max-time 20 -X POST "http://10.99.0.1:32500/v1/auth/sign-in" \
  -H 'Content-Type: application/json' \
  -d "{\"username\":\"owner\",\"password\":\"correct horse\",\"device\":$(device 4 outside-device)}" >/dev/null

where() { # device name: home or away, as the owner's sessions list says
  curl -sf "$api/v1/me/sessions" -H "Authorization: Bearer $token" | jq -r --arg d "$1" '[.items[] | select(.deviceName == $d)][0].network'
}
check() { # device, want
  got=$(where "$1")
  echo "$1: $got"
  [ "$got" = "$2" ] || { echo "::error::$1 counts as $got, want $2"; exit 1; }
}

warned() { # warning code: whether the owner's network settings show it
  curl -sf "$api/v1/admin/settings/network" -H "Authorization: Bearer $token" | jq -r --arg c "$1" '[.warnings[].code] | index($c) != null'
}

echo '--- while the server is guessing'
check lan-device away
check outside-device away
check relayed-device away
[ "$(warned container_no_home)" = true ] || { echo '::error::the owner is not told that nothing counts as at home'; exit 1; }

echo '--- after the owner lists the lan as home'
curl -sf -X PUT "$api/v1/admin/settings/network" -H "Authorization: Bearer $token" -H 'Content-Type: application/json' \
  -d '{"homeNetworks":["172.30.0.0/24"],"trustedProxies":[]}' >/dev/null
check lan-device home
check outside-device away
# The listed network holds the gateway, so what Docker relays through it counts as at home
# too, as the owner said, and the owner is told so (#2647).
check relayed-device home
[ "$(warned container_no_home)" = false ] || { echo '::error::the warning stays after the owner listed home networks'; exit 1; }
[ "$(warned gateway_at_home)" = true ] || { echo '::error::the owner is not told that relayed visitors count as at home'; exit 1; }
