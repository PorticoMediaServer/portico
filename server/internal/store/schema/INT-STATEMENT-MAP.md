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

Current change-controlled candidate SHA5785482ac840ebbfa456f9a805b0ff2f23b964ca9b5cd3c1e1e3913674088744; R review pending. See phase1/INT-CHANGE-001.md and PB-MANIFEST-V1.md. These are static statement contracts for HN/W, not runtime command implementations or an authority grant.

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

User-facing listening/Discover/music charts read only own-play counters under current personal epoch. UTCquarter/day/month rows provide exact historical windows; fine instants handle the two partial quarter edges of rolling30days. Counter selection never rounds the requested millisecond cutoff. Current artist/album attribution is joined at read, with current permission and retained identity/merge semantics. Profile totals avoid scanning Ditems for a scalar timeline; per-item attribution/ranking uses the item relation under the read work budget. No item/provider change fans out per user.

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

**Admin-only history** keeps the raw start-window×time-bucket and historical title/user/device-string semantics. Each request requires a time window, current admin/audience authority, bounded indexed keyset pages and the snapshot/deadline cap. The three existing admin time/user/item indexes serve the corresponding static variants. String search is a bounded scan within that indexed window, not a global string index or a user-chart path. Literal predicates below are compiled variants, never assembled from untrusted text. Time facts capture started_ms,time_bucket_ms and exact played_ms; append-only time events never update previous events.

```sql
-- Descending equality tail. The next stream uses occurred_at<:after_at instead.
SELECT id,occurred_at,started_ms,time_bucket_ms,played_ms,event_kind,
       user_name,profile_name,device_name,item_title
FROM play_history INDEXED BY play_history_admin_time
WHERE occurred_at>=:window_from AND occurred_at<:window_until
  AND occurred_at=:after_at AND id<:after_id
  AND started_ms>=:cohort_from AND started_ms<:cohort_until
  AND (instr(lower(item_title),:text)>0 OR instr(lower(user_name),:text)>0
       OR instr(lower(device_name),:text)>0)
ORDER BY occurred_at DESC,id DESC LIMIT :admitted_rows;
```

The typed event writer encodes occurred_at in fixed-width UTCnanoseconds and captures a time event at the logical bucket/time boundary so its required admin time path is complete. W/V must qualify interrupted, late/replayed and cross-midnight/DST donations; schema shape alone does not establish that producer contract. Admin report aggregation processes bounded pages and can refuse on its overall budget; no retained cursor/snapshot across pages or a UI event-log escape.
