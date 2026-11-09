# RB filter membership source notes

Status: bounded, values-only membership and contextual-source read window.
It returns a raw-title page plus proved fact values; it is not an exact total,
facet, broad-intersection composer, or a substitute for RS search or RB Home.

## Vocabulary and refusals

Supported fields: collection, memberOf, savedCollection, list, album, artist
and episodeGroup. `homeRow` and `search` refuse with `ErrMembershipHomeRowSource`
and `ErrMembershipSearchSource`; `episodeGroup` ranges refuse with
`ErrMembershipEpisodeRange`.

A collection operand is an opaque public identity. The legacy compiler lists
`starts-with` on every value-set field, so it accepts a collection prefix over
the encoded identity text. That prefix has no exact membership meaning here;
the operator parses and `ErrMembershipCollectionStartsWith` is returned.

Kinds 11-13 are outside this window. The legacy vocabulary lists author
(kind 13) in `collectedKinds` and `commonKinds`, so collection and memberOf
filters over an author are valid there. This recipe has no author membership
source; kind 13 returns the named `ErrMembershipAuthorSource` instead of a
generic vocabulary error. Kinds 11 (extra) and 12 (disc) are not listed by
either contract and remain `ErrUnqualified`.

## Public-id operands

`collection` and `memberOf` compare decoded public ids. A malformed id compiles
to `pid_blob(?) = NULL` in the legacy compiler, which never matches:

- `equals`, `in`, `contains` and `contains-any` ignore the malformed operand;
  a list with no valid ids matches nothing.
- `not-equals` and `not-in` never exclude a title because of a malformed id.
- `contains-all` expands to one equality atom per operand, so any malformed or
  otherwise unmatchable operand makes the whole conjunction false. The native
  evaluator reproduces that instead of dropping the operand from the loop.

The frozen legacy oracle covers partly and wholly malformed contains-all,
contains-any, equals and memberOf vectors.

## Episode groups

`schema-v1` keeps the authoritative episode hierarchy on `catalog_entities`:
`show_id`, `season_id`, `season_number` and `numbering`. The native
episode-group statements read those columns. The legacy compiler reads the
writer-derived `catalog_episodes` and `catalog_seasons` mirrors, which are not
part of the read schema. The oracle fixture derives those mirrors from the same
entity rows, so the two representations agree there by construction. A
deployment that exposes a stale mirror outside the read contract is not a read
source; no second hierarchy is consulted.

## Derived fixture shapes

The plan fixture installs a reviewed subset of the writer-derived tables
(`catalog_episodes`, `catalog_seasons`, `catalog_albums`, `catalog_songs`,
`catalog_browse_memberships`). `plan.Fixture.DerivedSchema` documents the
subset; exactness is claimed only for the canonical read schema.

## Plan access

Saved-resource reads start from `saved_resource_entries_order` on the selected
resource ids, reach each resource through
`sqlite_autoindex_saved_resources_1` with `id=?`, and reach the viewer's share
through the explicit viewer-first `saved_resource_shared` index keyed by
`authority,account_id,profile_id,resource_id`. Playlists are split: the owner
recipe seeks the explicit `catalog_playlists_owner` index, and the shared recipe
seeks the explicit `playlist_shares_listen_actor` index before reaching each
playlist by `sqlite_autoindex_catalog_playlists_1` on `token=?`. Both then use
the entry page and membership item indexes. Both analyzed profiles therefore
show keyed `SEARCH` lines for the viewer, owner and resource ids with no table
`SCAN`; the former scan waivers are gone. The planner bloom-filter waivers on
`me`, `ml`, `m`, `l` and `sh` remain statement-bound, and their indexed `SEARCH`
lines are still required.
