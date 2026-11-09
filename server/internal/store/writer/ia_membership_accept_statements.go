package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// IA-16 statement family for one explicit membership change cohort. Every
// statement is a point read, a PK-range read or one finite insert/update/delete
// CAS on the embedded catalogue and group C draft relations. There is no OFFSET,
// temporary sort, library-wide scan or caller-selected SQL. The typed member
// cohort is read only through its own receipt/kind prefix.
const (
	iaMembershipOwner              stmt.ID = "ia.membership.owner"
	iaMembershipHeader             stmt.ID = "ia.membership.header"
	iaMembershipMemberBytes        stmt.ID = "ia.membership.member_bytes"
	iaMembershipMembers            stmt.ID = "ia.membership.members"
	iaMembershipLibrary            stmt.ID = "ia.membership.library"
	iaMembershipSource             stmt.ID = "ia.membership.source"
	iaMembershipDirectory          stmt.ID = "ia.membership.directory"
	iaMembershipAsset              stmt.ID = "ia.membership.asset"
	iaMembershipIdentity           stmt.ID = "ia.membership.identity"
	iaMembershipEntity             stmt.ID = "ia.membership.entity"
	iaMembershipExtraItem          stmt.ID = "ia.membership.extra_item"
	iaMembershipSeasonChild        stmt.ID = "ia.membership.season_child"
	iaMembershipSeasonEpisode      stmt.ID = "ia.membership.season_episode"
	iaMembershipLink               stmt.ID = "ia.membership.link"
	iaMembershipLinkList           stmt.ID = "ia.membership.link_list"
	iaMembershipBoundary           stmt.ID = "ia.membership.boundary"
	iaMembershipEntityUpdate       stmt.ID = "ia.membership.entity_update"
	iaMembershipLinkInsert         stmt.ID = "ia.membership.link_insert"
	iaMembershipLinkUpdate         stmt.ID = "ia.membership.link_update"
	iaMembershipLinkUpdateNullEnd  stmt.ID = "ia.membership.link_update_null_end"
	iaMembershipLinkUpdateNullCue  stmt.ID = "ia.membership.link_update_null_cue"
	iaMembershipLinkUpdateNullBoth stmt.ID = "ia.membership.link_update_null_both"
	iaMembershipLinkDelete         stmt.ID = "ia.membership.link_delete"
	iaMembershipLinkDeleteNullEnd  stmt.ID = "ia.membership.link_delete_null_end"
	iaMembershipLinkDeleteNullCue  stmt.ID = "ia.membership.link_delete_null_cue"
	iaMembershipLinkDeleteNullBoth stmt.ID = "ia.membership.link_delete_null_both"
	iaMembershipBoundaryInsert     stmt.ID = "ia.membership.boundary_insert"
	iaMembershipBoundaryUpdate     stmt.ID = "ia.membership.boundary_update"
	iaMembershipBoundaryDelete     stmt.ID = "ia.membership.boundary_delete"
	iaMembershipDirectoryCAS       stmt.ID = "ia.membership.directory_cas"
	iaMembershipRunCAS             stmt.ID = "ia.membership.run_cas"
	iaMembershipReceiptInsert      stmt.ID = "ia.membership.receipt_insert"
	iaMembershipMemberInsert       stmt.ID = "ia.membership.member_insert"
	iaMembershipContributionInsert stmt.ID = "ia.membership.contribution_insert"
	iaMembershipContributionDelete stmt.ID = "ia.membership.contribution_delete"
)

const iaMembershipReceiptKind = "membership_accept"

