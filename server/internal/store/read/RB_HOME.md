# Home read drafts

Harness only, built on the merged chronology and policy-recipe seams.
Own Home files only; no DDL, runtime routes, providers or product composition.

## Callable behavior

`RBHomeDefinitions` composes the existing browse/foreground/chronology definitions
and nine Home statements. Capture is required on the actual Reader before registry
construction. `TestRBHomePlans` records unanalyzed and analyzed, present and empty,
SEARCH-only plans. `-record-rb-home-plans` deliberately adopts the own-file golden.

All public methods require `ConsumeForeground` LIVE/output custody. The shared
`publishRead`/`publishAttempt` protocol supplies the physical lookup and fresh
publication fences; dynamic two-stage methods retain its one whole-operation
retry, original deadline and shared copy counter across both stages. Draft errors return zero payload. The new closed Home recipe
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
DVR retirement excludes a leaf. Published DVR provenance fails closed until
MG supplies the current recording/sharing policy; it is not treated as an ordinary
permitted catalogue item. In a library without managed sources, accepted
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
cardinality-sized target stage (at most eight movie points or two show targets
plus one movie per snapshot, eight business calls and512 joined raw rows; lower
caller row budgets still refuse before their own ceiling). Both share one cumulative copy counter, one
original deadline and at most one whole-composition retry. The second stage
compares the complete first stage's shared/source fence before business work;
final publication rechecks it again. No spare empty snapshots are executed.
Source root copies and ordinary seven-column title reads follow the merged
reader's memory admission. No new wide display/Reading hydration is implemented.
Extending wide root/card hydration needs the closed actual-octet preflight
registration; no columns-times-maximum reservation is introduced here.

Current native Capture must bind the actual embedded schema and source journal.
Functional controls use virtual time; standard hosted race and Capture evidence
are retained separately from physical-host qualification. This draft does not
qualify large exact Home preparation or complete the remaining Home families.
