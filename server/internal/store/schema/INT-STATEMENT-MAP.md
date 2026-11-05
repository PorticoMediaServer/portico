# INT changed static-statement map

This is a build contract, not an implemented product adapter. Every production static statement also needs the complete V registry and bundled EXPLAIN golden. Generic dynamic SQL is not permitted.

| Current source / dependency | Target statement / lifetime |
|---|---|
| compactcatalog/write.go:132,174–178,219; library root/source-key lookup and allocating a fresh integer on reimport |Seek catalog_identities by(library_id,source_key), join catalog_subjects by id, resolve bounded redirect, then join/recreate live entity with that SAME retained integer. New identity creates anchor then key then entity atomically. LibraryID scopes overlapping paths; immutable UUID/kind cannot move to another library |
| compactcatalog/identity.go:165 INDEXED BY catalog_identities_public |Map to catalog_identities_subject. Use SELECT source_key FROM catalog_identities INDEXED BY catalog_identities_subject WHERE subject_id=? AND library_id=? ORDER BY source_key. Public UUID lookup first resolves catalog_subjects.public_id. The old index is intentionally removed with its old column |
| compactcatalog/identity.go Add/Move/DropIdentity |Mutate this library/source-key association only under exact retained subject/redirect/mutation witness. No personal/history rewrite and no alias-key global UPDATE on merge |
| library bridge root/name/kind joins |Current mutable name/kind come from libraries; historical root stays on catalog_libraries; retained namespace survives mutable config removal |
| catalog/audio_repository.go:486 ON CONFLICT(asset_id,ordinal) |catalog_asset_links: ON CONFLICT(asset_id,cue_ordinal) WHERE cue_ordinal IS NOT NULL. Canonical range is this link; no cue_slot or music_virtual_tracks companion |
| preparedmedia/music_cue_metadata.go:23–38 cohort |Same asset's nonNULL cue_ordinal complete1..N, same library and captured range/source witness. Count/min/max do not scan an unrelated/global cohort |
| Hot resolution ORDER BY |Repeat kind IN(1,2,3,4,10) exactly in addition to bound kind; library/kind/resolution/id tuple seeks intended partial index. No temp sort |
| Other partial-index statements |INT-INDEX-POLICY.json exact shared predicates, generated once into index and statement; each hot shape has intended-index/no-temp-sort golden |
| Personal live/retained filters |item_id now means retained subject identity and is never nulled by catalogue removal. Live eligibility joins catalog_entities/retirement; retained state uses that user's snapshot and retained subject. Do not keep old item_id IS NULL retention logic |
| All user feature history reads |Read current personal_items +that profile's generation/action witness. Append-only play_history is only admin paging. Historical-window feature semantics require explicit decision; do not silently claim parity |
| Reading text search |Join fragment.publication_id to exact current reading_text_index_state.active_publication_id, with captured source/availability/authority/search epoch. Preserve original section/UTF16 coordinates, bounded rowid keysets, no corpus-global ranking of hidden staged generations |
| Playlist entry insert/reorder |Sparse/fractional order_key only, ordered by(order_key,id). No integer position column or global renumber; uniqueness is(playlist_id,order_key) |
| notification_records read/archive |Join current audience/withdrawal/expiry with requesting principal's sparse acknowledgment/head; update only that principal's state |
| Global auth /remember entry points |Check current authority_generation/transition owner fence everywhere; remembered automatic sign-in also captures remember_generation. Off/on never revives old descriptors; active-session semantics remain distinct |

Source pins: initial8e5cb52b; X/T3 late source7ca1f0ce as recorded in their ledgers. Existing public IDs/formats/Root17 identity semantics remain binding except the explicitly approved library isolation/chronology/CUE corrections. The pre-release conversion must preserve existing identity mappings and sparse user state; this is not authorization for a post-release reshape or startup backfill.

## R21 exact automatic-source statements

SCHEMA.sql contains the full accepted evidence/publication/origin/policy/language predicates in catalog_automatic_sources and catalog_automatic_reverse_sources. Those view definitions are the single shared predicate authority. Current production automatic collections are movies, not a new music feature.

The collection-forward bounded candidate probe is:

```sql
SELECT item_id
FROM catalog_automatic_sources
WHERE library_id=:library AND provider=:provider AND provider_id=:collection
LIMIT :candidate_limit;
```

The point-item reverse shape is:

```sql
SELECT c.entity_id
FROM catalog_automatic_reverse_sources s
JOIN catalog_automatic_collections c
  ON c.library_id=s.library_id
 AND c.provider=s.provider AND c.provider_id=s.provider_id
WHERE s.library_id=:library AND s.item_id=:item AND c.removed=0;
```

Current-SHA bundled3.53.4 controls use library1/provider'tmdb'/collection'C'/item1. Forward EQP seeks metadata_evidence_collection_members(provider,collection_kind,collection_id), then metadata_field_origin_evidence(evidence_id), entity PK, accepted publication/operation, current policy and language. Reverse seeks entity PK, origin PK(entity_id,field), evidence-collection PK(evidence_id,collection_kind), then automatic collection PK(library_id,provider,provider_id). Neither uses a temp B-tree. The final SCAN of a coroutine/view output is not a base-table scan; the producer seeks the stated source indexes. Full exact plans are in INT-EXTENDED-CHECK.json under the two selected automatic-source controls, not a hand-written hypothetical EXPLAIN.

A first LIMIT probe is not a resumable whole-cohort cursor. Bulk discovery uses two bounded source-index stages:

```sql
SELECT evidence_id
FROM metadata_evidence_collections INDEXED BY metadata_evidence_collection_members
WHERE provider=:provider AND collection_kind='collection'
  AND collection_id=:collection AND evidence_id>:after_evidence
ORDER BY evidence_id LIMIT :window;

SELECT entity_id
FROM metadata_field_origins INDEXED BY metadata_field_origin_evidence
WHERE evidence_id=:evidence AND entity_id>:after_entity AND field='relations'
ORDER BY entity_id LIMIT :window;
```

Each candidate uses the same exact accepted-source point predicate; durable source cursors advance even when a candidate is ineligible. Source input/policy/operation witnesses fence resnapshot/restart. Work budgets bound rows/VM/bytes including rejected candidates. A collection's user-facing sort/page adapter remains an executed build qualification; the LIMIT diagnostic does not claim all ordered member routes pass.

