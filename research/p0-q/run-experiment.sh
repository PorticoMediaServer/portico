#!/usr/bin/env bash
set -euo pipefail
root="$RUNNER_TEMP/p0-q-experiment"
mkdir -p "$root/out" "$root/work"
mixed_args=(-rounds "${ROUNDS:-2048}")
if [ "${GENERATION:-1}" = 0 ]; then mixed_args+=(-generation=false); fi
if [ -n "${PEOPLE_DIVISOR:-}" ]; then mixed_args+=(-people-divisor "$PEOPLE_DIVISOR"); fi
if [ -n "${CJK_SHARE:-}" ]; then mixed_args+=(-cjk-share "$CJK_SHARE"); fi
if [ "${OLD_RECIPE:-0}" = 1 ]; then mixed_args+=(-old-cjk-recipe); fi
if [ "${SNAPSHOT_CAP:-0}" != 0 ]; then mixed_args+=(-snapshot-cap "${SNAPSHOT_CAP}ms"); fi
if [ "${BOUNDED:-0}" = 1 ]; then mixed_args+=(-bounded-readers); fi
if [ "${MIXED:-0}" = 1 ]; then mixed_args+=(-mixed -mixed-duration 600s); fi
cleanup() { sudo python3 -c 'import shutil,sys; shutil.rmtree(sys.argv[1],ignore_errors=True)' "$root/work"; }
trap cleanup EXIT
lscpu > "$root/out/cpu.txt"
free -b > "$root/out/memory.txt"
lsblk -o NAME,TYPE,SIZE,ROTA,MOUNTPOINT > "$root/out/disks.txt"
go version -m "$RUNNER_TEMP/p0-q-bench" > "$root/out/build.txt"
git rev-parse HEAD > "$root/out/head.txt"
git archive HEAD | sha256sum > "$root/out/archive.sha256"
if [ "$PROFILE" = old8g ]; then
 docker pull ubuntu:24.04
 docker image inspect ubuntu:24.04 --format '{{json .RepoDigests}}' > "$root/out/container.txt"
 docker run --rm --network none --memory 8g --memory-swap 8g --cpus 2 --cpuset-cpus 0-3 \
   -v "$RUNNER_TEMP/p0-q-bench:/bench:ro" -v "$root:/data" ubuntu:24.04 \
   /bin/bash -c "cat /sys/fs/cgroup/memory.max /sys/fs/cgroup/cpu.max; /bench -n $N -variant A -profile old8g -mode $MODE -config $CONFIG -samples ${SAMPLES:-8} ${mixed_args[*]} -dir /data/work" \
   > "$root/out/result.jsonl" 2> "$root/out/result.stderr"
else
 if [ "${TRACE:-0}" = 1 ]; then
  strace -f -ttt -T -e trace=fsync,fdatasync -o "$root/fsync.log" \
   /usr/bin/time -v "$RUNNER_TEMP/p0-q-bench" -n "$N" -variant A -profile standard -mode "$MODE" -config "$CONFIG" -samples ${SAMPLES:-8} "${mixed_args[@]}" -dir "$root/work" \
   > "$root/out/result.jsonl" 2> "$root/out/result.stderr"
  gzip -c "$root/fsync.log" > "$root/out/fsync.log.gz"
 else
  /usr/bin/time -v "$RUNNER_TEMP/p0-q-bench" -n "$N" -variant A -profile standard -mode "$MODE" -config "$CONFIG" -samples ${SAMPLES:-8} "${mixed_args[@]}" -dir "$root/work" \
   > "$root/out/result.jsonl" 2> "$root/out/result.stderr"
 fi
fi
