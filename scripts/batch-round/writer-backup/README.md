# P2-65 writer/WAL and backup orchestration

**Status: partial reality check on a private core fixture (28 Oct 2026).** This
folder still closes no ledger row by itself. Private receipts exist for:

- core scheduled + manual backup: gate pass; `cleanPass` false (one finding and
  four unavailable checks);
- live appdata-copy readiness/rebase/integrity: clean pass;
- real 100-file incremental source scan: gate pass; `cleanPass` false (one
  finding and three unavailable checks);
- stopped appdata copy (source 32665 stopped first): clean pass;
- the agreed 300k real-scan bar (which replaces the earlier 5M target), load,
  capacity and device runs: **unrun**.

On the private core run the source was stopped with SIGTERM to its parent PID
(process group 2268072), not restarted, and both servers are left stopped. The
appdata and private credentials were preserved for the lead; copy destinations
and scratch server processes were removed.

`result: pass` is only the gate over checks that ran. `cleanPass` is false when
any finding or unavailable check remains. Do not cite this folder as
O-043/G05/G06 acceptance from these tiny runs.

These tools are operator-only. No workflow, npm script, test inventory or timer
calls them. `--execute` is refused whenever `CI` or `GITHUB_ACTIONS` is present.
Default invocations print a plan and change nothing.

## Files

| File | Purpose |
| --- | --- |
| `common.py` | Bearer client, private receipt/plan I/O, real diagnostics compaction and thresholds, sampler. |
| `backup-window.py` / `backup_window.py` | Opens a real scheduled backup window, waits for scheduled-kind evidence, starts one manual backup, samples diagnostics, restores the original maintenance settings in `finally`. |
| `appdata-copy-recovery.py` / `appdata_copy.py` | Copies appdata to a separate destination (live or stopped), starts the current binary on the copy, verifies startup/rebase/integrity, preserves WAL/SHM. |
| `writer-wal-falsifier.py` / `writer_wal.py` | Triggers one real source-inventory scan, samples WAL/holds/plans/pressure, and reports the supported/unsupported quiet-gap cases. |
| `selftest.py` | Tiny mocked checks; no socket, no server, no product run. |

## Prerequisites: met by the partial run, and still open

Met on the private core fixture: a disposable current server, a real owner
session, real maintenance/backups documents, a real backup destination, no
running backup/restore, and a clock within the two-minute window.

Still open:

- for the write-latency half of G06, a concurrent `load.ts` run with
  `--label scheduled-backup|manual-backup --operator-window <window-id>`;
- for the quiet-gap half, a process log that actually contains the quiet-gap
  line (`--stdout-log`); the 100-file run found none, and there is no published
  quiet-gap diagnostic;
- the agreed 300k real-scan bar and any capacity/device run.

The live copy is **default recovery-scope proof, not full appdata acceptance**.
Do not promote these tiny receipts to the 300k, load or capacity gates.

## 1. Scheduled/manual backup under a real operator window

```sh
python3 scripts/batch-round/writer-backup/backup-window.py            # plan
python3 scripts/batch-round/writer-backup/backup-window.py --execute \
  --base "$TARGET" --token-file "$PRIVATE_OWNER_TOKEN" \
  --out "$EVIDENCE/p2-65-backup-$(date -u +%Y%m%dT%H%M%SZ).json" \
  --window-id p2-65-backup-1 --confirm CHANGE-MAINTENANCE-WINDOW \
  --load-receipt "$EVIDENCE/load-80-scheduled-backup.json"
```

`--confirm CHANGE-MAINTENANCE-WINDOW` is required for a live schedule change
(`--manual-only` does not change the schedule and does not need it).
What it does:

1. Reads the real maintenance document and backups document.
2. Refuses if a backup is already running, a restore is staged/running, or a
   listed scheduled backup already covers the proposed occurrence (the
   scheduler would not start another one).
3. Writes a private restore plan (`<out>.restore-plan.json`, mode 0600) **before**
   any change.
4. Replaces only the `nightly` window: enabled, `custom`, today (UTC), start two
   minutes ago, UTC, 45 minutes, `tasks:["backup"]`. The `heavy` window and all
   other settings are carried through unchanged. `--keep-count` is optional and
   also restored.
5. Waits up to `--scheduled-timeout` (default 360 s) for **scheduled-kind
   evidence**: `running.kind == "scheduled"` or a newly listed backup with
   `kind == "scheduled"`. A job that finished before its kind was observed is
   **not** counted as kind evidence.
6. Accepts exactly two scheduled outcomes: `completed`, or `failed` with the
   documented `errorCode == "backup_busy"` inside the approved 60 s no-progress
   budget. The budget is **60,000 ms plus one measured sampler interval and 2 s**;
   there is no wider product gate. `noProgressObservedMs`
   is measured from the last observed `phase`/`bytesDone` change and is reported
   separately from `totalObservedMs`; a long operation that stalls only at the
   end is judged on the terminal stall. `bytesDone` is the only published
   progress signal, so a zero-byte forward step can look like no progress; the
   receipt names the basis and the sampler interval.