Local nonempty normalized collectionName wins; provider fallback requires the local key empty. ANY owner override removes inference; present=1 is a disjoint explicit-add stream. Exact current membership is catalog_automatic_members_current. First accepted label initializes the collection; later source children cannot rename it accidentally. Explicit owner edit/collection refresh controls its sticky title.

Normal source acceptance updates old/new contributions and deduplicated parent keys, age/facets/revisions and receipt in the same bounded transaction. Exceptional source changes use the typed complete old/new image contract in INT-AGGREGATE-CONTRACT.md. Merely moving a provider pointer and adding a cursor is not valid publication.

## KEYSET-CURSOR and O11

All hot ordinary-table statements force their intended INDEXED BY path. Every partial statement repeats the exact generated predicate from INT-INDEX-POLICY.json, including nullable extrema. Planner statistics are declared for the golden environment; they cannot silently choose a full-library/temp-sort path.

A resolution ascending page, for example, consumes the equality tail first, then remaining capacity from next keys:

```sql
SELECT id,resolution_key FROM catalog_entities INDEXED BY catalog_entities_resolution
WHERE library_id=:library AND kind=:kind AND kind IN(1,2,3,4,10)
  AND resolution_key=:key AND id>:id
ORDER BY resolution_key,id LIMIT :remaining;

SELECT id,resolution_key FROM catalog_entities INDEXED BY catalog_entities_resolution
WHERE library_id=:library AND kind=:kind AND kind IN(1,2,3,4,10)
  AND resolution_key>:key
ORDER BY resolution_key,id LIMIT :remaining;
```

Current bundled controls assert equality-tail id>? and next-key resolution_key>? seeks with no temp sort. Descending pages use the corresponding direction for both keys/ties. NULL has a separate ISNULLstream. Title statements use `sort_key COLLATE NOCASE` in equality/range/order, exactly matching the index's inherited collation. Never use `(key,id)>(...)`, even a literal tuple or subquery. Large tied groups are a mandatory Vcontrol. Each statement/transaction closes at its bounded window; no SQLcursor survives client waits.

Admin history uses the same split form with occurred_atDESC/idDESC, plus account_key/subject_id prefixes for the two filtered indexes. Short search Showmore and restricted fallback obey the same rule. O16personal UUIDlookup first resolves retained subject.public_id, then personal_current_live(profile_id,item_id); the removed duplicate profile/public-id B-tree is not reconstructed.

## Native-bank and reclaim dispatch

Search query plans select the compiled native bank captured with search_revision, never interpolate a bank string. v1content views hardcodev1; a new global head cannot redirect an old native-table statement. Own-title content rowid remains entity ID; published-document IDs are custody identities only. Every search hit rejoins accepted job generation, live source/relations and current authority. See INT-SEARCH-CONTRACT.md for the atomic own native delete/insert/head boundary and old-token retention.

FK-CHILD-INDEX is mechanical for all625FKs: actual parent DELETE/UPDATE EQPs plus parent-collated child lookup. INT-FK-CHECK.json preserves1875scan-free bundled plans. Parent image reclaim uses search_publish_document_fk(document_id), proving its ISNOTNULLpartial predicate is usable by actual enforcement. Never infer reclaim cost solely from the visible parent PKseek.

## O18 bound-value goldens and O03top1

Production first reads the automatic collection identity, then binds values to the forward query. Do not list members by filtering the compound oracle view on parent_id; that blocks constraint pushdown and can scan all provider observations/build an automatic index.

```sql
SELECT library_id,provider,provider_id
FROM catalog_automatic_collections WHERE entity_id=5 AND removed=0;

SELECT item_id FROM catalog_automatic_sources
WHERE library_id=1 AND provider='tmdb' AND provider_id='C' LIMIT 257;

SELECT c.entity_id
FROM catalog_automatic_reverse_sources s
JOIN catalog_automatic_collections c
 ON c.library_id=s.library_id AND c.provider=s.provider AND c.provider_id=s.provider_id
WHERE s.library_id=1 AND s.item_id=1 AND c.removed=0;

SELECT file_size FROM catalog_asset_contributions_current
WHERE parent_id=5 ORDER BY file_size DESC LIMIT 1;
```

For effective member reads, the inferred stream excludes ANY owner override via its(collection_id,item_id)PK; a second indexed explicit present=1stream adds owner members. The streams are disjoint. The combined view is an independent oracle, not the production list statement.

Current-SHA bundled EXPLAIN details (FKON):

**selected automatic forward evidence reverse key**

```text
CO-ROUTINE catalog_automatic_sources
COMPOUND QUERY
LEFT-MOST SUBQUERY
SEARCH e USING INDEX catalog_entities_automatic_local (library_id=? AND automatic_collection_name_key=?)
UNION ALL
SEARCH c USING INDEX metadata_evidence_collection_members (provider=? AND collection_kind=? AND collection_id=?)
SEARCH o USING INDEX metadata_field_origin_evidence (evidence_id=?)
SEARCH b USING PRIMARY KEY (entity_id=? AND provider=? AND role=?)
SEARCH e USING INTEGER PRIMARY KEY (rowid=?)
SEARCH p USING INDEX sqlite_autoindex_metadata_publications_1 (id=?)
SEARCH op USING INDEX sqlite_autoindex_metadata_provider_operations_2 (provider=? AND id=?)
SEARCH policy USING PRIMARY KEY (library_id=? AND provider=?)
SEARCH ev USING INDEX sqlite_autoindex_metadata_evidence_1 (id=?)
SEARCH cl USING INTEGER PRIMARY KEY (rowid=?)
SEARCH lib USING INDEX sqlite_autoindex_libraries_1 (id=?)
SCAN catalog_automatic_sources
```

**selected automatic reverse parent primary source**

