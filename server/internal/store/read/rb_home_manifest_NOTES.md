# Independent native Home manifest/layout draft

Base: merged `fc32cf4419b4b6b2c19b60391134521d35447c9e` (#1482, hold lifted).
Branch: `schema-v1/p2-rb-h1-home-manifest-r1`.
Frozen saved-view dependency replay ends at `8871b0a541466d747672dcbd54b2b392b0eae2b6`;
its code is byte-identical to completed saved-view source `af688cbaf`.
The separate pristine root scalar dependency is `cbe9ca711` on this branch.
Parent already owns those dependencies. Integrate only the following NEW
rb_home_manifest* files; no existing saved-view, shared source, schema or golden
is modified. The five existing PR hash-only refreshes remain root's work.

## Native facts and publication

HomeManifestReader.Read is a callable harness-only LIVE operation. It reuses the
unchanged actual global/principal birth joins, original LIVE/output custody,
protected deadline, native snapshot governor and cumulative 4 MiB copy counter.
Complete current source frames must agree in separate initial/final snapshots;
one whole stale retry shares the ORIGINAL deadline/copy allowance. Principal
rebirth, settings/layout changes, unversioned library changes and configured-view
access/definition/catalogue drift cannot publish the earlier frame. Errors
return zero output, including budget/admission/cancellation failures.

The two actual Home keys are home.rowOrder and home.hiddenRowIds, stored in the
UO-owned profile-server preference document. Reads bind local authority and the
actual fresh token's account/profile, scope profile-server and empty device key.
No caller account/profile, fabricated viewer, settings callback, policy callback
or write capability exists. Native scope revision and presence are retained;
absence produces the public revision-1 empty-list default. Device-class/device
preferences cannot override keys whose published registry permits only the
profile-server scope. Type metadata must be string-list. Complete position
seeks use the actual schema's 0..63 bound; each UTF-8 row ID uses an exact native
octet point BEFORE hydration and must fit the published 128-rune domain.

Current permitted library IDs come from actual membership/profile grants; owner
access still respects an actual profile library ceiling. Every permitted member
ID is resolved, or an owner's whole raw library universe is proved by a bounded
PK seek. Names/kinds use exact native byte points before payloads. Data, order,
metadata, age-publication, configuration/preference and personal viewer revision
facts plus current native catalogue mapping/retirement are fenced. Only the
complete authorized projection is sorted by the public position,name,id order.
Missing/deleted IDs in a persisted grant are not invented libraries.

The conservative cold projection bound is 16 IDs. Above it, the document is
explicitly Pending with library_projection_requires_preparation, no partial
libraries/descriptors, SourceExhausted=false, and honest raw membership count.
Parent's preparation manager owns larger projections. The fallback never sorts
a truncated sample and calls it exact. Both source windows have a finite
1024-query ceiling and require a trusted raw-row budget of at least 1024
(functional tests fund 2048), never an unbounded walk. Existing snapshot profile
limits can still refuse wide/over-budget frames; no success truncation follows.

All actual strings are charged to the same original copy counter. Closed byte
points live in own stmt/plan/rb_home_manifest_octets.go through the merged private
registry; only exact library and full preference-slot PK points qualify. Native
builtin, complete MAIN key and exact SEARCH audits remain mandatory. Generic SQL
lint still refuses octet_length and cannot lend these purposes to another ID or
shape. Fixed decoder scratch and descriptor headers are separately reserved.
No columns-times-maximum forecast or legacy production import is introduced.

## Descriptor semantics

This is descriptor/layout publication, NOT an eager Home content reply. No
Entries, Total, empty shelf, recommendation children, count, or spotlight result
is fabricated. The catalogue records actual public fixed definitions, titles,
localization codes, privacy, family, artwork/platform, priority/TTL, cursor and
required/hideable/reorderable/critical policy. Required continue/listen/books rows
ignore a stored hidden flag. Community's default-hidden rule depends on actual
placement. Stored order wins before server priority/ID; unknown/stale row IDs
remain harmlessly in the user's layout without granting a descriptor.

Catalogue retains explicit unsupported/unresolved families. LayoutRows excludes
owner-disabled or unresolved policy. Rows additionally applies actual hidden
state and the public 64-row UI cap, preserving required rows. This cap is applied
only AFTER complete descriptor inventory; it is not a raw source truncation.
ReadState=unsupported identifies every uncomposed fixed row. Recommendation and
live families need their actual RS/MG source/authority integration, not guessed
content or an enabled-setting proxy. Spotlight is explicitly unsupported. No
native Home row content recipe is silently claimed merely because its identity
and layout policy are known.

Configured view IDs are taken only from actual rowOrder. Each selected view must
have fresh actual ownership/share access and an allowed current library. Names
of foreign private/revoked views are never projected. Valid current movie/title
views using the unchanged native scalar parser/browse fence get a deferred
saved-view target only inside the frozen native route ID bounds. Malformed
definitions and unauthorized/forbidden-library references are omitted as stale
layout references; well-formed unsupported native recipes stay explicitly
unsupported.
The saved-view code is reused as a dependency and never modified.

## Genuine remaining decisions/dependencies

The native preference tables are merged, but no native UO preference writer was
present in the inspected main source. That does not block current read-only
facts or defaults; UO owns every settings write and acceptance path.

An effective community policy is asserted only when the current source row says
server. A file source, or absent native source, remains unresolved: the runtime
file-effective-value authority is not represented by these DB columns. The
stored nullable flag alone must not invent an effective owner setting. Other
layout/library reads continue independently. RS recommendations/family/spotlight,
MG live availability/authority, broader saved-view recipes, and Home row content
composition remain explicit unattached dependencies. No other lane is steered.
Parent owns larger-set preparation, public DTO/wire/cache integration and route
cutover; this draft adds no product wiring, provider calls, DDL, or settings writes.

Only new manifest family tests run. They use actual current-main Reader Capture
in both statistics profiles, fresh principal checks, exact preference-slot byte
hydration, required/hidden rules, foreign preference isolation, complete library
projection/pending bounds, unversioned source drift and configured-view revocation.
Own plan controls refuse purpose/SQL/range/type/plan mutations. No unchanged
saved-view suite or existing full/race/capture matrix is rerun, and no golden is
refreshed or adopted here.

The final route-bound refinement is checked with its new targeted subtest only;
previous unchanged manifest/plan controls are reused from the baseline run.
