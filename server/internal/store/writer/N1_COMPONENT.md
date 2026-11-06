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

`-n1-component-population=first-use-reopen` is the historical default: it opens
a new physical connection/cache after setup, so its pilot is first-use evidence.
`setup-retained-first-use` instead installs the same test-only decorator at the
existing canonical fixture's initial open and retains that connection through
setup/Capture and its ONE fresh original. The source fixture/SQL/profile stay
the same. Both populations remain first-command controls, separate from steady
timing. Connection IDs, open counts and native-close counts are observed before
and after the turn. A retained-setup continuity break makes the attempt/run
incomplete; it is not silently moved into a successful replacement population.
Continuity `*_after` observations are taken after turn/physical return and BEFORE
fixture teardown. Native close records are emitted AFTER teardown, so
`native_closes_after` need not equal `len(native_closes)`. The default reopen
mode observes only its decorated measured pool, not the already-closed,
unobserved setup connection. Absent before-boundary counts stay null.

`-n1-component-population=steady -n1-component-warmup=1..64` retains the
reviewed progressive fixture's ONE physical writer connection/cache across real
fresh originals. The five-field config's `fixtures` now explicitly means 1..64
MEASURED originals in the `n1-progressive-steady-v1` layout, after the separate
warmup originals; exactly one database is requested. The start record replaces
`requested_fixtures` with explicit database, warmup and measured-original counts.
The historical first-use layouts and their config meaning remain separate.

Steady currently supports **only active/current/present/fixture**. Other cells,
zero warmup, and warmup attached to a first-use mode fail before opening output.
There is no SQL reset to preserve an absent bucket or replay to generate traffic.
Warmup means completed fresh effects → Resolve → ACK → current snapshot on the
retained connection. Exact warmup references/counts are carried into measured
turns. An unexpected failure remains in its original population, stops all later
offering and makes the final component incomplete. A replacement never becomes
a new passing steady subset. This is component preparation, not production
issuance, whole outer-authority acceptance, a qualified profile or p99 proof.
The previous first-use pilot and UNKNOWN/deadline tail are unchanged.

Both cohorts now record bounded request/row-close clocks, exact IDs, use counts
and returned/changed rows inside HOLD. `timing` omits native diagnostic getters;
`native-counters` adds
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

`native_closes` brackets the actual native driver's Close inside the existing
owned-close boundary, with connection ID and nullable start/return/elapsed/unwind
facts. It may enclose SQLite checkpoint/sync work but does not isolate xSync.
`Completed` means the native Close call returned; inspect its error separately.
It is not independent native-join/custody proof. An unwind has null return and
elapsed fields and remains partial; the existing owner cleanup facts still apply.
`post_commit_to_physical_return_ns` is separately named as an enclosing retirement
interval, not isolated close or sync. `xsync_ns` stays null with the existing
unavailable reason. Close observations are bounded; overflow makes the run
incomplete. Timing-observer overhead is included, never subtracted. No vendor
instrumentation, scheduling or qualification run follows from this seam.

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

The opt-in target now composes this reviewed fixture; the target always supplies
`time.Now`. Functional controls may use a frozen protocol clock and are never
measurements. No run or scheduling follows from enabling this test-only path.

## Progressive steady raw layout

The NDJSON schema remains `portico-n1-component-v1`, with start declaring
`population_layout="n1-progressive-steady-v1"`. Keep source/layout, host/resource
and cache epochs separate from all first-use pilots. Every original has its own
`original_id`, contiguous `original_ordinal`, immutable `original_reference` and
`original_population=warmup|measured`. Each gets distinct call/attempt IDs with
`turn_role=fresh_begin|resolve|ack`; each call emits attempt_start BEFORE custody
and one bounded attempt AFTER its physical call/cleanup returns. The same
original reference joins all three roles. `physical_attempt_ordinal` counts
known actual owner intervals across ALL roles in this ONE database. An owner
view not returned by the helper stays null/unprojectable, with an unobserved
census entry and sticky stop; zero is not pre-admission/rollback proof.

