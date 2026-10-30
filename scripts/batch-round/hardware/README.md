# Hardware execution fixture (P2-65)

Operator-only acceptance for the admin-only `delivery.hardware.execution` block
on `GET /v1/admin/sessions`. It is **not** a CI entry point and has a successful two-session light rehearsal. The original software-decode
failure is retained; the private conversion-target persistence fix repairs it.

The fixture starts **two or three overlapping real conversions** through
client-core's own `SessionsClient`, `CapabilityPublisher` and
`ObservationReporter` (via `scripts/batch-round/load-adapters.ts`), streams the
actual HLS bytes a viewer would receive, and accepts only time-separated admin
samples taken while those HLS streams are still draining, where every target
session shows hardware-only execution with a DRM render node. It never falls
back to software and never turns `unknown` into hardware.

This is a readiness fixture. It proves execution and overlap for a tiny
2-3-session window; it is not the 80/100-user load campaign, not a decoded-picture or
rendering-fidelity check, and not a capacity claim.

## Commands

Plan (default, no network, prints `UNRUN`):

```sh
node --experimental-transform-types scripts/batch-round/hardware/hardware-execution.ts
```

Offline checks only (no HTTP, no server, no manifest), with an explicit
operator-owned scratch directory. Node 24.21.0 exactly is required:

```sh
node --experimental-transform-types scripts/batch-round/hardware/hardware-execution.ts \
  --self-check --scratch "$PRIVATE_SCRATCH"
```

`--scratch` (or `P2_65_SCRATCH`/`TMPDIR`) must be an existing owned real
directory, not `/tmp`, `/dev/shm` or `/run`. The unique child is always removed
in a `finally`; the base directory is never deleted.

The self-check exercises the execution parser, the route constants, the media
path rules and the private-manifest checks. **It does not exercise a route, a
conversion or this fixture's HTTP workflow, and passing it does not make the
fixture runnable.** Only a cleared live run can do that.

Loopback rehearsal (after the lead clears a local server and source):

```sh
node --experimental-transform-types scripts/batch-round/hardware/hardware-execution.ts \
  --execute --rehearsal --target http://127.0.0.1:8096 \
  --manifest "$PRIVATE_MANIFEST" --mutation-store "$PRIVATE_MUTATION_DIRECTORY" --out "$EVIDENCE/hardware-rehearsal.json"
```

The rehearsal target must be exactly `http://127.0.0.1:<port>` with port ≥ 1024,
and the manifest's `target` must name that same loopback origin. It may run on
the server host because it never leaves loopback, but it is still a real HTTP
session and still fails on software, unknown or fallback execution.

Mixed direct + hardware (two children, same window):

```sh
/home/justin/.local/opt/node-v24.21.0-linux-x64/bin/node --experimental-transform-types \
  scripts/batch-round/mixed-load.ts --execute --users 80 --hardware-users 2 \
  --target http://100.120.50.24:8096 --manifest "$DIRECT_MANIFEST" \
  --hardware-manifest "$HARDWARE_MANIFEST" --mutation-store "$DIRECT_STORE" \
  --hardware-mutation-store "$HARDWARE_STORE" --appdata "$APPDATA" \
  --window-receipt "$PRIVATE_WINDOW_RECEIPT" --out "$EVIDENCE/mixed-80.json"
```

The supervisor starts `load.ts` first, waits its ramp plus warmup, then starts
this hardware fixture with `--direct-users 80`. The direct manifest must use the
`conversionMode: "observed-hardware"` and the supervisor passes the load child
`--observed-hardware --hardware-receipt` opt-in. The window receipt must live in an owned 0700
directory; it is a paired-window authorization, not hardware proof. The
hardware manifest must contain 2-3 member identities disjoint from the direct
cohort, with a separate 0700 mutation store. The hardware owner/admin may be
the same owner account as the direct run; reusing a direct member is refused.

Campaign/readiness target (mini-2 or `portico-runner` only):

```sh
node --experimental-transform-types scripts/batch-round/hardware/hardware-execution.ts \
  --execute --target http://100.120.50.24:8096 --sessions 2 \
  --manifest "$PRIVATE_MANIFEST" --mutation-store "$PRIVATE_MUTATION_DIRECTORY" --out "$EVIDENCE/hardware.json"
```

