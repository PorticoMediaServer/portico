FROM debian:trixie-slim@sha256:62bd423aeee912639ca8ee681c2b762cb3e25137e0a92b20be455898a9f034ad
LABEL portico="remote-sources-r40"
RUN apt-get -o Acquire::Retries=0 -o Acquire::http::Timeout=15 -o APT::Update::Error-Mode=any update \
    && apt-get -o Acquire::Retries=0 -o Acquire::http::Timeout=15 install -y --no-install-recommends samba \
    && rm -rf /var/lib/apt/lists/* \
    && smbd --version
COPY smb.conf /etc/samba/smb.conf
COPY entrypoint.sh /usr/local/bin/portico-smb-fixture
ENTRYPOINT ["/bin/sh", "/usr/local/bin/portico-smb-fixture"]
CMD ["smbd", "--foreground", "--no-process-group", "--debug-stdout", "--configfile=/etc/samba/smb.conf"]
