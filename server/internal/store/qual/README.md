# Qualification collector

The host collector defends against accidental mislabelling on the runners and
hosts we dispatch: wrong VM size, containers, inherited cgroup limits, and
missing or unreadable files. It does not defend against a deliberately hostile
environment that fakes procfs. Deliberate procfs forgery is out of scope.

The collector requires `/proc` and `/proc/1` to belong to the same procfs
instance: both opened directory descriptors must report `PROC_SUPER_MAGIC`
through statfs and the same `st_dev` through stat. It also requires readable
`/proc/self/ns/mnt` and `/proc/1/ns/mnt` descriptors. Failure of either structural
check refuses the profile. These checks do not authenticate a deliberately
forged procfs view; no further anti-forgery machinery is provided.

Namespace, hardware, affinity, cgroup ancestry and effective-limit checks remain
required. Unknown or unreadable required evidence fails rather than becoming
an unlimited resource claim. Receipts retain raw evidence and errors, including
namespace/procfs descriptor facts before and after collection.

The dispatch-only host workflow checks this evidence protocol. It opens no
database and supplies no engine, latency, service, memory-capacity or workload
qualification by itself.

`CaptureConnection` supplies a connection-policy receipt for the bundled SQLite
3.53.4 engine. Obtain each connection from the shared `sqliteconn` helper, select
its private durability policy, and capture outside a transaction before timed
work on every acquisition, including after a possible replacement. The caller
must keep that connection exclusive and invalidate timing if it loses custody
or changes policy. The capture does not identify physical connections or make
assertion and workload execution atomic.

Receipts retain the helper's actual PRAGMA and enforcement values. A helper
failure leaves the observation null and invalid; the observer never repairs
policy. Positive reader limits record the asserted native values. Zero reader
limits mean preserve-existing/unasserted, not observed defaults. Observer
elapsed time is neither usable-open latency nor writer custody, and these
receipts supply no workload, RAM, or snapshot-admission qualification.

`SummarizeRestarts` reduces a single manifest-bound host/database/cold-or-warm
and realistic-or-accelerated evidence cell. It consumes joined physical writer
attempts classified by an external collector from actual WAL generation/frame
evidence. It does not infer a restart from checkpoint completion or unchanged
allocation, collect native evidence, select a reset policy, or issue a pass.
Keep the raw ledger and manifest; strings/references do not prove provenance.

The report retains complete holds and COMMIT durations overall and by
recipe/actual grant class/durability. First-after-restart holds remain in those
overall distributions. Conditional first/rest/unknown and failed-attempt strata
are additional views; initial creation/recreation is counted separately from
restart. Failed first writes keep their restart event/cost. Events retain actual
versus intended class and evidence references. Unknown generation position or
commit outcome withholds the cadence ratio instead of becoming zero or warm
append. Empty populations and p99.9 below 10,000 observations are null. Counts,
refusals, fences and slow samples remain visible. Unit fixtures are fabricated
reporting controls, never measurements or evidence of actual restart frequency.
The planned sustained-workload cadence target is approximately one restart per
200 commits (0.005 per commit, five per 1,000). Report the actual ratio and raw
counts; this descriptive reducer does not turn that target into a gate verdict.
Fenced attempts, including committed appends, retain event IDs and evidence
references with their true outcome; each recipe/class/durability reports its
fence count separately.

### Device-only Linux IO statistics

Linux `blkcg_print_one_stat` emits a device name even when it suppresses the
core read/write counter block. This produces valid lines such as `7:7` in a
readable `io.stat`; an empty readable file is also valid before any IO. See the
[pinned Linux 7.0 implementation](https://github.com/torvalds/linux/blob/v7.0/block/blk-cgroup.c#L1116-L1132).
The collector retains those exact raw bytes and synthesizes no discard, delay
or other counters. A populated record still needs every required read/write
field; malformed/duplicate devices or fields fail. Unreadable/missing required
telemetry and incomplete/finite `io.max` controls still fail closed. This format
recognition supplies no latency, throughput or qualification verdict.

### Applying the hosted hold gate

`go run ./internal/store/qual/cmd/holdreport -input cohort.json` calls the
reviewed `EvaluateHostedHolds` and `Summarize` implementations without opening
SQLite or reimplementing their confidence-bound calculation. Input is ONE
externally verified source/profile/recipe/population, with `source_sha`,
`profile` (`standard` or `legacy8`), `recipe`, `population`, and `hosts` containing
`HostHolds` records. Durations are integer nanoseconds. The adapter owns raw
joins and must verify pins, actual policy, independent host/profile evidence,
all offered attempts and original identities before forming this input. This
command does not supply those proofs from labels or counts.

Run separately for each command role and warm/steady/error population. Do not
pool profiles, hosts, command roles, source epochs or cold/warm populations.
Keep failed/refused commands and protocol errors in their host records even
when their accepted duration population is empty. Nonpassing results are still
emitted as JSON, with a nonzero process exit. The output is only the hosted
statistical hold gate; quiet maximum, error-path ceiling, maintenance service,
foreground budgets, WAL, memory and final qualification remain separate.
