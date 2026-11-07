# Home read drafts

Harness only. Base `d09f554e0450a14bc3f7b54317877f9151ceb6cb`.
Chronology dependency: original `8031ab71c321ebc1280e1233957cccdefbfeef65`,
local separate commit `b812df84a`. Policy seam: original
`cb0714d539d64761ced5b6ef96cdf631aea288ca`, local separate commit `390c4dbbb`.
No shared files, DDL, runtime routes, providers or production composition changed.

## Callable behavior

`RBHomeDefinitions` composes the existing browse/foreground/chronology definitions
and eight Home statements. Capture is required on the actual Reader before registry
construction. `TestRBHomePlans` records unanalyzed and analyzed, present and empty,
SEARCH-only plans. `-record-rb-home-plans` deliberately adopts the own-file golden.

All public methods require `ConsumeForeground` LIVE/output custody and use
`publishRead`. Draft errors return zero payload. The new closed Home recipe
permits container business verification without weakening the default policy.
Unsupported account policies and blocked labels still fail in the shared fence.

* `HomeRecentTarget`: movie leaf, show, or season navigation from a complete
  bounded currently permitted/available leaf set. The newest eligible episode's
  cluster uses successive gaps of at most one hour. One episode navigates to
  that episode; multiple episodes in one season navigate to the current season;
  multiple seasons navigate to the work. The work identity stays stable.
* `HomeRecentRow`: exact cross-stream merge for an explicitly selected small
  movie/show source set. At most three distinct libraries and eight kind streams;
  each raw chronology stream must exhaust below64 rows, and the combined private
  candidate set must fit252. All eligible cards are composed before selecting
  the newest100. Restricted show order uses eligible child arrival dates only;
  unrestricted show order uses accepted monotonic content-added chronology.
  Undated works sort last; existing encoded public work IDs break equal chronology ties (base64url lexical
  order, which differs from BLOB order).
* `HomeActivityWindow`: current own unfinished movie/episode/song/audiobook-file
  source windows for `continue`, `listen_next`, and `continue_books`. Reuses
  `foreground.continue.*`, `personal_current_activity`, and `stateValue`.
  Composes movie/show/album/book work and actual resume leaf; checks current
  dismissal, completion, millisecond unit and available version duration.
  Audiobooks require the current book state's resume leaf to match the part.
  This window intentionally does not claim full Home-family completion.
* `HomeLeafWindow`: fenced show/season member candidates with raw ID keysets,
  including an explicit nonexhausted continuation through arbitrary denied
  prefixes. The continuation binds the work's exact root and library source
  envelope. These are content-permitted candidates; availability and final
  target/visible chronology reduction still must complete in preparation.

Assets and leaf membership use bounded joins, not correlated subqueries or a
copied Home table. Inventory acceptance is proved on the same object/source:
nonretired available object, no unsupported reason, enabled source in the same
library, matching incarnation, and health outside root_changed/removing. SQL
returns integer NULLIF/length comparisons for potentially wide reason strings;
it never fetches reason text, object paths, evidence JSON or Reading locators.
DVR retirement excludes a leaf. In a library without managed sources, accepted
link/asset availability is used. A managed library with an unbound legacy asset
fails closed: source-root/path fallback eligibility remains unimplemented.

Initial and final exact source roots include every selected library's actual
root, revision, viewer, admission and source lifecycle envelope. The token, not a
caller profile ID, determines secondary viewer revisions. Source enable/health/
incarnation changes are compared directly, including unversioned changes.
Each library envelope must exhaust below64 rows. Library/profile sharing is
validated for each selected library. No native rows survive a quantum.

## Bounds and incomplete work

The joined member window must exhaust below64 raw rows, including duplicate
versions and inventory objects. A full window returns `ErrUnqualified`; no
partial target or shelf escapes. Thus large shows currently require a subsequent
resumable `(member,asset,object)` traversal. A bounded asset existence proof may
stop after finding a valid version; duration aggregation requires full asset
window exhaustion. An unsuccessful full asset probe always fails closed.

