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
