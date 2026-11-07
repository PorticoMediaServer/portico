# Saved view read draft

Base: helper source `8ca28aa83aeccca625feaa0f23e827a4d65e9c88` (frozen Home
`93df103e12024dbd33e07ca1d18e53dabb8dea0b` remains untouched).

`SavedViewReader.DirectoryCandidates` is callable only through an actual LIVE
output grant. It uses the existing first/final principal-birth/library fence,
actual token -> family -> owner tuple or exact local share, and current resource
facts in BOTH snapshots. Views with server visibility grant no additional access,
as in the public saved-resources semantics. Neither owner_key nor caller account
or profile is used. The current source requires a genuinely authorized browse
library anchor; it is not an account-wide directory API.

The raw source deliberately visits every resource ID, including foreign,
deleted, and collection rows. Each call consumes at most ONE raw ID; a rejected
row still advances the opaque ID cursor. A separate empty seek proves EOF.
Generation and the exact source row/access facts are rechecked, so revocation
without a generation bump still discards/retries the earlier draft. Cursors
contain no resource names or definitions. A candidate's ID, revision and role
are emitted only after current access proof.

This is always candidate-only: it has no names and makes NO public order claim.
The public order is SQLite lower(name),id (or pin position then that order).
The existing name COLLATE NOCASE indexes do not prove that expression order.
Exact ordering, pins, directory DTO hydration, and account-wide principal-only
composition remain with the parent's preparation/common-fence integration.

The one-ID bound is intentional: HN currently permits forced autoindex range
proofs only for single-source queries. A separate full-key point obtains current
resource/access facts. With the five common fence calls, generation, raw seek
and resource point, the complete first/final snapshot uses eight calls. No SCAN,
OFFSET, silent sample sort, owner_key invention, or raw truncation success is
substituted for the missing joined-range proof.

`rbSavedViewDefinitionDrafts` holds inert exact owner/shared payload points and
matching actual octet_length preflights. The current HN grammar does NOT certify
these new octet points; they are excluded from rbSavedViewDefinitions. The
resource helper consequently fails closed under the source-only registry.
No ordinary length(), columns-times-max forecast, generic function exemption,
or test-only execution registry is used. HN needs to approve these exact points
before runtime capture/registration; source/plan captures must use the actual
merged schema, including the parent's #1450 gate. No frozen golden is rewritten.

The private execution draft accepts only a resource minted from that native
same-snapshot point, binds its entire current definition/revision/role and full
viewer fence into its private cursor, and reuses the existing age/retirement and item projection kernels. Its explicit
subset is movies, actual same library, title asc/desc, default article handling,
grid/list, and the shared native scalar AST (year/decade/title/library/entityKind). Unknown fields, duplicate JSON members, definitions over
32 KiB, unsupported filter fields/operators/pivots/sorts/seeds/multi-sort and trailing JSON are
refused. This is NOT a published execution endpoint. The parent must compose
actual resource reads and full-value comparison in first/final snapshots, with
one original LIVE deadline/copy/output budget, before publishing any result.
Descending execution requires the common browse fence to be acquired for the
actual stored direction. Facets and broader filters/sorts require the parent's
existing authorization/preparation manager; none are silently broadened.

`TestRBSavedView` exercises the actual source registry/Reader in both statistics
profiles, raw continuation and EOF, own/shared/server-private semantics, source
revocation between private draft and final publication without generation drift,
generation cursor invalidation, and closed wide hydration. Definition validation
controls are pure kernel tests, not native execution/publication qualification.


## Shared native scalar continuation

The local dependency commit stages unchanged rb_filter_parse.go,
rb_filter_scalar.go and rb_channel_config.go from root source
42d5d500ba4a18fa42053057bc4c03b1aad2d714. The third file supplies the existing
rbJSONDepth dependency. Parent already owns all three; skip that dependency
commit when integrating the following own-file saved-view change. No alternate
configuration dialect or edits to root's parser are introduced.

rbSavedViewScalarDefinitions adds six inert title first/equality-tail/next-key
recipes in the existing asc/desc index order. The only extra fact is the native
fixed-size year. readSavedViewScalarPage applies shared native scalar matching
AFTER current age/retirement admission, consumes at most 64 raw positions, emits
at most the requested 1..40 permitted matches, and retains continuations across
filtered/hidden tails. An empty raw-cap page is pending work. A short, completely
consumed source window alone proves EOF. No top-K sample is sorted as an exact
answer, and no new general preparation manager is introduced.

The actual Reader tests walk dense equal-title cohorts and the next-key boundary
in both directions, verify complete reachability and non-exhausted empty raw-cap
pages, apply current age restrictions, and preserve the shared missing-value/NOT
semantics. They certify the native source/filter kernel, not stored-definition
hydration or final publication; those gates above remain closed. The production
read dependency graph has no catalog, dbwork, or livechannels/library package.
