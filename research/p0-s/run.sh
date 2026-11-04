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
if [ "$stage" = full ]; then
  "$RUNNER_TEMP/p0-s-full" --ddl "research/p0-s/$TARGET_DDL" --db "$out/full.db" --out "$out" --seed 1000000
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
