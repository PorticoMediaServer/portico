package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// IA-15 statement family. Every statement is a point read, a PK-range read or
// one finite insert/CAS on the exact group C draft relations plus the embedded
// reading catalogue tables. There is no OFFSET, temporary sort, library-wide
// scan or caller-selected SQL. The typed member cohort is read only through its
// own receipt/kind prefix.
const (
	iaReadingOwner              stmt.ID = "ia.reading.owner"
	iaReadingHeader             stmt.ID = "ia.reading.header"
	iaReadingMemberBytes        stmt.ID = "ia.reading.member_bytes"
	iaReadingMembers            stmt.ID = "ia.reading.members"
	iaReadingLibrary            stmt.ID = "ia.reading.library"
	iaReadingLibraryKind        stmt.ID = "ia.reading.library_kind"
	iaReadingSource             stmt.ID = "ia.reading.source"
	iaReadingJob                stmt.ID = "ia.reading.job"
	iaReadingDirectory          stmt.ID = "ia.reading.directory"
	iaReadingAsset              stmt.ID = "ia.reading.asset"
	iaReadingIdentity           stmt.ID = "ia.reading.identity"
	iaReadingEntity             stmt.ID = "ia.reading.entity"
	iaReadingPublicationAsset   stmt.ID = "ia.reading.publication_asset"
	iaReadingPublications       stmt.ID = "ia.reading.publications"
	iaReadingDirectoryCAS       stmt.ID = "ia.reading.directory_cas"
	iaReadingRunCAS             stmt.ID = "ia.reading.run_cas"
	iaReadingSubjectInsert      stmt.ID = "ia.reading.subject_insert"
	iaReadingIdentityInsert     stmt.ID = "ia.reading.identity_insert"
	iaReadingEntityInsert       stmt.ID = "ia.reading.entity_insert"
	iaReadingLinkInsert         stmt.ID = "ia.reading.link_insert"
	iaReadingContributionInsert stmt.ID = "ia.reading.contribution_insert"
	iaReadingPublicationInsert  stmt.ID = "ia.reading.publication_insert"
	iaReadingReceiptInsert      stmt.ID = "ia.reading.receipt_insert"
	iaReadingMemberInsert       stmt.ID = "ia.reading.member_insert"
)

const iaReadingReceiptKind = "reading_inventory_accept"

