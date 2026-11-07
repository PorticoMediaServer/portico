# Sustained workload drivers

`workload` is opt-in harness code. It is **DIRECTIONAL**, always
`qualification=false`, and opens no databases or listeners. It does not run in
the product. QL owns qualification, external manifest/host/build/fixture proof,
WAL classification, independent physical cleanup evidence and reporting.

`Run` consumes an already prepared single-database composition, an immutable
`qual.RestartCell`, bounded streams, and a sink. Fixture construction must finish
before the duration/arrival epoch starts. Duration is bounded to two hours,
streams to eight, pattern entries to 32, and offer spacing to at least 1 ms.
There is one synchronous worker per stream, no work queue and no goroutine per
operation. Slots use a common monotonic epoch. Slow service skips expired slots;
there are no catch-up bursts, hidden warmup or retry-to-fill. Pattern advances
with actual scheduled slots, including misses. Foreground, WAL observation and
PASSIVE can run while the serial ingest stream is active.

Trusted composition supplies the actual writer, disclosures, sealed fresh inputs
and ORIGINAL historical requests. `Inventory` and `Effective` execute real
ACCEPT/BEGIN → RESOLVE → ACK calls, emitting each result immediately after its
physical call returns, before the next phase. They check receipt identity,
replay/result conservation and ACK floor. A failure, including a typed admission refusal returned with nil native error,
stops offers in all streams;
no new identity is minted to recover an uncertain call. Recover the ORIGINAL
identity through W's owning recovery composition. Resolve and ACK are distinct
observations and do not pad fresh acceptance counts. One writer stream with a
repeating pattern mixes InventoryAccept and EffectiveStage at a target cycle
rate; separately declare the resulting phase-call rate.

For example, QL's trusted harness can assemble these values after construction
(the identifiers below are composition inputs, not new constructors):

```go
streams := []workload.Stream{
    {Name: "ingest", Every: 10*time.Millisecond, Pattern: []workload.Step{
        {Name: "inventory", Run: workload.Inventory(w, scanDisclosure, nextInventory)},
        {Name: "effective", Run: workload.Effective(w, enrichDisclosure, nextEffective)},
    }},
    {Name: "browse", Every: 50*time.Millisecond, Pattern: []workload.Step{
        {Name: "browse-ready", Run: workload.Browse(reader, g, authority, snapshots, nextBrowse)},
        {Name: "detail-ready", Run: workload.Detail(foregroundReader, g, authority, snapshots, nextDetail)},
        {Name: "playback-lookup", Run: workload.PlaybackLookup(foregroundReader, g, authority, snapshots, nextPlayback)},
        {Name: "continue-ready", Run: workload.ContinueWatching(foregroundReader, g, authority, snapshots, nextContinue)},
    }},
    {Name: "checkpoint", Every: 250*time.Millisecond, Pattern: []workload.Step{
        {Name: "passive", Run: workload.Passive(checkpointer, nextCheckpoint)},
    }},
    {Name: "wal", Every: time.Second, Pattern: []workload.Step{
        {Name: "observe", Run: workload.WAL(databasePath, walGate)},
    }},
    {Name: "security", Every: 2*time.Second, Pattern: []workload.Step{
        {Name: "security-STUB", Run: workload.StubCadence("WC native Resolve/ACK/fixture composition unavailable; no security write")},
    }},
}
err := workload.Run(ctx, config, streams, workload.JSONSink{Output: freshOwnedFile})
```

That example offers 100 ingest cycles/s, alternating 50 inventory and 50 effective
cycles/s, each with up to three separate facade calls. Those are targets, never
observed throughput. PASSIVE keeps GV's actual admission, minimum start spacing,
whole-prefix forecast, native policy and physical-return protocol; no SQL or
forced reset enters this package. WAL observes through GV's read-only parser,
retaining allocation, active prefix, debt, salts/frames, before/after gate tokens,
reset counter and last event. It NEVER publishes samples or changes admission.
Its result is the named `*WALSnapshot`: timestamped before/after tokens, GV
parser interval and nullable state, metadata-read interval, and bounded file
probes on either side of that parser call. Each file probe retains observed
length, optional OS device/inode, at most32 raw header bytes (including a short
prefix), read/close errors and its own monotonic duration. Zero length/token is
an observed value; an unopened file or failed parser has null data. `ReadNS`,
`MetadataNS` and file `ElapsedNS` are derived before JSON, and are observer cost,
never writer HOLD or header-sync latency. No native write/xSync counter is
available through this read-only seam.

