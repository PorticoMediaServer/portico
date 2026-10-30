#!/usr/bin/env bash
# G03/G04 executable harness: large-playlist display orders + exact query-memory
# accounting. Plan-only by default; execution is confined to mini-2 scratch.
#
# Examples:
#   scripts/batch-round/query-memory/run.sh --tiny            # print both plans
#   scripts/batch-round/query-memory/run.sh --tiny --execute  # tiny runtime rehearsal
#   scripts/batch-round/query-memory/run.sh --execute         # 100k acceptance-sized fixture
#
# The fixture is a real catalogue built by scalebench; the playlist is a real
# playlist read through PlaylistContent. Nothing here writes query results,
# counts or selections to the database (the report proves it by inventory).
set -euo pipefail
root=$(cd -- "$(dirname -- "$0")/../../.." && pwd)
scratch=${HOME}/astra-scratch/P2-65/query
items=100000
tiny=0
execute=0
keep=0
out=""
while (($#)); do
  case $1 in
    --tiny) tiny=1; shift;;
    --execute) execute=1; shift;;
    --keep) keep=1; shift;;
    --root) root=${2:?}; shift 2;;
    --scratch) scratch=${2:?}; shift 2;;
    --out) out=${2:?}; shift 2;;
    --help)
      echo 'run.sh [--tiny] [--execute] [--keep] [--root DIR] [--scratch DIR] [--out DIR]'
      echo 'Plan-only without --execute. Execute only on mini-2, serially.'
      exit 0;;
    *) echo "Unknown argument: $1" >&2; exit 2;;
  esac
done
if ((tiny)); then
  items=100
  playlist_entries=100
  limit=10
  budget=0
  reorders=6
else
  playlist_entries=100000
  limit=100
  budget=0
  reorders=20
fi
expiry=65s
if ((tiny)); then expiry=0s; fi
out=${out:-$scratch/last}
lane=$scratch/lane-$items
fixture=$lane/.scratch/catalogue
host=$(hostname -s)
seed=(go run ./internal/catalog/scalebench -dir "$fixture" -items "$items" -seed-only -idle 1s)
measure=(go run ./internal/catalog/scalebench -dir "$fixture" -items "$items" -reuse -samples 1 -idle 1s
  -playlist -playlist-entries "$playlist_entries" -playlist-limit "$limit"
  -playlist-page-budget "$budget" -playlist-reorders "$reorders" -playlist-readers 2
  -query-memory -query-prepared 5 -query-expiry-wait "$expiry")
printf 'Machine: %s\n' "$host"
printf 'Root: %s\nFixture: %s\nOutput: %s\n' "$root" "$fixture" "$out"
printf 'Seed:   cd %q &&' "$root/server"; printf ' %q' "${seed[@]}"; printf '\n'
printf 'Measure: cd %q &&' "$root/server"; printf ' %q' "${measure[@]}"; printf '\n'
printf 'Verify: python3 %q --playlist %q --memory %q --requested %s%s\n' \
  "$root/scripts/batch-round/query-memory/verify.py" "$out/playlist-report.json" "$out/query-memory-report.json" "$playlist_entries" \
  "$( ((budget)) && printf ' --bounded %s' "$budget" )"
if ((!execute)); then
  echo 'PLAN ONLY; no build, fixture, Go process or database created.'
  exit 0
fi
case $host in ehler-homelab-mini-2) ;; *) echo 'Execute only on mini-2 (~/astra-scratch/P2-65/query); never mini-1' >&2; exit 2;; esac
[[ -z ${CI+x} && -z ${GITHUB_ACTIONS+x} ]] || { echo 'Live scale is prohibited in CI' >&2; exit 2; }
# Serialise: another lane's heavy job must not share the box with this one.
read -r load _ < /proc/loadavg
awk -v l="$load" 'BEGIN{exit !(l<16)}' || { echo "Load ${load} is above 16; wait before executing" >&2; exit 2; }
free_kb=$(df -Pk "$scratch" 2>/dev/null | awk 'NR==2 {print $4}')
if [[ -z $free_kb ]]; then mkdir -p "$scratch"; free_kb=$(df -Pk "$scratch" | awk 'NR==2 {print $4}'); fi
((free_kb > 15*1024*1024)) || { echo 'Need >15 GiB free for this lane' >&2; exit 2; }
[[ ! -e $lane ]] || { echo "Lane already exists: $lane (preserve or remove it before another run)" >&2; exit 2; }
mkdir -m 700 -p "$lane/.scratch/tmp" "$out"
export TMPDIR=$lane/.scratch/tmp
export GOMAXPROCS=4
cd "$root/server"
set +e
"${seed[@]}" > "$out/seed.log" 2>&1
seed_status=$?
set -e
if ((seed_status)); then
  echo "Seed failed; see $out/seed.log" >&2
  exit "$seed_status"
fi
set +e
"${measure[@]}" > "$out/measure.stdout" 2> "$out/measure.stderr"
status=$?
set -e
grep -E '^(PLAYLIST|QUERY_MEMORY|DRAIN|SEED|SLOW_STEP)' "$out/measure.stderr" > "$out/summary.log" || true
cp -f "$fixture/playlist-report.json" "$out/" 2>/dev/null || true
cp -f "$fixture/query-memory-report.json" "$out/" 2>/dev/null || true
verify_status=0
python3 "$root/scripts/batch-round/query-memory/verify.py" \
  --playlist "$out/playlist-report.json" --memory "$out/query-memory-report.json" \
  --requested "$playlist_entries" $( ((budget)) && printf -- '--bounded %s' "$budget" ) || verify_status=$?
# The fixture database is disposable; the reports are the retained evidence.
if ((keep)); then
  echo "Fixture retained at $fixture"
else
  chmod -R u+w -- "$lane" 2>/dev/null || true
  rm -rf -- "$lane"
  echo "Fixture removed; reports in $out"
fi
if ((status)); then exit "$status"; fi
exit "$verify_status"
