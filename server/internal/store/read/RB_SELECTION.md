# Complete selection, rank, range and duration preparation

Status: unwired Tier B preparation over one complete admitted order. No product
path calls it, and it creates no command, durable receipt, scheduler or grant.
It is not a qualification result.

## What one completed walk supplies

`RBSelectionPreparation` walks a closed source order once (RB-02's two recipes:
library/kind entity ID or the dual title points) and retains every admitted
row's typed order key plus the maintained duration metric in integer
milliseconds. The finished result answers, inside one fresh authorized
publication snapshot:

- an exact 0-based range (`RBSelectionQuery.Start`/`Limit`) in the requested
  direction, including a far start;
- an exact entity rank (`AnchorID`) or key-run anchor (`AnchorKey`), mirrored
  for a descending order;
- a bounded, identity-deduplicated selection with per-entity ranks;
- duration aggregates for the range and the selection.

There is no OFFSET, no per-page re-walk and no hidden count scan. `Total` is the
exact admitted membership of the finished preparation, not a capped count.

## Semantics copied from the legacy browse implementation

- `catalog/browse_range.go`: a range start is a position in the ordered result;
  a deep position is never clamped to a lower-bound count.
- `catalog/browse_anchors.go`: an anchor names a position, and a key anchor is
  the beginning of that run (the first equal-or-next key in the requested
  direction). Equal folded title keys (`A`/`a`) resolve to the same run.
- `catalog/browse_selection_*.go`: selection is a set of displayed entities;
  duplicates collapse, an absent entity is reported, and an over-large set is
  refused instead of silently truncated (`rbSelectionMaxIDs`).
- `catalog/browse_counts.go`: the duration aggregate sums each included title
  once and is an explicit lower bound while any included title has no usable
  duration. A missing metric is never silently counted as zero milliseconds.
  Two limits apply until the task 09 wiring lands. First, this producer sums the
  retained row's own `duration_key`; for the container kinds (2 show, 3 season,
  6 album, 8 book) that is the container's own metric, not `browse_counts`'
  distinct visible-member sum, so the duration parity claim here covers
  non-container kinds only. Range, rank and selection membership are unaffected
  for every kind. Second, this producer rounds each row
  (`round(seconds*1000)`) before summing, while `browse_counts` sums seconds and
  rounds the total once, so a container-free aggregate can differ by less than
  one millisecond.

## Custody

The job uses the ONE governor preparation lease (`Domain 398`, disjoint from
RB-02's cache purpose), the original <=12s deadline, at most two content
restarts and the pinned authorization comparison from RB-01. Every window binds
`rbSelectionRaw+1` and refuses a longer answer; a short window is the only EOF.
Rejected (retired/age-restricted) rows consume the raw window and still advance
the private source cursor. Payload rows are charged before append; the title
recipe performs a same-snapshot octet preflight before copying keys.

The first authorized handoff linearizes with the governor. A later lookup with a
drifted content/security fence or an expired token/horizon is denied and
disposes the retained values; no bytes escape on error.

## Open

- Restricted-filter and membership sources from packet tasks 06 and 08-12/15
  attach later by supplying the same complete ordered result; this task does
  not invent their predicates.
- The plan golden `testdata/rb-selection-prepare-plans.json.gz` is recorded by
  the desk's capture run, not by the builder task.