func iaMembershipStatements() []stmt.Definition {
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
		{ID: iaMembershipOwner, Kind: stmt.Read,
			SQL:    `SELECT command_incarnation,last_command_sequence,acknowledged_command_sequence FROM inventory_runs WHERE job_id=?`,
			Params: []stmt.Param{t("run_id")},
			Cohorts: []stmt.Cohort{
				{Name: "current_owner", Bindings: []any{"membership-run"}},
				{Name: "missing_owner", Bindings: []any{"membership-missing"}},
			}},
		{ID: iaMembershipHeader, Kind: stmt.Read,
			SQL: `SELECT operation_id,request_digest,command_kind,result_count,result_digest,accepted_revision,applied_ms,source_id,source_incarnation,
ia_media_result_version,ia_media_member_bytes,ia_media_object_id,ia_media_object_revision,ia_media_cursor_kind,
ia_media_directory_path,ia_media_directory_identity,ia_media_directory_revision,ia_media_expected_payload,ia_media_next_payload
FROM scan_command_receipts WHERE run_id=? AND command_incarnation=? AND command_sequence=?`,
			Params: key,
			Cohorts: []stmt.Cohort{
				{Name: "saved_membership_receipt", Bindings: []any{"membership-run", inc, int64(2)}},
				{Name: "missing_receipt", Bindings: []any{"membership-missing", inc, int64(2)}},
			}},
		{ID: iaMembershipMemberBytes, Kind: stmt.Read,
			SQL: `SELECT ordinal,retained_bytes FROM scan_media_result_members
WHERE run_id=? AND command_incarnation=? AND command_sequence=? AND command_kind='membership_accept'
ORDER BY ordinal LIMIT ?`,
			Params: append(append([]stmt.Param{}, key...), i("row_limit")),
			Cohorts: []stmt.Cohort{
				{Name: "saved_member_bytes", Bindings: []any{"membership-run", inc, int64(2), int64(9)}},
				{Name: "missing_member_bytes", Bindings: []any{"membership-missing", inc, int64(2), int64(9)}},
			}},
		{ID: iaMembershipMembers, Kind: stmt.Read,
			SQL: `SELECT ordinal,member_kind,library_id,subject_id,subject_kind,subject_revision,metadata_incarnation,
parent_id,parent_kind,ancestor_id,ancestor_kind,asset_id,asset_info_epoch,part_index,link_origin_revision,link_origin_incarnation,
edition,link_effect,cue_ordinal,start_seconds,end_seconds,episode_boundary_status,reading_format,reading_path_head,reading_path_sequence,extra_kind
FROM scan_media_result_members
WHERE run_id=? AND command_incarnation=? AND command_sequence=? AND command_kind='membership_accept'
ORDER BY ordinal LIMIT ?`,
			Params: append(append([]stmt.Param{}, key...), i("row_limit")),
			Cohorts: []stmt.Cohort{
				{Name: "saved_members", Bindings: []any{"membership-run", inc, int64(2), int64(9)}},
				{Name: "missing_members", Bindings: []any{"membership-missing", inc, int64(2), int64(9)}},
			}},
		{ID: iaMembershipLibrary, Kind: stmt.Read,
			SQL:    `SELECT id FROM catalog_libraries WHERE id=? AND library_id=? AND retired=0`,
			Params: []stmt.Param{i("library_id"), t("library_key")},
			Cohorts: []stmt.Cohort{
				{Name: "live_library", Bindings: []any{int64(7), "membership-library"}},
				{Name: "retired_library", Bindings: []any{int64(8), "membership-retired"}},
			}},
		{ID: iaMembershipSource, Kind: stmt.Read,
			SQL: `SELECT id FROM library_sources
WHERE id=? AND library_id=? AND generation=? AND incarnation=? AND root_identity=? AND root=? AND follow_symlinks=?
AND kind='local' AND classification='local' AND enabled=1 AND identity_confirmed=1 AND health='ready' AND remote_source_id IS NULL`,
			Params: []stmt.Param{t("source_id"), t("library_key"), i("generation"), t("incarnation"), t("root_identity"), t("resolved_root"), boolean("follow_symlinks")},
			Cohorts: []stmt.Cohort{
				{Name: "current_source", Bindings: []any{"membership-source", "membership-library", int64(3), "membership-incarnation", "membership-root-identity", "/media/membership", false}},
				{Name: "stale_source", Bindings: []any{"membership-source", "membership-library", int64(4), "membership-incarnation", "membership-root-identity", "/media/membership", false}},
			}},
		{ID: iaMembershipDirectory, Kind: stmt.Read,
			SQL: `SELECT pages,skipped FROM inventory_directories
WHERE job_id=? AND relative_path=? AND identity=? AND revision=? AND cursor=? AND pages=? AND skipped=? AND state='pending'`,
			Params: []stmt.Param{t("run_id"), t("relative_path"), t("identity"), t("revision"), t("cursor"), i("pages"), i("skipped")},
			Cohorts: []stmt.Cohort{
				{Name: "pending_directory", Bindings: []any{"membership-run", "Show/Season 1", "membership-directory-identity", "membership-directory-revision", "", int64(2), int64(0)}},
				{Name: "stale_directory", Bindings: []any{"membership-run", "Show/Season 1", "membership-directory-identity", "membership-directory-revision", "9", int64(2), int64(0)}},
			}},
		{ID: iaMembershipAsset, Kind: stmt.Read,
			SQL:    `SELECT id,token,info_epoch,file_size_revision FROM catalog_assets WHERE library_id=? AND path=? AND size=? AND modified_ns=? AND available=1`,
			Params: []stmt.Param{i("library_id"), t("path"), i("size"), i("modified_ns")},
			Cohorts: []stmt.Cohort{
				{Name: "observed_asset", Bindings: []any{int64(7), "/media/membership/Show/Season 1/Episode 1.mkv", int64(42), int64(7)}},
				{Name: "changed_asset", Bindings: []any{int64(7), "/media/membership/Show/Season 1/Episode 1.mkv", int64(43), int64(7)}},
			}},
		{ID: iaMembershipIdentity, Kind: stmt.Read,
			SQL:    `SELECT subject_id FROM catalog_identities WHERE library_id=? AND source_key=?`,
			Params: []stmt.Param{i("library_id"), t("source_key")},
			Cohorts: []stmt.Cohort{
				{Name: "retained_episode", Bindings: []any{int64(7), "membership-episode-key"}},
				{Name: "new_parent", Bindings: []any{int64(7), "membership-season-key"}},
				{Name: "missing_identity", Bindings: []any{int64(7), "membership-missing-key"}},
			}},
		{ID: iaMembershipEntity, Kind: stmt.Read,
			SQL:    `SELECT kind,parent_id,show_id,season_id,artist_id,album_id,book_id,retired,revision,metadata_incarnation FROM catalog_entities WHERE id=? AND library_id=?`,
			Params: []stmt.Param{i("subject_id"), i("library_id")},
			Cohorts: []stmt.Cohort{
				{Name: "retained_entity", Bindings: []any{int64(31), int64(7)}},
				{Name: "parent_entity", Bindings: []any{int64(32), int64(7)}},
				{Name: "missing_entity", Bindings: []any{int64(34), int64(7)}},
			}},
		{ID: iaMembershipExtraItem, Kind: stmt.Read,
			SQL:    `SELECT library_id,owner_id,kind FROM item_extras WHERE item_id=?`,
			Params: []stmt.Param{i("item_id")},
			Cohorts: []stmt.Cohort{
				{Name: "extra_item", Bindings: []any{int64(33)}},
				{Name: "missing_item", Bindings: []any{int64(35)}},
			}},
		{ID: iaMembershipSeasonChild, Kind: stmt.Read,
			SQL:    `SELECT id FROM catalog_entities INDEXED BY catalog_entities_parent WHERE parent_id=? AND library_id=? AND kind=4 LIMIT 1`,
			Params: []stmt.Param{i("season_id"), i("library_id")},
			Cohorts: []stmt.Cohort{
				{Name: "season_with_episode", Bindings: []any{int64(71), int64(7)}},
				{Name: "season_without_episode", Bindings: []any{int64(72), int64(7)}},
			}},
		{ID: iaMembershipSeasonEpisode, Kind: stmt.Read,
			SQL:    `SELECT id FROM catalog_entities INDEXED BY catalog_entities_season WHERE season_id=? AND library_id=? AND kind=4 LIMIT 1`,
			Params: []stmt.Param{i("season_id"), i("library_id")},
			Cohorts: []stmt.Cohort{
				{Name: "season_with_episode", Bindings: []any{int64(71), int64(7)}},
				{Name: "season_without_episode", Bindings: []any{int64(72), int64(7)}},
			}},
		{ID: iaMembershipLink, Kind: stmt.Read,
			SQL: `SELECT part_index,available,start_seconds,end_seconds,edition,cue_ordinal,playback_origin_incarnation,playback_origin_revision
FROM catalog_asset_links WHERE entity_id=? AND asset_id=? AND library_id=?`,
			Params: []stmt.Param{i("entity_id"), i("asset_id"), i("library_id")},
			Cohorts: []stmt.Cohort{
				{Name: "retained_link", Bindings: []any{int64(31), int64(41), int64(7)}},
				{Name: "missing_link", Bindings: []any{int64(34), int64(41), int64(7)}},
			}},
		{ID: iaMembershipLinkList, Kind: stmt.Read,
			SQL:    `SELECT asset_id FROM catalog_asset_links INDEXED BY catalog_asset_links_order WHERE entity_id=? ORDER BY part_index,asset_id LIMIT 17`,
			Params: []stmt.Param{i("entity_id")},
			Cohorts: []stmt.Cohort{
				{Name: "bounded_links", Bindings: []any{int64(31)}},
				{Name: "empty_links", Bindings: []any{int64(34)}},
			}},
		{ID: iaMembershipBoundary, Kind: stmt.Read,
			SQL:    `SELECT status FROM episode_asset_boundaries WHERE item_id=? AND asset_id=?`,
			Params: []stmt.Param{i("item_id"), t("asset_id")},
			Cohorts: []stmt.Cohort{
				{Name: "retained_boundary", Bindings: []any{int64(31), "membership-token"}},
				{Name: "missing_boundary", Bindings: []any{int64(34), "membership-token"}},
			}},
		{ID: iaMembershipEntityUpdate, Kind: stmt.Write,
			SQL: `UPDATE catalog_entities SET parent_id=?,show_id=?,season_id=?,artist_id=?,album_id=?,book_id=?,revision=revision+1
WHERE id=? AND library_id=? AND kind=? AND retired=0 AND revision=?
RETURNING revision,metadata_incarnation`,
			Params: []stmt.Param{ni("parent_id"), ni("show_id"), ni("season_id"), ni("artist_id"), ni("album_id"), ni("book_id"),
				i("subject_id"), i("library_id"), i("kind"), i("revision")},
			Cohorts: []stmt.Cohort{
				{Name: "episode_to_season", Bindings: []any{int64(32), int64(30), int64(32), nil, nil, nil, int64(31), int64(7), int64(4), int64(1)}},
				{Name: "stale_revision", Bindings: []any{int64(32), int64(30), int64(32), nil, nil, nil, int64(31), int64(7), int64(4), int64(9)}},
			}},
		{ID: iaMembershipLinkInsert, Kind: stmt.Write,
			SQL: `INSERT INTO catalog_asset_links(cue_ordinal,entity_id,asset_id,part_index,available,start_seconds,end_seconds,edition,playback_origin_incarnation,playback_origin_revision,library_id)
VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(entity_id,asset_id) DO NOTHING`,
			Params: []stmt.Param{ni("cue_ordinal"), i("entity_id"), i("asset_id"), i("part_index"), i("available"), r("start_seconds"), nr("end_seconds"),
				t("edition"), b("playback_incarnation"), i("playback_revision"), i("library_id")},
			Cohorts: []stmt.Cohort{
				{Name: "new_link", Bindings: []any{nil, int64(31), int64(41), int64(0), int64(1), float64(0), nil, "", inc, int64(1), int64(7)}},
				{Name: "new_ranged_link", Bindings: []any{int64(1), int64(31), int64(41), int64(0), int64(1), float64(0), float64(42), "", inc, int64(1), int64(7)}},
			}},
		{ID: iaMembershipBoundaryInsert, Kind: stmt.Write,
			SQL:    `INSERT INTO episode_asset_boundaries(item_id,asset_id,status) VALUES(?,?,?) ON CONFLICT(item_id,asset_id) DO NOTHING`,
			Params: []stmt.Param{i("item_id"), t("asset_id"), t("status")},
			Cohorts: []stmt.Cohort{
				{Name: "new_boundary", Bindings: []any{int64(31), "membership-token", "whole_source"}},
				{Name: "concurrent_boundary", Bindings: []any{int64(31), "membership-token", "unknown_multi_episode"}},
			}},
		{ID: iaMembershipBoundaryUpdate, Kind: stmt.Write,
			SQL:    `UPDATE episode_asset_boundaries SET status=? WHERE item_id=? AND asset_id=? AND status=?`,
			Params: []stmt.Param{t("next_status"), i("item_id"), t("asset_id"), t("status")},
			Cohorts: []stmt.Cohort{
				{Name: "boundary_change", Bindings: []any{"unknown_multi_episode", int64(31), "membership-token", "whole_source"}},
				{Name: "stale_boundary", Bindings: []any{"whole_source", int64(31), "membership-token", "unknown_multi_episode"}},
			}},
		{ID: iaMembershipBoundaryDelete, Kind: stmt.Write,
			SQL:    `DELETE FROM episode_asset_boundaries WHERE item_id=? AND asset_id=? AND status=?`,
			Params: []stmt.Param{i("item_id"), t("asset_id"), t("status")},
			Cohorts: []stmt.Cohort{
				{Name: "removed_boundary", Bindings: []any{int64(31), "membership-token", "whole_source"}},
				{Name: "stale_boundary", Bindings: []any{int64(31), "membership-token", "unknown_multi_episode"}},
			}},
		{ID: iaMembershipDirectoryCAS, Kind: stmt.Write,
			SQL: `UPDATE inventory_directories SET cursor=?,pages=?
WHERE job_id=? AND relative_path=? AND identity=? AND revision=? AND cursor=? AND pages=? AND skipped=? AND state='pending'
RETURNING cursor,pages,skipped`,
			Params: []stmt.Param{t("next_cursor"), i("next_pages"), t("run_id"), t("relative_path"), t("identity"), t("revision"), t("cursor"), i("pages"), i("skipped")},
			Cohorts: []stmt.Cohort{
				{Name: "pending_to_next", Bindings: []any{"42", int64(3), "membership-run", "Show/Season 1", "membership-directory-identity", "membership-directory-revision", "", int64(2), int64(0)}},
				{Name: "stale_directory_cas", Bindings: []any{"42", int64(3), "membership-run", "Show/Season 1", "membership-directory-identity", "membership-directory-revision", "9", int64(2), int64(0)}},
			}},
		{ID: iaMembershipRunCAS, Kind: stmt.Write,
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
				{Name: "actual_post_accept_revision", Bindings: []any{int64(6), int64(2), "membership-run", inc, int64(7), "membership-library", "/media/membership", false,
					"membership-source", int64(3), "membership-incarnation", "membership-root-identity", int64(4), "inventory", "first-import",
					int64(11), int64(13), int64(4000000000000), int64(5), int64(1), int64(1)}},
				{Name: "stale_run_cas", Bindings: []any{int64(6), int64(2), "membership-run", inc, int64(7), "membership-library", "/media/membership", false,
					"membership-source", int64(3), "membership-incarnation", "membership-root-identity", int64(4), "inventory", "first-import",
					int64(11), int64(13), int64(4000000000000), int64(5), int64(1), int64(0)}},
			}},
		{ID: iaMembershipContributionInsert, Kind: stmt.Write,
			SQL:    `INSERT INTO catalog_asset_contributions(library_id,parent_id,asset_id,membership_refs,authored_refs,file_size,file_size_revision) VALUES(?,?,?,?,?,?,?)`,
			Params: []stmt.Param{i("library_id"), i("parent_id"), i("asset_id"), i("membership_refs"), i("authored_refs"), i("file_size"), i("file_size_revision")},
			Cohorts: []stmt.Cohort{
				{Name: "one_membership", Bindings: []any{int64(7), int64(31), int64(41), int64(1), int64(0), int64(42), int64(1)}},
			}},
		{ID: iaMembershipContributionDelete, Kind: stmt.Write,
			SQL:    `DELETE FROM catalog_asset_contributions WHERE parent_id=? AND asset_id=? AND library_id=? AND membership_refs=1 AND authored_refs=0`,
			Params: []stmt.Param{i("parent_id"), i("asset_id"), i("library_id")},
			Cohorts: []stmt.Cohort{
				{Name: "removed_membership", Bindings: []any{int64(31), int64(41), int64(7)}},
			}},
	}
	// Nullable stored link facts have four CLOSED variants, not IS ?, OR or a
	// sentinel: the hot grammar admits IS NULL or a plain comparison only.
	for _, variant := range []struct {
		update, remove   stmt.ID
		endNull, cueNull bool
	}{
		{iaMembershipLinkUpdate, iaMembershipLinkDelete, false, false},
		{iaMembershipLinkUpdateNullEnd, iaMembershipLinkDeleteNullEnd, true, false},
		{iaMembershipLinkUpdateNullCue, iaMembershipLinkDeleteNullCue, false, true},
		{iaMembershipLinkUpdateNullBoth, iaMembershipLinkDeleteNullBoth, true, true},
	} {
		predicate := ` WHERE entity_id=? AND asset_id=? AND library_id=? AND part_index=? AND available=? AND start_seconds=?`
		var tail []stmt.Param
		if variant.endNull {
			predicate += ` AND end_seconds IS NULL`
		} else {
			predicate += ` AND end_seconds=?`
			tail = append(tail, nr("end_seconds"))
		}
		predicate += ` AND edition=?`
		tail = append(tail, t("edition"))
		if variant.cueNull {
			predicate += ` AND cue_ordinal IS NULL`
		} else {
			predicate += ` AND cue_ordinal=?`
			tail = append(tail, ni("cue_ordinal"))
		}
		predicate += ` RETURNING part_index,available,start_seconds,end_seconds,edition,cue_ordinal,playback_origin_incarnation,playback_origin_revision`
		old := func(stale bool) []any {
			args := []any{int64(31), int64(41), int64(7), int64(0), int64(1), float64(0)}
			if stale {
				args[3] = int64(9)
			}
			if !variant.endNull {
				args = append(args, float64(42))
			}
			args = append(args, "")
			if !variant.cueNull {
				args = append(args, int64(1))
			}
			return args
		}
		updateParams := []stmt.Param{i("next_part"), r("next_start"), nr("next_end"), t("next_edition"), ni("next_cue"),
			i("entity_id"), i("asset_id"), i("library_id"), i("part"), i("available"), r("start_seconds")}
		updateParams = append(updateParams, tail...)
		updateArgs := append([]any{int64(0), float64(0), nil, "Extended", nil}, old(false)...)
		staleUpdateArgs := append([]any{int64(1), float64(0), nil, "", nil}, old(true)...)
		defs = append(defs, stmt.Definition{ID: variant.update, Kind: stmt.Write,
			SQL:    `UPDATE catalog_asset_links SET part_index=?,available=1,start_seconds=?,end_seconds=?,edition=?,cue_ordinal=?` + predicate,
			Params: updateParams,
			Cohorts: []stmt.Cohort{
				{Name: "edition_change", Bindings: updateArgs},
				{Name: "stale_link", Bindings: staleUpdateArgs},
			}})
		deleteParams := []stmt.Param{i("entity_id"), i("asset_id"), i("library_id"), i("part"), i("available"), r("start_seconds")}
		deleteParams = append(deleteParams, tail...)
		defs = append(defs, stmt.Definition{ID: variant.remove, Kind: stmt.Write,
			SQL:    `DELETE FROM catalog_asset_links` + predicate,
			Params: deleteParams,
			Cohorts: []stmt.Cohort{
				{Name: "removed_link", Bindings: old(false)},
				{Name: "stale_link", Bindings: old(true)},
			}})
	}
	defs = append(defs,
		stmt.Definition{ID: iaMembershipReceiptInsert, Kind: stmt.Write,
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
				{Name: "new_membership_receipt", Bindings: []any{"membership-run", inc, int64(2), operation, digest, iaMembershipReceiptKind, int64(6), int64(1),
					"membership-source", "membership-incarnation", nil, nil, "", int64(4), digest,
					int64(1), int64(900), "membership-object", "membership-revision", "local_getdents64_cookie_v1",
					[]byte("Show/Season 1"), []byte("membership-directory-identity"), []byte("membership-directory-revision"), []byte(""), []byte("42")}},
			}},
		stmt.Definition{ID: iaMembershipMemberInsert, Kind: stmt.Write,
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
				{Name: "retained_identity", Bindings: []any{"membership-run", inc, int64(2), iaMembershipReceiptKind, int64(0), "identity", int64(7), int64(31), int64(4), int64(2), operation,
					int64(32), int64(3), nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil}},
				{Name: "present_membership", Bindings: []any{"membership-run", inc, int64(2), iaMembershipReceiptKind, int64(1), "membership", int64(7), int64(31), int64(4), int64(2), operation,
					nil, nil, nil, nil, int64(41), int64(1), int64(0), int64(1), inc, "", "present", nil, float64(0), nil, "whole_source", nil, nil, nil, nil}},
				{Name: "removed_membership", Bindings: []any{"membership-run", inc, int64(2), iaMembershipReceiptKind, int64(1), "membership", int64(7), int64(31), int64(4), int64(2), operation,
					nil, nil, nil, nil, int64(41), int64(1), int64(0), int64(1), inc, "", "removed", nil, float64(0), nil, "whole_source", nil, nil, nil, nil}},
			}},
	)
	for n := range defs {
		defs[n].Owner = "store/writer IA membership accept"
		defs[n].CallSites = []string{"store/writer/ia_membership_accept.go", "store/writer/ia_membership_accept_statements.go"}
		defs[n].Plan = stmt.Plan{Hot: true}
		switch defs[n].ID {
		case iaMembershipOwner:
			defs[n].Plan.Indexes = []string{"sqlite_autoindex_inventory_runs_1"}
			defs[n].Plan.Seeks = []string{"job_id=?"}
		case iaMembershipHeader:
			defs[n].Plan.PrimaryKeys = []string{"scan_command_receipts"}
			defs[n].Plan.Seeks = []string{"run_id=? AND command_incarnation=? AND command_sequence=?"}
		case iaMembershipMemberBytes, iaMembershipMembers:
			defs[n].Plan.PrimaryKeys = []string{"scan_media_result_members"}
			defs[n].Plan.Seeks = []string{"run_id=? AND command_incarnation=? AND command_sequence=? AND command_kind=?"}
		case iaMembershipLibrary:
			defs[n].Plan.PrimaryKeys = []string{"catalog_libraries"}
			defs[n].Plan.Seeks = []string{"rowid=?"}
		case iaMembershipSource:
			defs[n].Plan.Indexes = []string{"sqlite_autoindex_library_sources_2"}
			defs[n].Plan.Seeks = []string{"library_id=? AND root=?"}
		case iaMembershipDirectory:
			defs[n].Plan.PrimaryKeys = []string{"inventory_directories"}
			defs[n].Plan.Seeks = []string{"job_id=? AND relative_path=?"}
		case iaMembershipAsset:
			defs[n].Plan.Indexes = []string{"sqlite_autoindex_catalog_assets_3"}
			defs[n].Plan.Seeks = []string{"library_id=? AND path=?"}
		case iaMembershipIdentity:
			defs[n].Plan.PrimaryKeys = []string{"catalog_identities"}
			defs[n].Plan.Seeks = []string{"library_id=? AND source_key=?"}
		case iaMembershipEntity:
			defs[n].Plan.PrimaryKeys = []string{"catalog_entities"}
			defs[n].Plan.Seeks = []string{"rowid=?"}
		case iaMembershipExtraItem:
			defs[n].Plan.PrimaryKeys = []string{"item_extras"}
			defs[n].Plan.Seeks = []string{"rowid=?"}
		case iaMembershipSeasonChild:
			defs[n].Plan.Indexes = []string{"catalog_entities_parent"}
			defs[n].Plan.Seeks = []string{"parent_id=?"}
		case iaMembershipSeasonEpisode:
			defs[n].Plan.Indexes = []string{"catalog_entities_season"}
			defs[n].Plan.Seeks = []string{"season_id=?"}
		case iaMembershipLink:
			defs[n].Plan.PrimaryKeys = []string{"catalog_asset_links"}
			defs[n].Plan.Seeks = []string{"entity_id=? AND asset_id=?"}
		case iaMembershipLinkList:
			defs[n].Plan.Indexes = []string{"catalog_asset_links_order"}
			defs[n].Plan.Seeks = []string{"entity_id=?"}
		case iaMembershipBoundary:
			defs[n].Plan.Indexes = []string{"sqlite_autoindex_episode_asset_boundaries_1"}
			defs[n].Plan.Seeks = []string{"item_id=? AND asset_id=?"}
		case iaMembershipEntityUpdate:
			defs[n].Plan.PrimaryKeys = []string{"catalog_entities"}
			defs[n].Plan.Seeks = []string{"rowid=?"}
		case iaMembershipLinkUpdate, iaMembershipLinkUpdateNullEnd, iaMembershipLinkUpdateNullCue, iaMembershipLinkUpdateNullBoth,
			iaMembershipLinkDelete, iaMembershipLinkDeleteNullEnd, iaMembershipLinkDeleteNullCue, iaMembershipLinkDeleteNullBoth:
			defs[n].Plan.PrimaryKeys = []string{"catalog_asset_links"}
			defs[n].Plan.Seeks = []string{"entity_id=? AND asset_id=?"}
		case iaMembershipBoundaryInsert, iaMembershipBoundaryUpdate, iaMembershipBoundaryDelete:
			defs[n].Plan.Indexes = []string{"sqlite_autoindex_episode_asset_boundaries_1"}
			defs[n].Plan.Seeks = []string{"item_id=? AND asset_id=?"}
		case iaMembershipDirectoryCAS:
			defs[n].Plan.PrimaryKeys = []string{"inventory_directories"}
			defs[n].Plan.Seeks = []string{"job_id=? AND relative_path=?"}
		case iaMembershipRunCAS:
			defs[n].Plan.Indexes = []string{"sqlite_autoindex_inventory_runs_2"}
			defs[n].Plan.Seeks = []string{"job_id=? AND command_incarnation=?"}
		}
	}
	return defs
}

// Complete private family: current US owner authority and the finite
// membership points/CAS writes. No search-intent append: a membership change
// creates no entity and no descendant art/name fanout.
func iaMembershipDefinitions() []stmt.Definition {
	return append(usAuthorityDefinitions(), iaMembershipStatements()...)
}
