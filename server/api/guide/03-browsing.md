# 3. Browsing and search

Browse as the signed-in viewer or a scoped key/token. The server applies library
access and content restrictions. Render its headings, navigation, row order,
sorts and filters; do not build a second client-side visibility rule.

| Read | Purpose |
| --- | --- |
| `GET /v1/libraries` | Libraries visible to this viewer, with the default view. |
| `GET /v1/libraries/{id}/content` | A composed library screen; `view` and `entityId` select its sub-surface. |
| `GET /v1/content` | Composed global screens such as saved content. |
| `GET /v1/home` | Home rows, hero, layout and per-row paging. |
| `GET /v1/home/rows/{id}` | Continue a row without loading the whole library. |
| `GET /v1/items/{id}` | One item with the viewer's state. |
| `GET /v1/items/{id}/detail` | Details, files, metadata and available actions. |
| `GET /v1/libraries/{id}/browse-capabilities` | The library's fields, operators, sorts, quick filters and bounds. |
| `GET /v1/libraries/{id}/facets` | Available facet values and counts. |
| `POST /v1/libraries/{id}/browse` | Structured browse expressions and ordered, paged entries. |
| `POST /v1/browse` | The same, for one group across the viewer's libraries: a person's titles, a playlist, a personal collection, a Home row, My List, Favorites or History. The query must name the group. |

For structured browse, read capabilities first. An example request:

```json
{"limit":40,"query":{"variant":"predicate","field":"title","operator":"contains","value":"harbor"}}
```

Every node of a query carries a `variant`: `predicate` (field, operator, value),
`relative` (the `within-last-days` operator on a date field) or `group` (exactly
one of `all`, `any` or `not`). The server sends the same tags back in the applied
query, and a request without them is refused.

The response has `entries`, the applied query, `pageInfo` and optional letter
anchors in `positionIndex`. Use `pageInfo.nextCursor` for the next page or the
returned revision with a positional range. Do not use a page index from another
revision. Source: `internal/httpapi/browse_routes.go` and the browse engine.

Totals are exact up to 10,000. A filter that matches more says
`pageInfo.totalIsLowerBound: true` with `total: 10000`; show it as "10,000+"
and keep paging with `nextCursor` (or a positional range past 10,000) until
`hasMore` is false. A grouping entry whose count passed the threshold carries
`totalAtLeast`. A group's own order (playlist position, collection order,
episode or track order) is the sort `groupOrder`, and it stays in force under
a filter.

Content screens publish their allowed sort/filter choices, including grouped
screens. Use those choices for genres, collections, series, people and other
subgroups instead of forcing title order. Keep visible content while a scan or
projection refresh runs; use the [event feed](06-events.md) to reread quietly.

## Search

`GET /v1/search?q=harbor&limit=20` returns grouped results. Accepted parameters
are `q`, `group`, `groups`, `cursor`, `limit`, `sort`, `direction`, `libraryIds`
and `record`. The allowed search groups and sort values come from the search
contract; an unknown value is refused. Limit is 1–40. Set `record=1` only when
a person commits a search, not on every keystroke or continuation.

`GET /v1/search/history` and DELETE on the same address read/clear the viewer's
search history. `GET /v1/people` searches people; `GET /v1/people/{id}` reads
credits. Source: `internal/httpapi/search.go`, `search_sort_routes.go` and
`people_routes.go`.

For indexing by title or provider ID, use [GET /v1/titles](08-for-tools.md),
which is bounded separately from the composed-screen query grammar.

## Artwork and ordering

Returned artwork URLs may carry `v` (digest) and `w=400`, `800` or `1920`.
Use the returned URL. Versioned bytes have an ETag and immutable cache headers;
unversioned bytes require revalidation. A replaced version answers 410 rather
than different bytes under its old address. Authentication still applies.
Titles sort by stored Unicode-folded keys, taking an explicit sort title first
and otherwise handling leading articles according to metadata language.
