package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// IA-16 statement family for one accepted extra. Every statement is a point
// read, a PK-range read or one finite insert/CAS on the exact group C draft
// relations plus the embedded catalogue tables. There is no OFFSET, temporary
// sort, library-wide scan or caller-selected SQL. The typed member cohort is
// read only through its own receipt/kind prefix.
const (
	iaExtraAcceptOwner              stmt.ID = "ia.extra.owner"
	iaExtraAcceptHeader             stmt.ID = "ia.extra.header"
	iaExtraAcceptMemberBytes        stmt.ID = "ia.extra.member_bytes"
	iaExtraAcceptMembers            stmt.ID = "ia.extra.members"
	iaExtraAcceptLibrary            stmt.ID = "ia.extra.library"
	iaExtraAcceptSource             stmt.ID = "ia.extra.source"
	iaExtraAcceptDirectory          stmt.ID = "ia.extra.directory"
	iaExtraAcceptAsset              stmt.ID = "ia.extra.asset"
	iaExtraAcceptIdentity           stmt.ID = "ia.extra.identity"
	iaExtraAcceptEntity             stmt.ID = "ia.extra.entity"
	iaExtraAcceptItem               stmt.ID = "ia.extra.item"
	iaExtraAcceptDirectoryCAS       stmt.ID = "ia.extra.directory_cas"
	iaExtraAcceptRunCAS             stmt.ID = "ia.extra.run_cas"
	iaExtraAcceptSubjectInsert      stmt.ID = "ia.extra.subject_insert"
	iaExtraAcceptIdentityInsert     stmt.ID = "ia.extra.identity_insert"
	iaExtraAcceptEntityInsert       stmt.ID = "ia.extra.entity_insert"
	iaExtraAcceptItemInsert         stmt.ID = "ia.extra.item_insert"
	iaExtraAcceptLinkInsert         stmt.ID = "ia.extra.link_insert"
	iaExtraAcceptContributionInsert stmt.ID = "ia.extra.contribution_insert"
	iaExtraAcceptReceiptInsert      stmt.ID = "ia.extra.receipt_insert"
	iaExtraAcceptMemberInsert       stmt.ID = "ia.extra.member_insert"
)

const iaExtraAcceptReceiptKind = "extra_inventory_accept"