The final record's `steady.warmup` and `steady.measured` each report offered,
known `fresh_begins`, and complete ORIGINALS. Only actual returned fresh-BEGIN
owners count toward original N; Resolve/ACK are separate physical turns, never
extra originals. `turns_by_role` retains all started calls, known physical
intervals, returned/committed/rolled_back/unknown/unobserved/error/refusal/fence
counts. Complete original requires all three successful physical turns plus the
native current snapshot. An attempt's complete means that ONE physical component
turn, never whole command/profile qualification. Per-call owner HOLD/COMMIT,
actual Settings, governor request, custody/connection return and fence facts are
separate. `begin_succeeded=true` means the actual owner returned a native-BEGIN
turn, not its post-COMMIT current open flag. The unreturned view stays null.

Fresh turn_setup encloses the fixture Begin plus history MISS/library guards;
its invocation encloses SC's effects. Resolve/ACK invocation encloses the whole
existing helper; exact internal setup is null. ACK's invocation also encloses
its post-return snapshot, while HOLD ends at the actual physical return.
`historical_result`/digest come from successful actual Resolve; ACK's scalar
handler return and physical outcome remain separate. `post_ack_current_snapshot`
is emitted only after a successful native current read. No public Evidence or
Applied outcome is manufactured, including on errors.

The trace SQL/ID map is derived from the ACTUAL complete captured registry,
including history/library/ACK guards, rather than the old 43-effects list. The
128-unit ledger is freshly preallocated outside custody for each physical turn,
flushed after return, and never reused while a record retains its counters.
Timing omits native counter snapshots inside HOLD while retaining bounded ID/clock/row observations. Native-counters includes
its observer costs; the ACK current read is labelled post_return_observer when
its real start follows physical return. It is not charged back into HOLD.

Continuity after fields end after each physical call/current snapshot, BEFORE
fixture teardown. Per-turn native_joined stays null: that connection deliberately
stays alive. Final `steady.native_closes` and `native_joined_after_fixture_close`
are emitted AFTER the single fixture teardown, including its final native Close.
Completed means native Close returned, with error separately retained; it is not
independent custody proof. WAL samples carry actual gate tokens before/after the
bounded parser/file probes plus getter timestamps. Equality is not atomicity,
reset, publication or owner proof. Missing policy/VM/memory/IO/xSync dimensions
stay null with reasons; no native/vendor machinery or shared reducer was added.

The existing fixture retains bounded test cleanup callbacks until its scope ends.
Observed allocations/RSS include that fixture retention and observer overhead;
these are not production steady residency or a live-memory peak. Setup facts,
pre/post-close memory, observed physical connection counts, final pool stats and
actual authority usage retain their own scope instead of being subtracted.


## EC4 profile observations, not a qualified profile

Start emits `observation_method=n1-owner-max-observations-v1`. BOTH cohorts now
have populated bounded units and `profile_facts.per_id`: every ID in the actual
captured registry, including observed zero uses on untaken branches. An unreturned
registry stays unavailable with a reason. Do not pool this observer method with
Wave1/2: the new timing cohort includes request clocks/counts and row-close
wrappers, while native-counters additionally includes its diagnostic snapshots.
All observer overhead stays in HOLD; nothing is subtracted.

Each ID carries request-use/returned/error counts and separate maxima for rows
returned, rows changed, examined rows, VM operations, native pages, complete IO,
WAL attempted/written/append/header bytes/header writes/errors and elapsed spans.
`elapsed_ns` includes the diagnostic wrapper. `native_request_span_ns` excludes
its before/after diagnostic snapshots but includes native request/open-row/Scan
lifetime through Close; it is not pure SQLite CPU time. The raw per-use ledger
remains available. A maximum is null if any requested use lacks that metric;
observed_subset_max and known/missing-use census remain labelled partial.
Unused IDs have observed use_count=0 and null costs, never guessed zero costs.
Error/partial uses stay in the same original/cohort/connection/salt bracket.

