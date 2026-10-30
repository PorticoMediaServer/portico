#!/usr/bin/env bash
# Start one bounded G07 Portico container.
#
# No host storage is filled. The disk case needs an exact fixed-size filesystem:
# a private loop-backed ext4 image created under the fixture scratch root, with
# mount/umount and loop-detach custody. If the operator has not explicitly
# allowed that loop, the disk phases refuse as BLOCKED-ENV and touch nothing.
#
# No host network, no host port 53/88/445, no host DNS change. The only host
# listener is a loopback-only API port for the host-side driver.
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=../lib/common.sh
. "$here/../lib/common.sh"

usage() {
  cat >&2 <<'USAGE'
usage: run-container.sh --execute --image IMG --state DIR --media DIR --phase NAME --memory 1g|2g|4g|8g [options]
  --tmpfs-mib 512          container-private /memory-transcode tmpfs (0 disables)
  --disk-mib 0             exact disk size; requires --disk-mode loop
  --disk-mode none|loop    "loop" is an explicit, operator-approved root loop mount
  --parent-memory 0        0 or a size such as 2g for a tighter ancestor cgroup
  --network p2-65-g07      private Docker bridge used by this fixture only
  --port 32500             loopback-only published API port for the host driver
  --name portico-g07
USAGE
  exit 2
}

execute=0 image="" state="" media="" phase="" memory="" tmpfs_mib=512 disk_mib=0
disk_mode=none parent_memory=0 network=p2-65-g07 port=32500 name=portico-g07
while [ $# -gt 0 ]; do
  case "$1" in
    --execute) execute=1; shift ;;
    --image) image="$2"; shift 2 ;;
    --state) state="$2"; shift 2 ;;
    --media) media="$2"; shift 2 ;;
    --phase) phase="$2"; shift 2 ;;
    --memory) memory="$2"; shift 2 ;;
    --tmpfs-mib) tmpfs_mib="$2"; shift 2 ;;
    --disk-mib) disk_mib="$2"; shift 2 ;;
    --disk-mode) disk_mode="$2"; shift 2 ;;
    --parent-memory) parent_memory="$2"; shift 2 ;;
    --network) network="$2"; shift 2 ;;
    --port) port="$2"; shift 2 ;;
    --name) name="$2"; shift 2 ;;
    -h|--help) usage ;;
    *) usage ;;
  esac
done
[ "$execute" = 1 ] || { echo "PLAN: would start $name with memory=$memory tmpfs=${tmpfs_mib}MiB disk=${disk_mib}MiB"; exit 0; }
[ -n "$image" ] && [ -n "$state" ] && [ -n "$media" ] && [ -n "$phase" ] && [ -n "$memory" ] || usage
case "$memory" in 1g|2g|4g|8g) ;; *) echo "--memory must be 1g, 2g, 4g or 8g" >&2; exit 2 ;; esac

# CLI values become paths and Docker names; restrict them before any use.
case "$name" in *[!a-z0-9-]*|"") echo "invalid --name" >&2; exit 2 ;; esac
case "$phase" in *[!a-z0-9-]*|"") echo "invalid --phase" >&2; exit 2 ;; esac
case "$disk_mode" in none|loop) ;; *) echo "invalid --disk-mode" >&2; exit 2 ;; esac