```text
CO-ROUTINE catalog_automatic_reverse_sources
COMPOUND QUERY
LEFT-MOST SUBQUERY
SEARCH e USING INTEGER PRIMARY KEY (rowid=?)
UNION ALL
SEARCH e USING INTEGER PRIMARY KEY (rowid=?)
SEARCH o USING PRIMARY KEY (entity_id=? AND field=?)
SEARCH b USING INDEX metadata_subject_publication (accepted_publication_id=? AND entity_id=? AND provider=? AND role=?)
SEARCH policy USING PRIMARY KEY (library_id=? AND provider=?)
SEARCH p USING INDEX sqlite_autoindex_metadata_publications_1 (id=?)
SEARCH cl USING INTEGER PRIMARY KEY (rowid=?)
SEARCH op USING INDEX sqlite_autoindex_metadata_provider_operations_2 (provider=? AND id=?)
SEARCH lib USING INDEX sqlite_autoindex_libraries_1 (id=?)
SEARCH ev USING INDEX sqlite_autoindex_metadata_evidence_1 (id=?)
SEARCH c USING PRIMARY KEY (evidence_id=? AND collection_kind=?)
SCAN s
SEARCH c USING PRIMARY KEY (library_id=? AND provider=? AND provider_id=?)
```

**O18 top1 accepted size seeks both arms**

```text
MERGE (UNION ALL)
LEFT
SEARCH parent USING INTEGER PRIMARY KEY (rowid=?)
SEARCH c USING COVERING INDEX catalog_asset_contribution_size (parent_id=?)
CORRELATED SCALAR SUBQUERY 4
SEARCH d USING PRIMARY KEY (publication_id=? AND asset_id=?)
RIGHT
SEARCH parent USING INTEGER PRIMARY KEY (rowid=?)
SEARCH d USING INDEX catalog_asset_delta_size (publication_id=?)
```

The top1golden forbids `max(file_size)` through the accepted compound view. Caller maps an empty result to the defined0. Both base and staged-upsert arms seek their extrema indexes and merge, without a temp B-tree; hidden/deleted staged rows do not win.

## Query-owned source frontier

SEARCH-FRONTIER-SPILL keeps typed source IDs/heads in a private bounded ephemeral queue when the RAMfrontier fills. It does not truncate results, create a durable serving copy or leave a native SQLcursor across chunks. Its keyset reads obey equality-tail/next-key streams and exact collation too. Force-small-RAM tests must match full ordered membership; actual1M5%jo overflow controls remain negative until replacement traversals complete. Source/bank/authority continuation, quota/cancel and physical scratch retirement are explicit V/Gbuild tests. Exact4158→291static impact is INT-Q-IMPACT-291.json; only eight unrelated type grains change, not search indexes or views.

## INT-CHANGE-001: durable operations, exact user charts and bounded admin reporting

Current change-controlled candidate SHA567267b78205ab47b7efe0be764286b29d14f99ad476c5acdb44c65ac9f8bd3f; R review pending. See phase1/INT-CHANGE-001.md and PB-MANIFEST-V1.md. These are static statement contracts for HN/W, not runtime command implementations or an authority grant.

Receipt resolution: current permission first; read actual owner's incarnation/last/ack. An acknowledged sequence is ResultRetired before any partially drained result cohort is read. An absent unacknowledged receipt at sequence<=last is ResolveRequired; an unavailable owner without floor proof is ResolveRequired. Only a not-yet-applied next sequence may be Pending/Superseded. A matching historical receipt is returned before mutable source/lease/cursor checks. Use point keys, never a reconstructed current head.

```sql
SELECT command_incarnation,last_command_sequence,acknowledged_command_sequence
FROM inventory_runs WHERE job_id=:run;
SELECT operation_id,request_digest,command_kind,accepted_revision,next_cursor,
       next_family,next_copy_phase,next_copy_id,next_copy_aux,
       next_copy_key,next_copy_key2,next_copy_key3,after_entity,expected_epoch,
       result_count,result_digest
FROM scan_command_receipts
WHERE run_id=:run AND command_incarnation=:inc AND command_sequence=:seq;
SELECT input_ordinal,child_kind,child_ordinal,object_id,library_id,
       subject_id,asset_id,accepted_asset_revision
FROM scan_command_results
WHERE run_id=:run AND command_incarnation=:inc AND command_sequence=:seq
ORDER BY input_ordinal,child_kind,child_ordinal LIMIT :admitted_count_plus_one;
```

Child kind is the closed INTEGER discriminator0ordinary/1CUE/2episode; ordinary child ordinal0, CUE1..N, episode zero-based captured request position. No later association lookup substitutes for this immutable ordered allocation cohort. All result/index/FK/receipt/COMMIT cost is admitted; no truncated success. BasicStage/Seal receipts never claim BasicAccept availability.

Seal uses two bounded streams on named UNIQUE search_fragment_publication; no tuple cursor, whole-image scan or implementation-specific hash state. Final no-next proof uses the same streams and exact stage/verified count+bytes+chain. The compiled algorithm is portico-search-manifest-sha256-chain-v1, vector JSON d7d83853fc7655bfcfdc639017adfd929761c6c89ac5b8e0d424abefd8a48053. Each fragment's exact native TEXT source_revision is independent from the image INTEGER source revision.

```sql
SELECT id,field,ordinal,source_revision,prefix_tokens,lexical_tokens,normalized
FROM search_fragments INDEXED BY search_fragment_publication
WHERE publication_id=:image AND field=:field AND ordinal>:ordinal
ORDER BY ordinal LIMIT 1;
SELECT id,field,ordinal,source_revision,prefix_tokens,lexical_tokens,normalized
FROM search_fragments INDEXED BY search_fragment_publication
WHERE publication_id=:image AND field>:field
ORDER BY field,ordinal LIMIT 1;
SELECT id,prefix_tokens,lexical_tokens,normalized
FROM search_fragments INDEXED BY search_fragment_reclaim
WHERE publication_id=:image AND id>:after_fragment
ORDER BY id LIMIT :admitted_rows;
SELECT id FROM search_reclaim_jobs INDEXED BY search_reclaim_due
WHERE job_due_ms IS NOT NULL AND job_due_ms<=:now
ORDER BY job_due_ms,id LIMIT :admitted_rows;
```

Reclaimer has its own receipt/incarnation/sequence. Historical image/fragment/change result IDs have no resource FKs. Its live_publication_id is detached only in the final physically complete image-retirement command; publication_id and its unacknowledged receipt survive. Native delete reads retained old strings before deleting source rows. Publisher document_id changes cannot strand retirement.

