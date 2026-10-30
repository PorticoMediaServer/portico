#!/bin/sh
set -eu
umask 077
mkdir -p /run/samba /var/lib/samba/private
chmod 0700 /var/lib/samba/private
# The read-only fixture image keeps no server identity. Initialize only its
# throwaway tmpfs database; no owner's account or retained authentication enters.
net --configfile=/etc/samba/smb.conf getlocalsid >/dev/null
exec "$@"
