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
unavailable; sources may return typed pending without partial items. Full-mode
and explicit show-more queries return unavailable, never an approximation from
title candidates. A future native source must use the same pool's public LIVE
read and accepted authority/bank/profile/source plus HN fixed-slot attestation.
The synthetic title controls prove dispatch/custody only, not that native seam.
No runtime route or full-search execution is enabled.