The final token is read AFTER reset/event getters. `TokensEqual`, `SameFile`
and `HeaderEqual` describe only the probes; none certifies an atomic sample,
same-database authority, whole-run coverage, absence of a reset, or a reset.
In particular, GV `Publish` can update metadata without advancing its token,
and a replacement can occur between path reads. `LastRestart` with sequence0
or zero event time is a raw empty getter result, not a timestamped event. QL
must bind actual physical attempts and owned file lifetime before classifying.
The filename is a declared observation input, not W's opening/file capability.
Header/file probe errors remain raw gaps even when GV's separate parser call
succeeds; the parser's error still stops the action after emission. Consumers
must not turn optional-probe gaps into complete cadence/bound proof.
The observation is advisory and can race; QL must join/deduplicate events and
retain unknown/stale states. CP completion/allocation/token changes do not prove
a reset. Creation is not reset. Only owning composition may attest freshness.

`Browse`, `Detail`, `PlaybackLookup` and `ContinueWatching` call the real public
readers through one shared adapter and retain every lookup/publication window.
Its trusted `prepare` callback supplies the ORIGINAL foreground/read deadlines,
the separate output request and trusted request-entry clock; it does not allocate
a payload or output grant. The adapter calls `read.ConsumeForeground` to admit
LIVE before output/draft allocation. Each typed `Consume(context.Context, Page)`
is required and runs only after successful read publication, using the protected
context. It joins encoding/IO and drops all page references before returning.
The driver then retires output and LIVE, also on read/consumer failure. The
measured wall interval includes preparation, admission, every window, consumer
work and cleanup. `ReadyNS`
adds scheduling delay from the original offer; it is not HTTP/network latency.
The result retains the original ready budget and deadline. Compare paired
foreground-only/ingest cohorts against B-READS at the declared library size:
for sorted browse40 at 1M the proposed p95/p99 ready budgets are 50/150 ms;
background delta p95 ≤10 ms/p99 ≤25 ms. The ≤1 s snapshot protection ceiling is
not a ready budget. This package computes no gate verdict.

`ForegroundResult[Summary]` retains original ready/protected/read deadlines,
budget, separate read/consumer/scope errors, read evidence and full-consumption
timing. Unobserved phases, absent summaries and missing native window facts are
null; an observed zero interval stays numeric zero. Read failure never
becomes a successful empty page. A successful read followed by consumer failure
keeps its summary and remains a failed operation. Each summary contains bounded
counts/flags only, with no page, title, path, token, user state or cursor. Emission
runs after the shared scope cleanup; it carries no writer HOLD/COMMIT evidence.
The scheduler/reducer envelope is unchanged; phase-specific `Result` now uses
this named shape, including browse. Browse consumers now receive their context.

`PlaybackLookup` is file/stream resolution, not a playback session or first frame.
Continue watching uses the B-R02 Home-row budget. All whole-operation counts,
windows and permitted attempts remain visible; these adapters establish no ready,
VM, coexistence or memory qualification. Live/output remains held for a slow or
cancelled consumer, even after all native windows close.

WC progress/security adapters are not supplied here. `StubCadence` records
missing capability at the declared cadence,
performs NO write and has null HOLD/COMMIT/ready fields. A stub prevents schedule
completion and whole-run reduction. The `Action` contract accommodates future
typed adapters without expanding the writer registry or inventing a command.
Do not label progress/security stubs as playback/FULL samples.

## Raw protocol and reduction

New version: `portico-sustained-workload-v1`, not first light's `fresh_zero_wal`.
Records bind the same manifest/host/profile/database/cache/cadence cell. Each
record has a global contiguous `Sequence` and unique `UnitID` (run ID + sequence).
Original receipt identity/digest is retained separately as `Requested`, even when
W's public error result correctly returns entirely zero. Record kinds:

- `start`: declared duration and schedules/patterns;
- `operation`: stream/slot/scheduled time, raw this-call result/error/evidence,
  optional owner HOLD/COMMIT and scheduled-ready duration. Unmeasured wall
  intervals and call timestamps are null for stubs and preparation failures;
- `missed`: skipped slots, without fabricated command samples;
- `stream_end`: scheduled/offered/emitted-call/missed/unserved counts;
- `final`: driver return and schedule completeness, with errors.

`JSONSink` writes/syncs records outside writer custody and includes that overhead
in total schedule cost. Output failure stops offers; a missing/truncated final
remains incomplete. Retain the exact raw file/hash and an immutable record
reference. Scheduling uses O(streams × pattern) memory; results live for one
bounded synchronous call/encoding. Consumer and output bytes require separate
composition memory custody. QL must retain exact binary/manifest/schema pins.