7. Waits for the scheduled job to clear, then starts one manual backup through
   `POST /v1/admin/backups` with a fresh operation id. A 4xx (for example
   `insufficient_disk`, `request_in_progress`) is a failure; the manual start must
   be accepted (202) and complete without a capacity refusal. `--manual-timeout`
   defaults to 600 s; raise it (up to 3600 s) for a large copy.
8. Samples `GET /v1/admin/diagnostics/concurrency` once per poll (default 1 s).
9. Always restores the original maintenance settings in `finally` (three
   attempts). If the process is killed, the restore plan remains; finish with:

```sh
python3 scripts/batch-round/writer-backup/backup-window.py --execute --restore-only \
  --base "$TARGET" --token-file "$PRIVATE_OWNER_TOKEN" \
  --restore-plan "$EVIDENCE/<receipt>.restore-plan.json"
```

The `window-id` is the same value `load.ts --operator-window` expects. The
receipt records the load file hash, label, window and a scheduled overlap
estimate; load thresholds remain the driver's own gates.

### Thresholds and exact codes

- **Fail:** physical WAL peak above 192 MiB (`wal.walPeakFileBytes` /
  `wal.walFileBytes`).
- **Fail:** `writerAdmission.busyAfterAdmission` increases (an outside opener or a
  discipline defect, not ordinary contention).
- **Fail:** `writerAdmission.refused` increases (a named user ticket was refused).
- **Fail:** `wal.checkpoints.errors` increases.
- **Fail:** scheduled evidence missing/unobserved, or `backup_busy` outside the
  approved 60 s no-progress budget.
- **Fail:** manual start refused, manual ends non-`completed`, or manual
  `errorCode` is set.
- **Fail:** `lastRestore` changes across the window.
- **Finding:** a sampled hold at/above 250 ms, **or a process-lifetime max hold
  at/above 250 ms even when this window sampled no crossing**; a write-plan
  overrun; a recovery hold/pause. Findings are recorded, not silently passed.
- `cleanPass` is true only when every check passed and none is unavailable.
- **Unavailable:** WAL reservation ledger, per-collector queue, per-occurrence
  >250 ms log, user-write latency without the load driver.

`backup_busy` is the only accepted scheduled failure code. A manual backup must
never be refused for capacity; any refusal is a failure.

For a dedicated `load.ts --label manual-backup` window, add `--manual-only`. It
does not change the maintenance schedule; it waits for the server to be idle and
runs only the manual backup, rejecting any capacity refusal.

## 2. Live and stopped appdata-copy recovery (separate destination)

```sh
python3 scripts/batch-round/writer-backup/appdata-copy-recovery.py            # plan
python3 scripts/batch-round/writer-backup/appdata-copy-recovery.py --execute \
  --source-state "$SOURCE_STATE" --dest-state "$SEPARATE_DEST" \
  --mode live --scope recovery --binary "$BUILT_SERVER" \
  --bind 127.0.0.1:32599 --scheme http \
  --out "$EVIDENCE/p2-65-copy-live.json"
```

`--mode stopped` additionally checks `/proc/*/fd` and refuses if any process
still holds the source database. It cannot prove a stop on a non-Linux host; that
is recorded as a limitation. The destination's parent must already exist; the
tool creates only the destination itself.

`--scope recovery` copies only Portico's recovery set: `Database/server.sqlite`,
`-wal`, `-shm`, `Keys/`, `portico.toml`, the layout/ownership markers and the
`Cache`/`Temp` ownership markers. It deliberately does not carry artwork,
prepared media or other regenerable payloads; the scratch server must recreate
them. `--scope full` copies every regular file and refuses symlinks. The
destination must be separate and absent or empty.

Checks:

- **WAL/SHM preserved:** a non-empty source WAL/SHM must be non-empty in the copy
  **before** the scratch server starts.
- **Startup:** the scratch server must answer unauthenticated `/v1/readiness`
  with `ready:true`.
- **Clean stop:** a forced or nonzero stop is a finding, not a pass.
- **Rebase:** after the clean stop, the copied database's
  `configuration.state_directory` must equal the destination and
  `state_rebase_jobs` must be empty.
- **Integrity:** `--integrity quick` (default) or `full`; the copied database is
  opened **read-only after the scratch server stops**. A WAL left by a forced
  stop can make the read-only inspection unavailable; that is reported as
  unavailable, never as `ok`. A bounded `PRAGMA foreign_key_check` is recorded
  separately; a violation is a finding, not attributed to the copy.

