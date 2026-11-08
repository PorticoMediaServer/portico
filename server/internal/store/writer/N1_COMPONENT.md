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


## Paired N1 and foreground component

`TestN1PairedMeasurement` is a separate opt-in test target. Build/run the writer
test binary from this package directory, set `PORTICO_WRITER_N1_PAIRED=1` and
`PORTICO_WRITER_N1_PAIRED_CONFIG` to the same closed five-field config used above,
and select `-test.run=^TestN1PairedMeasurement$ -n1-paired`. Explicit flag without
its companion, or companion without flag, fails. `fixtures` means 1..64 measured
fresh N1 originals; `-n1-paired-warmup` requires 1..64 actual warmup originals.
`-n1-paired-every` declares the bounded1ms..1s offer interval (default100ms),
and `-n1-paired-route=browse|detail|playback|continue|all` selects one route or a
four-entry repeating foreground pattern. `-n1-paired-cohort=timing|native-counters`
remains separate; `-n1-paired-cadence=realistic|accelerated` is an explicit
population declaration. There is no native/remote run or qualification scheduling here.

The composition reuses ONE existing N1 seed/executor and progressive owner,
attaches the factored RD2 foreground seed outside custody, and captures BOTH
complete registries on that same canonical database before measurement. Reader
connections use ExistingFile and the bundled native main-file identity getter;
every actual physical connection must match the owner's observed native
device/inode. The existing test connector seam supplies the read-only
attestation; no opening permit is added. A refused attestation stays inside
the snapshot owner through actual native cleanup, with unobserved facts null. The
external driver receives opaque test-only handles and typed readers, never SQL,
connections, frames, grants or a new issuer. The actual read adapters and shared
ConsumeForeground remain unchanged. N1 uses library7; authenticated reads use
the existing library1 fixture on the SAME file, not the same item/library.

The two existing fixture configs are composed without new resource numbers:
N1's actual Work,25msHOLD,4MiB preparation/4MiB reserve/class amounts remain;
RD2 retains its existing LIVE profile,80MiB+bookkeeping partition,12MiB read,
8MiB output,1s ready and3s protected deadlines. The combined fixture uses RD2's
existing16 grant-metadata slots; one serial N1 worker still admits one8MiB writer
holding in its unchanged8MiB class partition. This is test composition, not a
new qualified resource/production profile. Default standalone fixtures retain
their old configs. Only the known foreground class may remain charged during
N1's own joined/snapshot checks; all other holdings still stop progression.
All class holdings must be zero after both streams and owner closes join.

Foreground preparation derives original deadlines from the original scheduled
offer, and uses its real trusted timestamp for Now. No frozen clock is accepted.
The existing bounded scheduler runs one serial N1 chain worker and one serial
foreground worker; it retains missed/unserved offers and errors and never retries
to fill a successful population. N1 preserves each original's BEGIN/Resolve/ACK
owner intervals and actual physical ordinals. Foreground results keep separate
read/consumer/scope errors, nullable native/window facts, bounded summaries and
scheduled-ready through full consumer/cleanup timing. The actual consumer JSON
encodes the bounded page to a joined discard sink; this is encoding work, not
HTTP transfer, playback start or first-frame proof. No page escapes consumption.

Raw `portico-n1-component-v1` records declare
`component_layout=n1-paired-foreground-v1`. Serialized post-custody records include
N1 originals/turns, each foreground observation and nested unchanged sustained-v1
scheduler records; their own IDs/ordinals/actual deadlines remain distinct.
Source/file/config/binary/host/database identities are retained. A setup failure
keeps a composition_start/final prefix with missing owners null; any action,
sink, schedule, native-return or retirement gap makes the full cell incomplete.
Reader idle Close timing is unavailable with a reason; Snapshots.Close return
is not relabelled an independently observed native Close. Process heap/RSS/
allocation samples include concurrent reader/observer allocations, not isolated
N1 LIVE peaks. This population is separate from previous N1 waves/methods.
Readiness remains false; counters stay raw/null. No reducer or qualification
verdict is produced.

The read fixture's source journals and default data are factored byte-for-byte;
existing browse/foreground captured plans are unchanged. A bounded setup-only
attachment is consumed by the exact test owner once, outside HOLD. The paired
movie cell reuses N1's already-present identical kind1 and omits the existing
two-kind seed statement; it does not add a kind2 claim or repair/delete rows.
Paired preparation rebases the existing synthetic authority/data time offsets
ONCE to actual time.Now and records that temporal origin; standalone RD2 seeds
remain unchanged. There is no renewal during the run, new issuer or repair of
failed inputs. Original request deadlines remain absolute and unchanged; actual
expiry remains a failed cell.

