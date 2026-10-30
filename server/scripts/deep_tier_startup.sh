#!/bin/sh
# What a million-item library costs to open and to sit idle.
#
# The bar includes "startup within 1–2 s" and it is the one number a scan cannot
# amortise: a person restarting the server waits for it. This copies a built
# fixture into a state directory, starts the server, times the listener becoming
# ready, and then samples what the process costs while nobody is using it.
#
# Usage: deep_tier_startup.sh <fixture.sqlite> <scratch-dir> [server-binary]
set -eu
fixture=$1
scratch=$2
binary=${3:-"$scratch/srv"}

mkdir -p "$scratch/state/Database"
# A copy-on-write clone where the filesystem has one: a deep fixture is twelve
# gigabytes and this measurement is about opening it, not about copying it.
cp -c "$fixture" "$scratch/state/Database/server.sqlite" 2>/dev/null || cp "$fixture" "$scratch/state/Database/server.sqlite"
rm -f "$scratch/state/Database/server.sqlite-wal" "$scratch/state/Database/server.sqlite-shm"
printf 'database %s MB\n' "$(( $(wc -c < "$scratch/state/Database/server.sqlite") / 1048576 ))"

PORTICO_DATA_DIR="$scratch/state" PORTICO_BIND=127.0.0.1:32599 "$binary" > "$scratch/server.log" 2>&1 &
pid=$!
trap 'kill "$pid" 2>/dev/null || true' EXIT

start=$(date +%s.%N)
ready=""
while [ -z "$ready" ]; do
  # While the server is coming up the listener answers 503 "starting" with the
  # phases it has finished; readiness is the first answer that is not that.
  body=$(curl -s --max-time 2 "http://127.0.0.1:32599/v1/readiness" 2>/dev/null || echo "")
  case "$body" in
    *'"ready":false'*|"") sleep 0.02 ;;
    *) ready=ready ;;
  esac
  if ! kill -0 "$pid" 2>/dev/null; then
    echo "the server exited during startup:"; tail -20 "$scratch/server.log"; exit 1
  fi
done
end=$(date +%s.%N)
printf 'ready after %.2f s (health answered %s)\n' "$(echo "$end - $start" | bc)" "$ready"

# Idle cost: sample a minute after the deferred backfills have had their chance.
sleep 30
printf 'idle at 30 s: '
ps -o %cpu=,rss= -p "$pid" | awk '{printf "cpu %s%%  rss %d MB\n", $1, $2/1024}'
sleep 30
printf 'idle at 60 s: '
ps -o %cpu=,rss= -p "$pid" | awk '{printf "cpu %s%%  rss %d MB\n", $1, $2/1024}'
