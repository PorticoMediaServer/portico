# Temporary P0-Q search research

Never merge this branch. Product source is read-only. Go module imports production
facettext normalization and the public server's pinned modernc SQLite v1.58.0.

Variants share native own-title prefix lengths 1–12 (six ASCII hex digits per
Unicode scalar), lexical and trigram subject indexes. Inherited parent titles use
the same own-title index and direct parent links in both variants. A indexes and
queries context prefixes 1–12; B indexes context prefixes 3–12 and deliberately
does not expand <3 context matches. Native FTS itself would still find those short
matches by a vocabulary range scan: B's missing matches come from dispatch.

Corpus: deterministic 15% movie, 45% TV, 30% music, 10% books. TV/music include
container items. Episodes inherit show titles; albums/songs inherit artist/album
titles. Credits and collections remain scoped to their direct entity. Two shared credits per entity, one collection membership; a few
explicit quality witnesses add one bounded edge. Shared people are N/50 (minimum
128); collections N/500. Titles contain three dictionary words and a unique
number. 1/47 are CJK and 1/37 start with 1984. This is synthetic cost evidence,
not a representative real metadata distribution. All recipes fit production
credit/alias caps; there is no general production recipe-cap verifier, provider
acceptance, full-text Reading, People-group, cursor, governor or HTTP stack here.

Query timings include building verified synthetic per-term sets, distinct counts,
rarest lead, mixed-context intersection, restriction filtering and top-41 rank.
Fallback materializes per-subject BM25 scores and weighted eligible best scores;
production public-ID ties are represented by monotonic synthetic IDs. No 600ms
cutoff is imposed: slow successful SQL is recorded as a failed runtime budget.
16 timed repetitions after one warmup per query; p95 is the maximum with n=16.
Concurrent-query events cover the ingest/rename phase on a separate connection.

Eight-item FULL/WAL build transactions; automatic native merging is active
(automerge 2, crisismerge 16, usermerge 2). A read-only, engine-pinned structure
record decoder monitors every level and admits explicit merge(1) when observed
same-level segments reach 4, stopping further production while repaying. This
post-transaction observation does not prove prospective reserved headroom.
Churn: 2,048 one-item ingest + one own-title rename + one shared-person rename
transactions, with maintenance and a concurrent reader; 256 merge(1) calls per
index drain afterward. 16 maximum-size subject updates (tokens prepared before writer entry) then stress native hold;
one merge+rollback per index is recorded. No optimize/rebuild/VACUUM is used.

Debt evidence includes all-level segments/in-progress merges/pages, operation
latency, tombstone-operation count, WAL peak between churn checkpoints, and
before/after allocations. Tombstone-operation count is offered deletion work,
not a count of physically unreclaimed postings. Internal merge CPU/IO and native
cancellation are not fully instrumented. Statement and full-transaction timing
include actual physical completion; no context cancellation is assumed to free
resources. Query EXPLAIN output is emitted with SQL/binds.

Standard profile is the actual ubuntu-24.04 hosted runner, recorded per job.
Old8g profile uses that same runner with an 8 GiB no-swap cgroup and 2 CPU quota
across 4 vCPUs; it is a resource-envelope approximation, not a calibrated 2015 i5.
The benchmark has no media-file IO; HDD media performance is not tested. Database
storage remains runner SSD. The workflow removes disposable databases and uploads
only JSONL and environment/build evidence, retained for 30 days.

## Candidate-schema / C / native-policy experiments

`schema-v1/p0-q-tuning` embeds the exact full candidate SCHEMA.sql, SHA256
`d07d13b0146246d28e5122c85b0880f48f9d95addded5bab19c3ffe91f18a2f0`.
All candidate tables/indexes load unchanged, including search_own,
search_fragments, search_revision and all four canonical FTS declarations.
Native experiments hold the A schema constant. Fixtures use auxiliary q_items,
rel/edges and own/fragments adapter views; foreign-key checks are OFF because
non-search domain parents are not populated. UNIQUE/CHECK/STRICT and exact
external-content ownership remain active. This isolates native FTS/source costs;
it does not qualify full typed parent/FK/revision/receipt acceptance. Every result
records the full candidate SHA and explicit configuration.

Original reduced prototype differences: renamed own/fragments/FTS objects,
ratings/title/kind/library fixture columns, one signed owner rather than separate
entity/person ownership, no ordinal/source revision/uniqueness/FKs/global head,
and synthetic edges. All four FTS tokenizers/content mode/column sets/prefix
lengths in A were logically equivalent. B changed only context prefix lengths
and dispatch. Empty independent People/Reading indexes now exist in the full
candidate but remain outside the measured four-index workload.