## EC2 sustained component attachment

`TestEC2SustainedMeasurement` reuses the merged `N1PairedComponent` fixture,
executor, complete captured registry, governor, original Resolve/ACK progression,
warm persistent writer and all four real foreground Actions on the same file.
It adds the `EC2Streams` schedule and read-only typed WAL snapshots. The existing
N1/RD2 synthetic profiles, work limits, grant recipes and 128-total-original
fixture bound are unchanged; the target accepts 1..64 measured plus 1..64 actual
warmup originals. There is no production issuer, profile, permit or enable.
The original N1 owner records, raw failure prefixes and final custody/native
joins remain authoritative. A chain's scheduler wall interval is not HOLD or
COMMIT. Raw physical ordinals continue across warmup and every actual role.

Every measured BEGIN, saved-original Resolve and ACK has an `ec2_wal_turn`
record joining the SAME original reference, actual owner attempt ID/physical
ordinal and before/after `WALSnapshot`. These probes execute outside HOLD and
retain parser failures, nullable native facts, bounded file/header probes,
allocation, salt/frame/backfill/debt and gate-event metadata. A failed pre-probe
has a null physical ID; it does not manufacture an attempt. A returned failed
owner turn still gets a post-probe, even after cancellation. Observer error is
separate from the saved owner error; it stops progression. The timer observer
is separate. Probe equality is not atomicity, reset or owner-opening proof.
Header-sync/xSync intervals remain null with a reason. This instrumentation and
its file/serialization cost are explicit component overhead, never subtracted
from a physical command or attributed as header sync.

Default runs skip the measurement target. Requested opt-in requires BOTH
`-ec2-sustained=true` and `PORTICO_WRITER_EC2=1`, plus
`PORTICO_WRITER_EC2_CONFIG` naming the existing strict five-field JSON config
(run_id, host_epoch, source_commit, output_path, fixtures). Missing companion
configuration fails. `-ec2-warmup=1..64`, `-ec2-cohort=timing|native-counters`,
and `-ec2-cadence=realistic|accelerated` declare distinct populations. Writer
spacing is 1ms..30s, the window <=2h and total scheduled offers <=65536.
The writer pattern has exactly one inventory entry and <=32 entries; a fresh
original is offered once per pattern. Readers remain one serial four-route
worker; no parallel second writer is added.

The default requested pattern is `inventory,security`, with concurrent CP and
recovery requested. Current main supplies only the N1/read/WAL attachment:
Effective, native WC and GV concurrent/recovery bindings are missing. Their
requested offers emit failed STUBs and stop all streams. A bool or callback
cannot open those native paths. `ec2_start` preserves requested patterns and
missing capabilities; WAL-BOUND/WAL-RESET coverage is explicitly false.

For an explicit partial component, QL may select
`-ec2-writer-pattern=inventory -ec2-passive-every=0 -ec2-recovery-every=0`;
this omits the missing populations by declaration, never by success filtering.
Other spacings are `-ec2-writer-every`, `-ec2-reader-every`, `-ec2-wal-every`.
`-ec2-checkpoint-mode=serialized|concurrent` is metadata only. A successful
subset final is not whole EC2 coverage, a real-rate/security claim or a reset
measurement. `ec2-sustained-component-v1` stays separate from earlier N1 waves,
DIRECTIONAL/qualification=false, readiness=false. No new host run is requested.

`TestEC2N1WALComposition` is a tiny disposable functional control for the actual
N1 chain, all three physical WAL joins and one real reader; it does not measure
budgets. Run focused controls on the GitHub lane-focused runner with Go1.26.10;
its default-skipped measurement target is not execution evidence.
## EC5 component memory extension

This extends QL's existing process checkpoint/host/cgroup scopes on the SAME
`N1PairedComponent`; it does not create a second issuer, fixture, SQL path,
connection opener, resource profile or permit. `-ec5-memory=true` requires
`PORTICO_EC5_MEMORY=1` and the separate `native-counters` cohort. The owner
verifies its actual cgroup with `qual.CaptureHost` before creating its fixture.
The requested assigned cap is literally 2,000,000,000 bytes, with zero swap;
page-rounded effective limits and ancestors are retained. This is distinct from
physical 2GB/M=1.6GB, and leaves all existing synthetic N1/RD2 limits unchanged.