`--execute` is refused when `CI` or `GITHUB_ACTIONS` is present, on the server
host for the heavy target, and without a private manifest, target, receipt path
and mutation-store directory. No hardware sessions may be started before the
lead's explicit go-ahead.

`--direct-users` is `0` by default, `1` in rehearsal, and `80`/`100` in the
mixed run. When non-zero, the fixture requires time-separated samples to show
that many distinct playing direct sessions (direct/copy only, no audio or video transcode
and no subtitle burn) **together with** the 2-3 hardware sessions. It samples the
admin page and conversion diagnostics concurrently with a500ms gap while HLS is still draining,
and needs at least two overlap samples spanning 500 ms with
`activeConversionSessions >= hardware sessions`. A final post-drain page is not
accepted as overlap proof. Every observed hardware execution must stay at zero
software, unknown and fallback launches, and the server fallback/refused
counters may not move for the whole sampled history.

## Prerequisites

- **Node 24.21.0 exactly** for `--self-check` and `--execute`, matching the
  load driver. The qualified local binary is
  `/home/justin/.local/opt/node-v24.21.0-linux-x64/bin/node`. Plan mode runs on
  any Node.
- **Manifest**: an owned, non-symlink, `0600` JSON file ≤ 4 MiB in the
  `load.ts` identity shape: `version: 1`, exact `target`, `serverId`, one
  `owner` (role `owner`) and at least two member `users`, each with a real
  captured capability document, access token, `expiresAt`,
  `sessionFamilyId`, `tokenGeneration`, `authorizationHorizon`, authority,
  account/profile/device/installation IDs and `itemIds`. Tokens must cover the
  run; no refresh loop is supplied. The owner token must be allowed to read the
  admin dashboard and playback diagnostics.
- **Mutation store**: an existing owned `0700` directory outside Git, used
  through `FileMutationStore` and one exclusive driver lock (P251 custody).
  A leftover lock or pending mutation record refuses a fresh run for operator
  recovery; never delete those records to obtain a clean receipt.
- **Server**: delivery running, HLS configured (finite HLS is recorded), a supported
  hardware backend (not `software`, not unresolved `auto`), a published
  conversion-session limit (when present) of at least the requested count, and
  no conversion already active. A null limit is recorded, not guessed; start
  admission remains authoritative. The server must allow conversion; a
  direct-play plan fails the fixture.
- **Source**: one controlled item above the requested fixed rung (default
  `720p-high`), with the audio and container the plan can convert. Pin the same
  item for all sessions with `--item-id`. Without a pin, each identity's first
  `itemIds` entry is used.
- **Render node**: a Linux DRM node (`/dev/dri/renderD…`) is required. macOS
  VideoToolbox, AMF and NVENC launches have no such node and are reported as
  unsupported by this fixture rather than accepted with a weaker check.

## What it checks

1. Owner identity and device match the manifest; a member's admin dashboard read
   is refused with `403`.
2. The ordinary viewer's `GET /v1/playback/sessions/{id}` response does not
   contain `launchCount`, `lastLaunch`, `renderNode` or `softwareLaunchCount`.
3. Each start returns `presentation.mode: "stream"`, a converted video decision,
   `fmp4_hls`/`mpegts_hls` and a `/v1/media/{grant}/master.m3u8` path.
4. Each session serves at least the requested HLS segment bytes and at least two
   media segments. Reads stay inside that session's `/v1/media/` grant.
5. At least two admin samples spanning 500 ms, both taken while HLS is still
   draining, show every target session `playing` with:
   - `delivery.hardware.execution` parsed by client-core's
     `parseHardwareExecution` (strict, bounded, no invalid-to-hardware),
   - `launchCount ≥ 1`, zero software, unknown and fallback launches,
   - hardware encoder and decoder, and a DRM render node (or the exact
     `--render-node`),
   - an `encode` stage executed on hardware, a converted video stream, a
     converted height within the requested rung, and `media.videoHardware: true`.
6. Playback and conversion diagnostics report no new hardware fallback or
   refused conversion at any sampled point, and `activeConversionSessions` is at
   least the hardware session count while direct sessions are playing.
7. With `--direct-users`, the same samples show that many distinct playing
   direct sessions (direct/copy only) on the same pages as the hardware rows.
   A post-drain page alone is never accepted as overlap proof.