C runs B's short-input title-first dispatch on the A index, then computes a
separate full A context population excluding the complete title population.
Count/ID witnesses verify A = C_titles disjoint-union C_context, including al,
jo and harbor ad. Context pages are bounded, but the candidate set is not
truncated. First-response and deferred-completion latency are separate; total
completion is not called fast. C needs no frozen-schema change. Production
cursor/fence/pending-group/total semantics and implementation of resumable
context pagination remain runtime/API build gates. The lab uses one stable read
snapshot across both parts to test the partition, not a proposed long-lived
production snapshot. Diagnostic context time includes set-count/witness checks.

Policy matrix at100k on both profiles: 2/16 auto/crisis, batch8, FULL baseline;
auto0 batch8; auto0 batch1; auto0 batch1 NORMAL (durability diagnostic only);
auto0 batch1 crisis8; auto0 batch1 merge4. All use governed explicit merges and
same-level ceiling4 observation; no silent unbounded debt or optimize. Slow
native calls emit start/end timestamps. Separate strace cohorts record only
fsync/fdatasync calls to distinguish sync wait from engine work; traced wall
latencies are labeled instrumented, never substituted for untraced results.

The bounded C experiment (`schema-v1/p0-q-cfast`) returns a top41 own-title page
from exact-title covering seeks plus rowid-ordered FTS title-start/title-word
streams. It avoids computing full title populations before first response.
Inherited title matches remain reachable in the deferred complete A set. It
changes title-group rank policy and requires the runtime continuation/fence/
pending-total design above; it does not change any candidate table/index.

The extended native-policy run raises only the research repayment-attempt limit
512→32768; same-level admission ceiling4, engine settings and offered data are
unchanged. It records progress while publication remains paused and reports
physical WAL bytes before every checkpoint. This distinguishes a diagnostic
loop limit from non-repaying debt. No successful native-write subset qualifies
the private connection's long TRUNCATE checkpoint calls or production FK/custody
acceptance. New candidate280f03e6 has ten byte-identical search-family statements
to the executed d07d13 schema; native search results apply to that family.

## R14 and the durability ruling

`schema-v1/p0-q-passive` executes candidate09a97242b681fe51077bb109fe2c1d28a0eb2cdd7fccfed0a661c3d9115782b6.
Prior d07d13/280f search-family results are historical controls, not direct
qualification: search_own folded into catalog_entities, exact index renamed and
FTS external content remapped to catalog_entities/id. New fixtures populate the
actual wide entity (640-byte synopsis, mandatory identity fields and all its
indexes) and use only an auxiliary rating table. FK parent domains remain outside
this isolated corpus; all row CHECK/UNIQUE/STRICT/index work is real. Old and new
SHAs are recorded, and every event carries the executed candidate SHA.

Main background/user-state class: WAL+NORMAL, auto0/batch8/merge1, writer
wal_autocheckpoint0. One separate PASSIVE connection gets a fake governor grant
every50ms (also250/1000ms controls), max one in flight. No TRUNCATE is issued in
these cases. A read-only pinned wal-index header probe reports committed frames,
backfill and physical file size. Publication pauses outside the transaction at
64MiB uncheckpointed debt or96MiB active-prefix pressure, permitting reset trials
after full backfill, with a16MiB reserve below192MiB hard stop. Unknown/incoherent
header state is charged/waits; it is not zero. Physical/active-prefix sizes differ
because PASSIVE does not truncate. This is a benchmark governor, not G integration.

Each checkpoint records duration/busy/frames plus writer and reader completions
while in flight. Commit latency is split into checkpoint-overlap and idle cohorts.
Slow native calls record locked-thread CPU time and Linux schedstat runqueue wait;
/proc/stat steal and effective cache_size/cache_spill are retained. Thread CPU
includes engine and runtime CPU; unclassified non-CPU wait is not assumed engine.

Mixed cases keep one writer. Seeded Poisson security arrivals have mean60s and
switch to FULL only for a tiny committed security row, then back to NORMAL.
Progress arrivals mean1s (10 streams updating every10s), use NORMAL. Background
build is followed by at least600s continuous ingest/rename offered work; after
2048 additions size remains bounded and renames continue. At high WAL pressure,
foreground service is retained while background publication waits. Foreground
FULL commit/transaction/queue/response timing and precommit WAL committed-prefix
and uncheckpointed bytes are recorded. These bytes bound the prefix carried;
they are not a VFS-exact unsynced-background-byte oracle. FULL still syncs the WAL
prefix, so security latency is not inferred from small row size. Few real-rate
security samples mean p99 is the observed maximum, not a high-confidence tail.
