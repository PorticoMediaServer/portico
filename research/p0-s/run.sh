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
args=()
if [ "$stage" = seed ]; then args+=(--seed-only); fi
python3 research/p0-s/bench.py --db "$out/library.db" --out "$out" --size "$size" --samples 21 --profile "$profile" "${args[@]}"
for field in memory.peak memory.events cpu.stat io.stat; do
  { echo "$field"; cat "/sys/fs/cgroup$cg/$field"; } >> "$out/cgroup-$stage.txt"
done
if [ "$stage" = probe ]; then python3 research/p0-s/cleanup.py "$out"; fi
