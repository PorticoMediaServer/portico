# Foreground read qualification recipes

This package is selected explicitly by tests and qualification harnesses. No
shipped route uses it. `HarnessDefinitions` returns inert compiled definitions;
the harness must obtain a fresh `plan.Capture` receipt on its actual Reader
configuration and fixture, then compose `stmt.New`, `governor.NewSnapshots`,
`NewBrowseReader` or `NewForegroundReader`. Stored compressed goldens are
comparison baselines, never execution authority.

Trusted composition provides a funded `governor.Config.Foreground`, the paired
Governor/Authority from `governor.New`, and the ORIGINAL `ForegroundRequest`
key/owner/ready/protected deadlines. Pure identity checks reject a mismatched
governor/authority/pool before admission; public reads must use that exact pool.
Capture `BrowseInput.Now` from the trusted
server clock at request entry, before admission; the public reader advances it
through admission and preparation. No default profile or runtime route is chosen.

Use `ConsumeForeground` around exactly one public reader call and all of its
output consumers. It calls #1119's `AdmitForeground` before the separate8MiB
output grant and before invoking the consumer callback. Read grants remain12MiB
per physical window; LIVE charges bookkeeping only, never another20MiB payload.
Its opaque scope is required by `Window`, `Detail`, `Playback` and
`ContinueWatching`; hand-built, foreign, altered or retired output cannot bypass
admission. The scope binds exact issuer/key/owner and both original deadlines.

```go
consumption, err := read.ConsumeForeground(ctx, g, authority, snapshots, liveRequest,
    outputRequest, func(ctx context.Context, output read.BrowseOutput) error {
        page, cost, err := reader.Window(ctx, readRequest, readyBudget, output, input)
        if err != nil { return err }
        // Consume/encode/write here and JOIN all owned consumers before return.
        return encodeAndWrite(ctx, page, cost)
    })
```

The output purpose is a memory-only8MiB reservation; its owner matches the LIVE
request, and its deadline cannot exceed the original protected deadline. Reader
purposes bind that LIVE key/owner, Foreground class and the unchanged12MiB recipe.
Admission waiting, output preparation and retries cannot renew the ready budget.
Read cancellation also follows the ORIGINAL context even if a caller supplies a
new context. Fresh bounded security validation still preserves denial precedence.

The synchronous callback owns encoding/client IO and MUST join its consumers and
release page references before returning. A scope cannot be reused for a second
payload or retained for later work. The driver also joins any already-active read
and its cancellation callback. It retires output, then LIVE, after callback and
owned-work completion, including error/cancel/expiry/panic paths. Cancellation,
expiry, native-window return and a slow consumer never refund LIVE early. If
output retirement is uncertain, LIVE stays charged. Consumer panics retain their
original value through cleanup. No goroutine or payload reference may escape the
callback; any separately retained cache copy requires its own custody.

Baseline fence/statement tests exercise the private read kernels. Separate
`TestLive*` controls exercise the public paths with real governor admission,
separate output/read grants and actual retained-consumer boundaries.

The shared publication helper performs the full exact shared fence first and
in a fresh final window. Composite reads capture root/asset/link/probe source
values in the first window and compare them at final publication. The shared
fence also retains both library roots and the full library origin incarnation
and revision. Middle windows
run business reads only, with no shared or item/source re-fencing. Each window has
at most eight registered calls and uses the original operation deadline. One
whole-operation retry is allowed for source/revision drift; a second drift gives
`ErrStale` plus typed governor Conflict refusal and zero output. Every failure,
current denial or cancellation returns a zero payload. Raw copied values across
all windows and retries are conservatively bounded against retained custody.

Callable recipes:

- `Detail`: direct-child ID keysets, effective locked/matched artwork per role,
  accepted sealed credit-image rows (independent SQL LIMIT100 cast/LIMIT50 crew
  reads, each with its own ordinal continuation for additional manual credits), up to16 version descriptors and
  optional selected-file streams/chapters. A child or credit tail is explicit,
  including empty non-exhausted windows. Children are direct relations, not an
  eager recursive hierarchy or final product-specific episode ordering.
