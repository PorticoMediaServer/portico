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
viewer fence into its private cursor, and reuses readBrowsePage. Its explicit
subset is movies, actual same library, title asc/desc, default article handling,
grid/list, and no query. Unknown fields, duplicate JSON members, definitions over
32 KiB, filters, unsupported pivots/sorts/seeds/multi-sort and trailing JSON are
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
