# G09 — STRM origins and accepted objects (BC-113…114)

Private network `p2-65-g09` (`172.30.67.0/24`) only. No host port 53/88/445, no
host DNS change, no host mount. The origins, the authoritative DNS responder,
the Portico server and the driver are all fixture containers.

## Shape

| Container | Address | Role |
| --- | --- | --- |
| `p2-65-g09-origin` | `172.30.67.10` | HTTP origin: MP4, MKV, HEVC 10-bit, ranges, strong ETags, redirects, offline, replacement |
| `p2-65-g09-origin-bad` | `172.30.67.11` | a second origin used only as an unapproved redirect target |
| `p2-65-g09-origin-tls` | `172.30.67.12` | HTTPS origin with a fixture CA (`SSL_CERT_FILE` inside the server) |
| `p2-65-g09-dns` | `172.30.67.53` | authoritative `media.portico.test`, zone file rewritten for the DNS-change case |
| `p2-65-g09-server` | `172.30.67.20` | unmodified Portico image, `--dns 172.30.67.53`, no published port |
| driver | one-off `python:3.13-slim` | bootstrap, scan, sessions, origin-log assertions |

The origin serves a strong `ETag` (`"sha256-<content>"`), `Accept-Ranges`,
`If-Match` → `412`, `If-None-Match` → `304`, exact `Content-Range`, and a
per-file `503 source_unavailable` with `Retry-After`. Replacement keeps the old
size and `mtime` but changes the strong ETag, which is exactly the accepted-object
falsifier.

## Cases the driver records

- direct MP4: direct mode, bytes through a `/v1/media/` grant, origin hit by the
  server IP only, no origin host in the grant.
- HTTPS direct with the fixture CA.
- Matroska H.264 remux (`mode != direct`, HLS container) plus a seek that keeps a
  playable playlist.
- HEVC 10-bit conversion (`mode != direct`).
- in-root redirect follows; out-of-root redirect and metadata redirect are
  refused with **no request to the refused origin**.
- offline origin returns retryable `source_unavailable`, then recovers.
- same-size/same-mtime replacement: old session's next range fails, new session
  serves the new bytes (hash-compared), ETag changed, size/mtime unchanged.
- controlled DNS change to `10.0.0.66` refuses before contact; restoring the
  record restores playback.
- changing library approvals replaces the policy, stops remote sessions, makes
  the old grant fail and refuses a new start.

Each case is `passed`, failed, or absent from the receipt. Client decoding,
seeking on a device and visual/focus checks remain G10 work.

## Commands

```sh
export PORTICO_FIXTURE_DISPOSABLE=yes
scripts/batch-round/disposable-fixtures/g09-strm/run.sh --execute \
  --portico-image "$PORTICO_IMAGE"
```

Receipts: `~/astra-scratch/P2-65/disposable/evidence/<run>/g09/`. The control
token, CA and origin key stay in `~/astra-scratch/P2-65/disposable/secrets`.
Origin logs hold only method, path (no query), status, byte count, ETag and user
agent — never a raw STRM URL or credential.