func iaExtraAcceptStatements() []stmt.Definition {
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
		{ID: iaExtraAcceptOwner, Kind: stmt.Read,
			SQL:    `SELECT command_incarnation,last_command_sequence,acknowledged_command_sequence FROM inventory_runs WHERE job_id=?`,
			Params: []stmt.Param{t("run_id")},
			Cohorts: []stmt.Cohort{
				{Name: "current_owner", Bindings: []any{"extra-run"}},
				{Name: "missing_owner", Bindings: []any{"extra-missing"}},
			}},
		{ID: iaExtraAcceptHeader, Kind: stmt.Read,
			SQL: `SELECT operation_id,request_digest,command_kind,result_count,result_digest,accepted_revision,applied_ms,source_id,source_incarnation,
ia_media_result_version,ia_media_member_bytes,ia_media_object_id,ia_media_object_revision,ia_media_cursor_kind,
ia_media_directory_path,ia_media_directory_identity,ia_media_directory_revision,ia_media_expected_payload,ia_media_next_payload
FROM scan_command_receipts WHERE run_id=? AND command_incarnation=? AND command_sequence=?`,
			Params: key,
			Cohorts: []stmt.Cohort{
				{Name: "saved_extra_receipt", Bindings: []any{"extra-run", inc, int64(2)}},
				{Name: "missing_receipt", Bindings: []any{"extra-missing", inc, int64(2)}},
			}},
		{ID: iaExtraAcceptMemberBytes, Kind: stmt.Read,
			SQL: `SELECT ordinal,retained_bytes FROM scan_media_result_members
WHERE run_id=? AND command_incarnation=? AND command_sequence=? AND command_kind='extra_inventory_accept'
ORDER BY ordinal LIMIT ?`,
			Params: append(append([]stmt.Param{}, key...), i("row_limit")),
			Cohorts: []stmt.Cohort{
				{Name: "saved_member_bytes", Bindings: []any{"extra-run", inc, int64(2), int64(2)}},
				{Name: "missing_member_bytes", Bindings: []any{"extra-missing", inc, int64(2), int64(2)}},
			}},
		{ID: iaExtraAcceptMembers, Kind: stmt.Read,
			SQL: `SELECT ordinal,member_kind,library_id,subject_id,subject_kind,subject_revision,metadata_incarnation,
parent_id,parent_kind,ancestor_id,ancestor_kind,asset_id,asset_info_epoch,part_index,link_origin_revision,link_origin_incarnation,
edition,link_effect,cue_ordinal,start_seconds,end_seconds,episode_boundary_status,reading_format,reading_path_head,reading_path_sequence,extra_kind
FROM scan_media_result_members
WHERE run_id=? AND command_incarnation=? AND command_sequence=? AND command_kind='extra_inventory_accept'
ORDER BY ordinal LIMIT ?`,
			Params: append(append([]stmt.Param{}, key...), i("row_limit")),
			Cohorts: []stmt.Cohort{
				{Name: "saved_members", Bindings: []any{"extra-run", inc, int64(2), int64(2)}},
				{Name: "missing_members", Bindings: []any{"extra-missing", inc, int64(2), int64(2)}},
			}},
		{ID: iaExtraAcceptLibrary, Kind: stmt.Read,
			SQL:    `SELECT id FROM catalog_libraries WHERE id=? AND library_id=? AND retired=0`,
			Params: []stmt.Param{i("library_id"), t("library_key")},
			Cohorts: []stmt.Cohort{
				{Name: "live_library", Bindings: []any{int64(7), "extra-library"}},
				{Name: "retired_library", Bindings: []any{int64(8), "extra-retired"}},
			}},
		{ID: iaExtraAcceptSource, Kind: stmt.Read,
			SQL: `SELECT id FROM library_sources
WHERE id=? AND library_id=? AND generation=? AND incarnation=? AND root_identity=? AND root=? AND follow_symlinks=?
AND kind='local' AND classification='local' AND enabled=1 AND identity_confirmed=1 AND health='ready' AND remote_source_id IS NULL`,
			Params: []stmt.Param{t("source_id"), t("library_key"), i("generation"), t("incarnation"), t("root_identity"), t("resolved_root"), boolean("follow_symlinks")},
			Cohorts: []stmt.Cohort{
				{Name: "current_source", Bindings: []any{"extra-source", "extra-library", int64(3), "extra-incarnation", "extra-root-identity", "/media/extra", false}},
				{Name: "stale_source", Bindings: []any{"extra-source", "extra-library", int64(4), "extra-incarnation", "extra-root-identity", "/media/extra", false}},
			}},
		{ID: iaExtraAcceptDirectory, Kind: stmt.Read,
			SQL: `SELECT pages,skipped FROM inventory_directories INDEXED BY sqlite_autoindex_inventory_directories_1
WHERE job_id=? AND relative_path=? AND identity=? AND revision=? AND cursor=? AND pages=? AND skipped=? AND state='pending'`,
			Params: []stmt.Param{t("run_id"), t("relative_path"), t("identity"), t("revision"), t("cursor"), i("pages"), i("skipped")},
			Cohorts: []stmt.Cohort{
				{Name: "pending_directory", Bindings: []any{"extra-run", "Extras", "extra-directory-identity", "extra-directory-revision", "", int64(2), int64(0)}},
				{Name: "stale_directory", Bindings: []any{"extra-run", "Extras", "extra-directory-identity", "extra-directory-revision", "9", int64(2), int64(0)}},
			}},
		{ID: iaExtraAcceptAsset, Kind: stmt.Read,
			SQL:    `SELECT id,token,info_epoch,file_size_revision FROM catalog_assets WHERE library_id=? AND path=? AND size=? AND modified_ns=? AND available=1`,
			Params: []stmt.Param{i("library_id"), t("path"), i("size"), i("modified_ns")},
			Cohorts: []stmt.Cohort{
				{Name: "observed_asset", Bindings: []any{int64(7), "/media/extra/Extras/Trailer.mkv", int64(42), int64(7)}},
				{Name: "changed_asset", Bindings: []any{int64(7), "/media/extra/Extras/Trailer.mkv", int64(43), int64(7)}},
			}},
		{ID: iaExtraAcceptIdentity, Kind: stmt.Read,
			SQL:    `SELECT subject_id FROM catalog_identities WHERE library_id=? AND source_key=?`,
			Params: []stmt.Param{i("library_id"), t("source_key")},
			Cohorts: []stmt.Cohort{
				{Name: "reused_extra_identity", Bindings: []any{int64(7), "extra-item-key"}},
				{Name: "new_extra_identity", Bindings: []any{int64(7), "extra-new-key"}},
				{Name: "resolved_owner_identity", Bindings: []any{int64(7), "extra-owner-key"}},
			}},
		{ID: iaExtraAcceptEntity, Kind: stmt.Read,
			SQL:    `SELECT kind,title,retired,revision,metadata_incarnation FROM catalog_entities WHERE id=? AND library_id=?`,
			Params: []stmt.Param{i("subject_id"), i("library_id")},
			Cohorts: []stmt.Cohort{
				{Name: "reused_extra_entity", Bindings: []any{int64(31), int64(7)}},
				{Name: "reused_owner_entity", Bindings: []any{int64(32), int64(7)}},
				{Name: "missing_entity", Bindings: []any{int64(33), int64(7)}},
			}},
		{ID: iaExtraAcceptItem, Kind: stmt.Read,
			SQL:    `SELECT library_id,parent_path,owner_id,kind,title,asset_id,relative_path FROM item_extras WHERE item_id=?`,
			Params: []stmt.Param{i("item_id")},
			Cohorts: []stmt.Cohort{
				{Name: "reused_item", Bindings: []any{int64(31)}},
				{Name: "missing_item", Bindings: []any{int64(34)}},
			}},
		{ID: iaExtraAcceptDirectoryCAS, Kind: stmt.Write,
			SQL: `UPDATE inventory_directories INDEXED BY sqlite_autoindex_inventory_directories_1 SET cursor=?,pages=?
WHERE job_id=? AND relative_path=? AND identity=? AND revision=? AND cursor=? AND pages=? AND skipped=? AND state='pending'
RETURNING cursor,pages,skipped`,
			Params: []stmt.Param{t("next_cursor"), i("next_pages"), t("run_id"), t("relative_path"), t("identity"), t("revision"), t("cursor"), i("pages"), i("skipped")},
			Cohorts: []stmt.Cohort{
				{Name: "pending_to_next", Bindings: []any{"42", int64(3), "extra-run", "Extras", "extra-directory-identity", "extra-directory-revision", "", int64(2), int64(0)}},
				{Name: "stale_directory_cas", Bindings: []any{"42", int64(3), "extra-run", "Extras", "extra-directory-identity", "extra-directory-revision", "9", int64(2), int64(0)}},
			}},
		{ID: iaExtraAcceptRunCAS, Kind: stmt.Write,
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
				{Name: "actual_post_accept_revision", Bindings: []any{int64(6), int64(2), "extra-run", inc, int64(7), "extra-library", "/media/extra", false,
					"extra-source", int64(3), "extra-incarnation", "extra-root-identity", int64(4), "inventory", "first-import",
					int64(11), int64(13), int64(4000000000000), int64(5), int64(1), int64(1)}},
				{Name: "stale_run_cas", Bindings: []any{int64(6), int64(2), "extra-run", inc, int64(7), "extra-library", "/media/extra", false,
					"extra-source", int64(3), "extra-incarnation", "extra-root-identity", int64(4), "inventory", "first-import",
					int64(11), int64(13), int64(4000000000000), int64(5), int64(1), int64(0)}},
			}},
		{ID: iaExtraAcceptSubjectInsert, Kind: stmt.Write,
			SQL:    `INSERT INTO catalog_subjects(public_id,library_id,kind) VALUES(?,?,?) RETURNING id`,
			Params: []stmt.Param{b("public_id"), i("library_id"), i("kind")},
			Cohorts: []stmt.Cohort{
				{Name: "new_extra_subject", Bindings: []any{inc, int64(7), int64(11)}},
				{Name: "second_extra_subject", Bindings: []any{operation, int64(7), int64(11)}},
			}},
		{ID: iaExtraAcceptIdentityInsert, Kind: stmt.Write,
			SQL:    `INSERT INTO catalog_identities(library_id,source_key,subject_id) VALUES(?,?,?) ON CONFLICT(library_id,source_key) DO NOTHING`,
			Params: []stmt.Param{i("library_id"), t("source_key"), i("subject_id")},
			Cohorts: []stmt.Cohort{
				{Name: "first_identity", Bindings: []any{int64(7), "extra-item-key", int64(31)}},
				{Name: "concurrent_identity", Bindings: []any{int64(7), "extra-item-key", int64(32)}},
			}},
		{ID: iaExtraAcceptEntityInsert, Kind: stmt.Write,
			SQL: `INSERT INTO catalog_entities(
id,public_id,library_id,kind,title,sort_title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,
random_key,year,added_at,metadata_language,metadata_incarnation,artwork_incarnation,playback_origin_incarnation,playback_origin_revision)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
RETURNING id,revision,metadata_incarnation`,
			Params: []stmt.Param{i("subject_id"), b("public_id"), i("library_id"), i("kind"), t("title"), t("sort_title"), t("sort_key"), t("sort_head"),
				t("sort_key_keep_articles"), t("sort_head_keep_articles"), i("random_key"), i("year"), t("added_at"), t("metadata_language"),
				b("metadata_incarnation"), b("artwork_incarnation"), b("playback_origin_incarnation"), i("playback_origin_revision")},
			Cohorts: []stmt.Cohort{
				{Name: "new_extra_entity", Bindings: []any{int64(31), inc, int64(7), int64(11), "Trailer", "", "trailer", "t", "trailer", "t",
					int64(1), int64(2020), "2026-11-08T00:00:00.000000000Z", "en-US", inc, operation, digest, int64(1)}},
			}},
		{ID: iaExtraAcceptItemInsert, Kind: stmt.Write,
			SQL: `INSERT INTO item_extras(item_id,library_id,parent_path,owner_id,kind,title,asset_id,relative_path)
VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(item_id) DO NOTHING`,
			Params: []stmt.Param{i("item_id"), t("library_id"), t("parent_path"), ni("owner_id"), t("kind"), t("title"), t("asset_id"), t("relative_path")},
			Cohorts: []stmt.Cohort{
				{Name: "owned_extra", Bindings: []any{int64(31), "extra-library", "Extras", int64(32), "trailer", "Trailer", "extra-token", "Extras/Trailer.mkv"}},
				{Name: "unresolved_extra", Bindings: []any{int64(31), "extra-library", "Extras", nil, "trailer", "Trailer", "extra-token", "Extras/Trailer.mkv"}},
			}},
		{ID: iaExtraAcceptLinkInsert, Kind: stmt.Write,
			SQL: `INSERT INTO catalog_asset_links(cue_ordinal,entity_id,asset_id,part_index,available,start_seconds,end_seconds,edition,playback_origin_incarnation,playback_origin_revision,library_id)
VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(entity_id,asset_id) DO NOTHING`,
			Params: []stmt.Param{ni("cue_ordinal"), i("entity_id"), i("asset_id"), i("part_index"), i("available"), r("start_seconds"), nr("end_seconds"),
				t("edition"), b("playback_incarnation"), i("playback_revision"), i("library_id")},
			Cohorts: []stmt.Cohort{
				{Name: "whole_source_link", Bindings: []any{nil, int64(31), int64(41), int64(0), int64(1), float64(0), nil, "", inc, int64(1), int64(7)}},
				{Name: "ranged_link", Bindings: []any{nil, int64(31), int64(41), int64(1), int64(1), float64(60), float64(120), "Extended", inc, int64(1), int64(7)}},
			}},
		{ID: iaExtraAcceptContributionInsert, Kind: stmt.Write,
			SQL:    `INSERT INTO catalog_asset_contributions(library_id,parent_id,asset_id,membership_refs,authored_refs,file_size,file_size_revision) VALUES(?,?,?,?,?,?,?)`,
			Params: []stmt.Param{i("library_id"), i("parent_id"), i("asset_id"), i("membership_refs"), i("authored_refs"), i("file_size"), i("file_size_revision")},
			Cohorts: []stmt.Cohort{
				{Name: "one_extra_membership", Bindings: []any{int64(7), int64(31), int64(41), int64(1), int64(0), int64(42), int64(1)}},
			}},
	}
	// The extra accept appends the shared search-intent family under the exact
	// IDs applyIAMediaSearchInput names. IA-13 already owns that registration;
	// reuse it here rather than duplicating a second copy.
	defs = append(defs, iaBookSearchStatements()...)
	defs = append(defs,
		stmt.Definition{ID: iaExtraAcceptReceiptInsert, Kind: stmt.Write,
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
				{Name: "new_extra_receipt", Bindings: []any{"extra-run", inc, int64(2), operation, digest, iaExtraAcceptReceiptKind, int64(6), int64(1),
					"extra-source", "extra-incarnation", nil, nil, "", int64(1), digest,
					int64(1), int64(300), "extra-object", "extra-revision", "local_getdents64_cookie_v1",
					[]byte("Extras"), []byte("extra-directory-identity"), []byte("extra-directory-revision"), []byte(""), []byte("42")}},
			}},
		stmt.Definition{ID: iaExtraAcceptMemberInsert, Kind: stmt.Write,
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
				{Name: "new_extra_identity", Bindings: []any{"extra-run", inc, int64(2), iaExtraAcceptReceiptKind, int64(0), "extra", int64(7), int64(31), int64(11), int64(1), operation,
					int64(32), int64(1), nil, nil, int64(41), int64(1), int64(0), int64(1), inc,
					"", "present", nil, float64(0), nil, nil, nil, nil, nil, "trailer"}},
				{Name: "unresolved_extra_identity", Bindings: []any{"extra-run", inc, int64(2), iaExtraAcceptReceiptKind, int64(0), "extra", int64(7), int64(31), int64(11), int64(1), operation,
					nil, nil, nil, nil, int64(41), int64(1), int64(0), int64(1), inc,
					"", "present", nil, float64(0), nil, nil, nil, nil, nil, "trailer"}},
			}},
	)
	for n := range defs {
		defs[n].Owner = "store/writer IA extra accept"
		defs[n].CallSites = []string{"store/writer/ia_extra_accept.go", "store/writer/ia_extra_accept_statements.go"}
		defs[n].Plan = stmt.Plan{Hot: true}
		switch defs[n].ID {
		case iaExtraAcceptOwner:
			defs[n].Plan.Indexes = []string{"sqlite_autoindex_inventory_runs_1"}
			defs[n].Plan.Seeks = []string{"job_id=?"}
		case iaExtraAcceptHeader:
			defs[n].Plan.PrimaryKeys = []string{"scan_command_receipts"}
			defs[n].Plan.Seeks = []string{"run_id=? AND command_incarnation=? AND command_sequence=?"}
		case iaExtraAcceptMemberBytes, iaExtraAcceptMembers:
			defs[n].Plan.PrimaryKeys = []string{"scan_media_result_members"}
			defs[n].Plan.Seeks = []string{"run_id=? AND command_incarnation=? AND command_sequence=? AND command_kind=?"}
		case iaExtraAcceptLibrary:
			defs[n].Plan.PrimaryKeys = []string{"catalog_libraries"}
			defs[n].Plan.Seeks = []string{"rowid=?"}
		case iaExtraAcceptSource:
			defs[n].Plan.Indexes = []string{"sqlite_autoindex_library_sources_2"}
			defs[n].Plan.Seeks = []string{"library_id=? AND root=?"}
		case iaExtraAcceptDirectory:
			defs[n].Plan.Indexes = []string{"sqlite_autoindex_inventory_directories_1"}
			defs[n].Plan.AutoIndexes = []stmt.AutoIndexBinding{{Table: "inventory_directories", Name: "sqlite_autoindex_inventory_directories_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "job_id", Collation: "BINARY"}, {Name: "relative_path", Collation: "BINARY"}}}}
			defs[n].Plan.Seeks = []string{"job_id=? AND relative_path=?"}
		case iaExtraAcceptAsset:
			defs[n].Plan.Indexes = []string{"sqlite_autoindex_catalog_assets_3"}
			defs[n].Plan.Seeks = []string{"library_id=? AND path=?"}
		case iaExtraAcceptIdentityInsert:
			defs[n].Plan.PrimaryKeys = []string{"catalog_identities"}
		case iaExtraAcceptLinkInsert:
			defs[n].Plan.PrimaryKeys = []string{"catalog_asset_links"}
		case iaExtraAcceptItemInsert:
			defs[n].Plan.PrimaryKeys = []string{"item_extras"}
		case iaExtraAcceptIdentity:
			defs[n].Plan.PrimaryKeys = []string{"catalog_identities"}
			defs[n].Plan.Seeks = []string{"library_id=? AND source_key=?"}
		case iaExtraAcceptEntity:
			defs[n].Plan.PrimaryKeys = []string{"catalog_entities"}
			defs[n].Plan.Seeks = []string{"rowid=?"}
		case iaExtraAcceptItem:
			defs[n].Plan.PrimaryKeys = []string{"item_extras"}
			defs[n].Plan.Seeks = []string{"rowid=?"}
		case iaExtraAcceptDirectoryCAS:
			defs[n].Plan.Indexes = []string{"sqlite_autoindex_inventory_directories_1"}
			defs[n].Plan.AutoIndexes = []stmt.AutoIndexBinding{{Table: "inventory_directories", Name: "sqlite_autoindex_inventory_directories_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "job_id", Collation: "BINARY"}, {Name: "relative_path", Collation: "BINARY"}}}}
			defs[n].Plan.Seeks = []string{"job_id=? AND relative_path=?"}
		case iaExtraAcceptRunCAS:
			defs[n].Plan.Indexes = []string{"sqlite_autoindex_inventory_runs_2"}
			defs[n].Plan.Seeks = []string{"job_id=? AND command_incarnation=?"}
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

// Complete private family: current US owner authority, the finite extra
// points/CAS writes and the shared search-intent append. No configuration,
// ordinary-child or legacy result definition is composed here.
func iaExtraAcceptDefinitions() []stmt.Definition {
	return append(usAuthorityDefinitions(), iaExtraAcceptStatements()...)
}
