#!/bin/sh
set -eu
# In rootless Docker, container uid 0 is the unprivileged host user. A
# subuid such as container 1000 cannot inherit that user's render-node ACL.
default_uid=1000
default_gid=1000
if awk 'NR==1 { exit !($1==0 && $2!=0) }' /proc/self/uid_map; then
 default_uid=0
 default_gid=0
fi
uid=${PUID:-$default_uid}
gid=${PGID:-$default_gid}
mask=${UMASK:-0077}
for identity in "$uid" "$gid"; do
 case "$identity" in ''|*[!0-9]*) echo 'PUID and PGID must be numeric' >&2; exit 1;; esac
done
case "$mask" in [0-7][0-7][0-7]|0[0-7][0-7][0-7]) ;; *) echo 'UMASK must be three octal digits, optionally prefixed by 0' >&2; exit 1;; esac
# libc honours TZ without writing /etc/localtime in the container layer.
if [ -n "${TZ:-}" ]; then
 case "$TZ" in /*|*..*|*[!A-Za-z0-9_+/:.-]*) echo 'TZ must be an IANA time zone' >&2; exit 1;; esac
 [ -f "/usr/share/zoneinfo/$TZ" ] || { echo 'TZ is not installed' >&2; exit 1; }
 export TZ
fi
umask "$mask"
export HOME=/config TMPDIR=/config/Temp TMP=/config/Temp TEMP=/config/Temp
# A directory existing in the image is not a persistent mount.
if ! awk '$5 == "/config" { found=1 } END { exit !found }' /proc/self/mountinfo; then
 echo 'Map /config to persistent storage before starting Portico' >&2; exit 1
fi
if [ "$(id -u)" = 0 ]; then
 chown "$uid:$gid" /config
 if awk '$5 == "/transcode" { found=1 } END { exit !found }' /proc/self/mountinfo; then chown "$uid:$gid" /transcode; fi
 # Preserve explicit Docker --group-add device groups while dropping group 0.
 groups="$gid"
 for group in $(id -G); do
  if [ "$group" != 0 ] && [ "$group" != "$gid" ]; then groups="$groups,$group"; fi
 done
 # Keep existing supplementary groups, then discover the actual device GIDs.
 # No /etc/passwd edits or recursive ownership changes to the owner's media.
 for device in /dev/dri/* /dev/nvidia* /dev/dxg /dev/mpp_service /dev/rga; do
  [ -c "$device" ] || continue
  group=$(stat -c '%g' "$device")
  case ",$groups," in *",$group,"*) ;; *) [ "$group" = 0 ] || groups="$groups,$group";; esac
 done
 exec setpriv --reuid="$uid" --regid="$gid" --groups="$groups" "$@"
fi
exec "$@"