**Caveat:** a live multi-file copy of the main file and its WAL/SHM is not a
transaction snapshot. This check proves whether the copy starts and recovers; it
is **not** a backup and must not replace the product backup for a consistent live
copy. Use a product backup folder for the consistent case.

## 3. Source inventory and quiet-gap observation

```sh
python3 scripts/batch-round/writer-backup/writer-wal-falsifier.py            # plan
python3 scripts/batch-round/writer-backup/writer-wal-falsifier.py --execute \
  --base "$TARGET" --token-file "$PRIVATE_OWNER_TOKEN" \
  --library-id "$REAL_LIBRARY_ID" --mode incremental --observe-seconds 120 \
  --stdout-log "$SERVER_STDOUT_LOG" \
  --out "$EVIDENCE/p2-65-inventory.json"
```

This triggers one real `POST /v1/libraries/{id}/scans?mode=incremental|full|integrity`,
polls the real ingestion job, and samples the same diagnostics. It reports the
quiet-gap case only from the process log line
`Reading COMPLETE deferred: ... waiting for ... without changes`; quiet-gap
deadline/state is internal and has no published diagnostic.

Truthfully **not** covered here: the agreed 300k real-scan scale bar (which
replaces the earlier 5M target), bounded
removal/retention, SRV023 10k-track release, hardware-only conversion, and
user-write progress during high-fanout inventory. Those stay with their owners
(P2-14a/P2-47/P2-52b/P2-54, Media, and the load driver).

## Diagnostics keys (verified against the source)

`compact_diagnostics` reads only these published keys and refuses the sample if
the shape changes instead of recording zeros:

- `wal` (`dbwork.WALStats`): `walFileBytes`, `walPeakFileBytes`, `logFrames`,
  `checkpointedFrames`, `backlogFrames`, `busy`, `lastError`, `readers`,
  `checkpoints`.
- `wal.readers` (`ReaderLifetimeStats`): `snapshots`, `oldestSnapshotMs`,
  `maxSnapshotMs`, `admissionWaiting`, `admissionPaused`, `admissionRefused`.
- `wal.checkpoints` (`CheckpointStats`): `attempts`, `truncates`, `busy`,
  `errors`, `pressureTimeouts`, `maxDurationMs`.
- `writeGate` (`GateStats`): `active`, `heldMillis`, `maxHeldMillis`,
  `queueWaitMillis`, `byClass[]{currentHeldMillis,maxHeldMillis,queued}`.
- `writerAdmission` (`AdmissionStats`): `capacity`, `userQueued`,
  `backgroundQueued`, `deferred`, `refused`, `busyAfterAdmission`,
  `recoveryHold`, `recoveryPause`.
- `writePlans` (`PlanStats`): `measured`, `overruns`, `overrunsByClass`,
  `overrunsByDimension`, `largest`.
- `pressure` (`PressureStats`): `yields`, `foregroundActive`, `healthy`.
- `health` (`HealthReport`): `state`, `consecutiveFailures`, `lastError`,
  `handleRecycles`, `backgroundPaused`.
- `process` (`ProcessDiagnostics`): `goroutines`, `heapAllocBytes`,
  `nextGCBytes`, `gcCycles`, `containedPanics`.
- top level: `publications`, `ungatedWrites{writer,reader}`.

`walPeakFileBytes` is process-lifetime. Using it is conservative for a window: a
pre-window peak is attributed to this window rather than hidden. The internal
`dbwork.WALReservations` ledger (before-BEGIN debits, reservation overruns,
unknown VFS I/O) is **not** on this wire and is reported as unavailable.

## Self-check

```sh
python3 scripts/batch-round/writer-backup/selftest.py
python3 scripts/batch-round/writer-backup/backup-window.py --self-check
python3 scripts/batch-round/writer-backup/appdata-copy-recovery.py --self-check
python3 scripts/batch-round/writer-backup/writer-wal-falsifier.py --self-check
```

The self-checks use fake APIs, a fake clock and a fake harness. They exercise the
orchestration ordering, the finally-restore path, the 60 s no-progress bounds,
the exact `backup_busy` code, the manual refusal path, the WAL threshold, the
copy preservation and the rebase/integrity checks. They are **not** a server,
browser, workload or recovery rehearsal.

## Evidence and hygiene

- Receipts are exclusive-create mode 0600 and are never overwritten.
- Tokens are read from operator-owned 0600 files and never printed or stored.
- Receipts contain no bearer token, request body, grant URL or source media URL.
  The copy-recovery receipt necessarily records the source/destination state
  paths because the rebase assertion is path-based; keep it private.
- The scratch server receives a minimal environment (PATH, HOME, TMPDIR and
  `PORTICO_*`), not the operator's full environment.
- The separate copy and scratch home are removed unless `--retain`; stopped
  source state is never modified.
- One task scratch folder; remove disposable build/test scratch at handback.