- `Playback`: selected same-library asset/link, current file path and origin
  witnesses, size/mtime-matched ready probe facts, up to255 streams per type and
  512 chapters. This is a lookup, not a durable start, media capability, complete
  playback options policy, or first-frame measurement.
- `ContinueWatching`: leaf resume candidates from the current profile's own
  `personal_items` activity stream. It reads at most64 raw positions per turn,
  advancing over denied, retired, completed and other-library/kind candidates.
  Ties use equality-tail then next timestamp with the exact index order. This is
  the leaf recipe; container/on-deck projection and retained-merge resolution
  remain separate qualification/cutover work. No write or user fan-out occurs.

The policy envelope is inherited from browse: local principal, supported kinds,
current library age summary, age restrictions on leaves, empty blocked-label
policy, and no account access policy. Unsupported policies/restricted containers
fail closed. These recipes do not qualify missing authorization predicates.

## Original-family permission fences

Every successful foreground operation below executes `read.browse.principal` in its initial
and fresh final shared fence. The statement requires exact
`f.command_member_incarnation=m.incarnation` and
`f.command_profile_lifetime=p.lifetime`, in addition to its existing global
generation, account epoch, current membership/profile/device, credential and
policy checks. Recreated equal IDs do not revive an older family. The distinct
family `command_incarnation` identifies commands and cannot replace either
original birth witness. There is no fallback or execute-time rebasing.

| Public entry point | Shared permission path | Principal statement ID |
| --- | --- | --- |
| `BrowseReader.Window` | `publishRead` → `readBrowseFence` | `read.browse.principal` |
| `ForegroundReader.Detail` | `publishRead` → `readBrowseFence` | `read.browse.principal` |
| `ForegroundReader.Playback` (start-path lookup, not a start issuer) | `publishRead` → `readBrowseFence` | `read.browse.principal` |
| `ForegroundReader.ContinueWatching` | `publishRead` → `readBrowseFence` | `read.browse.principal` |
| `SearchDispatcher.Consume` (all query modes) | `permissionFence` → `readBrowseFence` | `read.browse.principal` |

Search dispatch fences independently of its optional title executor, including
empty/pending/unavailable responses. Its fresh final fence follows prepare-only
encoding and precedes the publisher. A callback promise, LIVE admission or
`SearchRequests.Current` is not authorization. A denial suppresses publication,
including one observed after cancellation. Other exact fence drift returns a
typed Conflict refusal; dispatch does not retry an executor/consumer callback.

The dispatcher's own successful checks add **10 SQL calls / two serial windows**
to any executor's work. `SearchDispatchResult.Read` contains all those windows
and exact counts even without a native executor, plus any returned executor
evidence. Each fence-only window has five calls; read/output/LIVE grants and
ORIGINAL deadlines are unchanged. Whole search cost, maximum-body memory and
ready latency remain **UNQUALIFIED**, and no native title executor is installed.

Exact normal counts (including shared/source fences and final validation):

| Recipe | Statements | Physical snapshots |
| --- | ---: | ---: |
| Browse |11–12|2|
| Detail without selected file |19|3|
| Detail with selected file |26|4|
| Playback lookup |19|3|
| Continue watching |11–12|2|

When a credit class fills its SQL cap, its exhaustion remains unknown until a
subsequent window confirms the tail; no extra credit row is fetched as lookahead.
If the Acting label is absent, the bounded crew-only variant covers all rows.
That path uses one fewer statement.

A complete retry may double a recipe's work; all attempts/windows and statement
counts are returned in `BrowseReadResult`. `BrowseReadResult.Elapsed` includes admission, preparation, read windows and
physical cleanup; `ForegroundConsumption.Elapsed` additionally includes encoding,
consumer completion and final retirement; browse's `LookupElapsed` remains only its business lookup. Failed,
stale or refused operations must not be counted as fast successes.