The paired scheduler adds one process-only `MEMORY` stream (20ms..1s, default
100ms) using the existing `n1Memory()` RSS/VmHWM/Go heap/allocation/GC helpers.
Every observation has its actual interval and no HOLD/COMMIT/owner Evidence.
Instrumentation and Go runtime pauses remain included; no GC, memory limit,
subtraction or runtime repair is introduced. Fixture/pre-warmup and joined-close
checkpoints remain separate. Lifetime VmHWM and sampled current heap/RSS values
are not incremental LIVE ownership peaks.

Writer SQLite cache/schema/statement CURRENT bytes use the existing per-turn
owner snapshots OUTSIDE HOLD. No new goroutine polls native connection pointers.
The existing test-only reader Connect seam records its actual native Connect
interval and one synchronous current-memory snapshot before returning the
connection to its normal snapshot owner. Counter-sampling cost is separately
bracketed. Its bounded 16-entry open ledger preserves failed openings/null native
facts and marks overflow incomplete. Initial writer native Connect, complete
usable-open/Capture cost, reader idle Close and CP lifecycle/aggregate native
peaks remain null with reasons; Setup and raw DBStatus high0 cannot supply them.

`memory_fixture` states the existing small N1+foreground population, actual
observed database table cardinalities and explicit unavailable per-library/
scalable population. There is no 1M or whole-server claim. SC/helpers, TMDB,
media/legacy and complete native lifecycle remain missing bindings. The merged
EC2 composer is attached through `TestEC5SustainedMemoryMeasurement`; its native
WC/Effective and GV concurrent/recovery requests retain their failed bindings.
No unmerged #1285 code is imported.

`ci/ec5_memory.py` runs a precompiled test binary in ONE new private cgroup-v2
leaf on a disposable standard GitHub runner. It sets/readbacks the 2e9 cap and
zero swap BEFORE exec, drops root to the runner UID, rejects stricter ancestors,
and collects memory.current/peak/stat/events outside that leaf at 100ms. The leaf
survives process exit so the final kernel charged peak includes teardown/tails.
It preserves signals/errors/OOM/unknowns, joins the child and kernel populated
state before deleting ONLY its own unique leaf. No global controller is enabled.
The controller remains outside the cap; child observer costs remain inside.
Raw charged file/kernel memory is never subtracted to manufacture a cap pass.
Peak-vs-cap/reserve numbers are planning comparisons, not whole-server fit.

`TestEC5PairedMemoryMeasurement` invokes the ONE existing paired measurement
entrypoint/config and original deadlines/consumer/error/join behavior. Use the
existing strict paired config with `ci/ec5_memory.py --binary ABS_TEST_BINARY
--output OWN_ARTIFACT_DIR --target paired --config ABS_PAIRED_CONFIG`; run from
the writer package through noninteractive sudo on a disposable runner. Default
product/test runs skip this target. `--target sustained --config ABS_CONFIG`
uses the merged EC2 target with its default mixed-cadence failed bindings.
`--sustained-subset` explicitly selects the existing N1/read/WAL component only;
it cannot claim FULL, checkpoint or reset coverage. No host measurement is
requested by this source change.

Functional proof uses the fixed lane-focused `ec5-memory` harness, Go1.26.10:
it starts a tiny real N1/read child inside that owned cgroup and checks actual
parent execution, cap/zero swap, kernel peak, process join and leaf removal.
Its source and binary remain component controls, qualification=false.

## EC3 user foreground-write wait

The test-only EC2 attachment accepts `-ec3-foreground-write-every=1ms..1min`
(default0/off) and a closed `-ec3-foreground-write-pattern` drawn from
`progress_capture,progress_drain,security` (default drain/security). This creates
one independent bounded submission stream while the scan stream and original
four-route reader stream run. They share the SAME actual native writer/governor;
no driver lock, priority queue, retry, issuer or second physical writer is added.
Scan pattern must then contain only Inventory/Effective, so WC inputs are not
concurrently reused by two workers. At most4096 planned foreground requests;
overflow stops and retains incomplete coverage. WC's actual missing Actions
remain failed STUBs with null owner intervals, never synthetic FULL writes.

