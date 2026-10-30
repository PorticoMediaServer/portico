# Disposable fixtures for batch-round gaps G07–G09 (P2-65 / O-044)

Operator-only. Nothing here is a CI entry point, npm script, workflow or timer. No
file in this directory is imported by product code. Live execution needs an
explicit `--execute`/phase flag and a disposable host; every runner refuses CI
and refuses mini-1, mini-2 and their Tailscale addresses.

This directory closes the *tooling* part of `astra-integration/BATCH-ROUND-GAPS.md`
G07 (real conversion memory pressure), G08 (real KDC/SMB Kerberos) and G09
(STRM origin and accepted-object runtime). It does not itself make the
acceptance claims: BC-101…BC-114 still need an actual run on the designated
disposable environment, with receipts, before a stage owner may claim them.

## Safety rules encoded here

- No Docker on `ehler-homelab-mini-2` (`100.113.197.98`), ever. The shared
  solution runs on a **designated disposable VPS** only. `lib/common.sh` refuses
  mini-1, mini-2 and both campaign Tailscale addresses.
- The disposable host must be named by the operator through
  `PORTICO_FIXTURE_DISPOSABLE=yes`; the guard prints the hostname and primary
  address into the receipt so the receipt names where it ran.
- No host tmpfs stress and no host/volume filling. G07 uses a container-private
  tmpfs and a **fixed-size loop filesystem** only when the operator explicitly
  allows it. It never fills host or Docker-volume space; the disk phases refuse
  as BLOCKED-ENV instead.
- Never bind host 53/88/445, change host DNS or create host services. G08/G09
  run entirely on private Docker networks with container-internal DNS.
- Never provoke the host OOM killer. G07 sets `memory.max`, `memory.high` and a
  `pids` cap, and treats any `oom`/`oom_kill` increment as a failure, so the
  fixture stops rather than pushes the host into reclaim storms.
- Generated credentials, private keys, tokens, ccaches and receipts stay outside
  Git under `~/astra-scratch/P2-65/disposable/` (0700, files 0600). No real
  credentials are used or accepted.
- Raw STRM URLs, bearer tokens, Kerberos passwords and rclone configs are never
  printed or stored in receipts. G09 records pathnames and hashes only.
- Cleanup is explicit: containers, network, loop files, tmpfs mounts, slices,
  DNS helper, origin processes and scratch state all have teardown traps, and
  `--keep` exists only for an operator inspecting a failure.

## Prerequisites (one disposable Linux host)

- Docker Engine with cgroup v2, root SSH, cgroup v2 `memory` controller, and
  `unshare`/user namespaces available.
- `ffmpeg` and `ffprobe` on the host (media generation), `openssl`, `python3`
  (stdlib only), `curl`, `jq`.
- For G08: SMB/Kerberos ports are container-internal only; host DNS and host port listeners are unchanged. The fixture needs a private Docker bridge and enough disposable memory for Samba AD DC.
- For G09 HTTPS: nothing beyond `openssl`. The private CA and certificate are
  generated into the secret directory.
- A current Portico server image. The image is supplied, never built here:
  `PORTICO_IMAGE=ghcr.io/porticomediaserver/portico-server:<pin>`.
- A source checkout containing this directory, pinned by SHA in the receipt.

## Tooling self-check

`selfcheck.sh` is safe on any machine: it parses every script, runs a loopback
API mock for the envelope/headers, and exercises the real origin HTTP/TLS and
DNS responders plus `testparm`. It starts no Docker, no KDC and no Portico
server. A green self-check is not acceptance evidence for BC-101…BC-114.

```sh
scripts/batch-round/disposable-fixtures/selfcheck.sh
```

## What each fixture does

| Directory | Gap | Shape |
| --- | --- | --- |
| `g07-memory/` | G07 / BC-101…108 | Container with bounded tmpfs, cgroup tiers 2/4/8 GiB, tighter parent, ≤1 GiB disk loop, 100 ms sampler, 2–4 real conversions with seeks/rendition/subtitles, cap/location changes, spill/restart/ENOSPC fault points |
| `g08-smb-kerberos/` | G08 / BC-110…114 | Disposable Samba AD DC (real KDC + SMB), registered `cifs/dc1.portico.test` SPN, rclone implicit/explicit SPN runs, numeric-dial traces, production child-environment reproduction, DNS removal/re-pin |
| `g09-strm/` | G09 / BC-113…114 | HTTP(S) origin with strong ETags, byte ranges, in/out-of-root redirects, offline toggle, same-size/same-mtime replacement, controlled DNS, origin request log; server-side grant/refusal checks |

## Order and expected outcome

Run G09 first (cheapest, no container), then G08, then G07 (longest, host
memory work last). Each runner writes `receipt.json`, `samples.jsonl` or
`origin.jsonl` and a `README`-level summary into `$FIXTURE_EVIDENCE/<phase>/`,
mode `0600`.

A green receipt is evidence for that phase on that host only. The stage owner
still has to attach it to the BC row, name the image/source pin, and state
whether any required case is `BLOCKED-ENV`.

## What is not covered here

- Web/phone/tablet/TV playback remains device work (G10). These fixtures drive
  the server API and inspect grants, bytes and origin logs; they do not claim
  client decoding, focus or visual acceptance.
- Hardware-encoder-only launch (G01) is a separate media concern; G07 uses
  software conversions on a host with no GPU and says so.
- A real KDC on a production host is not established by the container fixture;
  it qualifies the SMB/Kerberos path and the numeric pinning contract.

## Canonical playback prerequisite and light coverage

G07/G09 playback commands now use a persistent private stdio bridge to the real
client-core SessionsClient, CapabilityPublisher, ObservationReporter and durable
mutation coordinator. No raw Python session writer substitutes for the client.
Both require qualified Node24.21.0 and an actual captured capability document:

```sh
export PORTICO_FIXTURE_NODE="$BIN/node"
export PORTICO_FIXTURE_CAPABILITIES="$PRIVATE_INPUT/capabilities.json" # owned0600
```

G09 mounts this exact runtime and the complete source checkout into its private
Python driver container. Run from a complete checkout as the disposable root
operator. Source tooling imported by Node uses no extra runtime dependency.
The bridge owns start preparation, generation-aware changes, observation leases,
final Stop and custody acknowledgement; stop/custody errors fail instead of
silently passing. Grant/session replies stay on pipes; artifacts redact grants.
A real tiny HTTP start/read/Stop through Python+this bridge passed on core.

The HTTP/TLS/DNS/ETag/range/replacement origin self-check and API envelope/setup
mock pass. The real KDC light run passes both numeric/SPN carriers in a shared
private network namespace. Full G07 pressure, G08 cross-bridge/default-ccache/
DNS-repin and G09 accepted-object/app cases are **UNRUN**, with an immutable
current server image and a suitable disposable host required for the round.
The8GiB pressure phase must not run on core's8GiB host while Hosted is running.

G07 now propagates phase assertion failures, resolves the actual /dev/loop
mount source and retains busy mounts/images on failed cleanup. G08/G09 refuse
retained named fixtures and install cleanup before mutations; early startup
errors retire created containers/networks/volumes. No Docker prune is used.
