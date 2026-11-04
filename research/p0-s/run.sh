#!/usr/bin/env bash
set -euo pipefail
out=$1; size=$2; profile=$3; stage=$4
mkdir -p "$out"
cg=$(awk -F: '$1=="0" {print $3}' /proc/self/cgroup)
{
 date -u +%FT%TZ
 uname -a
 lscpu
 free -b
 df -B1 "$out"
 for field in memory.max memory.swap.max cpu.max cpuset.cpus.effective; do
   echo "$field"; cat "/sys/fs/cgroup$cg/$field"
 done
} > "$out/host-$stage.txt"
if [ "$stage" = frames ]; then
  "$RUNNER_TEMP/p0-s-full" --ddl research/p0-s/full-09a972.sql --db "$out/full.db" --out "$out" --seed 1000 --normal --write-only --frames
  exit
fi
if [ "$stage" = facets ]; then
  "$RUNNER_TEMP/p0-s-full" --ddl research/p0-s/full-09a972.sql --db "$out/full.db" --out "$out" --seed "$size" --normal --write-only --facets
  exit
fi
if [ "$stage" = normal ]; then
  "$RUNNER_TEMP/p0-s-full" --ddl research/p0-s/full-09a972.sql --db "$out/full.db" --out "$out" --seed 1000000 --normal --write-only
  exit
fi
if [ "$stage" = widthpair ]; then
  for ddl in full-280f03.sql full-09a972.sql; do
    sub="$out/${ddl%.sql}"
    mkdir -p "$sub"
    "$RUNNER_TEMP/p0-s-full" --ddl "research/p0-s/$ddl" --db "$sub/full.db" --out "$sub" --seed 1000000
    python3 - "$sub" <<'PYCODE'
import pathlib,sys
p=pathlib.Path(sys.argv[1])
for name in ['full.db','full.db-wal','full.db-shm']:(p/name).unlink(missing_ok=True)
PYCODE
  done
  exit
fi
if [ "$stage" = fullpair ]; then
  for ddl in full-d07d13.sql full-280f03.sql; do
    sub="$out/${ddl%.sql}"
    mkdir -p "$sub"
    "$RUNNER_TEMP/p0-s-full" --ddl "research/p0-s/$ddl" --db "$sub/full.db" --out "$sub" --seed 1000000
    python3 - "$sub" <<'PYCODE'
import pathlib,sys
p=pathlib.Path(sys.argv[1])
for name in ['full.db','full.db-wal','full.db-shm']:(p/name).unlink(missing_ok=True)
PYCODE
  done
  exit
fi
if [ "$stage" = full ]; then
  "$RUNNER_TEMP/p0-s-full" --ddl "research/p0-s/$TARGET_DDL" --db "$out/full.db" --out "$out" --seed 1000000
  exit
fi
if [ "$stage" = selected ]; then
  export TARGET_ENTITY=1
  python3 research/p0-s/selected.py "$out"
  exit
fi
if [ "$stage" = envelope2 ]; then
  export TARGET_ENTITY=1 SUPPORTED=1 ENVELOPE=1 CUE_RANGE=1
  python3 research/p0-s/adversarial.py --out "$out" --mode fanout
  exit
fi
if [ "$stage" = envelope ]; then
  export TARGET_ENTITY=1 SUPPORTED=1 ENVELOPE=1
  python3 research/p0-s/adversarial.py --out "$out" --mode fanout
  exit
fi
if [ "$stage" = supported ]; then
  export TARGET_ENTITY=1 SUPPORTED=1
  python3 research/p0-s/adversarial.py --out "$out" --mode fanout
  python3 research/p0-s/contribution.py "$out"
  exit
fi
if [ "$stage" = sweep ] || [ "$stage" = fanout ]; then
  python3 research/p0-s/adversarial.py --out "$out" --mode "$stage"
  exit
fi
args=()
if [ "$stage" = seed ]; then args+=(--seed-only); fi
if [ "${MODE:-churn}" = growth ]; then args+=(--growth); fi
python3 research/p0-s/bench.py --db "$out/library.db" --out "$out" --size "$size" --samples 21 --profile "$profile" "${args[@]}"
for field in memory.peak memory.events cpu.stat io.stat; do
  { echo "$field"; cat "/sys/fs/cgroup$cg/$field"; } >> "$out/cgroup-$stage.txt"
done
if [ "$stage" = probe ]; then python3 research/p0-s/cleanup.py "$out"; fi
