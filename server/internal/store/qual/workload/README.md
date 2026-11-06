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
replay/result conservation and ACK floor. A failure stops offers in all streams;
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
        {Name: "browse-ready", Run: workload.Browse(reader, nextBrowse)},
    }},
    {Name: "checkpoint", Every: 250*time.Millisecond, Pattern: []workload.Step{
        {Name: "passive", Run: workload.Passive(checkpointer, nextCheckpoint)},
    }},
    {Name: "wal", Every: time.Second, Pattern: []workload.Step{
        {Name: "observe", Run: workload.WAL(databasePath, walGate)},
    }},
    {Name: "security", Every: 2*time.Second, Pattern: []workload.Step{
        {Name: "security-STUB", Run: workload.StubCadence("WC FULL unavailable; no security write")},
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
The observation is advisory and can race; QL must join/deduplicate events and
retain unknown/stale states. CP completion/allocation/token changes do not prove
a reset. Creation is not reset. Only owning composition may attest freshness.

`Browse` calls RD's real two-window facade and retains both snapshot results.
Its trusted `prepare` callback must bind the original ready deadline and obtain
output custody. `Consume` is required and runs after Window even on read failure,
so it can encode/consume and retire that output through its owner. The measured
wall interval includes preparation, both windows and consumer work. `ReadyNS`
adds scheduling delay from the original offer; it is not HTTP/network latency.
The result retains the original ready budget and deadline. Compare paired
foreground-only/ingest cohorts against B-READS at the declared library size:
for sorted browse40 at 1M the proposed p95/p99 ready budgets are 50/150 ms;
background delta p95 ≤10 ms/p99 ≤25 ms. The ≤1 s snapshot protection ceiling is
not a ready budget. This package computes no gate verdict.

RD2 reads and WC progress/security are not available as executable facades in
this base. `StubCadence` records missing capability at the declared cadence,
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
  optional owner HOLD/COMMIT and scheduled-ready duration;
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
physical ordinal gap, unknown fence, stub, missed slot or incomplete envelope
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

`Run` waits for every active action after stop. `DriversReturned` is explicitly
NOT native cleanup proof. After it returns, owning composition still closes and
proves writer/reader/output/checkpointer/helper retirement, preserves raw evidence
and removes only its own scratch. QL owns cooperative/hard watchdogs and treats
a hard-killed prefix as incomplete. All local tests use fabricated values and
small timing controls; they establish driver behavior, not database performance.
