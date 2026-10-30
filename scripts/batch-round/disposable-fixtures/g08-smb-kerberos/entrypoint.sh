#!/bin/sh
# Idempotent first-run provisioning for the disposable AD DC. Every secret is a
# generated throwaway value supplied by the operator's environment; nothing is
# written to a receipt or a log.
set -eu

: "${REALM:?REALM required}"
: "${DOMAIN:?DOMAIN required}"
: "${ADMIN_PASS:?ADMIN_PASS required}"
: "${FIXTURE_PASS:?FIXTURE_PASS required}"
SPN_ALIAS="${SPN_ALIAS:-}"

if [ ! -f /var/lib/samba/private/sam.ldb ]; then
  rm -f /etc/samba/smb.conf
  samba-tool domain provision \
    --server-role=dc \
    --use-rfc2307 \
    --dns-backend=SAMBA_INTERNAL \
    --realm="$REALM" \
    --domain="$DOMAIN" \
    --adminpass="$ADMIN_PASS" \
    --option="dns forwarder=127.0.0.11"
  cp /var/lib/samba/private/krb5.conf /etc/krb5.conf
fi

mkdir -p /srv/media
if [ ! -f /srv/media/fixture.txt ]; then
  printf 'P2-65 KDC/SMB fixture\n' >/srv/media/fixture.txt
  printf 'second file for directory listings\n' >/srv/media/second.txt
  chmod 0644 /srv/media/fixture.txt /srv/media/second.txt
fi

if ! grep -q '^\[media\]' /etc/samba/smb.conf; then
  cat >>/etc/samba/smb.conf <<'SHARE'

[media]
	path = /srv/media
	read only = yes
	guest ok = no
	valid users = fixture
SHARE
fi

if ! samba-tool user list | grep -qx fixture; then
  samba-tool user create fixture "$FIXTURE_PASS"
fi
samba-tool user setexpiry fixture --noexpiry >/dev/null

# An explicit owner-style SPN on the DC machine account, when requested.
if [ -n "$SPN_ALIAS" ]; then
  if ! samba-tool spn list 'DC1$' | grep -q "cifs/$SPN_ALIAS"; then
    samba-tool spn add "cifs/$SPN_ALIAS" 'DC1$' || printf 'SPN alias add failed; explicit-SPN case will fail honestly\n' >&2
  fi
fi

printf 'fixture DC host=%s realm=%s\n' "$(hostname -f)" "$REALM"
exec samba --foreground --no-process-group --debug-stdout
