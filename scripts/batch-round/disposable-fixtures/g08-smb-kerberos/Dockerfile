# Disposable Samba AD DC: a real KDC and SMB server in one container.
# Only the fixture network reaches it; no host port is published.
FROM debian:bookworm-slim

ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      samba winbind samba-ad-provision samba-dsdb-modules samba-vfs-modules smbclient krb5-user dnsutils ldb-tools ca-certificates procps \
 && rm -rf /var/lib/apt/lists/*

COPY entrypoint.sh /usr/local/bin/fixture-dc-entrypoint
RUN chmod 0755 /usr/local/bin/fixture-dc-entrypoint

EXPOSE 53/tcp 53/udp 88/tcp 88/udp 389/tcp 445/tcp 464/tcp 636/tcp
ENTRYPOINT ["/usr/local/bin/fixture-dc-entrypoint"]