8. Every session is stopped with the frozen `finalStop` position and the P251
   stop acknowledgement; a refused stop fails the run.

## Receipt

The receipt is reserved before any request with `O_EXCL` and written `0600`.
It contains the target origin, server ID, direct-user count, start/finish
times, preflight/postflight counters, observed sample/overlap summary,
per-session account/profile/device IDs, generation, container, redacted media
path, delivered bytes/segments/position, parsed execution evidence and stop
result. It never contains a bearer token, capability document, media grant,
source URL or raw server body. An existing receipt is never overwritten.

## Receipt validation export

`hardware-execution.ts` exports `validateHardwareReceipt(raw, expected)`. It
re-parses every accepted fact: pass verdict, target/server identity, session and
direct-user counts, two time-separated during-drain overlap samples, active
conversion count, distinct direct sessions/accounts, hardware-only execution
with render nodes, delivered bytes/segments, stopped sessions and unchanged
fallback/refusal counters. `mixed-load.ts` uses it before correlating the two
children; it is not a rail for a failed or partial receipt.

## Paired load opt-in

`load.ts` implements `--observed-hardware --hardware-receipt` and the explicit
`conversionMode: "observed-hardware"` manifest union. The mixed supervisor writes
the private paired-window gate, passes it to the load child and independently
validates the final hardware receipt. Direct-only defaults remain unchanged.
No mixed80/100-user run was performed during preparation.

## Grounded routes in this pin

The runner does not spell session wire calls itself:

- start, session read, observer claim/sample and stop come from
  client-core's `SessionsClient`, wrapped by `mutationClient` from
  `scripts/batch-round/load-adapters.ts` (P251 custody + `finalStop`).
- capability publication is `CapabilityPublisher`.
- `GET /v1/admin/sessions` is read through `HttpLocalApi.request` (via the
  owner's real adapter) and is a direct `{scope, observedAt, items, page,
  totals}` page. It is the current route (`list_admin_sessions`), not
  `/v1/admin/playback-sessions`.
- `GET /v1/admin/playback/diagnostics` unwraps its real
  `{scope, observedAt, freshUntil, diagnostics}` envelope. There is no
  `{data, context}` envelope on either admin route in this pin; if a proxy adds
  one, the page read fails closed rather than guessing.
- the only direct media HTTP is the presentation's own
  `/v1/media/{grant}/master.m3u8` playlist and the segment/init references it
  lists, fetched with the session bearer and confined to that grant.

## Rehearsal lifecycle

The coordinated two-session mini-2 app rehearsal passed; see the result below.
Its server and all task scratch have been removed after private evidence/input
collection. Recreate a fresh fixture through the documented ordinary API factory
for a later run. Never reset or borrow another lead's live fixture.
`load.ts` is imported only for `targetOrigin`/`requireDriver`; importing it starts
nothing because its `main` is guarded by `import.meta.url`.

## Explicit non-claims

- No campaign or capacity result: 2-3 overlapping conversions only.
- No proof of sustained multi-user hardware capacity, thermal behavior, or the
  80/100-user target.
- No decoder/renderer quality or device acceptance claim; only the server's
  published execution facts and the bytes it actually served.
- No fallback avoidance beyond this fixture's window; the postflight counter
  check covers only what happened during the run.
- No CI execution and no automatic scheduling.

A standalone FFmpeg hardware sanity encode is supplementary and lives outside
this fixture; it is not an acceptance substitute.

## Light rehearsal result

On Intel i915/renderD128 with the qualified FFmpeg8.1.3 and a private extracted
full-feature iHD26.1.2 driver, two members converted the same150s1080p source to
720p-high. Each served12,422,288 HLS bytes/five segments; both encoder h264_vaapi
and decoder h264 reported hardware, with zero software/unknown/fallback launches.
Two concurrent observations spanned3.23s; postflight active conversions0 and both
sessions stopped. This is two-session execution proof, not80/100-user capacity.
The controlled before/after server used the original matching appdata baseline;
the same fix was separately built/tested on current main. Private evidence and
runtime/driver hashes are recorded in the integration report, outside Git.

Sampling reads admin sessions and diagnostics concurrently with a500ms gap,
retains start/completion timestamps, and paces HLS consumption by media time.
Canonical mutation coordinators are disposed on exit; receipts never replace
older failures. There is no production deny-software switch.