User-facing listening/Discover/music charts read only own-play counters under current personal epoch. UTCquarter/day/month rows provide exact historical windows; fine instants handle started/completed edges and quarter-contained exact spans handle played-time edges. Counter selection never rounds the requested millisecond cutoff. Current artist/album attribution is joined at read, with current permission and retained identity/merge semantics. Profile totals avoid scanning Ditems for a scalar timeline; per-item attribution/ranking uses the item relation under the read work budget. No item/provider change fans out per user.

```sql
SELECT b.bucket_ms,b.started_count,b.completed_count,b.played_ms
FROM personal_activity_revisions p JOIN listening_profile_buckets b
  ON b.profile_id=p.profile_id AND b.activity_generation=p.activity_generation
WHERE p.profile_id=:personal_key AND b.grain=:grain
  AND b.bucket_ms>=:from_ms AND b.bucket_ms<:until_ms
ORDER BY b.bucket_ms LIMIT :bounded_positions;
SELECT started_ms,subject_id,started_count,completed_count
FROM listening_play_instants
WHERE profile_id=:personal_key AND activity_generation=:generation
  AND started_ms>=:partial_quarter_from AND started_ms<:partial_quarter_until
ORDER BY started_ms,subject_id LIMIT :admitted_rows;
```

Any cursor over tied instant rows uses equality-tail then next-time streams; no row-value comparisons. All-time output uses existing reportwindow's<=1201months with exact timezone/DST boundary corrections. No fixed lifetime retention or approximate counts. Read/VM/deadline exhaustion refuses instead of returning a partial total.

**R-23 complete admin cohort streams.** Six admin paths ship: three event-time paging paths plus global/account/item start-cohort paths. The report below is ordered by start, independent of occurred_at. Cohort and bucket windows are both mandatory. Current admin/audience authority is checked before disclosure. Scope is a closed compiled variant; user text is a bound value, never SQL. Every non-index eligibility test is a CASE projection: unmatched raw candidates still count against LIMIT and the overall candidate/VM/deadline budget. Started metrics have no bucket requirement. Time metrics use the independently required bucket interval. Fully consumed candidate pages combine into finite report bins outside the writer, with no global SQL GROUP BY/sort or retained snapshot. Refuse incomplete totals. Every keyset uses equality-tail then next-start with a stable revision fence and <=1s snapshot. These are static contracts, not a runtime adapter qualification.

Bound fixture global ASC equal; cohort/bucket2026-05-01..02UTC, text needle, limit4. Expected indexed SEARCH through play_history_admin_start, no USE TEMP B-TREE.

```sql
SELECT id,started_ms,CASE WHEN event_kind='time' AND time_bucket_ms>=1777593600000 AND time_bucket_ms<1777680000000 AND library_public_id='lib' AND item_kind=7 AND ('needle'='' OR instr(lower(item_title),'needle')>0 OR instr(lower(user_name),'needle')>0 OR instr(lower(device_name),'needle')>0) THEN played_ms ELSE 0 END AS eligible_ms,CASE WHEN event_kind='started' AND library_public_id='lib' AND item_kind=7 AND ('needle'='' OR instr(lower(item_title),'needle')>0 OR instr(lower(user_name),'needle')>0 OR instr(lower(device_name),'needle')>0) THEN 1 ELSE 0 END AS eligible_play FROM play_history INDEXED BY play_history_admin_start WHERE started_ms IS NOT NULL AND started_ms>=1777593600000 AND started_ms<1777680000000 AND started_ms=1777593601000 AND id>0 ORDER BY started_ms ASC,id ASC LIMIT 4;
```

Bound fixture global ASC next; cohort/bucket2026-05-01..02UTC, text needle, limit4. Expected indexed SEARCH through play_history_admin_start, no USE TEMP B-TREE.

```sql
SELECT id,started_ms,CASE WHEN event_kind='time' AND time_bucket_ms>=1777593600000 AND time_bucket_ms<1777680000000 AND library_public_id='lib' AND item_kind=7 AND ('needle'='' OR instr(lower(item_title),'needle')>0 OR instr(lower(user_name),'needle')>0 OR instr(lower(device_name),'needle')>0) THEN played_ms ELSE 0 END AS eligible_ms,CASE WHEN event_kind='started' AND library_public_id='lib' AND item_kind=7 AND ('needle'='' OR instr(lower(item_title),'needle')>0 OR instr(lower(user_name),'needle')>0 OR instr(lower(device_name),'needle')>0) THEN 1 ELSE 0 END AS eligible_play FROM play_history INDEXED BY play_history_admin_start WHERE started_ms IS NOT NULL AND started_ms>=1777593600000 AND started_ms<1777680000000 AND started_ms>1777593600999 ORDER BY started_ms ASC,id ASC LIMIT 4;
```

Bound fixture global DESC equal; cohort/bucket2026-05-01..02UTC, text needle, limit4. Expected indexed SEARCH through play_history_admin_start, no USE TEMP B-TREE.

```sql
SELECT id,started_ms,CASE WHEN event_kind='time' AND time_bucket_ms>=1777593600000 AND time_bucket_ms<1777680000000 AND library_public_id='lib' AND item_kind=7 AND ('needle'='' OR instr(lower(item_title),'needle')>0 OR instr(lower(user_name),'needle')>0 OR instr(lower(device_name),'needle')>0) THEN played_ms ELSE 0 END AS eligible_ms,CASE WHEN event_kind='started' AND library_public_id='lib' AND item_kind=7 AND ('needle'='' OR instr(lower(item_title),'needle')>0 OR instr(lower(user_name),'needle')>0 OR instr(lower(device_name),'needle')>0) THEN 1 ELSE 0 END AS eligible_play FROM play_history INDEXED BY play_history_admin_start WHERE started_ms IS NOT NULL AND started_ms>=1777593600000 AND started_ms<1777680000000 AND started_ms=1777593601000 AND id<9223372036854775807 ORDER BY started_ms DESC,id DESC LIMIT 4;
```

Bound fixture global DESC next; cohort/bucket2026-05-01..02UTC, text needle, limit4. Expected indexed SEARCH through play_history_admin_start, no USE TEMP B-TREE.

