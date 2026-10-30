#!/usr/bin/env bash
set -euo pipefail
root=$(cd -- "$(dirname -- "$0")/../.." && pwd)
items=1000000
samples=20
idle=1m
execute=0
tiny=0
extra=(-exercise)
while (($#)); do
  case $1 in
    --items) items=${2:?}; shift 2;;
    --tiny) tiny=1; items=100; samples=1; idle=1s; shift;;
    --execute) execute=1; shift;;
    --help) echo 'scalebench.sh [--items 1000000|5000000 | --tiny] [--execute]'; exit 0;;
    *) echo 'Unknown argument' >&2; exit 2;;
  esac
done
if ((tiny)); then extra=(-matrix-only -only '^movies/movies/sort/title/'); fi
case $items in 1000000|5000000) ;; 100) ((tiny)) || exit 2;; *) exit 2;; esac
host=$(hostname -s)
case $host in ehler-homelab-mini-2) ;; portico-runner) ((tiny == 0)) || { echo 'Tiny rehearsal belongs on mini-2' >&2; exit 2; };; *)
  if ((execute)); then echo 'Execute only on mini-2 or portico-runner; never mini-1' >&2; exit 2; fi;;
esac
# An independent root BEFORE /.scratch/ is important: scalebench measures its ancestor.
lane=$root/.batch-scale-$items
fixture=$lane/.scratch/catalogue
printf 'Machine: %s\n' "$host"
printf 'Command: cd %q && GOMAXPROCS=4 go run ./internal/catalog/scalebench -dir %q -items %s -samples %s -idle %s' "$root/server" "$fixture" "$items" "$samples" "$idle"
printf ' %q' "${extra[@]}"
printf '\n'
if ((!execute)); then echo 'PLAN ONLY; no directories, build or database created'; exit 0; fi
if [[ -n ${CI+x} || -n ${GITHUB_ACTIONS+x} ]]; then echo 'Live scale is prohibited in CI' >&2; exit 2; fi
[[ ! -e $lane ]] || { echo 'Dedicated lane already exists; preserve/reconcile it before another run' >&2; exit 2; }
free_kb=$(df -Pk "$root" | awk 'NR==2 {print $4}')
((free_kb > 25*1024*1024)) || { echo 'Need >25 GiB free before scale' >&2; exit 2; }
mkdir -m 700 -p "$lane/.scratch/tmp"
trap 'rm -rf -- "$lane/.scratch/tmp"' EXIT
export TMPDIR=$lane/.scratch/tmp
export GOMAXPROCS=4
cd "$root/server"
set +e
go run ./internal/catalog/scalebench -dir "$fixture" -items "$items" -samples "$samples" -idle "$idle" "${extra[@]}" > "$lane/stdout.log" 2> "$lane/stderr.log"
result=$?
set -e
set +e
python3 - "$lane" "$items" "$result" <<'PY'
import json, pathlib, sys, time
lane=pathlib.Path(sys.argv[1])
route_errors=[]
for line in (lane/'stderr.log').read_text().splitlines():
 try:
  entry=json.loads(line)
 except ValueError:
  continue
 if isinstance(entry,dict) and entry.get('error'):
  route_errors.append(str(entry.get('route','unknown'))[:256])
receipt={'routeErrors':route_errors,'items':int(sys.argv[2]),'exitCode':int(sys.argv[3]),'atUnix':time.time(),
         'database':'.scratch/catalogue/catalog.sqlite','acceptanceScale':int(sys.argv[2]) in (1000000,5000000),
         'logs':['stdout.log','stderr.log'],'scope':'catalogue only; not full-server capacity'}
target=lane/'receipt.json'; temp=target.with_suffix('.tmp');temp.write_text(json.dumps(receipt,indent=2)+'\n');temp.replace(target)
print(json.dumps(receipt))
raise SystemExit(1 if route_errors else 0)
PY
check_result=$?
set -e
if ((result == 0 && check_result != 0)); then result=$check_result; fi
exit "$result"
