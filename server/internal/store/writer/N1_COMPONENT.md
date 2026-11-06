# Opt-in N1 component measurements

This target emits **N1 COMPONENT DIRECTIONAL**, `qualification=false`. It calls
`inventoryFreshFixture` and `inventoryMemoryFixtureTurn`, then SC's unchanged
effects body and W's actual terminal/retirement. It supplies no production
issuer, opening, profile, permit, dispatcher or public Applied/Evidence result.
W retains those boundaries. The component excludes W's outer authority/history
and fresh-library work. It cannot qualify a whole command or memory profile.

The target and controls are `_test.go` only and require `-n1-component`.
Default tests do no measurement. No build-tag/opening-audit exception is needed.
From `server/`, build a pinned binary with `go test -c -o <absolute-binary>
./internal/store/writer`. Run it from `server/internal/store/writer` in the exact
source checkout, so fixture/method hashes and native Capture use those sources:

```sh
PORTICO_WRITER_N1_COMPONENT=1 \
PORTICO_WRITER_N1_COMPONENT_CONFIG=/owned/scratch/config.json \
  /absolute/binary -test.run='^TestN1ComponentMeasurement$' \
  -test.timeout=7m -n1-component \
  -n1-component-case=active/current/present/fixture \
  -n1-component-cohort=timing
```

The config reuses the existing strict five-field test config decoder:

```json
{"run_id":"unique-run","host_epoch":"external-resource-epoch","source_commit":"0000000000000000000000000000000000000000","output_path":"/owned/scratch/new.ndjson","fixtures":1}
```

Use the actual source commit; declaration is not external build attestation.
This target accepts 1–64 fixtures, no SQL/DB/profile/budget inputs. Output is
exclusive, 0600, incremental and synced outside HOLD. The case selects exactly
one cell: `active|dual` / `current|dirty` / `present|absent` / `fixture|escaped`.
The building bank is distinct and copies the fixture's valid stored RAW tuple.
Actual stored profiles, age clocks, row cardinalities and input lengths are
recorded. Escaped input exercises the existing evidence-object/change-token
boundaries; these two inputs do **not** cover whole legal MIN/MAX. Each fixture
has a fresh database, one original and one component attempt. Repetition is not
sustained throughput, OS-cold, warm-append or reset-cadence evidence.

`timing` omits per-statement attribution inside HOLD. `native-counters` adds
VMOperations, native WALIO and DBStatus cache-event snapshots around exact
compiled SQL and row close. Its overhead is included, never subtracted. Keep
these cohorts separate. Native DBStatus cache/schema/statement memory walks are
sampled **outside HOLD only**; per-unit memory counters are null with reasons.
An initial full per-unit memory-walk control exhausted the unchanged synthetic
quantum; moving those observations does not enlarge the recipe or erase a miss.

The real `time.Now` owner clock is installed before BEGIN. The fixture's Work
and 4 MiB preparation + 4 MiB nonborrowable reserve stay unchanged and synthetic.
The actual bound request and limit are emitted when the private turn exists.
Any refusal/overrun/fence/unknown stops offering; no retries or success filtering.
SIGINT/SIGTERM and a five-minute cooperative deadline stop further turns. Active
native cleanup/close is joined; nonpreemptible IO may delay exit. A hard kill or
missing final leaves an incomplete prefix.

## Component wire v1

All records have `schema="portico-n1-component-v1"`, label, qualification,
run_id, process_epoch, contiguous sequence, kind and monotonic-origin `at_ns`.
Start binds origin UTC/PID/observed host/boot, external host epoch, declared source,
actual binary/config/schema/source/compiled-SQL hashes, schedule, cohort and
counter methods. Missing host facts retain errors; QL supplies host/build proof.

`attempt_start` is flushed before the turn helper acquires custody. It identifies
the original receipt/digest, attempt ID/ordinal, fresh database ID/path, cell,
input lengths and cardinalities. `attempt` has those IDs plus its bounded payload
under `attempt`: owner_clock, settings, units, WAL/memory/native checkpoints,
original component result, errors, physical outcome and cleanup observations.
The physical connection ID is decorator-assigned, scoped by database/attempt,
and survives close; it is not a sql.Conn checkout number. A known private owner
turn has physical_attempt_ordinal=1 in its fresh database; an unobserved turn
keeps that ordinal null.