```sql
SELECT id,started_ms,CASE WHEN event_kind='time' AND time_bucket_ms>=1777593600000 AND time_bucket_ms<1777680000000 AND library_public_id='lib' AND item_kind=7 AND ('needle'='' OR instr(lower(item_title),'needle')>0 OR instr(lower(user_name),'needle')>0 OR instr(lower(device_name),'needle')>0) THEN played_ms ELSE 0 END AS eligible_ms,CASE WHEN event_kind='started' AND library_public_id='lib' AND item_kind=7 AND ('needle'='' OR instr(lower(item_title),'needle')>0 OR instr(lower(user_name),'needle')>0 OR instr(lower(device_name),'needle')>0) THEN 1 ELSE 0 END AS eligible_play FROM play_history INDEXED BY play_history_admin_start WHERE started_ms IS NOT NULL AND started_ms>=1777593600000 AND started_ms<1777680000000 AND started_ms<1777593601001 ORDER BY started_ms DESC,id DESC LIMIT 4;
```

Bound fixture account ASC equal; cohort/bucket2026-05-01..02UTC, text needle, limit4. Expected indexed SEARCH through play_history_admin_user_start, no USE TEMP B-TREE.

```sql
SELECT id,started_ms,CASE WHEN event_kind='time' AND time_bucket_ms>=1777593600000 AND time_bucket_ms<1777680000000 AND library_public_id='lib' AND item_kind=7 AND ('needle'='' OR instr(lower(item_title),'needle')>0 OR instr(lower(user_name),'needle')>0 OR instr(lower(device_name),'needle')>0) THEN played_ms ELSE 0 END AS eligible_ms,CASE WHEN event_kind='started' AND library_public_id='lib' AND item_kind=7 AND ('needle'='' OR instr(lower(item_title),'needle')>0 OR instr(lower(user_name),'needle')>0 OR instr(lower(device_name),'needle')>0) THEN 1 ELSE 0 END AS eligible_play FROM play_history INDEXED BY play_history_admin_user_start WHERE account_key='r23-account' AND started_ms IS NOT NULL AND started_ms>=1777593600000 AND started_ms<1777680000000 AND started_ms=1777593601000 AND id>0 ORDER BY started_ms ASC,id ASC LIMIT 4;
```

Bound fixture account ASC next; cohort/bucket2026-05-01..02UTC, text needle, limit4. Expected indexed SEARCH through play_history_admin_user_start, no USE TEMP B-TREE.

```sql
SELECT id,started_ms,CASE WHEN event_kind='time' AND time_bucket_ms>=1777593600000 AND time_bucket_ms<1777680000000 AND library_public_id='lib' AND item_kind=7 AND ('needle'='' OR instr(lower(item_title),'needle')>0 OR instr(lower(user_name),'needle')>0 OR instr(lower(device_name),'needle')>0) THEN played_ms ELSE 0 END AS eligible_ms,CASE WHEN event_kind='started' AND library_public_id='lib' AND item_kind=7 AND ('needle'='' OR instr(lower(item_title),'needle')>0 OR instr(lower(user_name),'needle')>0 OR instr(lower(device_name),'needle')>0) THEN 1 ELSE 0 END AS eligible_play FROM play_history INDEXED BY play_history_admin_user_start WHERE account_key='r23-account' AND started_ms IS NOT NULL AND started_ms>=1777593600000 AND started_ms<1777680000000 AND started_ms>1777593600999 ORDER BY started_ms ASC,id ASC LIMIT 4;
```

Bound fixture account DESC equal; cohort/bucket2026-05-01..02UTC, text needle, limit4. Expected indexed SEARCH through play_history_admin_user_start, no USE TEMP B-TREE.

```sql
SELECT id,started_ms,CASE WHEN event_kind='time' AND time_bucket_ms>=1777593600000 AND time_bucket_ms<1777680000000 AND library_public_id='lib' AND item_kind=7 AND ('needle'='' OR instr(lower(item_title),'needle')>0 OR instr(lower(user_name),'needle')>0 OR instr(lower(device_name),'needle')>0) THEN played_ms ELSE 0 END AS eligible_ms,CASE WHEN event_kind='started' AND library_public_id='lib' AND item_kind=7 AND ('needle'='' OR instr(lower(item_title),'needle')>0 OR instr(lower(user_name),'needle')>0 OR instr(lower(device_name),'needle')>0) THEN 1 ELSE 0 END AS eligible_play FROM play_history INDEXED BY play_history_admin_user_start WHERE account_key='r23-account' AND started_ms IS NOT NULL AND started_ms>=1777593600000 AND started_ms<1777680000000 AND started_ms=1777593601000 AND id<9223372036854775807 ORDER BY started_ms DESC,id DESC LIMIT 4;
```

Bound fixture account DESC next; cohort/bucket2026-05-01..02UTC, text needle, limit4. Expected indexed SEARCH through play_history_admin_user_start, no USE TEMP B-TREE.

```sql
SELECT id,started_ms,CASE WHEN event_kind='time' AND time_bucket_ms>=1777593600000 AND time_bucket_ms<1777680000000 AND library_public_id='lib' AND item_kind=7 AND ('needle'='' OR instr(lower(item_title),'needle')>0 OR instr(lower(user_name),'needle')>0 OR instr(lower(device_name),'needle')>0) THEN played_ms ELSE 0 END AS eligible_ms,CASE WHEN event_kind='started' AND library_public_id='lib' AND item_kind=7 AND ('needle'='' OR instr(lower(item_title),'needle')>0 OR instr(lower(user_name),'needle')>0 OR instr(lower(device_name),'needle')>0) THEN 1 ELSE 0 END AS eligible_play FROM play_history INDEXED BY play_history_admin_user_start WHERE account_key='r23-account' AND started_ms IS NOT NULL AND started_ms>=1777593600000 AND started_ms<1777680000000 AND started_ms<1777593601001 ORDER BY started_ms DESC,id DESC LIMIT 4;
```

Bound fixture item ASC equal; cohort/bucket2026-05-01..02UTC, text needle, limit4. Expected indexed SEARCH through play_history_admin_item_start, no USE TEMP B-TREE.

