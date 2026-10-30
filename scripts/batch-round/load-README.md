# Real client load adapters

Operator-only; no CI entry point or automatic run. Execution requires exactly
Node24.21.0, an owned0600 non-symlink manifest, an empty owned0700 mutation-store
directory outside Git, and an explicit `--execute`. Receipts are exclusively
created and failures never replace an older result. Credentials remain private.

The default80/100-user campaign driver runs on mini-2 or portico-runner against
mini-1's100.120.50.24 Tailscale address. `--rehearsal` separately permits1..3
users/<=300s on loopback and labels every result non-acceptance. `--ingest`
permits1..20 daily users on loopback or the campaign target; it qualifies only
its300k ingest trend and never80/100-user capacity. All live modes refuse CI.

```sh
node --experimental-transform-types scripts/batch-round/load.ts --self-check
node --experimental-transform-types scripts/batch-round/load.ts --rehearsal \
  --users 1 --seconds 60 --target http://127.0.0.1:32665 \
  --manifest "$PRIVATE_MANIFEST" --mutation-store "$PRIVATE_STORE" \
  --appdata "$APPDATA" --out "$EVIDENCE/tiny.json" --execute
# Test round only, not run during preparation:
node --experimental-transform-types scripts/batch-round/load.ts --users 100 \
  --seconds 1800 --ramp-seconds 60 --target "$TARGET" \
  --manifest "$PRIVATE_MANIFEST" --mutation-store "$PRIVATE_STORE" \
  --appdata "$APPDATA" --rotate-sessions --out "$EVIDENCE/load-100.json" --execute
```

For the replacement300k target use the supervised commands in
[ingest/README.md](ingest/README.md). The earlier1M/5M and proportional-reference
builders are superseded. `provision-scale.py proposal` prints the replacement
plan; `--scalable-count` refuses rather than executing the old design.

## Workload and evidence

The driver imports the real HttpLocalApi, SessionsClient, CapabilityPublisher,
ObservationReporter, BrowseQueryBuilder, HomeService, SearchService,
LibraryManagementService, EventsClient and Saved validators. It publishes real
captured capabilities, verifies account/profile/device admission, consumes real
direct media bodies at the configured byte rate, reports progress at1Hz and
stops with the exact generation/frozen final position. Early EOF, conversion,
missing bytes, observation gaps, failed stop or unacknowledged custody fail.
The driver proves network delivery and app behavior, not decoded pictures.

Users browse, search, Home, Settings, Reading and Saved; a quiet member listens
on SSE. The owner normally scans once/minute. In ingest mode the orchestrator
exclusively owns its six source scans. Source playback must exceed the full
window+ramp+60s. Default p99 read/write gates are2000ms, observation gap2500ms,
minimum delivered byte fraction0.8. Latency histograms report conservative
bucket upper bounds; do not compare them to exact raw maxima as equal values.

Private `Manifest`/identity shapes are exported from load.ts. Each identity
has a separate real account/profile/device/installation/session family, current
access+refresh tokens, captured server identity public key/fingerprint, role
and capability document. Each measured user names an accessible library/pivot,
known scanned playable item IDs, byte rate, search and Reading library/facet.
The owner and quiet member are additional to measured users:100+1+1=102.
The factory accepts `identities --members 100` for that exact cardinality.
No real102-account creation or100-user run was done during preparation.

`provision-scale.py` provides plan-only-by-default media/bootstrap/identities/
manifest steps. Bootstrap performs ordinary setup, declares movie+book sources,
waits for actual terminal scans, reads browse/storage, cleanly stops and records
the stopped appdata's own schema. It never writes/transposes a product DB.
Use `media --distinct-copies` for a multi-title fixture: ordinary hardlinks
collapse to one title, which bootstrap detects and refuses as a claimed100.
Owner/member passwords are written0600 before account-creation requests.
The appdata prep receipt must prove stopped+terminal scan+probed media+
databaseUnderAppdata and match the manifest's exact stateAdapterReceipt.

## Refresh, storage and mutation custody

FileConnectionStore uses private0700 directories/0600 files, atomic CAS,
file+directory fsync, canonical parseRememberedServer readback and distinct
unavailable/damaged outcomes. LocalSessionRefresher owns renewal. The canonical
refreshStoredSession needs a process-global installation identity, so every
rotation executes in a one-shot child for exactly one installation; the parent
never swaps102 installations in global state. The child prints no tokens and
its renewed store is parsed after completion. `--rotate-sessions` forces a real
canonical rotation on every identity. Retired owner families abort; do not
reuse Local owner credentials after a Hosted owner move.

Mutation custody uses FileMutationStore, exclusive driver.lock, scope hashes,
canonical256-entry/8MiB bounds, atomic CAS and fsync. Saved retains operation IDs
and bodies through ambiguity until a parsed applied receipt, then acknowledges.
Final Stop memoizes successful exact-generation completion. Nonempty work or a
crash lock refuses a new run; review/recover it under the original authority.
Never delete unknown custody just to obtain a green result. Coordinators,
observers, refreshers, service controllers and timers are disposed at exit.

Server diagnostics/storage are read before the window. The direct-only capacity
campaign requires no configured conversion engine. Ingest records the engine
but still requires actual direct plans; mixed-load.ts explicitly permits a
configured hardware engine with a private paired-window gate and independently
validated hardware receipt. Defaults remain direct-only. See
[hardware/README.md](hardware/README.md) for exact mixed orchestration.

## Phases and trend

`--phases --control <0600 file>` reads an atomic JSON control or marker JSONL.
Accepted API counts authorize markers; generated totalFiles never does.
`--ingest` requires baseline,phase-1..6,after with>=20 samples/operation/phase,
and resident markers50000,150000,300000. Receipts include per-op p50/p95/p99/max,
epoch phase timestamps, resident reads and malformed-control counts.
The trend fails missing evidence, p50>baseline*1.2+10ms, or
p99>baseline*1.2+50ms or the absolute2000ms gate. `ingestTrendQualified` can be
true only when every user gate and all trend evidence pass. Campaign/device/
decoder acceptance stays false. A tiny rehearsal is never a qualified trend.

Backup comparisons add `--label scheduled-backup|manual-backup` and
`--operator-window <id>` while the owner runs backup-window.py with the same
label/window. These labels identify a window, not proof of a backup. Host
WAL/RSS/cgroup observation is supplementary; missing measures stay unavailable.

## What ran

Offline checks pass canonical custody, Saved lost-response recovery, prepared
handle releases, generation Stop, connection-store reopen/CAS, real local
refresh child rotation/refusal, storage readback and phase/mode/trend gates.
They use loopback mocks and make no external requests.
A real private core fixture exercised setup, real scans/media, three identities
and canonical rotation. Its original2000ms Reading workflow FAILED (p99 bucket
2620ms); later5000ms overrides are readiness only, with disclosed409s, never
capacity passes. The Reading producer repair is separately owned/measured.
A distinct-copy fixture did scan100 actual titles. The joined300k daily-use
trend,80/100-user campaigns and device work remain unrun. No Contabo or Hosted
state was changed by this tooling.