`RestartSample` projects complete owner evidence into the existing QL reducer.
QL supplies actual WAL position, physical attempt ordinal, immutable evidence
reference, actual nullable fence proof and outcome reason. No internal
classification guesses are made. Failed-first/unknown/fenced costs can project
when they have actual owner evidence; missing COMMIT is not numeric zero unless
W proves COMMIT was never attempted. Error results on current main lack private
physical diagnostics, so remain null/unprojectable. A later Resolve cannot
reconstruct the failed call. `RefusalError` is NOT before-custody proof.

`Reduce` adds coverage around the shared reducer: it requires the complete raw
run, exact bindings/order and a join for EVERY writer call. A missing call/join,
physical ordinal gap, unknown fence, stub, refusal, missed slot or incomplete envelope
withholds the whole-run report. It never drops null attempts and densely
renumbers successes. Unknown WAL/outcome with complete physical evidence is
retained in the reducer and withholds its classification ratio. Empty or
refusal-only populations produce no fabricated zero report. This is consistency
validation, not source authentication or qualification.

## Remaining execution gates

W owns trusted single-database sustained fixture/issuance and private failed-call
physical/fence diagnostics. Existing first light opens one fresh database per
BEGIN and cannot supply sustained cadence evidence. This PR provides drivers,
not a competing opener, SQL fixture, production seal proof or runnable host
qualification cell. QL must supply reviewed W composition and its own owner-join
adapter before native execution. Missing capability must fail or be explicitly
stubbed, never silently substituted. No native workload/host run is authorized
by the example.

W's proposed separate TEST-ONLY sustained composition is not on main. It will
share one writer/executor/governor/WAL/registry on one database, pre-seeding a
bounded set of DISTINCT legal provider owners/entities and freezing their
original requests. Each owner gets ONE fresh BEGIN followed by Resolve/ACK of
that SAME reference. Reusing an already-staged owner is replay/conflict, never
fresh throughput. Never clear image pins, cursors or receipts with SQL to turn
the existing fresh-only fixture into sustained state. QS must not clone W's
constructor/dispatcher, add a public factory or import qualification code into
writer. Fixture source remains NOT production seal proof.

W's private fence/ambiguity, pool/fault and authority-usage snapshots are
observation-only. Only actual physical close/ledger return supplies join proof;
no snapshot manufactures missing execution evidence. Fresh InventoryAccept
invocations remain unqualified until SC's complete body and W's reviewed central
enable exist. Production opening, ingress and source proof remain separate
missing capabilities. Until these owner gates land, this package's local controls
remain bounded scheduler/reducer controls only.

`Run` waits for every active action after stop. `DriversReturned` is explicitly
NOT native cleanup proof. After it returns, owning composition still closes and
proves writer/reader/output/checkpointer/helper retirement, preserves raw evidence
and removes only its own scratch. QL owns cooperative/hard watchdogs and treats
a hard-killed prefix as incomplete. Scheduler/reducer controls use fabricated
values. Separate read-package tests reuse its existing actual-Capture/LIVE fixture
to exercise all four adapters, consumer custody and cancellation. Their blocked
writer action performs no native write: it proves scheduler overlap/join behavior,
not real scan/enrichment coexistence. Native paired execution still requires W's
reviewed single-database legal issuance/diagnostic composition. No database
performance is established.


## Progress, mixed security and lineage: values stage

`ProgressCapture()`, `ProgressDrain()` and `Security()` are explicit CLOSED
native execution gates. `WCCadence(name, every, offset, pattern)` freezes a
bounded serial pattern mixing progress `capture_report`/`drain_piece` with
`admin_revoke`: intended Foreground/NORMAL and Recovery/FULL respectively.
Patterns may contain several captures or drains per security tick; they do not
assume one capture equals one drain. The metadata is not actual policy, a grant,
a physical call or a FULL commit. Each currently emits an untimed STUB and
returns `ErrWCComposition`; Run stops new offers and withholds completion.
No callback, caller flag or SQL input can open the gate.

Progress's component body (#1199) is merged; security targets draft #1201 head
`8b4db2f580a8fda2936dc5d6112ff3573c8d5873`. This first slice imports no draft
implementation and enables neither. WC's REAL original classifier, floor-only
ACK and api_events helper are the next dependency. Then QS will factor ONE
TEST-only owner fixture with the same executor, full actual Capture registry,
governor/grant and immutable originals, actual Resolve → ACK → native current
snapshot before advancing. `wc008NativeTurn`/`testCustody` and SQL floor repair
cannot substitute for it. W checks authority and WC checks domain; production
issuer, profile and enable remain separate. No Netcup/native run is authorized
by these values or by the example.