```sql
SELECT id,started_ms,CASE WHEN event_kind='time' AND time_bucket_ms>=1777593600000 AND time_bucket_ms<1777680000000 AND library_public_id='lib' AND item_kind=7 AND ('needle'='' OR instr(lower(item_title),'needle')>0 OR instr(lower(user_name),'needle')>0 OR instr(lower(device_name),'needle')>0) THEN played_ms ELSE 0 END AS eligible_ms,CASE WHEN event_kind='started' AND library_public_id='lib' AND item_kind=7 AND ('needle'='' OR instr(lower(item_title),'needle')>0 OR instr(lower(user_name),'needle')>0 OR instr(lower(device_name),'needle')>0) THEN 1 ELSE 0 END AS eligible_play FROM play_history INDEXED BY play_history_admin_item_start WHERE subject_id=1 AND started_ms IS NOT NULL AND started_ms>=1777593600000 AND started_ms<1777680000000 AND started_ms=1777593601000 AND id>0 ORDER BY started_ms ASC,id ASC LIMIT 4;
```

Bound fixture item ASC next; cohort/bucket2026-05-01..02UTC, text needle, limit4. Expected indexed SEARCH through play_history_admin_item_start, no USE TEMP B-TREE.

```sql
SELECT id,started_ms,CASE WHEN event_kind='time' AND time_bucket_ms>=1777593600000 AND time_bucket_ms<1777680000000 AND library_public_id='lib' AND item_kind=7 AND ('needle'='' OR instr(lower(item_title),'needle')>0 OR instr(lower(user_name),'needle')>0 OR instr(lower(device_name),'needle')>0) THEN played_ms ELSE 0 END AS eligible_ms,CASE WHEN event_kind='started' AND library_public_id='lib' AND item_kind=7 AND ('needle'='' OR instr(lower(item_title),'needle')>0 OR instr(lower(user_name),'needle')>0 OR instr(lower(device_name),'needle')>0) THEN 1 ELSE 0 END AS eligible_play FROM play_history INDEXED BY play_history_admin_item_start WHERE subject_id=1 AND started_ms IS NOT NULL AND started_ms>=1777593600000 AND started_ms<1777680000000 AND started_ms>1777593600999 ORDER BY started_ms ASC,id ASC LIMIT 4;
```

Bound fixture item DESC equal; cohort/bucket2026-05-01..02UTC, text needle, limit4. Expected indexed SEARCH through play_history_admin_item_start, no USE TEMP B-TREE.

```sql
SELECT id,started_ms,CASE WHEN event_kind='time' AND time_bucket_ms>=1777593600000 AND time_bucket_ms<1777680000000 AND library_public_id='lib' AND item_kind=7 AND ('needle'='' OR instr(lower(item_title),'needle')>0 OR instr(lower(user_name),'needle')>0 OR instr(lower(device_name),'needle')>0) THEN played_ms ELSE 0 END AS eligible_ms,CASE WHEN event_kind='started' AND library_public_id='lib' AND item_kind=7 AND ('needle'='' OR instr(lower(item_title),'needle')>0 OR instr(lower(user_name),'needle')>0 OR instr(lower(device_name),'needle')>0) THEN 1 ELSE 0 END AS eligible_play FROM play_history INDEXED BY play_history_admin_item_start WHERE subject_id=1 AND started_ms IS NOT NULL AND started_ms>=1777593600000 AND started_ms<1777680000000 AND started_ms=1777593601000 AND id<9223372036854775807 ORDER BY started_ms DESC,id DESC LIMIT 4;
```

Bound fixture item DESC next; cohort/bucket2026-05-01..02UTC, text needle, limit4. Expected indexed SEARCH through play_history_admin_item_start, no USE TEMP B-TREE.

```sql
SELECT id,started_ms,CASE WHEN event_kind='time' AND time_bucket_ms>=1777593600000 AND time_bucket_ms<1777680000000 AND library_public_id='lib' AND item_kind=7 AND ('needle'='' OR instr(lower(item_title),'needle')>0 OR instr(lower(user_name),'needle')>0 OR instr(lower(device_name),'needle')>0) THEN played_ms ELSE 0 END AS eligible_ms,CASE WHEN event_kind='started' AND library_public_id='lib' AND item_kind=7 AND ('needle'='' OR instr(lower(item_title),'needle')>0 OR instr(lower(user_name),'needle')>0 OR instr(lower(device_name),'needle')>0) THEN 1 ELSE 0 END AS eligible_play FROM play_history INDEXED BY play_history_admin_item_start WHERE subject_id=1 AND started_ms IS NOT NULL AND started_ms>=1777593600000 AND started_ms<1777680000000 AND started_ms<1777593601001 ORDER BY started_ms DESC,id DESC LIMIT 4;
```

Production bindings replace only the fixture literals: `:cohort_from`, `:cohort_until`, `:bucket_from`, `:bucket_until`, `:text_fold`, `:library`, `:item_kind`, `:account` or `:subject`, `:after_start`, `:after_id`, `:admitted_rows`. Cursor values are not windows. Start-play eligibility checks kind, library and historical strings; it never requires time_bucket_ms. Time eligibility additionally checks bucket_from<=time_bucket_ms<bucket_until. No occurred_at predicate may exclude late-recorded facts. No eligibility predicate may migrate into WHERE without a new bounded-work proof and HN golden. Initial page uses next-key form with an explicit boundary variant; never subtract one from minimum timestamps in application code. Tied-group, nonmatch, late-record and missing-index controls live in INT-CHANGE-001-R23-R24-CONTROLS.json.

**R-24 exact played-time edge.** For edge[a,b), quarter containment makes `[max(0,a-900000),b)` a complete from_ms candidate range. Stream raw rows (including nonoverlaps) and clip with checked integer arithmetic outside SQL. Sum independent occurrences through copies, not a union across devices. No double-count between these edges and complete coarse blocks. Current epoch/pending/revision/permission preflight precedes a complete result; overlapping undrained source means Pending/refusal, not partial exactness.

