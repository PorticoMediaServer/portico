# RB group and relationship read drafts

Harness-only; no runtime registration, HTTP adapter, DDL, durable result copy,
provider call, or per-profile import/fanout. All definitions live in this family.
Current inventory: 62 definitions for ten source families and the relationship
exact fallback, with both statistics profiles captured on the actual Reader.

Base: `d09f554e0450a14bc3f7b54317877f9151ceb6cb` (fresh origin/main,
7 November 2026). Separate dependency: policy seam original
`cb0714d539d64761ced5b6ef96cdf631aea288ca`, cherry-picked locally as
`247e491af`; shared browse/publication files have no helper-authored changes.
No scalar, chronology, or lease dependency is included.

## Implemented callables

`BrowseReader.RBGroupWindow` requires `ConsumeForeground` and registered
`RBGroupDefinitions()`. It returns a bounded direct source window, not an exact
count/rank or a lifted ancestor-membership result. Private cursors bind the full
shared fence, policy recipe/subject witness, family, direction, output kind and
source keys. Initial and final policy checks prove the current subject; every
raw row separately checks library/kind/retirement and the common item predicate.

| Family | Physical stream | Public direct source order |
| --- | --- | --- |
| Show seasons | kind=3 partial index + show_id + numeric season + entity ID | Candidate only; specials-last rotation unfinished |
| Show episodes | show_id + entity ID | Candidate only; season number/specials order unfinished |
| Season episodes | season_id + episode number + entity ID | Exact direct order across accepted numbering modes |
| Artist albums | artist_id + entity ID | Candidate only; release type/year/title unfinished |
| Artist credited songs | artist credit + song ID, then current song point | Candidate only; appearances must merge/deduplicate with primary albums before release order |
| Album songs | album_id + COALESCE(disc,1) + COALESCE(track,0) + entity ID | Exact direct order |
| Author books | library + exact author name (NOCASE) + entity ID | Candidate only; series position/title/part projection unfinished |
| Book parts | book_id + COALESCE(disc,1) + COALESCE(part,0) + entity ID | Exact direct order |
| Collection members | collection + BINARY accepted order_key + item ID | Exact direct edge order; no ancestor lifting |
| Own playlist entries | playlist + BINARY accepted order_key + entry ID | Exact direct occurrence order; NULL entries consume raw work |

`SourceOrdered=false` explicitly identifies candidate streams. It must not be
converted into a ready groupOrder page by an adapter. The accepted hierarchy
invariants remain writer-owned (kind-compatible same-library source parents and
non-NULL episode numbers); this recipe adds no DDL or acceptance shortcut.

`BrowseReader.RBRelationshipExact` reconstructs album order for songs and artist
order for songs/albums from current parent points. It sorts only after complete
raw-source exhaustion. Equal parent keys merge by output entity ID across all
parents, rather than concatenating a parent-ID order. Both article keys and both
directions bind the cursor; folding matches SQLite's ASCII lower/NOCASE behavior.
Retired or wrong-library/kind required ancestors cannot contribute an order key.

The small complete-set fallback processes at most four 64-row quanta plus a
single-row exhaustion probe. More than 256 raw source rows returns ZERO payload
with `ErrNotReady`, even when all excess rows are ineligible. This is not a
bounded top-K approximation, a large-library completion algorithm, a retained
cache, or a claim of F-04 lease coverage. The root's exact manager must adopt a
larger source walk/merge under its own retained-memory and publication lease.

## Budgets and policy envelope

Direct source queries fetch at most 64 rows across up to three disjoint
lexicographic seek legs. Raw fetches include lookahead, retired members, foreign
library/kind positions and NULL playlist entries. Cursor progress precedes
eligibility; output is at most 40. A full 64-row window is never exhaustion; an
empty non-exhausted window keeps its raw continuation. No OFFSET, SQL sort,
whole-library COUNT, or unbounded join fanout is used.

The group recipe runs six-call common/subject fencing separately from its
business quantum, then repeats the six-call current fence before publication.
Author-name capture adds one point read in the first window. Each native scope stays below eight calls. Route SQL is 13..15, exceeding B-R04's
proposed10-call ceiling; this is qualification debt, not an exception or pass.
Relationship exact likewise carries all windows and shared fence cost in its
receipt, rather than reporting source SQL as route latency.

This draft supports the common unrestricted catalogue envelope only. Age
restrictions refuse even for requested leaves, because a subject proof is not
proof of all source/ancestor visibility. The common fence also refuses unsupported
label/account policies. Playlist access is local owner account+profile only;
shared/saved playlist/resource authorization is unfinished. No hidden child is
used to derive a count, duration, latest date, or other aggregate.

## Remaining semantics and integration queue

1. Full show order (specials last; seasonal/absolute/date groups and show settings),
   inherited season title/year order, and parent/target projection need exact
   reconstruction; raw show ID order deliberately does not stand in for them.
2. Artist release role (Album/EP/Single/Compilation/credited appearance), unknown
   year last and selected title ties need accepted provider-generic evidence
   joins plus merging/deduplication of the credited-song and primary-album streams. The old mb_* link tables are absent
   from the canonical schema; do not infer current publication from evidence alone.
3. Author series/nullable numeric position, standalone title order, deduplicated
   book-to-part projection, collection/playlist ancestor lifting and occurrence
   semantics require root exact preparation; no synthetic ROW_NUMBER SQL copied.
4. Large-library album/artist exact order needs a bounded hierarchical equal-key
   frontier or the root's full exact walk, with capacity fallback and F-04 lease.
5. Synthetic category/genre/series vocabulary, authorized membership/cardinality,
   context filters, totals, range/rank, generic cache/preparation, personal sorts
   and restricted-container eligibility remain root-owned integration work.

