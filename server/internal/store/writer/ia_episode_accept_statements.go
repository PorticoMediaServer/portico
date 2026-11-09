package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// IA-11 statement family. Every statement is a point read, a PK-range read or
// one finite insert/CAS on the exact group C draft relations. There is no
// OFFSET, temporary sort, library-wide scan or caller-selected SQL. The typed
// member cohort is read only through its own receipt/kind prefix.
const (
	iaEpisodeOwner                stmt.ID = "ia.episode.owner"
	iaEpisodeHeader               stmt.ID = "ia.episode.header"
	iaEpisodeMemberBytes          stmt.ID = "ia.episode.member_bytes"
	iaEpisodeMembers              stmt.ID = "ia.episode.members"
	iaEpisodeLibrary              stmt.ID = "ia.episode.library"
	iaEpisodeSource               stmt.ID = "ia.episode.source"
	iaEpisodeDirectory            stmt.ID = "ia.episode.directory"
	iaEpisodeAsset                stmt.ID = "ia.episode.asset"
	iaEpisodeIdentity             stmt.ID = "ia.episode.identity"
	iaEpisodeEntity               stmt.ID = "ia.episode.entity"
	iaEpisodeDirectoryCAS         stmt.ID = "ia.episode.directory_cas"
	iaEpisodeRunCAS               stmt.ID = "ia.episode.run_cas"
	iaEpisodeContainer            stmt.ID = "ia.episode.container"
	iaEpisodeContainerAdvance     stmt.ID = "ia.episode.container_advance"
	iaEpisodeContainerAdvanceNull stmt.ID = "ia.episode.container_advance_null"
	iaEpisodeSubjectInsert        stmt.ID = "ia.episode.subject_insert"
	iaEpisodeIdentityInsert       stmt.ID = "ia.episode.identity_insert"
	iaEpisodeEntityInsert         stmt.ID = "ia.episode.entity_insert"
	iaEpisodeLinkInsert           stmt.ID = "ia.episode.link_insert"
	iaEpisodeContributionInsert   stmt.ID = "ia.episode.contribution_insert"
	iaEpisodeSearchHead           stmt.ID = "ia.episode.search_head"
	iaEpisodeSearchBank           stmt.ID = "ia.episode.search_bank"
	iaEpisodeSearchExisting       stmt.ID = "ia.episode.search_existing"
	iaEpisodeSearchInsert         stmt.ID = "ia.episode.search_insert"
	iaEpisodeSearchPending        stmt.ID = "ia.episode.search_pending"
	iaEpisodeSearchSequence       stmt.ID = "ia.episode.search_sequence"
	iaEpisodeSearchBuilding       stmt.ID = "ia.episode.search_building"
	iaEpisodeReceiptInsert        stmt.ID = "ia.episode.receipt_insert"
	iaEpisodeMemberInsert         stmt.ID = "ia.episode.member_insert"
)

const iaEpisodeReceiptKind = "episode_inventory_accept"

