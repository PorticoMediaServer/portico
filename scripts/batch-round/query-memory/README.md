# Query memory and large-playlist harness (P2-65 / O-042, G03+G04)

This directory drives two executable checks that the batch round lists as gaps:

- **G03** — a real playlist with >=100,000 real playable members: first, deep and last pages of the
  title, year, duration and own (position) orders, filter counts, the largest tie group, and
  concurrent reorder while readers page.
- **G04** — byte accounting on a real read-only query-churn run: prepared-read charges, facet and
  across-order cache retention, a warmed maintained count, restart/expiry semantics, and a
  schema/row/file inventory before and after.

The fixture and exercise live in `server/internal/catalog/scalebench/{playlist.go,query_memory.go}`.
Run from this repository, never as a production workload.

## Commands

```sh
# Plan only (default; no build, no fixture, no Go process)
scripts/batch-round/query-memory/run.sh --tiny
scripts/batch-round/query-memory/run.sh

# Tiny runtime rehearsal, mini-2 only, from ~/astra-scratch/P2-65/query
scripts/batch-round/query-memory/run.sh --tiny --execute

# 100k acceptance-sized fixture (>=100k playlist members), same lane, serial
scripts/batch-round/query-memory/run.sh --execute
```

`--execute` refuses mini-1, CI, load >= 16 or less than 15 GiB free. The fixture database is deleted
after the reports are copied to `--out` unless `--keep` is passed. Nothing else on the box is
touched and Docker is never used.

## What the reports mean

`playlist-report.json`

- `entries` and `seedSeconds` — the fixture size and build cost; `reusedFixture` says the playlist
  already existed.
- `sorts[]` — one row per measured display order: pages walked, `reachedLast`, first/deep/last page
  latency, whole-walk time, max VM steps and statements per page, hidden occurrences and stale
  restarts. A non-zero page budget makes `reachedLast=false` an honest bounded result, not a pass.
- `filters[]` — every published option for the open and movies-only viewers (the latter makes the
  non-movie occurrences hidden), with the complement check
  (`watched + unwatched <= all; hidden entries remain in all`, `inProgress <= unwatched`).
- `ties[]` — a complete walk of the largest tie group (all durations are equal in the reference
  fixture) and a year tie group; a duplicate or missing occurrence fails the checker.
- `concurrentReorder` — writes, reader pages, stale restarts, max write latency and the exact final
  order check against the applied swaps.

`query-memory-report.json`

- `preparedReadBytes` — exact charged bytes from `readprep.Store.Stats()` (entry, handle, delivery
  and payload bytes), `exact: true`, and bytes/entries after a fresh store replaces it. The budget is
  the store's own `MaxBytes()` (32 MiB by default).
- `facetPages[]` — cold and warm cost of each library/field facet. The retained-byte figure uses the
  same cost formula as the cache over the values the page returned; it is exact only when the whole
  list fits the published 200-value window and `truncated:false`, otherwise it is a lower bound and
  says so. A single list over 16 MiB is reported as a failure (the cache refuses it).
- `acrossOrder` / `maintainedCount` — cold/warm cost of a real across-library list and a Home row.
  A warmed cache must not repeat the grouped pass.
- `cacheStatsBefore/after/peak` — the six exact counters from the approved read-only
  `catalog.Service.QueryMemoryStats` observer (`FacetBytes`, `FacetEntries`, `FacetInFlight`,
  `AcrossBytes`, `AcrossEntries`, `CountEntries`), read before, during and after the churn.
  `cacheBudgets` records the compile-time 64 MiB/16 MiB/single-entry/entry-count contract those
  counters are checked against. `cacheCountersExact` is `true` only when the observer was read.
- `inventory` — schema object hashes, row counts of the catalog/personal/playlist tables, file sizes,
  page count. `noStoredQueryState: true` means query churn persisted no result, count, selection or
  scratch row.
- `heapSnapshots` — plateau evidence only. It is never offered as proof of a cache byte budget.

## Boundaries

- Tiny mode is a rehearsal, not acceptance. 1M/5M runs stay held; this harness does not bypass the
  existing 7 GB stop / 8 GB hard scratch cap (`checkScratchBudget`).
- The 100k fixture is real catalogue rows and real playlist entries, but it is not a full-server
  appdata installation and does not qualify load, devices, WAL or backup.
- The playlist entry rows are inserted in bounded writer batches with the create/append row shape;
  the public append mutation is capped at 200 entries per write. Reads, filters, counts, reorder and
  cursors go through the real `PlaylistContent`/`MutatePlaylist` paths.

## Rehearsal and limits

The100-item real playlist/query-memory rehearsal passes all seven full display
orders, both tie walks, open/movies-only filter counts, six concurrent reorders,
exact observer counters, prepared restart and no-persisted-query-state inventory.
The100k command includes a65s facet TTL wait and its verifier requires observed
recomputation. Tiny mode records that the TTL was not waited. The across single-
entry rejection and a direct continueCount cache-hit assertion remain separate
source/unit checks; this receipt does not claim they ran at scale. No100k test
has been executed. The approved observer has focused charged-byte/eviction/
oversized-entry/concurrent-writer tests. Core/Actions can run the printed Go
commands directly in their private scratch; run.sh placement remains mini-2.