Activity examines at most64 raw personal positions, advances past missing,
foreign-library/kind and denied leaves, and returns a fenced continuation while
work remains. A successful empty nonexhausted window is pending source work.
No arbitrary history cap changes reachability. Output candidates are at most40.
Activity windows do not deduplicate one work across pages; exact whole-row
grouping belongs to preparation, not this sparse source cursor.
Activity source capture and candidate composition also share one copy counter,
deadline and whole-operation retry. Only actual candidates create target windows;
each target window has at most five business calls and67 raw rows. Continuations
also bind the complete source lifecycle envelope.

The recent row uses two private publication stages: source capture and a
cardinality-sized target stage (at most four movie or two show targets per
snapshot, eight business calls and260 raw joined rows). Both share one cumulative copy counter, one
original deadline and at most one whole-composition retry. The second stage
compares the complete first stage's shared/source fence before business work;
final publication rechecks it again. No spare empty snapshots are executed.
Source root copies and ordinary seven-column title reads follow the merged
reader's memory admission. No new wide display/Reading hydration is implemented.
Extending wide root/card hydration needs the closed actual-octet preflight
registration; no columns-times-maximum reservation is introduced here.

CHANGE-011 embed#1373 was published but unmerged at the latest SUP2 steer.
New captures bind to the actual merged schema at run time; parent owns the
post-merge chronology/scalar/random recaptures. Their goldens are not edited here.

## Complete row-family inventory and remaining queue

| Family | Draft/support | Remaining semantics/owner |
|---|---|---|
| new_on_server | Small fully exhausted entity-work shelf, chronology/targets, exact merge/cap | Large/sparse streaming frontier (finish equal-time wire-ID ties) and exact preparation; movie provider-edition equivalence/canonical representative; complete art/DTO projection |
| recent_* / Discover recently added | Chronology primitive and movie/show target proof reusable | Album/book/Reading source chronology, screen-specific work folding and descriptor settings |
| continue | Own unfinished movie/episode source and show navigation | On-deck after completion, season/show clearing/dismissal, per-library cutoffs/premieres/maximum, removed-title cards, full work grouping |
| listen_next | Own unfinished song source and album navigation | Album completion/next-track continuation, exact grouping, recent-album fallback; current songs normally retain no playback position |
| continue_books | Own audiobook part, current book resume state and book navigation | Native ebook/comic accepted publication/locator eligibility (RS/MP/UO), cross-format merging/grouping and Reading presentation |
| because_you_watched | Unsupported | RS recommendation candidates/explanations; source freshness and row projection |
| recommended | Unsupported | RS candidates and ranking |
| recommendation family / rec:* | Unsupported | RS generators, stable family identities and candidate order |
| trending_now | Unsupported | RS accepted trend/recommendation inputs |
| community_watching | Unsupported | Current accepted sharing/privacy and own/community activity policy (UO/RS), no inferred permission |
| featured_collection | Unsupported | RS collection candidates; complete allowed-member proof and collection presentation |
| on_now | Unsupported | MG immutable guide/channel/programme facts and current permissions |
| configured view/saved rows | Unsupported | Parent browse/filter/preparation plus current saved-resource authorization/settings |
| suggestions, manifest/layout/descriptors | Unsupported | Current layout/settings/library projection and family capability selection; no successful fake empty unsupported row |

The row methods return core identities/state, not API Home DTO parity. Caller
selection of source streams is trusted harness composition, not a new route or
replacement Home manifest. No exact cache/preparation lease design is owned here.
No persistent profile catalogue, per-user import fanout or durable Home cache.

## Validation

Go1.26.10 exactly. Tiny local compile, SEARCH-only Capture and target projection
checks only. Full read/race controls run through `lane-focused.yml` on the own
helper source branch; run/source receipts are reported separately. No1M, B-R02,
physical-host or performance qualification is claimed.
