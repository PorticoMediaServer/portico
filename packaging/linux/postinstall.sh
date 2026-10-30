#!/bin/sh
set -eu
if ! getent group portico >/dev/null 2>&1; then groupadd --system portico; fi
if ! id portico >/dev/null 2>&1; then useradd --system --gid portico --home-dir /var/lib/portico --shell /usr/sbin/nologin portico; fi
# Grant only available device groups; no broad device sandbox restriction.
for group in render video; do
 if getent group "$group" >/dev/null 2>&1; then usermod -a -G "$group" portico; fi
done
install -d -o portico -g portico -m 0700 /var/lib/portico /var/cache/portico

# Debian runs this as "configure <previous version>", with no previous version
# on a first install. RPM passes how many copies of the package are installed:
# 1 on a first install, 2 or more on an upgrade.
family=rpm
case "${1:-}" in
  configure) family=deb; if [ -n "${2:-}" ]; then mode=upgrade; else mode=install; fi ;;
  abort-remove) family=deb; mode=install ;;
  abort-upgrade | abort-deconfigure) family=deb; mode=upgrade ;;
  1 | '') mode=install ;;
  *) mode=upgrade ;;
esac

# On Debian a package may start or restart a service only if the
# administrator's policy-rc.d allows it; deb-systemd-invoke asks before it
# acts, and does nothing when the answer is no. RPM systems have no such layer.
service_action() {
  if [ "$family" = deb ] && command -v deb-systemd-invoke >/dev/null 2>&1; then
    deb-systemd-invoke "$1" portico-media-server.service || true
  else
    systemctl "$1" portico-media-server.service || true
  fi
}

if command -v systemctl >/dev/null 2>&1; then
  systemctl daemon-reload || true
  if [ "$mode" = install ]; then
    systemctl enable portico-media-server.service || true
  fi
  # /run/systemd/system exists only while systemd is the running init, so an
  # image build or a chroot installs the service without trying to start it.
  if [ -d /run/systemd/system ]; then
    if [ "$mode" = install ]; then
      service_action start
    elif systemctl is-active --quiet portico-media-server.service; then
      # An upgrade keeps what the owner had: a running server moves onto the
      # new build, and one they stopped or disabled stays that way.
      service_action restart
    fi
  fi
fi
