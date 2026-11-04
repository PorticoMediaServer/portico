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
container items. Two shared credits per entity, one collection membership; a few
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
