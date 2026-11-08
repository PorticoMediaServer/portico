# RB media filter primitives (task 05)

Unwired, values-only kernel. `compileRBMediaFilter` closes the published
asset/Reading vocabulary; `readRBMediaFilterFacts` reads accepted current facts
for one admitted item in one admitted snapshot. There is no source opening,
path following, converter invocation, permission flag or viewer scope.

## Closed vocabulary and sources

| Field | Accepted source |
| --- | --- |
| `availability` | `inventory_item_availability` meaning from bounded inventory/asset/source points (`rb.filter.media.inventory.*`, `asset`, `sources`), including DVR retirement |
| `resolution` | accepted `catalog_assets.height` classes, per version |
| `dynamicRange` | stored accepted `asset_streams.detail_json.dynamicRange` for video streams whose own `asset_stream_facts` size/mtime matches the asset |
| `audioLanguage` | accepted audio `asset_streams.language`, non-empty |
| `subtitleLanguage` | accepted `asset_subtitles.language` from a `known` `asset_subtitle_facts` row whose size/mtime matches the asset, plus current shared `subtitle_resources` whose revision source identity matches the asset |
| `durationMs` | accepted `catalog_assets.duration` per version; request ms normalized to seconds exactly as the public compiler does |
| `versions` | `part_index=0` asset links (the published `catalog_item_version_counts` trigger rule) for every kind, Reading included: `reading_publications` is an FK child of `catalog_asset_links`, so each publication is one part-0 link |
| `discNumber` | `catalog_entities.disc_number` with the published `COALESCE(...,1)` |
| `author`, `series` | accepted `reading_author_*` / `reading_series_*` tuples, verified through `reading_entity_author_facet` / `reading_entity_series_facet` |

Set-valued fields follow the public EXISTS semantics: positives match any
version, `not-equals`/`not-in`/`is-missing` require no version to match, and
`contains-all` requires every requested value. An untagged/empty language is
absence, not an empty-string value.

## Named pending / refused facts

These are not exact empty results:

- `series` on an audiobook/book kind: the accepted book series projection is not
  adopted (`errRBMediaSeriesPending`). Raw `catalog_entities.series` tags and
  `audio_tag_evidence` are never read.
- `narrator`: no accepted narrator projection (`errRBMediaNarratorPending`).
- `author` on an audiobook/book kind: needs the accepted book projection
  (`errRBMediaAudioAuthorPending`).
- A current personal `subtitle_resources` row needs the viewing principal, which
  this kernel does not bind (`errRBMediaSubtitlePrincipalPending`). Shared
  resources are read exactly; a personal resource is a named refusal, never an
  exact answer. The legacy subtitleLanguage metadata attribute source
  (`catalog_item_attribute_edges`) is not read here; like other accepted file
  facts, this kernel answers from the files themselves.
- Container kinds are refused rather than guessed from children; membership and
  container metrics belong to tasks 06 and 09.
- Mid-publication assets (`info_publication_pending`), stale stream/subtitle
  identity, an invalid stored dynamic range and a partial accepted attribution
  tuple are `errRBMediaUnresolved`.

## Budget and execution

Every text read has an owned `.bytes` preflight (`stmt/plan/rb_filter_media_octets.go`);
actual bytes are reserved twice (stored plus one folded/decoded copy), plus
4096 fixed decode bytes and 256 bytes per raw row, capped at 1 MiB. Raw caps are
64 links, 256 streams, 256 subtitles, 64 subtitle resources, 64 publications and
64 sources; a larger set is `ErrUnqualified`, never truncated.

One item needs more than the ordinary 8 statement calls, so callers use a
declared `governor.SnapshotForecast` (the tests use `MaxQueries: 512`) and own
the first/final fence, revocation and deadline rules. This kernel makes no
second snapshot and retains no row after return.

## Plans

`testdata/rb-filter-media-plans.json.gz` records the 22 inert definitions in both
native statistics profiles. Record with
`go test ./internal/store/read -run TestRBFilterMediaPlans -record-rb-filter-media-plans`.