Sustained-v1's optional `Observation.Progress` carries bounded `ProgressFact`
values. `SourceID` is stable across capture/coalesced/pending/drained/error/unknown
records; the original receipt remains in Observation.Requested. Owner scope
keeps actual playback/command/clock incarnations and occurrence/activity/remember
generations. Report sequence, accepted sequence and accepted ordinal are
separate actual native units, never aliases or driver ordinals. Nullable owner,
physical ID/order, range, counter or linkage facts retain missing reasons; a
zero is not fabricated for an unavailable observation. Coalesced sources have
no new physical ID and require an explicit nonphysical reason.

A physical capture must provide its actual accepted `PendingRange`/cursor and
`AcceptedCaptureID=SourceID`. Logical coalesced sources can only attach to that
observed parent and remain within its native range; they cannot create, extend
or replace a pending obligation. Any live binding on the actual claim key
(playback + command incarnation) blocks replacement even if proof generations
change. Drain binds its own actual pending counters to that accepted parent,
not just to the presence of fields. The binding survives split drains until
native final clear, including when an individual contributing source retires.

Each drain's actual physical call/order, consumed range, native nonnegative
CreditedMS, pending flag/cursor and bounded source-ID/counter/range edges stay
separate. FIRST pieces fit one UTC quarter. Edge UNION must cover the actual
consumed interval; overlapping coalesced sources are not double-counted by
summed lengths. Pending cursor is actual, and the final NULL clear stays NULL.
Edges report remaining/drained source dispositions explicitly. Native credited
milliseconds are never inferred from ranges. Unknown/error/refused/fenced or
missing edges stop the lineage check and withhold coverage; aggregate counts
cannot repair a gap or create a passing subset.

The cycle-free `internal/progresslineage` checker stores only diagnostic IDs,
counters and ranges: 128 pending sources/owner scopes, 128 edges per drain and
4096 observed capture IDs plus 4096 drain IDs. These are harness memory bounds,
not product caps or new profile allowances. Emit EVERY raw fact before applying
the check, including rejected/overflow facts; overflow is incomplete, never a
silent drop. Linkage Complete certifies these metadata constraints only. While
native composition is closed, Run and the workload coverage wrapper reject ANY
progress metadata as native report input, even if a caller claims Complete.
The shared qual reducer remains unchanged.

`workload.WALSnapshot`/file types are aliases to the ONE unchanged read-only
implementation in `internal/walobserve`. A W-private test fixture can now import
it without a workload→writer test import cycle. `walobserve.Observe` uses the
same GV parser, bounded file/header probes, metadata/token ordering and derived
observer durations; it issues no grant, admission publication or reset proof.
Optional `Observation.WALBefore`/`WALAfter` provide advisory slots for the future
source-bound driver. Until then they stay absent, never fake zero snapshots.
Owner Evidence/HOLD/commit/error/fence/native facts remain independent. Existing
original ready/consumer timing is preserved; a read-only security check or
coalesced capture does not add a FULL commit.

## EC2 schedule composition

`EC2Streams` builds one serial writer worker for the declared Inventory,
Effective, Progress capture/drain and FULL-security pattern, plus one bounded
foreground worker, optional PASSIVE/recovery workers and a typed-WAL observer.
The foreground pattern contains up to the four real reader Actions. It reuses
`Run`'s original offer epoch/deadlines, missed/unserved census, sticky error stop,
join and incremental sustained-v1 records. The manifest copies the requested
patterns and lists every missing native binding. Missing Actions remain failed,
null-timed STUB offers; they are never removed or replaced with no-op writes.
`MixedFULLCadence` is intended scheduling metadata. `CompleteCapabilities` says
only that the selected Actions exist; neither establishes native authority,
continuous coverage, a FULL commit or WAL-BOUND/WAL-RESET.

The owning composition supplies the existing typed Actions on one database and
one governor. Checkpoint mode is a declaration, not an enable or a maintenance
budget. Only GV's actual owner can permit concurrent PASSIVE, determine hard
pressure and drain physical reader/writer custody for recovery. A recovery
request, checkpoint completion, token change, equal salt or absent WAL file is
not a reset event. No raw checkpoint, synthetic maintenance write, retry,
qualification classifier or shared reducer change is introduced here.

The test-only runnable attachment and its current missing capabilities are
documented in writer/N1_COMPONENT.md. An explicitly selected N1/read/WAL subset
can complete its own bounded schedule; it does not execute Effective, security,
progress, checkpoint or reset populations. Those whole-EC2 gates remain closed.