VM and WAL deltas require monotonic same-connection, nonoverlapping intervals.
Raw hook Qualified=false stays raw/incomplete. Written bytes include rewrite
and spill, so neither append-only bytes nor exact header bytes is synthesized.
HeaderWrites is a count; multiplying/subtracting 32 bytes is forbidden. Cache
events/fullscan steps/WAL frames are not native page visits or examined rows.
Before/after WAL salt is only this call's observed generation scope, not an
atomic per-statement generation or reset proof. Native operation arrays keep
policy, DQS, BEGIN, COMMIT and rollback observations separate from registered IDs.

W's fixed-size owner hook supplies `owner_window` bound to THIS sequential
invocation's actual setup entry. Stale/absent observations remain null (ErrBusy
creates none). Setup and Terminal use the owner's real monotonic clock; legacy
HOLD/COMMIT/physicalReturnedAt remain distinct. Successful Setup ends after setup;
failed Setup ends after cleanup and OVERLAPS Terminal. Never add those intervals.
Terminal preserves its actual attempt return plus separate native-return and
custody-retirement entry/return/completed facts, including partial/error paths.
NativeReturnedAt means required borrower/quarantine return, not idle native Close.
Actual native Close intervals stay in the decorator ledger. No callback runs in
HOLD, no missing end is reconstructed, and no observed duration becomes a bound.
Acquisition, complete Assert, isolated header-sync/xSync remain null unless
separately observed; native policy brackets are not promoted into those phases.
Never sum unrelated per-ID/bracket maxima to invent an owner recipe.

Actual GV request device ID/class/outstanding/reserved bytes are emitted only
if present in the bound request; they are reservations, not measured service.
The synthetic fixture currently supplies no device mapping. Complete per-command
IO and native page counts remain null with reasons, rather than replacing them
with procfs IO, OS device identity, WAL writes or declared Work Pages.

Input facts retain actual byte/codepoint/escaping lengths, file/root shape,
declared branch cell and known codec limits, alongside existing actual setup
profiles/age/cardinality readback and executed IDs. Existing fixtures cover a
partial branch/input matrix. inventoryProgressiveFixture is one synthetic branch,
not a legal-MAX source. Complete legal MIN/MAX stays null with reasons: the named cap/prefix cells
below prove their exact source shape, not an entire simultaneous MAX envelope.
A registry hash, forecast or cumulative WAL length is not that proof.

Incremental LIVE Go/native/bind/row/temp/result/preparation/consumer-retained
peaks through final release stay null with reasons: no complete live ownership
hook exists. inventoryFreshRequiredBytes is an allocation-free FORECAST only.
Existing idle/native checkpoints, allocations, RSS and raw DBStatus high values
remain separate. Raw high=0 is not a native peak and process lifetime VmHWM is
not a per-command LIVE peak. `profile_gate_missing` lists these unmet gates and
`qualified_profile_ready=false`; production enable/profile/caps are unchanged.
No new run or qualification/reference scheduling follows from this exposure.

The decorator also retains the actual six-scalar registered library read
(data/order/metadata/age-input/age-published/age-pending) and the completed age
bucket point-read row count, when reached. These are call-bound native readback,
not the declared case or a registry hash. Fixed internal clocks in protocol-only
controls are explicitly labelled; measurement config/flags cannot select them.
The measurement target retains time.Now, its original quantum and all misses.


## Owner and source-bound cap cells

The same explicit opt-in/config are required. Each new cell requires
`fixtures: 1` and zero warmup; these are separate populations, never steady timing
replicates or a Cartesian sweep. `n1-owner-max-observations-v1` must not be pooled
with Wave1/2 or `n1-profile-observations-v1`.

