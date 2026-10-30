# G08 — real KDC and SMB Kerberos (BC-110…114)

Everything runs inside the private Docker network `p2-65-g08`
(`172.30.65.0/24`). No host port 53/88/445 is bound, no host DNS is changed, and
no host mount or service is created. The DC, client and Portico server are
fixture containers; the driver is a `python:3.13-slim` container on the same
network.

## Shape

- `p2-65-g08-dc` (`dc1.portico.test`, 172.30.65.10): Samba AD DC with its
  internal KDC and DNS. Realm `PORTICO.TEST`, domain `PORTICO`. Share `media`
  holds generated files; user `fixture` is created with a throwaway password.
  The DC machine account carries `cifs/DC1.portico.test`; `G08_SPN_ALIAS` adds a
  second owner-style SPN when the operator wants one.
- `p2-65-g08-client` (172.30.65.20): the pinned Portico image plus `krb5-user`,
  `smbclient` and `strace`. `kinit`, `smbclient -k` and the bundled rclone run
  here.
- `p2-65-g08-server` (172.30.65.30): the unmodified Portico image with
  `--dns 172.30.65.10`, a read-only `/etc/krb5.conf`, and the shared ccache
  volume. No published port; the driver reaches it by container name.

## What is proven

1. `kinit fixture` against the real KDC, then `klist`.
2. `smbclient //dc1.portico.test/media -k` reads the share with Kerberos.
3. The **exact carrier Portico's pin rewrite produces**: `host = 172.30.65.10`
   (the selected numeric address) with `spn = cifs/dc1.portico.test` (the
   original hostname preserved). This is `remotesources/smb_pins.go`: the child
   INI gets the numeric host and, when Kerberos had no owner SPN, the original
   hostname as `cifs/<host>`.
4. The same numeric transport with an **owner-set explicit SPN**
   (`spn = cifs/smb-alias.portico.test`), registered on the DC machine account
   by the entrypoint. It must be accepted by the KDC and smbd.
5. A hostname-transport control (`host = dc1.portico.test`, no `spn`) exercises
   rclone's own implicit `cifs/<host>` derivation and gives the DNS-removal case
   something to resolve. It is a control, never the pinned carrier.
6. A `strace` connect trace keeps the raw destination lines, including the
   numeric address (`sin_addr=inet_addr("172.30.65.10")`) and port `445`; the
   receipt never contains config bytes.
7. The **production child environment** is reproduced with `env -i` inside the
   server container: `PATH`, `HOME`, `TMPDIR` only, the bundled rclone, and no
   `KRB5CCNAME`. This tests the launch path's actual environment and the default
   ccache location requirement.
8. DNS removal (`samba-tool dns delete`) makes a fresh hostname resolution and a
   fresh hostname-transport rclone run fail; re-adding the record restores it.
9. A Portico server restart returns to a ready state.
10. The current saved-source candidate route (`POST
    /v1/storage/sources/rclone/test`, registered at `foundation.go:705`, defined
    at `remote_rclone_connection.go:43`) is exercised and its exact outcome
    recorded. On an unapproved private endpoint the current policy refuses before
    contact; that is the approval boundary, not a Kerberos result, and the saved
    source path needs an owner approval before it can be claimed.

## Commands

```sh
export PORTICO_FIXTURE_DISPOSABLE=yes
scripts/batch-round/disposable-fixtures/g08-smb-kerberos/run.sh --execute \
  --portico-image "$PORTICO_IMAGE"
# keep the environment for inspection (then remove it explicitly):
scripts/batch-round/disposable-fixtures/g08-smb-kerberos/run.sh --execute \
  --portico-image "$PORTICO_IMAGE" --keep --no-cleanup
```

Receipts land in `~/astra-scratch/P2-65/disposable/evidence/<run>/g08/`.
Secrets (throwaway KDC/admin/fixture passwords, obscured pass, krb5.conf,
ccache export) stay in `~/astra-scratch/P2-65/disposable/secrets` at 0600.
No real credentials are used or accepted.

## Known prerequisite gap

The production launch path constructs a fixed child environment (`PATH`,
`HOME`, `TMPDIR`, config-pass and proxy variables) and does not forward
`KRB5CCNAME` or `KRB5_CONFIG` (`internal/mounts/guardian.go`, `native.go`,
`rcloneprogram`). Kerberos therefore works through Portico only when
`/etc/krb5.conf` and the credential cache are at their default locations for the
server's UID, refreshed by something outside Portico. Phase B measures that
exactly; it does not paper over it. If Phase B fails while Phase A passes, the
result is a product/env finding for the Media/P2-33 owner, not a fixture error.

## Light rehearsal actually run

`rehearse.sh` passed real kinit/TGT, smbclient Kerberos and qualified rclone1.75.2
against a disposable Samba AD/KDC. Both numeric transport cases (original SPN
and explicit alias SPN) succeeded and retained raw numeric connect-to445 lines.
The client shared the DC's private network namespace because cross-container
bridge traffic timed out on that compute host. Its receipt explicitly says so:
this is real KDC/auth/numeric transport evidence, **not** cross-bridge, DNS repin,
Portico child/default-ccache or fullG08 acceptance. No host ports or DNS were
changed. All created containers, networks and images were removed.

```sh
PORTICO_FIXTURE_DISPOSABLE=yes \
 scripts/batch-round/disposable-fixtures/g08-smb-kerberos/rehearse.sh \
 --execute --rclone "$BIN/rclone" --scratch "$HOME/astra-scratch/P2-65/smb" \
 --receipt "$HOME/astra-scratch/P2-65/smb-light.json"
```

The full run still needs an approved disposable host whose private Docker
bridge works, a pinned current server image and the documented default Kerberos
cache path. Production child launch omits KRB5CCNAME/KRB5_CONFIG; the fixture
records that constraint and does not silently forward extra environment state.
