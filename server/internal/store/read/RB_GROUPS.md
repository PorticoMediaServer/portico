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
| Season episodes | season_id + episode number + entity ID | Candidate only; public numbering/settings order unfinished |
| Artist albums | artist_id + entity ID | Candidate only; release type/year/title unfinished |
| Artist credited songs | artist credit + song ID, then current song point | Candidate only; appearances must merge/deduplicate with primary albums before release order |
| Album songs | album_id + COALESCE(disc,1) + COALESCE(track,0) + entity ID | Exact direct order |
| Author books | library + exact author name (NOCASE) + entity ID | Candidate only; series position/title/part projection unfinished |
| Book parts | book_id + COALESCE(disc,1) + COALESCE(part,0) + entity ID | Exact direct order |
| Collection members | collection + BINARY accepted order_key + item ID | Candidate edge order; ancestor lifting unfinished |
| Own playlist entries | playlist + BINARY accepted order_key + entry ID | Candidate occurrence order; lifting/dedup unfinished; NULL entries consume raw work |

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

## Integration evidence

Initial source head acd2fdae passed the full read package (96 parents, run42390102133) and focused group race (12 parents, run42390104357). The parent rebuilt on CHANGE-011 without the separate policy dependency commit. Runner42394524324 passes traversal and retained-byte refusal; its Capture exports the new schema/fixture provenance and differs only in environment. All62 manifests and both124-plan READ VM/EQP arrays are raw-exact against the initial capture, with172 SEARCH rows per statistics profile. The adopted baseline changes no SQL proof.

Season, collection and playlist windows report `SourceOrdered=false` until settings/lifting/dedup semantics are complete. Complete-set relationship retention charges actual title/key/fence lengths and copy/vector overlap before each append. A focused native control proves independent source quanta fit while their cumulative retained prefix exceeds output custody and refuses with zero output. Fixture provenance includes the shared browse seed and group journal.