Owner *_ns scalars and durations are calculated using monotonic time.Sub before
JSON. Invocation encloses SC's private effects call, not a public facade call.
`turn_setup` encloses the existing helper; exact acquire/assert intervals
stay null. Native BEGIN/rollback intervals are available only in the instrumented
cohort. COMMIT and physical return come from the actual transaction. An early
helper failure retains its native units, assertion error and original identity,
with null owner timing/Settings and nullable commit_not_attempted. No later
Resolve, expected policy values or error-name timing reconstruction fills gaps.

Units are preallocated (128 maximum), preserve execution order and repeated ID
use, and match exact executed SQL bytes against the compiled definitions.
Returned rows count actual driver Next calls; changed rows are separate and are
not examined/rejected rows. VM deltas require a nonoverlapping, nonregressing
same-connection interval. Native snapshots retain cumulative values without
reset. DBStatus array order/units/semantics appear in start. Its memory current
and native WAL write-end high water are not command memory/allocation peaks.
Overflow withholds further measured native work, reserves rollback space, marks
the entire attempt incomplete and stops the schedule.

WAL snapshots use GV's real parser, actual header, logical length and observed
allocation separately. Tokens remain null: the existing fixture owns its gate
without exposing tokens. Native WALIO before/after includes header writes and
failed/uncommitted writes; no xSync duration or reset cadence is invented.
Memory checkpoints separate Go heap/in-use/total allocations/GC, procfs RSS and
process-lifetime VmHWM, and native baseline/retained current memory. Process CPU
ticks retain their units; no guessed clock frequency converts them to ns.
Process IO is cumulative process scope, not per-device service. Complete LIVE
ownership/peak, consumer/publication/recovery, xSync, examined/rejected rows and
other unavailable dimensions stay explicit in keyed reasons with null fields.

Final records requested/offered/started/physical/complete/error/unknown/refusal/
fence/join census and cell/cohort. Complete means this component's observed
execution and join completed, never that all requested recipe dimensions were
measured. Producer sink/test/stop failure retains the prefix and stops new work.
SQL, constructor and domain semantics are unchanged. QL consumes this new wire
outside measured server memory; there is no shared reducer change.

## Progressive fixture for later target integration

`newInventoryProgressiveFixture(t, count, clock)` is a private `_test.go`
component fixture, **NOT production issuer, permit, opening or profile proof**.
It factors the existing `inventoryFreshFixture` native seed/Capture builder and
the ONE `inventoryMemoryFixtureTurn` governor/grant/recipe builder. It retains
the same executor, complete `inventoryCommandDefinitions()` captured registry,
governor, authority, WAL gate and unchanged synthetic Work/4+4 MiB partition.
It accepts 1–128 originals, allowing a future bounded warmup + measured cohort;
this bound does not change command caps or the resource recipe.

The sequential protocol is `NextOriginal` → `Begin` → existing
`applyInventoryAccept` + physical commit → `Resolve` → `AckAndSnapshot`.
Begin proves the original's history MISS and active native library on the same
turn. Resolve uses W's actual historical decoder, and ACK uses its actual
receipt lookup/CAS and physical commit, all under the retained governor. The
next-original gate opens only after a successful joined current native snapshot.
That single read observes run/lease/sequence/revision, source, directory cursor,
pages and progress counters after ACK; expected counters are never written back.
Each original declares a distinct synthetic filename, object, source key and
operation identity with the next observed receipt sequence. It is not a replay,
filesystem discovery claim or native source capability. All old receipt/result
rows remain; ACKed references obey W's retired floor.

Fence, unknown outcome, physical/ledger return gaps, refusal or snapshot errors
stop progression. An unconsumed pre-BEGIN Busy grant returns via W's one-shot
custody retirement; consumed/unknown native holdings stay with W's terminal
path. No SQL reset, state repair, constructor/grant clone, callback issuer or
production disclosure is introduced. Correctness controls freeze only their
protocol clock; later measurements must pass the real owner clock.

This fixture PR does not run or enable steady measurement. The existing target's
positive-warmup/steady requests stay explicitly unavailable until reviewed,
merged fixture integration and its source-bound raw output are available to QL.