These counts and ready/VM/memory targets are **UNQUALIFIED budget debt**. QL owns
paired qualification against B-READS: B-R09 detail, B-R13/B-R14 playback, and
B-R02 for continue watching. The12MiB read/8MiB retained-output recipe and funded test profile are prospective
custody, not a2GB fit or maximum-body proof. The test
Reader's64KiB native value/row limit can refuse larger legal descriptions rather
than truncate them; that legal-maximum/decoder composition needs qualification.
No production, hosted or reference-host run is implied by local tests.

`SearchDispatcher` is an unwired lifecycle harness using `search.PlanQuery` and
the separately funded `SearchRequests` metadata group. A valid replacement
cancels the older session request; its metadata and LIVE/output custody remain
charged through actual completion. Invalid queries do not replace valid work.
The same shared `ConsumeForeground` admits LIVE before payload preparation.
The draft title executor receives only the bounded view and normalized query,
with the original ready/read deadlines. Its page is copied within the output
grant: at most20 titles, each at most64KiB. These are harness recipe bounds,
not a new governor quota or qualified memory/ready result.

The consumer first prepares encoding without publishing, then returns a joined
publisher. The driver checks current generation, cancellation and grants again
after preparation, immediately before publishing. Both phases stay inside the
same LIVE/output lifetime. Generation must survive transport for ordering of
responses already in flight; supersession cannot retract delivered bytes.

No native title executor is installed here. Missing sources return typed
unavailable; sources may return typed pending without partial items. A future native source must use the same pool's public LIVE
read and accepted authority/bank/profile/source plus HN fixed-slot attestation.
The synthetic title controls prove dispatch/custody only, not that native seam.
No runtime route or full-search execution is enabled.

## Full-search components

`NewFullSearchDispatcher` adds a separate optional `FullSearchRead` callback for
full and explicit show-more queries. The original constructor remains valid;
an absent callback returns typed unavailable, with no title-only approximation.
Both paths retain the dispatcher's initial/final principal fences, ORIGINAL
deadlines, joined consumer and separate LIVE/read/output custody. Callback cost
is added to those ten fence statements, never substituted for route cost.

`FullSearchDefinitions` is an inert catalogue of fourteen ordinary MAIN reads:
bounded person-credit, artist-album, song-artist, show-child and authored-member
reverse pages, plus physical logical-field sibling pages. Actual Capture
goldens cover all definitions in present/empty and analyzed/unanalyzed cohorts.
Each page charges at most64 raw positions before eligibility filtering. Person
credit continuations cover image, ordinal and entity tails independently.
These raw relations do not certify visibility, accepted heads or automatic
collection membership. The draft album ID-order index is still required.

Private proof reducers verify complete logical fields, exact distinct-term
populations, mixed work/owner conjunctions and canonical source/route winners.
Title tiers precede foreign owner-name-only tier5; entity/person identities stay
disjoint. Native rowid order is not logical-source order: canonical-first
posting proofs must cover all five literal slots before a population is exact.
The bounded top-K retains at most20 results; incomplete proof/frontier returns
pending with no partial page. CJK siblings reuse `search.CJKFieldMatcher`.

Searchable text is the first **64 KiB of normalized UTF-8 per logical field**,
ending before a partial scalar. Primary-title folding, lexical/prefix/People
postings, normalized fragments, CJK confirmation, logical counts and ranking
all use this same prefix. A physical overlap does not reset the field byte
position. Stored/display values and raw-source admission/custody are unchanged.
Text outside that search prefix cannot produce a match or a confirmation; no
census/SCAN fallback is used. Preparation-source identity changes make old banks
stale under the existing profile check and governed rebuild protocol.


