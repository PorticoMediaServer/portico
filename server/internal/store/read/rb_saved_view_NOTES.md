# Saved view native read draft

Rebuilt helper branch: `schema-v1/p2-rb-h1-saved-view-r1`.
Exact merged base: `4d5750346bd4dd298eea3166addb1045e943d9cc`.
Earlier helper source and frozen Home `93df103e` remain untouched on their branch.
Replayed own saved-view commits: `be86283fe`, `4926d4415`.
Separate local scalar dependency: `b2291da2b` copies unchanged
rb_filter_parse.go, rb_filter_scalar.go and rb_channel_config.go from root
`42d5d500ba4a18fa42053057bc4c03b1aad2d714`. Parent already owns those three;
skip that dependency commit when integrating own saved-view changes.

## Actual resource execution

`SavedViewReader.Execute` is a callable harness-only LIVE read. In its first
immutable snapshot it uses the existing registered global/principal birth joins,
proves the actual saved-resource owner tuple or exact local share, and reads its
name, summary and complete stored definition through native octet preflights.
It resolves the native catalogue library from the actual stored library ID and
takes the existing full browse/library/policy fence in that SAME snapshot.
Caller library, kind, sort preferences, account and profile cannot choose the
execution target or authority. A denied stored library returns no page.

The supported stored definition is movies, title asc/desc, default article
handling, grid/list, and the existing root scalar AST: year, decade, title,
library and entityKind. Duplicate JSON members, invalid UTF-8, trailing JSON,
definitions over 32 KiB, unsupported fields/operators (also beneath NOT), other
pivots/sorts/seeds/multi-sort refuse. The shared parser retains its exact public
missing-value/three-valued semantics. No alternate configuration dialect or
legacy production imports are introduced.

A separate business snapshot streams the existing indexed title order, adding
only native year to the common item projection. Current age/retirement admission
precedes scalar matching. Every consumed raw position advances the cursor;
64 raw rows without a match remain explicit non-exhausted work. Equality-tail
and next-key seeks preserve complete reachability in both directions.

The fresh final snapshot re-runs the full common fence and the same actual
resource/access/octet/payload points. It compares the ENTIRE resource value,
including definition bytes, revision, role and actual principal/catalogue fence.
Unversioned definition changes cannot release an earlier page. Denial wins over
a failed private business step; every error returns zero output. One complete
stale retry shares the ORIGINAL LIVE deadline and cumulative 4 MiB Go copy
counter. The output grant stays held through the actual consumer. Query direction
comes from the stored definition and is included in the final common fence.

The first snapshot is a finite 11-query forecast: principal(2), resource(3),
native library mapping(1), full browse fence(5). Final publication is eight
queries. Forecast windows retain the original 4 MiB Go allowance and use a 2 MiB
whole-window variable-byte envelope implied by that allowance, rather than a
columns-times-maximum estimate. Each resource payload independently checks actual
native octets BEFORE allocation/query and compares every copied byte afterward.
All windows and retries debit the same attempt-wide counter. Closed AST scratch
and retained directory-key allocations are charged separately before allocation.

## Complete bounded named/pinned directory

`SavedViewReader.Directory` needs only the fresh actual principal, not a catalogue
library anchor. It uses the SAME registered global/principal birth joins and
retains their actual epochs, births, restrictions and revisions. The existing
unsupported access-limit/blocked-label classes remain closed. The canonical pin
owner key is derived solely from that fresh local principal, using the existing
persisted authority/account/profile encoding. Caller account/profile/owner_key
is never accepted, and this pin key is never substituted for actual resource
owner tuple/share authorization.

The fallback proves the COMPLETE raw saved_resources universe in a bounded PK
seek. It includes foreign, deleted and collection positions in its raw budget;
only currently permitted live views receive payload/pin point reads. Server
visibility grants no view access. Foreign private names/definitions are never
selected, emitted or stored in cursors. The cold bound is 16 TOTAL raw resources,
not 16 permitted resources and not a sampled top-K list. A 17th raw row returns
empty, explicit pending/preparation-required work with SourceExhausted=false and
Exhausted=false. Parent's general preparation manager owns larger sets.

Only after complete membership does the reducer sort all authorized views by
the exact public SQLite lower(name),id order, or the pinned public
printf('%08d',COALESCE(position,99999999)) || char(31) || lower(name),id order.
Pin filtering and order come from exact fresh principal-owned saved_pins and
saved_pin_order points. ASCII/NUL/non-ASCII name ordering, duplicate names,
missing arrangements, negative positions and positions wider than eight digits
are checked against actual SQLite public-order expressions in READ tests.
Initial and fresh final snapshots compare complete membership, each resource's
access facts, all authorized payload bytes, pin facts, and actual principal.
Unversioned pin changes and a universe growing across the bound invalidate the
earlier draft; no truncated exact page is published.

Exact pagination binds principal, complete binary-safe source digest, pinned
category and limit. Only an authorized name's sort key/ID enters its opaque
cursor. Digest encoding preserves raw UTF-8/NUL bytes without JSON replacement
collisions. Directory metadata/key/hash construction is charged to the original
copy allowance. Both initial/final windows have a finite 68-query maximum and
hold 2 MiB native plus the original 4 MiB Go allowance. Original one-position
`DirectoryCandidates` remains an explicitly candidate-only source; it makes no
public order claim and still requires its original browse anchor.

## Merged octet contribution and qualification

Own domain file: `store/stmt/plan/rb_saved_view_octets.go`. It contributes only the
two exact owner/shared saved-resource byte points through the MERGED private
closed registry. It pins complete definition/parameter/plan shapes, ordinary
MAIN native primary keys, unshadowed native octet_length and exact SEARCH EQP.
Generic SQL lint still rejects octet_length; another ID or changed SQL cannot
borrow this purpose's authority. No registry/core shared hunks are changed.
`rbSavedViewDefinitions` now supplies the complete inert saved-view inventory;
actual Reader Capture remains mandatory before any registry executes it.

Owned focused tests cover actual Reader Capture in both statistics profiles,
raw EOF/continuations, initial/final resource/principal/definition/pin drift,
actual stored-library authorization, original deadlines across retry, cumulative
copy-budget refusal, preflight before wide payload, complete exact ordering and
explicit over-bound pending. Plan controls reject purpose, range, projection,
parameter, plan and key mutations. No frozen golden is rewritten. Captures bind
the schema/Reader compiled at run time; #1450 was replaced by the unmerged #1480
publication hold. Parent owns post-merge publication recapture. The hold does
not prevent these local drafts or actual-current-main focused checks.

Remaining semantics are explicit, rather than successful approximations:
large-set exact preparation, broader pivots/sorts/facets, authenticated wire
cursor integration and product route/DTO composition remain with parent. No
provider calls, production wiring, saved-resource writes or DDL are introduced.

## Parent fresh-main integration

The directory principal purpose now registers an opt-in, private stateful
principal-scope recipe through RS's merged sealed registry (ID32500). It calls
the shared first/final principal checks without duplicating them. The marker
selects only this resource-read purpose and proves no resource by itself; actual
owner/share/current stored payload and complete frame checks remain mandatory.
Execution separately uses the actual stored library's default catalogue recipe.
Functional tests use virtual time within each leaf subtest. Scalar dependency
files are byte-identical to RB's LC port; no shared fence/registry hunk changes.
