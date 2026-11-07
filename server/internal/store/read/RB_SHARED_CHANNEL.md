# Shared channel read port

`SharedChannelReader` issues a closed sessionless scope for the actual enabled
`lc_channels` row, building generation, revision, stored configuration, rule and
named library. It supplies no viewer or general service authority. Its scope
metadata names every library across the channel configuration; the guide must
be visible only to a viewer authorized for every one. Playback remains freshly
authorized per viewer.

MG's adapter outside native writer/plan uses the existing `library.Config` →
`Normalize` → `Validate` → `json.Marshal` path. RB accepts no transported config
as authority: it projects bounded query/authority fields from the actual stored
bytes and fences those bytes. The native package imports no legacy catalogue or
LC initialization. The scalar subset follows the published AST and is checked
against its parser in read tests only. Unsupported predicates fail closed,
including beneath negation; text, membership and rating families remain pending.

`SharedChannelWindow` consumes at most four raw IDs. Rejected/unbound rows count
as progress; an empty batch with continuation is pending work. `RawEOF` comes
only from the raw stream ending. Whole playable values require one unambiguous
whole-file link and a current confirmed source/object binding, observed size and
mtime, duration and incarnation/revision fences. DVR provenance is unresolved
and refuses. This is catalogue identity evidence, not a physical byte hash.

Positive bindings, negative observations, raw traversal and authority are
rechecked together in one final snapshot. `RevalidateSharedChannelBatch` proves
copied values against current facts and returns no new EOF claim. A successful
read is an observation; the accepting writer still owns its publication fence.

For copying, freeze the source values while their `BrowseOutput` custody is live:

1. Call `MeasureSharedChannelValues(ctx, sourceOutput, values)`. `PayloadBytes`
   counts exact owned Go layout/backing storage and actual string bytes,
   including private roots in scope, fence and opaque continuation. Shared
   cursor/info nodes are counted once. The immutable issuer stays shared under
   composition custody.
2. Admit a separate memory-only grant for `ChargeBytes`, with the same owner and
   class. This page-aligned copy/allocator envelope is a logical reservation,
   not an RSS qualification claim.
3. Call `CloneSharedChannelValues(ctx, sourceOutput, retainedOutput, values)`.
   It checks both grants before and after copying, clones private/public strings,
   nullable values and exact-length item storage, and preserves issuer/fences.
4. Release source output after copying. Keep retained output until all copied
   values and consumers exit. The helper cancels or retires neither grant.

The complete native key proof accepts one additional `rowid=?` equality only
for the two compiled RB channel-check/whole-binding read IDs. Every declared
UNIQUE key column must already have SQL equality and matching native equality;
the additional constraint only narrows an at-most-one-row point. No range,
missing-key or scan permission is added, and existing command qualification
stays on its original path. The sole shared hunk is the full-key branch in
`stmt/plan/autoindex_access.go`; the opt-in proof and controls are in RB files.

Native controls cover private UTF-8 root sizing, backing-pointer separation,
underfunding, source-grant reuse, unchanged issuer/fences, source mutation,
whole binding, rejected-row continuation and raw EOF. Publication adversaries
change prepared revision, source root, availability, retirement and the empty
tail without changing library revisions. Standard runner race and complete
capture evidence remain required before publication; this draft is unqualified.