`ec3-foreground-write-wait-v1` records each original mutation's actual driver
Action-entry submission, original scheduled offer/lag, owner HOLD start, actual
COMMIT return and physical return. Queue is the pre-HOLD interval including
preparation/admission/lock waiting; no mutex-only timestamp is invented.
`QueueWaitNS + HoldToCommitNS = SubmissionToCommitNS`. Full physical HOLD can
extend past COMMIT into terminal/retirement and is reported separately.
Facade return and later Resolve/ACK never reconstruct missing commit evidence.
The original typed Result, requested receipt, errors/refusals and owner Evidence
remain raw; only the first original-mutation observation counts as that write.
Security's actual class stays Recovery/FULL, progress Foreground/NORMAL.

The report supplies nearest-rank p50/p99/max for queue, service-to-commit,
submission-to-commit and physical HOLD, overall AND per requested operation.
Observed-zero queue is retained. Known failed/replay tails remain in explicit
partial distributions; null/error/refusal/unserved/missed/overflow gaps withhold
whole-window coverage and the nullable observed-p99<=50ms comparison. A rare slow
security write cannot be hidden by many fast progress samples. This comparison
is descriptive, qualification=false, not a new deadline/profile/admission rule.

The original read/consumer/deadline/evidence stream is unchanged.
`ReadsUnaffected=null` until QL performs its independent paired foreground-only
comparison. Concurrent request return order is not a writer physical ordinal:
future WC owner-proven IDs/order must be retained; none is manufactured here.
No native foreground-write qualification is executable until WC's same-owner
Actions/diagnostics land. Frozen clocks, raw SQL and failed-cell p99 claims remain
forbidden. Functional controls run on GitHub with Go1.26.10.

Canonical population screening attachment: set BOTH
`PORTICO_PAIRED_POPULATION_DIRECTORY` and
`PORTICO_PAIRED_POPULATION_MANIFEST_SHA256` for paired, EC2 or EC5 constructors.
The unchanged five-field workload config and existing opt-in flags still apply.
The actual exact-t `UseQualificationPopulation` copy seeds library7 without
reinserting its parents; browse selects that full population. Detail/playback/
personal routes retain the existing library1 overlay and are labelled separately.
EC5 controller forwards the same coordinates with `--population-directory` and
`--population-manifest-sha256`. Missing/invalid pairs fail, never fall back.
This slice attaches population only; EC2 native WC/CP/recovery composition remains
explicitly missing until the following composition slice. No qualification claim.

EC2 native composition uses the same attested existing-file opening and funded
keeper lifecycle as canonical N1, plus WC's one factored birth/driver builder.
Progress capture/drain, FULL security and retained reclaim are actual merged
Actions. The serial writer queue is shared by scan and independent user-write
submissions; reads and funded PASSIVE remain separate. Select
`-ec2-writer-pattern=inventory` with a sustainable
`-ec3-foreground-write-every` for the user-write cell (500ms with 128 measured
originals is the screening pin, giving 256 offers; 50ms remains the wait target that the ledger
classifies against, not the offer cadence, because one action over the interval
is a missed offer and the bounded schedule is correctly incomplete). A bounded
miss never submits an invalid phase: capture with retained pending and drain with
nothing pending record an explicit `*_DEFERRED` observation and do no write.
A read whose publication fence changed under concurrent ingest retains the stale
attempt raw and is retried once within its original deadline.
The EC2 verdict requires the serial `writer` stream to serve every scheduled
offer; read, checkpoint and WAL misses stay recorded gaps and every action error
still fails. The EC3 verdict additionally requires complete foreground-write
coverage. The default foreground pattern is capture, drain, security. The wait
ledger selects ACCEPT, preserving SNAPSHOT/Resolve/ACK separately.

Default mixed sustained writer pattern is inventory/security. Optional writer
entries progress_capture, progress_drain and reclaim stay real source operations;
undrained pending state/refusals stop rather than being repaired. Real recovery
is requested only at the observed hard-minus-reserve threshold. A below-threshold
probe emits RECOVERY_NOT_DUE, with no native interval/reset proof. Actual space
refusals and all partials remain failures; no caps, retries or missing-zero repair.
The full captured union, extra WC unit funding, retained preparations, actual
setup/reopen counts and final all-class retirement are emitted. Effective remains
unbound. All profiles remain test-only and qualification=false.