```sql
-- Equality tail, ascending. Descending uses id<:after_id and DESC.
SELECT id,from_ms,until_ms,copies,subject_id
FROM listening_time_spans INDEXED BY listening_time_span_profile
WHERE profile_id=:profile AND activity_generation=:epoch
  AND from_ms>=:lookback AND from_ms<:until
  AND from_ms=:after_from AND id>:after_id
ORDER BY from_ms,id LIMIT :admitted_rows;
-- Next keys; descending uses from_ms<:after_from and DESC.
SELECT id,from_ms,until_ms,copies,subject_id
FROM listening_time_spans INDEXED BY listening_time_span_profile
WHERE profile_id=:profile AND activity_generation=:epoch
  AND from_ms>=:lookback AND from_ms<:until AND from_ms>:after_from
ORDER BY from_ms,id LIMIT :admitted_rows;
-- One indexed adjacent-tail candidate, including copies!=1; no hidden filter scan.
-- Extend only if that one candidate has copies=1, else exact-geometry UPSERT.
SELECT id,from_ms,copies FROM listening_time_spans INDEXED BY listening_time_span_identity
WHERE profile_id=:profile AND activity_generation=:epoch AND subject_id=:subject
  AND until_ms=:piece_from AND from_ms>=:quarter_start
ORDER BY from_ms DESC LIMIT 1;
-- Pending exact-chart preflight: consume raw candidates and test until>from,
-- cursor<until. Eligibility must not hide unbounded scans before LIMIT.
SELECT playback_id,pending_time_cursor_ms,pending_time_until_ms
FROM playback_personal_claims INDEXED BY playback_time_pending
WHERE profile_key=:profile AND pending_time_activity_generation=:epoch
  AND pending_time_from_ms IS NOT NULL AND pending_time_cursor_ms<:until
ORDER BY pending_time_cursor_ms,playback_id LIMIT :admitted_rows;
```

Pending preflight keysets split tied cursor values by playback_id with the exact BINARY collation. One drain piece ends at min(pending_until,nextUTCquarter(cursor)). It changes at most one span plus six counter rows, one claim cursor and the complete admitted receipt/audit/revision effect set. A piece receipt never claims the whole report. Refusal/ambiguous COMMIT preserves the exact captured remainder. See INT-CHANGE-001 for long-session, gaps, clock discontinuity, overlap, forever retention and growth. All48actual reportwindow intervals from R are inputs to exact-overlap controls, including Monrovia's fractional boundary and a negative preepoch window start. Producer/runtime work remains unqualified.

---

# Adopted Change002 static statement contracts

Adopted5f3c7f676ba3e751487ab92a1fced313a583df49eedbe5c2e438031574418a73. Lead adopted this exact schema after R agreement; product embedding follows the canonical PR. Exact predicate authority INT-INDEX-POLICY-CHANGE-002.json; original001map remains unchanged. All pages use two bounded streams, never row-value tuples; snapshot/deadline/resource budgets remain binding. These statements show access, not a complete W source/authority/receipt executor.

```sql
-- Current detail, same-owner head followed by image-leading ordered PK.
SELECT c.ord,c.person_id,c.role_id,c.department_id,c.credited_name,c.source_row_id,
       c.owner_entry_id,c.owner_incarnation,c.owner_revision,c.source_status
FROM catalog_entities e JOIN catalog_credits c
  ON c.image_id=e.credit_image_id AND c.entity_id=e.id
WHERE e.id=:entity AND e.retired=0 ORDER BY c.ord LIMIT :admitted_rows;
-- Reverse person, equality tail. Next stream has entity_id>:entity and
-- ORDER BY entity_id,ord,image_id; tied ord/image keys use nested streams.
SELECT c.entity_id,c.ord,c.image_id
FROM catalog_credits c INDEXED BY catalog_credits_person
JOIN catalog_entities e ON e.id=c.entity_id AND e.credit_image_id=c.image_id
WHERE e.retired=0 AND c.person_id=:person AND c.entity_id=:entity AND c.ord>:after_ord
ORDER BY c.ord,c.image_id LIMIT :admitted_rows;
-- No legacy current owner scan or entry-only historical join survives cutover.
SELECT o.entry_id,o.position,o.operation,o.source_row_id,o.credited_name,o.role,o.department
FROM catalog_entities e JOIN credit_owner_entries o
 ON o.entity_id=e.id AND o.owner_incarnation=e.metadata_incarnation
 AND o.owner_revision=e.credit_owner_revision
WHERE e.id=:entity ORDER BY o.position LIMIT :admitted_rows;
SELECT o.entry_id,o.entity_id,o.owner_incarnation,o.owner_revision,o.position,
       o.input_ordinal,o.operation,o.source_row_id,o.credited_name,o.role,o.department
FROM credit_owner_entries o INDEXED BY credit_owner_entries_entity_id
WHERE o.entry_id=:entry AND o.entity_id=:entity
 AND o.owner_incarnation=:owner_incarnation AND o.owner_revision=:owner_revision;
-- Captured owner-cohort pins: stream raw candidates; test held/current or
-- active staging intent in caller. Never hide arbitrary nonmatches before LIMIT.
SELECT h.id,h.reclaim_state
FROM catalog_credit_images h INDEXED BY catalog_credit_images_entity
WHERE h.entity_id=:entity AND h.entity_incarnation=:owner_incarnation
 AND h.owner_revision=:owner_revision AND h.id>:after_image
ORDER BY h.id LIMIT :admitted_rows;
SELECT id FROM metadata_provider_operations INDEXED BY metadata_operation_credit_image
WHERE credit_image_id IS NOT NULL AND credit_image_id=:image LIMIT 1;
-- Due and ordinal staging/seal/reclaim paths.
SELECT id FROM catalog_credit_images INDEXED BY catalog_credit_images_due
WHERE job_due_ms IS NOT NULL AND job_due_ms<=:now
ORDER BY job_due_ms,id LIMIT :admitted_rows;
SELECT c.ord,c.person_id,c.role_id,c.department_id,c.credited_name,c.provider,
       c.credit_id,c.provider_person_id,c.source_ordinal,c.source_row_id,
       c.owner_entry_id,c.owner_incarnation,c.owner_revision,c.source_status
FROM catalog_credits c WHERE image_id=:image AND ord>:after_ord
ORDER BY ord LIMIT :admitted_rows;
```