func iaEpisodeStatements() []stmt.Definition {
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	r := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Real} }
	nt := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text, Nullable: true} }
	ni := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer, Nullable: true} }
	nr := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Real, Nullable: true} }
	nb := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob, Nullable: true} }
	boolean := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Boolean} }
	inc, operation, digest := make([]byte, 16), make([]byte, 16), make([]byte, 32)
	key := []stmt.Param{t("run_id"), b("command_incarnation"), i("command_sequence")}
	defs := []stmt.Definition{
		{ID: iaEpisodeOwner, Kind: stmt.Read,
			SQL:    `SELECT command_incarnation,last_command_sequence,acknowledged_command_sequence FROM inventory_runs WHERE job_id=?`,
			Params: []stmt.Param{t("run_id")},
			Cohorts: []stmt.Cohort{
				{Name: "current_owner", Bindings: []any{"episode-run"}},
				{Name: "missing_owner", Bindings: []any{"episode-missing"}},
			}},
		{ID: iaEpisodeHeader, Kind: stmt.Read,
			SQL: `SELECT operation_id,request_digest,command_kind,result_count,result_digest,accepted_revision,applied_ms,source_id,source_incarnation,
ia_media_result_version,ia_media_member_bytes,ia_media_object_id,ia_media_object_revision,ia_media_cursor_kind,
ia_media_directory_path,ia_media_directory_identity,ia_media_directory_revision,ia_media_expected_payload,ia_media_next_payload
FROM scan_command_receipts WHERE run_id=? AND command_incarnation=? AND command_sequence=?`,
			Params: key,
			Cohorts: []stmt.Cohort{
				{Name: "saved_episode_receipt", Bindings: []any{"episode-run", inc, int64(2)}},
				{Name: "missing_receipt", Bindings: []any{"episode-missing", inc, int64(2)}},
			}},
		{ID: iaEpisodeMemberBytes, Kind: stmt.Read,
			SQL: `SELECT ordinal,retained_bytes FROM scan_media_result_members
WHERE run_id=? AND command_incarnation=? AND command_sequence=? AND command_kind='episode_inventory_accept'
ORDER BY ordinal LIMIT ?`,
			Params: append(append([]stmt.Param{}, key...), i("row_limit")),
			Cohorts: []stmt.Cohort{
				{Name: "saved_member_bytes", Bindings: []any{"episode-run", inc, int64(2), int64(9)}},
				{Name: "missing_member_bytes", Bindings: []any{"episode-missing", inc, int64(2), int64(9)}},
			}},
		{ID: iaEpisodeMembers, Kind: stmt.Read,
			SQL: `SELECT ordinal,member_kind,library_id,subject_id,subject_kind,subject_revision,metadata_incarnation,
parent_id,parent_kind,ancestor_id,ancestor_kind,asset_id,asset_info_epoch,part_index,link_origin_revision,link_origin_incarnation,
edition,link_effect,cue_ordinal,start_seconds,end_seconds,episode_boundary_status,reading_format,reading_path_head,reading_path_sequence,extra_kind
FROM scan_media_result_members
WHERE run_id=? AND command_incarnation=? AND command_sequence=? AND command_kind='episode_inventory_accept'
ORDER BY ordinal LIMIT ?`,
			Params: append(append([]stmt.Param{}, key...), i("row_limit")),
			Cohorts: []stmt.Cohort{
				{Name: "saved_members", Bindings: []any{"episode-run", inc, int64(2), int64(9)}},
				{Name: "missing_members", Bindings: []any{"episode-missing", inc, int64(2), int64(9)}},
			}},
		{ID: iaEpisodeLibrary, Kind: stmt.Read,
			SQL:    `SELECT id FROM catalog_libraries WHERE id=? AND library_id=? AND retired=0`,
			Params: []stmt.Param{i("library_id"), t("library_key")},
			Cohorts: []stmt.Cohort{
				{Name: "live_library", Bindings: []any{int64(7), "episode-library"}},
				{Name: "retired_library", Bindings: []any{int64(8), "episode-retired"}},
			}},
		{ID: iaEpisodeSource, Kind: stmt.Read,
			SQL: `SELECT id FROM library_sources
WHERE id=? AND library_id=? AND generation=? AND incarnation=? AND root_identity=? AND root=? AND follow_symlinks=?
AND kind='local' AND classification='local' AND enabled=1 AND identity_confirmed=1 AND health='ready' AND remote_source_id IS NULL`,
			Params: []stmt.Param{t("source_id"), t("library_key"), i("generation"), t("incarnation"), t("root_identity"), t("resolved_root"), boolean("follow_symlinks")},
			Cohorts: []stmt.Cohort{
				{Name: "current_source", Bindings: []any{"episode-source", "episode-library", int64(3), "episode-incarnation", "episode-root-identity", "/media/episode", false}},
				{Name: "stale_source", Bindings: []any{"episode-source", "episode-library", int64(4), "episode-incarnation", "episode-root-identity", "/media/episode", false}},
			}},
		{ID: iaEpisodeDirectory, Kind: stmt.Read,
			SQL: `SELECT pages,skipped FROM inventory_directories
WHERE job_id=? AND relative_path=? AND identity=? AND revision=? AND cursor=? AND pages=? AND skipped=? AND state='pending'`,
			Params: []stmt.Param{t("run_id"), t("relative_path"), t("identity"), t("revision"), t("cursor"), i("pages"), i("skipped")},
			Cohorts: []stmt.Cohort{
				{Name: "pending_directory", Bindings: []any{"episode-run", ".", "episode-directory-identity", "episode-directory-revision", "", int64(2), int64(0)}},
				{Name: "stale_directory", Bindings: []any{"episode-run", ".", "episode-directory-identity", "episode-directory-revision", "9", int64(2), int64(0)}},
			}},
		{ID: iaEpisodeAsset, Kind: stmt.Read,
			SQL:    `SELECT id,token,info_epoch,file_size_revision FROM catalog_assets WHERE library_id=? AND path=? AND size=? AND modified_ns=? AND available=1`,
			Params: []stmt.Param{i("library_id"), t("path"), i("size"), i("modified_ns")},
			Cohorts: []stmt.Cohort{
				{Name: "observed_asset", Bindings: []any{int64(7), "/media/episode/Show S01E01.mkv", int64(42), int64(7)}},
				{Name: "changed_asset", Bindings: []any{int64(7), "/media/episode/Show S01E01.mkv", int64(43), int64(7)}},
			}},
		{ID: iaEpisodeIdentity, Kind: stmt.Read,
			SQL:    `SELECT subject_id FROM catalog_identities WHERE library_id=? AND source_key=?`,
			Params: []stmt.Param{i("library_id"), t("source_key")},
			Cohorts: []stmt.Cohort{
				{Name: "reused_identity", Bindings: []any{int64(7), "episode-show-key"}},
				{Name: "new_identity", Bindings: []any{int64(7), "episode-new-key"}},
			}},
		{ID: iaEpisodeEntity, Kind: stmt.Read,
			SQL:    `SELECT kind,parent_id,show_id,season_id,season_number,episode_number,retired,revision,metadata_incarnation FROM catalog_entities WHERE id=? AND library_id=?`,
			Params: []stmt.Param{i("subject_id"), i("library_id")},
			Cohorts: []stmt.Cohort{
				{Name: "reused_entity", Bindings: []any{int64(31), int64(7)}},
				{Name: "missing_entity", Bindings: []any{int64(32), int64(7)}},
			}},
		{ID: iaEpisodeDirectoryCAS, Kind: stmt.Write,
			SQL: `UPDATE inventory_directories INDEXED BY sqlite_autoindex_inventory_directories_1 SET cursor=?,pages=?
WHERE job_id=? AND relative_path=? AND identity=? AND revision=? AND cursor=? AND pages=? AND skipped=? AND state='pending'
RETURNING cursor,pages,skipped`,
			Params: []stmt.Param{t("next_cursor"), i("next_pages"), t("run_id"), t("relative_path"), t("identity"), t("revision"), t("cursor"), i("pages"), i("skipped")},
			Cohorts: []stmt.Cohort{
				{Name: "pending_to_next", Bindings: []any{"42", int64(3), "episode-run", ".", "episode-directory-identity", "episode-directory-revision", "", int64(2), int64(0)}},
				{Name: "stale_directory_cas", Bindings: []any{"42", int64(3), "episode-run", ".", "episode-directory-identity", "episode-directory-revision", "9", int64(2), int64(0)}},
			}},
		{ID: iaEpisodeRunCAS, Kind: stmt.Write,
			SQL: `UPDATE inventory_runs SET job_revision=?,last_command_sequence=?
WHERE job_id=? AND command_incarnation=? AND original_library_id=? AND original_library_key=? AND original_resolved_root=? AND original_follow_symlinks=?
AND source_id=? AND source_generation=? AND root_incarnation=? AND root_identity=? AND policy_revision=? AND phase=? AND scan_mode=?
AND job_lease_owner=? AND job_lease_generation=? AND job_lease_expires_ms=? AND job_revision=? AND last_command_sequence=? AND acknowledged_command_sequence=?
AND authoritative=0 AND root_rebound=0 AND finished_at=''
RETURNING job_revision,last_command_sequence`,
			Params: []stmt.Param{i("next_revision"), i("next_sequence"), t("run_id"), b("command_incarnation"), i("library_id"), t("library_key"),
				t("resolved_root"), boolean("follow_symlinks"), t("source_id"), i("generation"), t("incarnation"), t("root_identity"),
				i("policy_revision"), t("phase"), t("scan_mode"), i("lease_owner"), i("lease_generation"), i("lease_expires_ms"),
				i("run_revision"), i("last_sequence"), i("ack_sequence")},
			Cohorts: []stmt.Cohort{
				{Name: "actual_post_accept_revision", Bindings: []any{int64(6), int64(2), "episode-run", inc, int64(7), "episode-library", "/media/episode", false,
					"episode-source", int64(3), "episode-incarnation", "episode-root-identity", int64(4), "inventory", "first-import",
					int64(11), int64(13), int64(4000000000000), int64(5), int64(1), int64(1)}},
				{Name: "stale_run_cas", Bindings: []any{int64(6), int64(2), "episode-run", inc, int64(7), "episode-library", "/media/episode", false,
					"episode-source", int64(3), "episode-incarnation", "episode-root-identity", int64(4), "inventory", "first-import",
					int64(11), int64(13), int64(4000000000000), int64(5), int64(1), int64(0)}},
			}},
		{ID: iaEpisodeContainer, Kind: stmt.Read,
			SQL:    `SELECT last_content_added_at FROM catalog_entities WHERE id=? AND library_id=? AND kind=? AND retired=0`,
			Params: []stmt.Param{i("subject_id"), i("library_id"), i("kind")},
			Cohorts: []stmt.Cohort{
				{Name: "show_container", Bindings: []any{int64(31), int64(7), int64(2)}},
				{Name: "season_container", Bindings: []any{int64(32), int64(7), int64(3)}},
			}},
		{ID: iaEpisodeContainerAdvance, Kind: stmt.Write,
			SQL: `UPDATE catalog_entities SET last_content_added_at=?
WHERE id=? AND library_id=? AND kind=? AND retired=0 AND last_content_added_at=?
RETURNING last_content_added_at`,
			Params: []stmt.Param{t("last_content_added_at"), i("subject_id"), i("library_id"), i("kind"), nt("prior_content_added_at")},
			Cohorts: []stmt.Cohort{
				{Name: "older_to_accepted", Bindings: []any{"2026-11-08T00:00:00.000000000Z", int64(31), int64(7), int64(2), "2026-11-01T00:00:00.000000000Z"}},
				{Name: "stale_container", Bindings: []any{"2026-11-08T00:00:00.000000000Z", int64(31), int64(7), int64(2), "2026-11-07T00:00:00.000000000Z"}},
			}},
		{ID: iaEpisodeContainerAdvanceNull, Kind: stmt.Write,
			SQL: `UPDATE catalog_entities SET last_content_added_at=?
WHERE id=? AND library_id=? AND kind=? AND retired=0 AND last_content_added_at IS NULL
RETURNING last_content_added_at`,
			Params: []stmt.Param{t("last_content_added_at"), i("subject_id"), i("library_id"), i("kind")},
			Cohorts: []stmt.Cohort{
				{Name: "null_to_accepted", Bindings: []any{"2026-11-08T00:00:00.000000000Z", int64(31), int64(7), int64(2)}},
				{Name: "already_advanced", Bindings: []any{"2026-11-08T00:00:00.000000000Z", int64(31), int64(7), int64(3)}},
			}},
		{ID: iaEpisodeSubjectInsert, Kind: stmt.Write,
			SQL:    `INSERT INTO catalog_subjects(public_id,library_id,kind) VALUES(?,?,?) RETURNING id`,
			Params: []stmt.Param{b("public_id"), i("library_id"), i("kind")},
			Cohorts: []stmt.Cohort{
				{Name: "new_show_subject", Bindings: []any{inc, int64(7), int64(2)}},
				{Name: "new_season_subject", Bindings: []any{operation, int64(7), int64(3)}},
			}},
		{ID: iaEpisodeIdentityInsert, Kind: stmt.Write,
			SQL:    `INSERT INTO catalog_identities(library_id,source_key,subject_id) VALUES(?,?,?) ON CONFLICT(library_id,source_key) DO NOTHING`,
			Params: []stmt.Param{i("library_id"), t("source_key"), i("subject_id")},
			Cohorts: []stmt.Cohort{
				{Name: "first_identity", Bindings: []any{int64(7), "episode-show-key", int64(31)}},
				{Name: "concurrent_identity", Bindings: []any{int64(7), "episode-show-key", int64(32)}},
			}},
		{ID: iaEpisodeEntityInsert, Kind: stmt.Write,
			SQL: `INSERT INTO catalog_entities(
id,public_id,library_id,kind,parent_id,title,sort_title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,
random_key,year,added_at,metadata_language,show_id,season_id,season_number,episode_number,numbering,local_key,
metadata_incarnation,artwork_incarnation,playback_origin_incarnation,playback_origin_revision,
tv_incarnation,tv_source_revision,tv_hierarchy_revision,tv_selection_revision,tv_descriptive_revision)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
RETURNING id,revision,metadata_incarnation`,
			Params: []stmt.Param{i("subject_id"), b("public_id"), i("library_id"), i("kind"), ni("parent_id"), t("title"), t("sort_title"), t("sort_key"), t("sort_head"),
				t("sort_key_keep_articles"), t("sort_head_keep_articles"), i("random_key"), i("year"), t("added_at"), t("metadata_language"),
				ni("show_id"), ni("season_id"), ni("season_number"), ni("episode_number"), nt("numbering"), t("local_key"),
				b("metadata_incarnation"), b("artwork_incarnation"), b("playback_origin_incarnation"), i("playback_origin_revision"),
				b("tv_incarnation"), i("tv_source_revision"), i("tv_hierarchy_revision"), i("tv_selection_revision"), i("tv_descriptive_revision")},
			Cohorts: []stmt.Cohort{
				{Name: "new_show_entity", Bindings: []any{int64(31), inc, int64(7), int64(2), nil, "Show", "", "show", "s", "show", "s",
					int64(1), int64(0), "2026-11-08T00:00:00.000000000Z", "en-US", nil, nil, nil, nil, nil, "",
					inc, operation, digest, int64(1), digest, int64(1), int64(1), int64(1), int64(1)}},
				{Name: "new_episode_entity", Bindings: []any{int64(33), inc, int64(7), int64(4), int64(32), "Episode 1", "", "episode 1", "e", "episode 1", "e",
					int64(1), int64(0), "2026-11-08T00:00:00.000000000Z", "en-US", int64(31), int64(32), nil, int64(1), "seasonal", "",
					inc, operation, digest, int64(1), digest, int64(1), int64(1), int64(1), int64(1)}},
			}},
		{ID: iaEpisodeLinkInsert, Kind: stmt.Write,
			SQL: `INSERT INTO catalog_asset_links(entity_id,asset_id,part_index,available,start_seconds,end_seconds,edition,playback_origin_incarnation,playback_origin_revision,library_id)
VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(entity_id,asset_id) DO NOTHING`,
			Params: []stmt.Param{i("entity_id"), i("asset_id"), i("part_index"), i("available"), r("start_seconds"), nr("end_seconds"), t("edition"), b("playback_incarnation"), i("playback_revision"), i("library_id")},
			Cohorts: []stmt.Cohort{
				{Name: "whole_source_link", Bindings: []any{int64(33), int64(41), int64(0), int64(1), float64(0), nil, "", inc, int64(1), int64(7)}},
				{Name: "ranged_link", Bindings: []any{int64(33), int64(41), int64(1), int64(1), float64(60), float64(120), "", inc, int64(1), int64(7)}},
			}},
		{ID: iaEpisodeContributionInsert, Kind: stmt.Write,
			SQL:    `INSERT INTO catalog_asset_contributions(library_id,parent_id,asset_id,membership_refs,authored_refs,file_size,file_size_revision) VALUES(?,?,?,?,?,?,?)`,
			Params: []stmt.Param{i("library_id"), i("parent_id"), i("asset_id"), i("membership_refs"), i("authored_refs"), i("file_size"), i("file_size_revision")},
			Cohorts: []stmt.Cohort{
				{Name: "one_episode_membership", Bindings: []any{int64(7), int64(33), int64(41), int64(1), int64(0), int64(42), int64(1)}},
			}},
		{ID: iaEpisodeSearchHead, Kind: stmt.Read,
			SQL:     `SELECT revision,active_generation_id,building_generation_id,input_sequence FROM search_revision WHERE singleton=1`,
			Params:  nil,
			Cohorts: []stmt.Cohort{{Name: "singleton", Bindings: []any{}}}},
		{ID: iaEpisodeSearchBank, Kind: stmt.Read,
			SQL:    `SELECT bank_key,recipe_version,engine_version,unicode_fingerprint,state,pending_subjects FROM search_generations WHERE id=?`,
			Params: []stmt.Param{i("bank_id")},
			Cohorts: []stmt.Cohort{
				{Name: "active_bank", Bindings: []any{int64(1)}},
				{Name: "missing_bank", Bindings: []any{int64(999)}},
			}},
		{ID: iaEpisodeSearchExisting, Kind: stmt.Read,
			SQL:    `SELECT id FROM search_publish_jobs WHERE entity_id=? LIMIT 1`,
			Params: []stmt.Param{i("subject_id")},
			Cohorts: []stmt.Cohort{
				{Name: "new_subject", Bindings: []any{int64(31)}},
				{Name: "existing_subject", Bindings: []any{int64(32)}},
			}},
		{ID: iaEpisodeSearchInsert, Kind: stmt.Write,
			SQL: `INSERT INTO search_publish_jobs(entity_id,search_generation_id,input_generation,source_incarnation,source_revision,recipe_version,phase,job_next_attempt_ms)
VALUES(?,?,?,?,?,?,?,?)`,
			Params: []stmt.Param{i("subject_id"), i("bank_id"), i("input_generation"), b("source_incarnation"), i("source_revision"), t("recipe_version"), t("phase"), i("accepted_at_ms")},
			Cohorts: []stmt.Cohort{
				{Name: "first_intent", Bindings: []any{int64(31), int64(1), int64(1), inc, int64(1), "v1", "capture", int64(1000)}},
			}},
		{ID: iaEpisodeSearchPending, Kind: stmt.Write,
			SQL: `UPDATE search_generations SET pending_subjects=?
WHERE id=? AND state=? AND bank_key=? AND recipe_version=? AND engine_version=? AND unicode_fingerprint=?
AND pending_subjects=? AND pending_subjects<9223372036854775807`,
			Params: []stmt.Param{i("next_pending"), i("bank_id"), t("bank_state"), t("bank_key"), t("recipe_version"), t("engine_version"), t("unicode_fingerprint"), i("expected_pending")},
			Cohorts: []stmt.Cohort{
				{Name: "first_pending", Bindings: []any{int64(1), int64(1), "active", "v1", "v1", "unicode-v1", searchInputExampleFingerprint, int64(0)}},
			}},
		{ID: iaEpisodeSearchSequence, Kind: stmt.Write,
			SQL: `UPDATE search_revision SET input_sequence=?
WHERE singleton=1 AND revision=? AND active_generation_id=? AND building_generation_id IS NULL
AND input_sequence=? AND input_sequence<9223372036854775807`,
			Params: []stmt.Param{i("next_input_sequence"), i("expected_revision"), i("active_bank"), i("expected_input_sequence")},
			Cohorts: []stmt.Cohort{
				{Name: "active_only", Bindings: []any{int64(1), int64(1), int64(1), int64(0)}},
			}},
		{ID: iaEpisodeSearchBuilding, Kind: stmt.Write,
			SQL: `UPDATE search_revision SET input_sequence=?
WHERE singleton=1 AND revision=? AND active_generation_id=? AND building_generation_id=?
AND input_sequence=? AND input_sequence<9223372036854775807`,
			Params: []stmt.Param{i("next_input_sequence"), i("expected_revision"), i("active_bank"), i("building_bank"), i("expected_input_sequence")},
			Cohorts: []stmt.Cohort{
				{Name: "active_building", Bindings: []any{int64(1), int64(1), int64(1), int64(2), int64(0)}},
			}},
		{ID: iaEpisodeReceiptInsert, Kind: stmt.Write,
			SQL: `INSERT INTO scan_command_receipts(
run_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,accepted_revision,applied_ms,
source_id,source_incarnation,source_revision,change_id,next_cursor,result_count,result_digest,
ia_media_result_version,ia_media_member_bytes,ia_media_object_id,ia_media_object_revision,ia_media_cursor_kind,
ia_media_directory_path,ia_media_directory_identity,ia_media_directory_revision,ia_media_expected_payload,ia_media_next_payload)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			Params: []stmt.Param{t("run_id"), b("command_incarnation"), i("command_sequence"), b("operation_id"), b("request_digest"), t("command_kind"),
				i("accepted_revision"), i("applied_ms"), t("source_id"), t("source_incarnation"), nt("source_revision"), ni("change_id"), t("next_cursor"),
				i("result_count"), b("result_digest"), i("result_version"), i("member_bytes"), t("object_id"), t("object_revision"), t("cursor_kind"),
				b("directory_path"), b("directory_identity"), b("directory_revision"), b("expected_payload"), b("next_payload")},
			Cohorts: []stmt.Cohort{
				{Name: "new_episode_receipt", Bindings: []any{"episode-run", inc, int64(2), operation, digest, iaEpisodeReceiptKind, int64(6), int64(1),
					"episode-source", "episode-incarnation", nil, nil, "", int64(9), digest,
					int64(1), int64(900), "episode-object", "episode-revision", "local_getdents64_cookie_v1",
					[]byte("."), []byte("episode-directory-identity"), []byte("episode-directory-revision"), []byte(""), []byte("42")}},
			}},
		{ID: iaEpisodeMemberInsert, Kind: stmt.Write,
			SQL: `INSERT INTO scan_media_result_members(
run_id,command_incarnation,command_sequence,command_kind,ordinal,member_kind,library_id,subject_id,subject_kind,subject_revision,metadata_incarnation,
parent_id,parent_kind,ancestor_id,ancestor_kind,asset_id,asset_info_epoch,part_index,link_origin_revision,link_origin_incarnation,
edition,link_effect,cue_ordinal,start_seconds,end_seconds,episode_boundary_status,reading_format,reading_path_head,reading_path_sequence,extra_kind)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			Params: []stmt.Param{t("run_id"), b("command_incarnation"), i("command_sequence"), t("command_kind"), i("ordinal"), t("member_kind"),
				i("library_id"), i("subject_id"), i("subject_kind"), i("subject_revision"), b("metadata_incarnation"),
				ni("parent_id"), ni("parent_kind"), ni("ancestor_id"), ni("ancestor_kind"), ni("asset_id"), ni("asset_info_epoch"), ni("part_index"),
				ni("link_origin_revision"), nb("link_origin_incarnation"), nt("edition"), nt("link_effect"), ni("cue_ordinal"),
				nr("start_seconds"), nr("end_seconds"), nt("episode_boundary_status"), nt("reading_format"), ni("reading_path_head"),
				ni("reading_path_sequence"), nt("extra_kind")},
			Cohorts: []stmt.Cohort{
				{Name: "new_episode_identity", Bindings: []any{"episode-run", inc, int64(2), iaEpisodeReceiptKind, int64(0), "identity", int64(7), int64(31), int64(2), int64(1), operation,
					nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil}},
				{Name: "new_episode_membership", Bindings: []any{"episode-run", inc, int64(2), iaEpisodeReceiptKind, int64(3), "membership", int64(7), int64(33), int64(4), int64(1), operation,
					int64(32), int64(3), int64(31), int64(2), int64(41), int64(1), int64(0), int64(1), inc,
					"", "present", nil, float64(0), nil, "whole_source", nil, nil, nil, nil}},
			}},
	}
	for n := range defs {
		defs[n].Owner = "store/writer IA episode accept"
		defs[n].CallSites = []string{"store/writer/ia_episode_accept.go", "store/writer/ia_episode_types.go"}
		defs[n].Plan = stmt.Plan{Hot: true}
		switch defs[n].ID {
		case iaEpisodeOwner:
			defs[n].Plan.Indexes = []string{"sqlite_autoindex_inventory_runs_1"}
			defs[n].Plan.Seeks = []string{"job_id=?"}
		case iaEpisodeHeader:
			defs[n].Plan.PrimaryKeys = []string{"scan_command_receipts"}
			defs[n].Plan.Seeks = []string{"run_id=? AND command_incarnation=? AND command_sequence=?"}
		case iaEpisodeMemberBytes, iaEpisodeMembers:
			defs[n].Plan.PrimaryKeys = []string{"scan_media_result_members"}
			defs[n].Plan.Seeks = []string{"run_id=? AND command_incarnation=? AND command_sequence=? AND command_kind=?"}
		case iaEpisodeLibrary:
			defs[n].Plan.PrimaryKeys = []string{"catalog_libraries"}
			defs[n].Plan.Seeks = []string{"rowid=?"}
		case iaEpisodeSource:
			defs[n].Plan.Indexes = []string{"sqlite_autoindex_library_sources_2"}
			defs[n].Plan.Seeks = []string{"library_id=? AND root=?"}
		case iaEpisodeDirectory:
			defs[n].Plan.Indexes = []string{"sqlite_autoindex_inventory_directories_1"}
			defs[n].Plan.AutoIndexes = []stmt.AutoIndexBinding{{Table: "inventory_directories", Name: "sqlite_autoindex_inventory_directories_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "job_id", Collation: "BINARY"}, {Name: "relative_path", Collation: "BINARY"}}}}
			defs[n].Plan.Seeks = []string{"job_id=? AND relative_path=?"}
		case iaEpisodeAsset:
			defs[n].Plan.Indexes = []string{"sqlite_autoindex_catalog_assets_3"}
			defs[n].Plan.Seeks = []string{"library_id=? AND path=?"}
		case iaEpisodeIdentity:
			defs[n].Plan.PrimaryKeys = []string{"catalog_identities"}
			defs[n].Plan.Seeks = []string{"library_id=? AND source_key=?"}
		case iaEpisodeEntity:
			defs[n].Plan.PrimaryKeys = []string{"catalog_entities"}
			defs[n].Plan.Seeks = []string{"rowid=?"}
		case iaEpisodeDirectoryCAS:
			defs[n].Plan.Indexes = []string{"sqlite_autoindex_inventory_directories_1"}
			defs[n].Plan.AutoIndexes = []stmt.AutoIndexBinding{{Table: "inventory_directories", Name: "sqlite_autoindex_inventory_directories_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "job_id", Collation: "BINARY"}, {Name: "relative_path", Collation: "BINARY"}}}}
			defs[n].Plan.Seeks = []string{"job_id=? AND relative_path=?"}
		case iaEpisodeRunCAS:
			defs[n].Plan.Indexes = []string{"sqlite_autoindex_inventory_runs_2"}
			defs[n].Plan.Seeks = []string{"job_id=? AND command_incarnation=?"}
		case iaEpisodeContainer, iaEpisodeContainerAdvance, iaEpisodeContainerAdvanceNull:
			defs[n].Plan.PrimaryKeys = []string{"catalog_entities"}
			defs[n].Plan.Seeks = []string{"rowid=?"}
		case iaEpisodeSearchHead, iaEpisodeSearchSequence, iaEpisodeSearchBuilding:
			defs[n].Plan.PrimaryKeys = []string{"search_revision"}
			defs[n].Plan.Seeks = []string{"rowid=?"}
		case iaEpisodeSearchBank, iaEpisodeSearchPending:
			defs[n].Plan.PrimaryKeys = []string{"search_generations"}
			defs[n].Plan.Seeks = []string{"rowid=?"}
		case iaEpisodeSearchExisting:
			defs[n].Plan.Indexes = []string{"sqlite_autoindex_search_publish_jobs_1"}
			defs[n].Plan.Seeks = []string{"entity_id=?"}
		}
	}
	return defs
}

// Complete private family: current US owner authority, the finite episode
// points/CAS writes, and the closed search-intent append. No configuration,
// ordinary-child or legacy result definition is composed here.
func iaEpisodeDefinitions() []stmt.Definition {
	return append(usAuthorityDefinitions(), iaEpisodeStatements()...)
}