These are explicit unfinished semantics, not a completed SORT-KIND-21 claim.
There is no requested DDL delta and no publication-gate wait stopping this draft.

## Validation and qualification target

Go must be exactly 1.26.10. mini-1 runs compile and tiny grammar checks only; native fixtures, complete
traversals, actual Reader Capture and race controls run on standard GitHub runners.
`TestRBGroupPlans` verifies every present/empty cohort, unanalyzed/analyzed SEARCH
plans and mints fresh receipts for exactly the compiled definitions. A golden is
only a comparison artifact and never authority. It emits its prospective capture
into `PORTICO_LANE_EVIDENCE_DIR` for review/adoption. Final run pins and failures
are recorded in the parent handoff.

Qualification handoff: `BrowseReader.RBGroupWindow`, the ten family selectors,
first/far continuation and both directions; `BrowseReader.RBRelationshipExact`
small complete sets and truthful raw-cap refusals. QS/QL must additionally adapt
the root's large exact source walk on the canonical 1M seed, including equal and
prefix parent keys, sparse allowed rows, default disc/track/part, duplicate keys,
numbering overlaps, playlist occurrences, cancellation/revocation and source
reparent/rename between windows. B-R04/SORT-KIND-21 remain UNQUALIFIED; no runner
functional timing is a host/read-latency qualification.

The disc/track and disc/part continuation definitions pin exact SEARCH-only
`<expr>` EQP details as `UNRECOGNIZED EQP` declarations in their own definitions.
The generic HN recognizer accepts only plain-column constraint tokens. These
are explicit review items for the parent/owner; no SCAN, temporary sort, SQL
grammar or dynamic planner exception is granted. The actual Reader must match
the complete pinned detail in both statistics profiles.

## Runner history

All runs below use the lane-focused standard Ubuntu runner and Go 1.26.10;
none is performance qualification. Source pins are retained in their planner
and result artifacts. Failed runs are kept as evidence, not relabeled passes.

| Run | Source | Result / correction |
| --- | --- | --- |
| 42384700985 | abbreviated 413cf1dcf | Planner rejected abbreviated SHA as a branch; no tests ran |
| 42384726133 | 413cf1dcf | Literal partial-index predicate rejected; use exact unqualified conjunct |
| 42385171450 | 99f67f95e | ORDER BY expression rejected; subsequent tiny lint also found subquery/CASE; fixed aliases and static shapes |
| 42385563577 | 3ba61bbee | Generic recognizer rejected SEARCH expression token; exact SEARCH details pinned |
| 42385775948 | 01ddd64b5 | Same expression-token rejection in capture-only run |
| 42385967282 | 8eac5cd6c | Real temporary ORDER BY B-tree rejected; omit equality-fixed ordering prefix |
| 42386276442 | 6c982ed6e | Hydration primary-key metadata needed SQL aliases |
| 42386526593 | b319778be | All nine non-golden parents passed, both-profile Capture completed; artifact directory was absent |
| 42386783393 | 5cc2b50fa | Both profiles completed; expected absent-baseline failure; artifact adopted verbatim after review |

The original policy dependency and local cherry-pick have identical binary
patch SHA256 `0cfe2f4dc7021a3a2715a9cf1aee9f47bbba75e792ee4b374ec5ce8eea871246`.
Helper-authored commits change only this document, rb_group*.go and rb-group-*
fixtures. Functional successes establish the implemented subset only.

Initial capture adoption (before the credited-source extension): runner source `5cc2b50fa108c1286ff8f95eef9536b7ae3409a1`,
56 definitions, 112 present/empty plans per statistics profile, 152 SEARCH rows
per profile (304 total); zero SCAN or temporary-sort rows. Compressed artifact
SHA256 `98694abecfbd5bc560861b65b77a87737268f94058f4f655e9f0eedcd3c0cad4`;
uncompressed JSON SHA256
`9e66c369ce2954748b4afb9a2d32392ef7b98914cd7dfded507f6451b8c0ebf9`.
Article context is accepted for direct source streams, whose order is independent
of articles, and still binds their cursor. The final tests also exercise that
context change; the captured SQL definitions and seed are unchanged.

Credited appearances are a separate indexed candidate family, including songs
whose album has another primary artist. The primary-album stream can compose
with album songs, while the credit stream enumerates the appearance side;
neither list alone establishes an artist's complete release order. Runs
42387177369 (full read package) and 42387179474 (group race) on 05d9f8e9b were
superseded/canceled when this final source gap was added. Their final states
remain in the runner record; final validation uses the later 60-definition head.

Final capture refresh: source `c6f33d0956c7bb8af8be46fc2c20015b86a593e5`,
run 42387454387. All 11 non-golden parents passed, including credited appearances
and article-context continuation. The expected old-fixture SHA comparison failed
until the new artifact was reviewed/adopted. Both profiles contain 60 definitions,
120 plans and 168 SEARCH rows each (336 total), with zero SCAN/temporary sorts.
The credited reverse seeks explicitly constrain artist_id plus song_id for far
continuation. Current compressed SHA256
`85a21da6de275dd87dd2980372387caab82381958489585101414f5609083423`;
current JSON SHA256
`d01577ace4f28b7a3a8f52f68540776e374eb5ad3eb331ffec0844d909791f2c`.

The final show-season source uses canonical `catalog_entities_season_number`
with exact `kind=3`, avoiding raw episode work when enumerating seasons. Its
candidate order is numeric (0,1,2,10 in the fixture), not inherited title order
or the public specials-last group order. Per-item eligibility remains separate;
this structural kind index does not turn a subject proof into grid permission.
The prior 6599585fc full/race baseline predates this final access-shape change.