fixture_guard
fixture_scratch_root
state="$(realpath -m "$state")"
case "$state" in
  "$FIXTURE_SCRATCH"/*) ;;
  *) fixture_refuse "state directory must stay under $FIXTURE_SCRATCH" ;;
esac
case "$state" in
  *[!A-Za-z0-9/._-]*) fixture_refuse "state path may contain only letters, digits, / . _ -" ;;
esac
fixture_need docker curl jq findmnt mountpoint nsenter realpath
if [ "$disk_mode" = loop ]; then fixture_need truncate mkfs.ext4 losetup; fi
case "$(docker info --format '{{.CgroupVersion}}' 2>/dev/null || echo unknown)" in
  2) ;;
  *) fixture_refuse "cgroup v2 is required" ;;
esac
driver="$(docker info --format '{{.CgroupDriver}}' 2>/dev/null || echo unknown)"

if [ "$disk_mib" != 0 ] && [ "$disk_mode" != loop ]; then
  mkdir -p "$state/log"
  printf '%s\n' "exact disk ceiling needs --disk-mode loop on a fixed-size filesystem" >"$state/log/blocked.txt"
  printf 'BLOCKED-ENV: an exact disk ceiling needs a fixed-size filesystem.\n' >&2
  printf 'No host or Docker-volume space was touched. Run the documented scoped loop command\n' >&2
  printf 'on the disposable host and pass --disk-mode loop, or skip this disk phase.\n' >&2
  exit 4
fi
if [ "$disk_mode" = loop ] && [ "${PORTICO_FIXTURE_DISK_QUOTA:-}" != "loop" ]; then
  mkdir -p "$state/log"
  printf '%s\n' "PORTICO_FIXTURE_DISK_QUOTA=loop was not set; no fixed-size filesystem was created" >"$state/log/blocked.txt"
  printf 'BLOCKED-ENV: set PORTICO_FIXTURE_DISK_QUOTA=loop to allow the scoped loop filesystem.\n' >&2
  exit 4
fi
if [ "$disk_mode" = loop ]; then
  if [ "$disk_mib" -lt 64 ] || [ "$disk_mib" -gt 1024 ]; then
    fixture_refuse "--disk-mib for the loop case must be 64..1024 MiB"
  fi
  if [ "$(id -u)" != 0 ]; then fixture_refuse "the scoped loop case needs root on the disposable host"; fi
fi

mkdir -m 700 -p "$state/log"
config_volume="${name}-config"
transcode_volume="${name}-transcode-${phase}"
network="$(fixture_network "$network" 172.30.64.0/24)"
container_started=0
disk_mounted=0
disk_image=""
disk_loop=""
parent_path=""

cleanup_container() {
  if [ "$container_started" = 1 ]; then docker rm -f "$name" >/dev/null 2>&1 || true; fi
}
cleanup_disk() {
  if [ "$disk_mounted" = 1 ]; then
    umount "$state/disk" || { echo "busy owned mount retained: $state/disk" >&2; return 1; }
    disk_mounted=0
  fi
  if [ -n "$disk_image" ] && [ -f "$disk_image" ]; then
    [ -z "$disk_loop" ] || losetup -d "$disk_loop" || return 1
    rm -f "$disk_image"
  fi
  rmdir "$state/disk" >/dev/null 2>&1 || true
}
cleanup_parent() {
  if [ -n "$parent_path" ]; then rmdir "$parent_path" >/dev/null 2>&1 || true; fi
}
on_exit() {
  local rc=$?
  if [ "$container_started" != 1 ]; then
    cleanup_container
    docker volume rm -f "$transcode_volume" >/dev/null 2>&1 || true
    cleanup_disk
    cleanup_parent
  fi
  exit "$rc"
}
trap on_exit EXIT
trap 'exit 130' INT
trap 'exit 143' TERM HUP

docker volume create "$config_volume" >/dev/null
docker volume create "$transcode_volume" >/dev/null

tmpfs_arg=()
if [ "$tmpfs_mib" != 0 ]; then
  tmpfs_arg=(--tmpfs "/memory-transcode:rw,noexec,nosuid,nodev,size=${tmpfs_mib}m,nr_inodes=8192,mode=1777")
fi

transcode_mount="$transcode_volume"
if [ "$disk_mode" = loop ]; then
  disk_image="$state/transcode-disk.ext4"
  mkdir -m 700 -p "$state/disk"
  # Exact fixed size: the image itself is the ceiling, nothing else on the host
  # is filled. Mount options match the memory volume's hardening.
  [[ ! -e $disk_image ]] || fixture_refuse "owned disk image already exists"
  truncate -s "${disk_mib}M" "$disk_image"
  mkfs.ext4 -q -m 0 -F "$disk_image"
  disk_loop="$(losetup --find --show "$disk_image")"
  mount -o nosuid,nodev,noexec,noatime "$disk_loop" "$state/disk"
  disk_mounted=1
  # Verify the mount really is this image and this size before the container sees it.
  findmnt -n -o SOURCE --target "$state/disk" | grep -qx "$disk_loop" \
    || fixture_refuse "loop mount source did not match $disk_image"
  blocks="$(stat -f -c '%b %S' "$state/disk")"
  total=$(( ${blocks% *} * ${blocks#* } ))
  limit=$(( disk_mib * 1024 * 1024 ))
  [ "$total" -le "$limit" ] || fixture_refuse "loop filesystem is larger than requested"
  transcode_mount="$state/disk"
fi

parent_arg=()
parent_mode="none"
if [ "$parent_memory" != 0 ]; then
  if [ "$driver" = "cgroupfs" ]; then
    parent_path="/sys/fs/cgroup/${name}-parent"
    grep -qw memory /sys/fs/cgroup/cgroup.subtree_control 2>/dev/null \
      || fixture_refuse "the memory controller is not enabled in the root cgroup subtree"
    [ ! -e "$parent_path" ] || fixture_refuse "parent cgroup path already exists"
    mkdir "$parent_path"
    printf '%s' "$parent_memory" >"$parent_path/memory.max"
    parent_arg=(--cgroup-parent="/${name}-parent")
    parent_mode="cgroupfs"
  elif [ "${PORTICO_FIXTURE_ALLOW_TRANSIENT_SLICE:-}" = "yes" ]; then
    parent_unit="${name}-parent"
    systemd-run --quiet --collect --unit="$parent_unit" --slice /bin/sleep 86400 >/dev/null
    systemctl set-property "$parent_unit.slice" "MemoryMax=$parent_memory"
    [ "$(systemctl show -p MemoryMax --value "$parent_unit.slice")" = "$parent_memory" ] \
      || fixture_refuse "parent slice limit did not apply"
    parent_path="$parent_unit.slice"
    parent_arg=(--cgroup-parent="$parent_unit.slice")
    parent_mode="transient-slice"
  else
    mkdir -p "$state/log"
    printf '%s\n' "Docker cgroup driver is $driver; a parent slice needs PORTICO_FIXTURE_ALLOW_TRANSIENT_SLICE=yes" >"$state/log/blocked.txt"
    printf 'BLOCKED-ENV: Docker cgroup driver is %s; set PORTICO_FIXTURE_ALLOW_TRANSIENT_SLICE=yes to bound a parent slice\n' "$driver" >&2
    exit 4
  fi
fi

docker rm -f "$name" >/dev/null 2>&1 || true
docker run -d --name "$name" \
  --label portico.fixture=p2-65-g07 \
  --network "$network" \
  --memory="$memory" --memory-swap="$memory" \
  --pids-limit=512 --cpus=2 \
  "${parent_arg[@]}" \
  -p "127.0.0.1:$port:32500" \
  -e PUID="$(id -u)" -e PGID="$(id -g)" -e UMASK=0077 -e TZ=UTC \
  -v "$config_volume:/config" \
  -v "$media:/media:ro" \
  -v "$transcode_mount:/transcode" \
  "${tmpfs_arg[@]}" \
  --log-driver=local --log-opt max-size=8m --log-opt max-file=2 \
  "$image" >/dev/null
container_started=1

end=$((SECONDS + 180))
until curl -fsS "http://127.0.0.1:$port/v1/server" >/dev/null 2>&1; do
  [ "$SECONDS" -lt "$end" ] || { docker logs --tail 40 "$name" >&2 || true; fixture_refuse "server did not become ready"; }
  sleep 2
done

pid="$(docker inspect -f '{{.State.Pid}}' "$name")"
docker exec "$name" sh -c 'cat /sys/fs/cgroup/memory.max; cat /sys/fs/cgroup/memory.high 2>/dev/null || echo unset' \
  >"$state/log/cgroup-inside.txt"
docker inspect -f '{{.HostConfig.Memory}} {{.HostConfig.MemorySwap}} {{.HostConfig.PidsLimit}} {{.HostConfig.CgroupParent}}' "$name" \
  >"$state/log/cgroup-host.txt"
if [ "$tmpfs_mib" != 0 ]; then
  nsenter -t "$pid" -m -- stat -f -c '%b %f %a %S %c %d' /memory-transcode >"$state/log/tmpfs-statfs.txt"
  nsenter -t "$pid" -m -- findmnt -n -o TARGET,FSTYPE,OPTIONS --target /memory-transcode >"$state/log/tmpfs-mount.txt"
fi
nsenter -t "$pid" -m -- stat -f -c '%b %f %a %S' /transcode >"$state/log/transcode-statfs.txt"
docker image inspect -f '{{.Id}}' "$image" >"$state/log/image-id.txt"
if [ "$disk_mounted" = 1 ]; then
  findmnt -n -o SOURCE,TARGET,FSTYPE,OPTIONS --target "$state/disk" >"$state/log/disk-mount.txt"
fi

jq -n --arg host "$FIXTURE_HOST" --arg address "$FIXTURE_ADDRESS" \
  --arg name "$name" --arg cid "$(docker inspect -f '{{.Id}}' "$name")" \
  --arg image "$(cat "$state/log/image-id.txt")" \
  --arg memory "$memory" --argjson tmpfs "$tmpfs_mib" --argjson disk "$disk_mib" \
  --arg diskMode "$disk_mode" --arg parent "$parent_memory" --arg parentMode "$parent_mode" \
  --arg driver "$driver" --arg phase "$phase" --arg port "$port" --arg network "$network" \
  --arg transcode "$transcode_mount" --arg config "$config_volume" \
  --arg source "$(git -C "$here" rev-parse HEAD 2>/dev/null || echo unknown)" \
  --arg at "$(fixture_now)" \
  '{phase:$phase,host:$host,address:$address,name:$name,containerId:$cid,imageId:$image,
    memoryLimit:$memory,tmpfsMiB:$tmpfs,diskMiB:$disk,diskMode:$diskMode,
    parentMemory:$parent,parentMode:$parentMode,cgroupDriver:$driver,network:$network,
    configVolume:$config,transcodeVolume:$transcode,port:($port|tonumber),
    sourceSha:$source,startedAt:$at}' >"$state/log/container.json"
chmod 600 "$state/log/container.json"

# Cleanup is a fixed, validated path list; no eval and no user-supplied fragment.
{
  printf '#!/usr/bin/env bash\nset -euo pipefail\n'
  printf 'docker rm -f %s >/dev/null 2>&1 || true\n' "$name"
  printf 'docker volume rm -f %s >/dev/null 2>&1 || true\n' "$transcode_volume"
  if [ "$disk_mounted" = 1 ] || [ -n "$disk_image" ]; then
    printf 'umount %s\n' "$state/disk"
    printf 'losetup -d %s\n' "$disk_loop"
    printf 'rm -f %s\n' "$disk_image"
    printf 'rmdir %s >/dev/null 2>&1 || true\n' "$state/disk"
  fi
  if [ -n "$parent_path" ]; then
    if [ "$parent_mode" = transient-slice ]; then
      printf 'systemctl stop %s >/dev/null 2>&1 || true\n' "$parent_path"
    else
      printf 'rmdir %s >/dev/null 2>&1 || true\n' "$parent_path"
    fi
  fi
} >"$state/log/cleanup.sh"
chmod 700 "$state/log/cleanup.sh"
printf 'container %s ready on a private network; loopback API only\n' "$name"