`NativeFullSearchReader.Read` is the unwired native adapter: one LIVE operation
returns one global page across kinds (`View.Kind == 0`), with kind-bearing hits.
Its inert `NativeFullSearchDefinitions` must all receive actual Reader Capture
receipts. The merged fixed-slot READ capability supplies native rowid candidates,
not logical populations, publisher eligibility or permission.

Native search selects per-hit eligibility from the existing compiled
`readPolicyRecipe` registry. A non-default domain must opt in with
`nativeSearch: true`; at most one such recipe may own a kind. The private binding
uses the actual source ID and kind, never the request's policy selector. The
same validator runs in candidate and fresh final windows, comparing the exact
bounded witness and source birth/revision/age. Catalogue fallback keeps restricted
containers Unqualified; Reading stays refused until its domain opts in. A
container's own age cannot replace its complete visible-member proof. Every
future domain must keep its eligibility inputs within the shared source/revision
fence used by native continuations; opting in is not a writer or epoch proof. Domain validators may use fully qualified `read.*` statement IDs (or
`rs.*` names); trusted composition must include their inert definitions in the
SAME actual Capture/closed read registry. The opt-in adds no executable SQL.

Hits additionally carry canonical `SortKey`, `Year` and `AddedAt` for RS grouping.
Entities use the same current source row's `sort_key`, `year` and `added_at`;
People use `sort_name`, year0 and empty added time. Empty `AddedAt` means SQL NULL.

Every validated native page, including exhausted pages with no cursor, carries
an opaque `Stamp`. Its `MetadataBytes()` reports owned-field bytes, capped at640KiB;
`RetainHits(hits)` can narrow issued identity/value proofs but cannot add them.
Prospective old/new metadata overlap is charged inside output8/RB custody and
the existing2MiB copy ceiling, never through another grant. When retaining hits across polls, trusted composition supplies
prior stamps in `FullSearchInput.Retained` (at most128 stamps/128 distinct retained
source-policy witnesses), including hits from several libraries. Library IDs
come only from opaque issued evidence; this SAME native operation revalidates
each library fence and its per-hit proofs in both windows. An empty
`RetainHits(nil)` anchor keeps its original own source-library fence. Narrowing
may drop discarded-hit libraries, but never the stamp's own source scope. Inside its still-live Consume callback, current
`Stamp.ValidateRetained(ctx, out, prior)` verifies that the prior witnesses were
actually rechecked. A prior stamp alone, including one omitted from the current
input, grants no disclosure. RS separately owns retained payload/source-write
custody and the shared final fence after response preparation.

The prototype declares raw4096/read12MiB/output8MiB and finite forecasted calls,
with a cumulative2MiB copy bound plus bounded proof workspace. Final windows do
permission/source checks only, never rerun MATCH/ranking. All statements, calls
and both windows count toward the complete route; these bounds are not a ready
or physical-fit qualification. The rank cursor is opaque harness state, not an
authenticated wire cursor. No INSERT receipt is READ authority, no publisher or
runtime route is installed, and actual complete native proof/Capture controls,
whole-route cost, maximum-body memory and ready/physical fit remain **UNQUALIFIED**.

All-group preview has a fixed2,000candidate-work budget (all MATCH results,
including population/ranking, plus reverse-owner advancement to bound fan-out).
RS composition stops polling earlier once every supported group has8hits.
It does not wait for a complete population. Preview emits verified conjunction
matches found within that bound with `Partial`, explicit lower-bound/unknown
count status and internal `NativeCandidates` work progress. Work positions may
include aliases or rejected sources and must never be displayed as logical
result counts. Selected full/top40 or paging retains the exact-population and
winner-proof path. This is a component work bound, not broad-term ready-latency
qualification.

Search limits target realistic use. Pathological fields or unusually large
proof cohorts may return a bounded partial result or typed refusal; they never
justify a scan, an unbounded allocation or bypassing current permission checks.