func iaReadingStatements() []stmt.Definition {
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
		{ID: iaReadingOwner, Kind: stmt.Read,
			SQL:    `SELECT command_incarnation,last_command_sequence,acknowledged_command_sequence FROM inventory_runs WHERE job_id=?`,
			Params: []stmt.Param{t("run_id")},
			Cohorts: []stmt.Cohort{
				{Name: "current_owner", Bindings: []any{"reading-run"}},
				{Name: "missing_owner", Bindings: []any{"reading-missing"}},
			}},
		{ID: iaReadingHeader, Kind: stmt.Read,
			SQL: `SELECT operation_id,request_digest,command_kind,result_count,result_digest,accepted_revision,applied_ms,source_id,source_incarnation,
ia_media_result_version,ia_media_member_bytes,ia_media_object_id,ia_media_object_revision,ia_media_cursor_kind,
ia_media_directory_path,ia_media_directory_identity,ia_media_directory_revision,ia_media_expected_payload,ia_media_next_payload
FROM scan_command_receipts WHERE run_id=? AND command_incarnation=? AND command_sequence=?`,
			Params: key,
			Cohorts: []stmt.Cohort{
				{Name: "saved_reading_receipt", Bindings: []any{"reading-run", inc, int64(2)}},
				{Name: "missing_receipt", Bindings: []any{"reading-missing", inc, int64(2)}},
			}},
		{ID: iaReadingMemberBytes, Kind: stmt.Read,
			SQL: `SELECT ordinal,retained_bytes FROM scan_media_result_members
WHERE run_id=? AND command_incarnation=? AND command_sequence=? AND command_kind='reading_inventory_accept'
ORDER BY ordinal LIMIT ?`,
			Params: append(append([]stmt.Param{}, key...), i("row_limit")),
			Cohorts: []stmt.Cohort{
				{Name: "saved_member_bytes", Bindings: []any{"reading-run", inc, int64(2), int64(9)}},
				{Name: "missing_member_bytes", Bindings: []any{"reading-missing", inc, int64(2), int64(9)}},
			}},
		{ID: iaReadingMembers, Kind: stmt.Read,
			SQL: `SELECT ordinal,member_kind,library_id,subject_id,subject_kind,subject_revision,metadata_incarnation,
parent_id,parent_kind,ancestor_id,ancestor_kind,asset_id,asset_info_epoch,part_index,link_origin_revision,link_origin_incarnation,
edition,link_effect,cue_ordinal,start_seconds,end_seconds,episode_boundary_status,reading_format,reading_path_head,reading_path_sequence,extra_kind
FROM scan_media_result_members
WHERE run_id=? AND command_incarnation=? AND command_sequence=? AND command_kind='reading_inventory_accept'
ORDER BY ordinal LIMIT ?`,
			Params: append(append([]stmt.Param{}, key...), i("row_limit")),
			Cohorts: []stmt.Cohort{
				{Name: "saved_members", Bindings: []any{"reading-run", inc, int64(2), int64(9)}},
				{Name: "missing_members", Bindings: []any{"reading-missing", inc, int64(2), int64(9)}},
			}},
		{ID: iaReadingLibrary, Kind: stmt.Read,
			SQL:    `SELECT id FROM catalog_libraries WHERE id=? AND library_id=? AND retired=0`,
			Params: []stmt.Param{i("library_id"), t("library_key")},
			Cohorts: []stmt.Cohort{
				{Name: "live_library", Bindings: []any{int64(7), "reading-library"}},
				{Name: "retired_library", Bindings: []any{int64(8), "reading-retired"}},
			}},
		{ID: iaReadingLibraryKind, Kind: stmt.Read,
			SQL:    `SELECT kind FROM libraries WHERE id=?`,
			Params: []stmt.Param{t("library_key")},
			Cohorts: []stmt.Cohort{
				{Name: "book_library", Bindings: []any{"reading-library"}},
				{Name: "comic_library", Bindings: []any{"reading-comics"}},
			}},
		{ID: iaReadingSource, Kind: stmt.Read,
			SQL: `SELECT id FROM library_sources
WHERE id=? AND library_id=? AND generation=? AND incarnation=? AND root_identity=? AND root=? AND follow_symlinks=?
AND kind='local' AND classification='local' AND enabled=1 AND identity_confirmed=1 AND health='ready' AND remote_source_id IS NULL`,
			Params: []stmt.Param{t("source_id"), t("library_key"), i("generation"), t("incarnation"), t("root_identity"), t("resolved_root"), boolean("follow_symlinks")},
			Cohorts: []stmt.Cohort{
				{Name: "current_source", Bindings: []any{"reading-source", "reading-library", int64(3), "reading-incarnation", "reading-root-identity", "/media/books", false}},
				{Name: "stale_source", Bindings: []any{"reading-source", "reading-library", int64(4), "reading-incarnation", "reading-root-identity", "/media/books", false}},
			}},
		{ID: iaReadingJob, Kind: stmt.Read,
			SQL:    `SELECT source_id,incarnation,generation,root_identity,root FROM inventory_reading_jobs WHERE job_id=?`,
			Params: []stmt.Param{t("run_id")},
			Cohorts: []stmt.Cohort{
				{Name: "current_reading_job", Bindings: []any{"reading-run"}},
				{Name: "missing_reading_job", Bindings: []any{"reading-missing"}},
			}},
		{ID: iaReadingDirectory, Kind: stmt.Read,
			SQL: `SELECT pages,skipped FROM inventory_directories
WHERE job_id=? AND relative_path=? AND identity=? AND revision=? AND cursor=? AND pages=? AND skipped=? AND state='pending'`,
			Params: []stmt.Param{t("run_id"), t("relative_path"), t("identity"), t("revision"), t("cursor"), i("pages"), i("skipped")},
			Cohorts: []stmt.Cohort{
				{Name: "pending_directory", Bindings: []any{"reading-run", "Books/Novel", "reading-directory-identity", "reading-directory-revision", "", int64(2), int64(0)}},
				{Name: "stale_directory", Bindings: []any{"reading-run", "Books/Novel", "reading-directory-identity", "reading-directory-revision", "9", int64(2), int64(0)}},
			}},
		{ID: iaReadingAsset, Kind: stmt.Read,
			SQL:    `SELECT id,token,info_epoch,file_size_revision FROM catalog_assets WHERE library_id=? AND path=? AND size=? AND modified_ns=? AND available=1`,
			Params: []stmt.Param{i("library_id"), t("path"), i("size"), i("modified_ns")},
			Cohorts: []stmt.Cohort{
				{Name: "observed_asset", Bindings: []any{int64(7), "/media/books/Books/Novel/novel.epub", int64(42), int64(7)}},
				{Name: "changed_asset", Bindings: []any{int64(7), "/media/books/Books/Novel/novel.epub", int64(43), int64(7)}},
			}},
		{ID: iaReadingIdentity, Kind: stmt.Read,
			SQL:    `SELECT subject_id FROM catalog_identities WHERE library_id=? AND source_key=?`,
			Params: []stmt.Param{i("library_id"), t("source_key")},
			Cohorts: []stmt.Cohort{
				{Name: "reused_identity", Bindings: []any{int64(7), "reading-item-key"}},
				{Name: "new_identity", Bindings: []any{int64(7), "reading-new-key"}},
			}},
		{ID: iaReadingEntity, Kind: stmt.Read,
			SQL:    `SELECT kind,retired,revision,metadata_incarnation,reading_publication_type,reading_publication_type_source FROM catalog_entities WHERE id=? AND library_id=?`,
			Params: []stmt.Param{i("subject_id"), i("library_id")},
			Cohorts: []stmt.Cohort{
				{Name: "reused_entity", Bindings: []any{int64(31), int64(7)}},
				{Name: "missing_entity", Bindings: []any{int64(32), int64(7)}},
			}},
		{ID: iaReadingPublicationAsset, Kind: stmt.Read,
			SQL:    `SELECT item_id FROM reading_publications WHERE asset_id=?`,
			Params: []stmt.Param{i("asset_id")},
			Cohorts: []stmt.Cohort{
				{Name: "unattached_asset", Bindings: []any{int64(41)}},
				{Name: "attached_asset", Bindings: []any{int64(42)}},
			}},
		{ID: iaReadingPublications, Kind: stmt.Read,
			SQL:    `SELECT asset_id FROM reading_publications WHERE item_id=? ORDER BY asset_id LIMIT 1`,
			Params: []stmt.Param{i("item_id")},
			Cohorts: []stmt.Cohort{
				{Name: "no_publication", Bindings: []any{int64(31)}},
				{Name: "existing_publication", Bindings: []any{int64(32)}},
			}},
		{ID: iaReadingDirectoryCAS, Kind: stmt.Write,
			SQL: `UPDATE inventory_directories SET cursor=?,pages=?
WHERE job_id=? AND relative_path=? AND identity=? AND revision=? AND cursor=? AND pages=? AND skipped=? AND state='pending'
RETURNING cursor,pages,skipped`,
			Params: []stmt.Param{t("next_cursor"), i("next_pages"), t("run_id"), t("relative_path"), t("identity"), t("revision"), t("cursor"), i("pages"), i("skipped")},
			Cohorts: []stmt.Cohort{
				{Name: "pending_to_next", Bindings: []any{"42", int64(3), "reading-run", "Books/Novel", "reading-directory-identity", "reading-directory-revision", "", int64(2), int64(0)}},
				{Name: "stale_directory_cas", Bindings: []any{"42", int64(3), "reading-run", "Books/Novel", "reading-directory-identity", "reading-directory-revision", "9", int64(2), int64(0)}},
			}},
		{ID: iaReadingRunCAS, Kind: stmt.Write,
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
				{Name: "actual_post_accept_revision", Bindings: []any{int64(6), int64(2), "reading-run", inc, int64(7), "reading-library", "/media/books", false,
					"reading-source", int64(3), "reading-incarnation", "reading-root-identity", int64(4), "inventory", "first-import",
					int64(11), int64(13), int64(4000000000000), int64(5), int64(1), int64(1)}},
				{Name: "stale_run_cas", Bindings: []any{int64(6), int64(2), "reading-run", inc, int64(7), "reading-library", "/media/books", false,
					"reading-source", int64(3), "reading-incarnation", "reading-root-identity", int64(4), "inventory", "first-import",
					int64(11), int64(13), int64(4000000000000), int64(5), int64(1), int64(0)}},
			}},
		{ID: iaReadingSubjectInsert, Kind: stmt.Write,
			SQL:    `INSERT INTO catalog_subjects(public_id,library_id,kind) VALUES(?,?,?) RETURNING id`,
			Params: []stmt.Param{b("public_id"), i("library_id"), i("kind")},
			Cohorts: []stmt.Cohort{
				{Name: "new_ebook_subject", Bindings: []any{inc, int64(7), int64(20)}},
				{Name: "new_comic_subject", Bindings: []any{operation, int64(7), int64(21)}},
			}},
		{ID: iaReadingIdentityInsert, Kind: stmt.Write,
			SQL:    `INSERT INTO catalog_identities(library_id,source_key,subject_id) VALUES(?,?,?) ON CONFLICT DO NOTHING`,
			Params: []stmt.Param{i("library_id"), t("source_key"), i("subject_id")},
			Cohorts: []stmt.Cohort{
				{Name: "first_identity", Bindings: []any{int64(7), "reading-item-key", int64(31)}},
				{Name: "concurrent_identity", Bindings: []any{int64(7), "reading-item-key", int64(32)}},
			}},
		{ID: iaReadingEntityInsert, Kind: stmt.Write,
			SQL: `INSERT INTO catalog_entities(
id,public_id,library_id,kind,title,sort_title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,
random_key,year,added_at,metadata_language,reading_publication_type,reading_publication_type_source,
metadata_incarnation,artwork_incarnation)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
RETURNING id,revision,metadata_incarnation`,
			Params: []stmt.Param{i("subject_id"), b("public_id"), i("library_id"), i("kind"), t("title"), t("sort_title"), t("sort_key"), t("sort_head"),
				t("sort_key_keep_articles"), t("sort_head_keep_articles"), i("random_key"), i("year"), t("added_at"), t("metadata_language"),
				nt("reading_publication_type"), nt("reading_publication_type_source"), b("metadata_incarnation"), b("artwork_incarnation")},
			Cohorts: []stmt.Cohort{
				{Name: "new_ebook_entity", Bindings: []any{int64(31), inc, int64(7), int64(20), "Novel", "", "novel", "n", "novel", "n",
					int64(1), int64(2020), "2026-11-08T00:00:00.000000000Z", "en-US", "book", "file-metadata", inc, operation}},
				{Name: "new_comic_entity", Bindings: []any{int64(32), inc, int64(7), int64(21), "Comic", "", "comic", "c", "comic", "c",
					int64(1), int64(2021), "2026-11-08T00:00:00.000000000Z", "en-US", nil, nil, inc, operation}},
			}},
		{ID: iaReadingLinkInsert, Kind: stmt.Write,
			SQL: `INSERT INTO catalog_asset_links(entity_id,asset_id,part_index,available,start_seconds,end_seconds,edition,playback_origin_incarnation,playback_origin_revision,cue_ordinal,library_id)
VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(entity_id,asset_id) DO NOTHING`,
			Params: []stmt.Param{i("entity_id"), i("asset_id"), i("part_index"), i("available"), r("start_seconds"), nr("end_seconds"), t("edition"), b("playback_incarnation"), i("playback_revision"), ni("cue_ordinal"), i("library_id")},
			Cohorts: []stmt.Cohort{
				{Name: "reading_publication_link", Bindings: []any{int64(31), int64(41), int64(0), int64(1), float64(0), nil, "", inc, int64(1), nil, int64(7)}},
			}},
		{ID: iaReadingContributionInsert, Kind: stmt.Write,
			SQL:    `INSERT INTO catalog_asset_contributions(library_id,parent_id,asset_id,membership_refs,authored_refs,file_size,file_size_revision) VALUES(?,?,?,?,?,?,?)`,
			Params: []stmt.Param{i("library_id"), i("parent_id"), i("asset_id"), i("membership_refs"), i("authored_refs"), i("file_size"), i("file_size_revision")},
			Cohorts: []stmt.Cohort{
				{Name: "one_publication_membership", Bindings: []any{int64(7), int64(31), int64(41), int64(1), int64(0), int64(42), int64(1)}},
			}},
		{ID: iaReadingPublicationInsert, Kind: stmt.Write,
			SQL: `INSERT INTO reading_publications(item_id,asset_id,format,has_reading_path_head,path_publisher_sequence,path_object_id,path_source_revision)
VALUES(?,?,?,?,?,?,?)`,
			Params: []stmt.Param{i("item_id"), i("asset_id"), t("format"), i("has_reading_path_head"), ni("path_publisher_sequence"), nt("path_object_id"), nt("path_source_revision")},
			Cohorts: []stmt.Cohort{
				{Name: "path_head_publication", Bindings: []any{int64(31), int64(41), "epub", int64(1), int64(1), "object:reading:1", "object-revision:1"}},
				{Name: "alternative_publication", Bindings: []any{int64(31), int64(42), "pdf", int64(0), nil, nil, nil}},
			}},
	}
	// The reading accept appends the shared search-intent family. applyIAMediaSearchInput
	// names the exact IA-11 IDs; IA-13 registers that family once, so it is reused
	// here instead of duplicated.
	defs = append(defs, iaBookSearchStatements()...)
	defs = append(defs,
		stmt.Definition{ID: iaReadingReceiptInsert, Kind: stmt.Write,
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
				{Name: "new_reading_receipt", Bindings: []any{"reading-run", inc, int64(2), operation, digest, iaReadingReceiptKind, int64(6), int64(1),
					"reading-source", "reading-incarnation", nil, nil, "", int64(1), digest,
					int64(1), int64(900), "reading-object", "reading-revision", "local_getdents64_cookie_v1",
					[]byte("Books/Novel"), []byte("reading-directory-identity"), []byte("reading-directory-revision"), []byte(""), []byte("42")}},
			}},
		stmt.Definition{ID: iaReadingMemberInsert, Kind: stmt.Write,
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
				{Name: "new_reading_publication", Bindings: []any{"reading-run", inc, int64(2), iaReadingReceiptKind, int64(0), "reading", int64(7), int64(31), int64(20), int64(1), operation,
					nil, nil, nil, nil, int64(41), int64(1), int64(0), int64(1), inc,
					"", "present", nil, float64(0), nil, nil, "epub", int64(1), int64(1), nil}},
				{Name: "new_reading_alternative", Bindings: []any{"reading-run", inc, int64(2), iaReadingReceiptKind, int64(0), "reading", int64(7), int64(31), int64(21), int64(1), operation,
					nil, nil, nil, nil, int64(42), int64(1), int64(0), int64(1), inc,
					"", "present", nil, float64(0), nil, nil, "cbz", int64(0), nil, nil}},
			}},
	)
	for n := range defs {
		defs[n].Owner = "store/writer IA reading accept"
		defs[n].CallSites = []string{"store/writer/ia_reading_accept.go", "store/writer/ia_reading_types.go"}
		defs[n].Plan = stmt.Plan{Hot: true}
		switch defs[n].ID {
		case iaReadingOwner:
			defs[n].Plan.Indexes = []string{"sqlite_autoindex_inventory_runs_1"}
			defs[n].Plan.Seeks = []string{"job_id=?"}
		case iaReadingHeader:
			defs[n].Plan.PrimaryKeys = []string{"scan_command_receipts"}
			defs[n].Plan.Seeks = []string{"run_id=? AND command_incarnation=? AND command_sequence=?"}
		case iaReadingMemberBytes, iaReadingMembers:
			defs[n].Plan.PrimaryKeys = []string{"scan_media_result_members"}
			defs[n].Plan.Seeks = []string{"run_id=? AND command_incarnation=? AND command_sequence=? AND command_kind=?"}
		case iaReadingLibrary:
			defs[n].Plan.PrimaryKeys = []string{"catalog_libraries"}
			defs[n].Plan.Seeks = []string{"rowid=?"}
		case iaReadingLibraryKind:
			defs[n].Plan.Indexes = []string{"sqlite_autoindex_libraries_1"}
			defs[n].Plan.Seeks = []string{"id=?"}
		case iaReadingSource:
			defs[n].Plan.Indexes = []string{"sqlite_autoindex_library_sources_2"}
			defs[n].Plan.Seeks = []string{"library_id=? AND root=?"}
		case iaReadingJob:
			defs[n].Plan.PrimaryKeys = []string{"inventory_reading_jobs"}
			defs[n].Plan.Seeks = []string{"job_id=?"}
		case iaReadingDirectory:
			defs[n].Plan.PrimaryKeys = []string{"inventory_directories"}
			defs[n].Plan.Seeks = []string{"job_id=? AND relative_path=?"}
		case iaReadingAsset:
			defs[n].Plan.Indexes = []string{"sqlite_autoindex_catalog_assets_3"}
			defs[n].Plan.Seeks = []string{"library_id=? AND path=?"}
		case iaReadingIdentity:
			defs[n].Plan.PrimaryKeys = []string{"catalog_identities"}
			defs[n].Plan.Seeks = []string{"library_id=? AND source_key=?"}
		case iaReadingEntity:
			defs[n].Plan.PrimaryKeys = []string{"catalog_entities"}
			defs[n].Plan.Seeks = []string{"rowid=?"}
		case iaReadingPublicationAsset:
			defs[n].Plan.Indexes = []string{"sqlite_autoindex_reading_publications_1"}
			defs[n].Plan.Seeks = []string{"asset_id=?"}
		case iaReadingPublications:
			defs[n].Plan.Indexes = []string{"sqlite_autoindex_reading_publications_2"}
			defs[n].Plan.Seeks = []string{"item_id=?"}
		case iaReadingDirectoryCAS:
			defs[n].Plan.PrimaryKeys = []string{"inventory_directories"}
			defs[n].Plan.Seeks = []string{"job_id=? AND relative_path=?"}
		case iaReadingRunCAS:
			defs[n].Plan.Indexes = []string{"sqlite_autoindex_inventory_runs_2"}
			defs[n].Plan.Seeks = []string{"job_id=? AND command_incarnation=?"}
		case iaEpisodeSearchHead:
			defs[n].Plan.PrimaryKeys = []string{"search_revision"}
			defs[n].Plan.Seeks = []string{"rowid=?"}
		case iaEpisodeSearchBank:
			defs[n].Plan.PrimaryKeys = []string{"search_generations"}
			defs[n].Plan.Seeks = []string{"rowid=?"}
		case iaEpisodeSearchExisting:
			defs[n].Plan.Indexes = []string{"sqlite_autoindex_search_publish_jobs_1"}
			defs[n].Plan.Seeks = []string{"entity_id=?"}
		}
	}
	return defs
}

// Complete private family: current US owner authority, the finite reading
// points/CAS writes, the shared search-intent append and the typed receipt
// cohort. No configuration, ordinary-child or legacy result definition is
// composed here.
func iaReadingDefinitions() []stmt.Definition {
	return append(usAuthorityDefinitions(), iaReadingStatements()...)
}