Reverse reads must not return retired/staged postings. Their candidate work must remain bounded even with a rare-person mostly-retired negative; SEARCH alone is insufficient. A successful complete page needs its candidate/revision fence and no missed current rows. If post-join filtering could hide arbitrarily many candidates, use raw candidate streams with a CASE/current-head marker and count every row against the budget, or prove a bounded posting-debt invariant. No successful partial page/count is allowed. HN/QL qualify the final chosen production form; tiny two-image plans do not establish that bound.

Reclaimer checks same transaction: image is neither live head nor a current provider OR repair build pointer; held/staged candidates are not re-adoptable once queued; exact header nonce/revision/cursor/lease and prior receipt/floor. Each admitted ord chunk, remaining counts, cursor/revision and typed receipt commit atomically. Final resource retirement preserves header until last ack. Header lookup/deletion and owner-source cleanup use real FK child plans; no source/owner/live cascade is used to drain images. Retained owner-source cleanup independently checks current owner head, all accepted and still-needed staged image/source/undo pins, with bounded raw source rows and refusal on unknown proof.

Exact literal fixture goldens/results for detail, reverse, current owner ordering, captured cohort pin, staging-intent and due/ord retirement are in INT-CHANGE-002-{CONTROLS,RESULTS}.json. Complete privilege/source/lease/manifest validation belongs to W's closed command and is not inferred from these reduced forms. Both directions/tied tails/missing-index and worst-backlog qualification remain required HN/QL gates before release.

## Actual single-target repair input and receipts

The same closed intent/predicate comes from INT-INDEX-POLICY-CHANGE-002.json. These are bound access probes, not permission to omit current authority, exact whole-input verification, one-unresolved rule or resource forecasts. Current permission precedes historical disclosure; exact historical receipt lookup precedes any stale source/lease refusal. Missing above-floor result fails closed.

```sql
-- Discovery of the SAME original stable identity, not permission to recreate.
SELECT id,command_incarnation,last_command_sequence,acknowledged_command_sequence
FROM metadata_repair_cascades INDEXED BY metadata_repair_credit_request
WHERE actor=:actor AND entity_id=:entity AND base_revision=:base_revision
  AND credit_request_digest=:original_request_digest
  AND intent='credit-owner-single-v1'
  AND credit_phase IN('credit_capture','credit_prepare','credit_stage','credit_seal','credit_ready','credit_published')
LIMIT 1;
-- Own actual-driver historical result: no join to a mutable resource.
SELECT command_kind,accepted_revision,owner_revision,after_position,
       captured_entries,captured_bytes,verified_entries,verified_bytes,chain,
       input_complete,image_id,image_revision,owner_manifest_digest,image_manifest_digest,
       result_from_position,result_until_position,result_count,result_digest
FROM metadata_repair_command_receipts
WHERE repair_id=:repair AND command_incarnation=:nonce AND command_sequence=:sequence;
-- W owns reservation; creation + prospective pin + begin receipt are one command.
UPDATE catalog_entities SET credit_owner_next_revision=credit_owner_next_revision+1
WHERE id=:entity AND retired=0 AND metadata_incarnation=:captured_incarnation
  AND credit_owner_revision=:base_owner_revision
  AND credit_owner_next_revision=:reserved_revision
  AND credit_owner_next_revision<9223372036854775807;
-- Direct capture pin, including no rows or no image, one exact cohort.
SELECT id,credit_phase,credit_cohort_pin,last_command_sequence,acknowledged_command_sequence
FROM metadata_repair_cascades INDEXED BY metadata_repair_credit_cohort
WHERE entity_id=:entity AND credit_owner_incarnation=:incarnation
  AND credit_target_owner_revision=:owner_revision LIMIT 1;
-- Repair image pin must be checked together with provider pin and current head.
SELECT id,credit_phase FROM metadata_repair_cascades INDEXED BY metadata_repair_credit_image
WHERE credit_image_id IS NOT NULL AND credit_image_id=:image LIMIT 1;
-- Ordered whole-normalized-input seal stream; resume from position, no row tuple.
SELECT position,input_ordinal,entry_id,operation,source_row_id,credited_name,role,department
FROM credit_owner_entries
WHERE entity_id=:entity AND owner_incarnation=:incarnation
  AND owner_revision=:owner_revision AND position>:after_position
ORDER BY position LIMIT :admitted_rows;
-- Original explicit input allocation map, pinned until source-dependent ACK.
SELECT input_ordinal,entry_id,position FROM credit_owner_entries
INDEXED BY credit_owner_entries_input
WHERE entity_id=:entity AND owner_incarnation=:incarnation
  AND owner_revision=:owner_revision AND input_ordinal IS NOT NULL
  AND input_ordinal>:after_input
ORDER BY input_ordinal LIMIT :admitted_rows;
```

The source-reclaim command separately probes current live owner head, this direct repair pin, all exact captured image cohorts (held/retiring headers remain custody), provider/repair candidates and actual undo/history retention. Unknown proof refuses. It never deletes a source cohort because an image is merely off-head. Image reclaim tests both families in the same W transaction; cross-family exclusivity is a W invariant, not two separate UNIQUE indexes. Supersession resolves the prior receipt first, then atomically detaches eligible pins and queues discoverable debt. No re-adoption of retiring candidates.

At cutover, old descendant paths EXCLUDE `intent='credit-owner-single-v1'`: repair_relationships.go:304 active descendant admission, :310 terminal prune, :329 pending worker selection, repair_cascade_completion.go:153 zero-child completion. The new actual driver schedules by its own phase, pins, G state and unacknowledged result. General root-list status/permission paths dispatch the closed branch. A legacy count of zero descendants cannot prove this operation complete. These are required implementation changes, not accepting runtime SQL already shipped by this packet.

Reduced literal goldens in INT-CHANGE-002-CONTROLS.json assert indexed request discovery, exact cohort pin, repair image pin and immutable receipt point lookup, in addition to effective detail/reverse/retirement paths. Complete mutation contracts remain in INT-CHANGE-002.md; HN must register the real fully bound W statements and all ASC/DESC/tie/negative plans. The tiny relational fixtures do not qualify sparse/retired posting debt, native cryptographic proof, crash or command holds.