- `-n1-component-population=owner-window -n1-component-owner-cell=commit|rollback|discard|startup|reset|restart`
  uses `active/current/present/fixture`. Commit/rollback use the retained setup
  connection. Startup measures a first-use connection after the canonical setup
  pool was joined, not OS-cold/fresh-file startup. Discard deliberately refuses
  ROLLBACK before native entry; its injected request span/counters stay null,
  while actual native Close/return/retirement remain observable. Only the exact
  injected failure and complete joins complete this deliberate-error cell;
  unexpected errors remain incomplete. Raw rollback/discard attempt `complete`
  stays false because it is not a committed command; the final owner-cell
  completeness describes the declared negative control, never qualification.
- RESET remains unavailable/incomplete: no existing closed owner fixture proves
  a fully-backfilled native checkpoint restart/truncation and the following
  command's header/generation. The requested target emits the gap and fails,
  with no physical attempt/zero sample. No raw checkpoint is added. Pooled
  ResetSession, salt equality, PASSIVE and allocation are not reset proof.
- RESTART commits one original, joins the old executor, then uses W's existing
  canonical opening on the same native file. The initial unobserved opening
  connection is joined and checked for sticky close faults before the test-only
  decoration seam is applied; its count/scope are explicit. Replacement uses
  `ExistingFile` and the actual native binding. The same governor/recipe/full
  recaptured registry and saved original continue through actual Resolve/ACK/
  snapshot. The old executor stays closed; no state or SQL ACK is repaired.
  This is COMPONENT restart only, not production fresh lease/issuer recovery.
  Two decorated physical connections plus the separately labelled initial W
  opening connection are not silently represented as one lifetime.
- `-n1-component-case=dual/current/absent/canonical-max` with default first-use
  population is a PURE constructor/refusal proof, opening no DB/taking no grant.
  SC's actual immutable constructor and typed encoder accept exactly the 32MiB
  canonical limit and reject one extra byte. One legal text is grown with NUL
  escaping; title/object/change-token named caps are also present. Its reference,
  actual input counts, encoded bytes, adjacent rejection and allocation-free
  forecast are emitted as `constructor_cell`, not a physical attempt. Before
  EACH at-limit/+1 boundary constructor, a synced `constructor_offer` records
  stable case/offer ID, constructor ordinal, exact offered canonical bytes and
  measure source (actual typed seed encoding plus one field's exact byte growth).
  EACH actual value/error/digest/policy-field/actual-limit result is synced as
  `constructor_result` immediately on return, before later probes or summary
  work. An interrupted offer remains unmatched; no result/receipt is invented.
  Rejected probes have null returned digest; the summary reference is explicitly
  `accepted_constructor_reference`. Constructor IDs never become physical IDs.
  The deterministic interruption control stops after +1 offer but before its
  constructor, retaining the first actual result and unmatched second offer. Physical
  MAX remains gated: forecast exceeds the unchanged 4MiB preparation partition.
  This is not an observed native refusal or a measured LIVE peak. W supplies any
  future suitable test-only owner recipe/partition; this target cannot widen it.
- `-n1-component-population=full-prefix -n1-component-case=dual/current/absent/domain-cap`
  executes one escaped/domain-cap original through the SAME progressive fixture:
  fresh BEGIN, saved-original Resolve, ACK, actual joined current snapshot.
  Object=8192 escaped bytes, change token=4096 escaped bytes, title=1000 codepoints
  use SC's revision/immutable constructor. All command effects and both RAW banks
  remain present. Each role has its own actual owner intervals/physical ordinal;
  all three retain the original identity/digest. Only actual final ACK/snapshot
  earns full-prefix=true; failed Resolve retains its prefix and stops. This is
  not canonical32MiB physical MAX, a dirty-WAL population or production authority.

Counters stay raw/null, including pages/header-sync/xSync/LIVE gaps. The exact
owner hook dependency and source epoch must be pinned for any later QL run.
There is no automatic run, limit increase or qualification scheduling here.
