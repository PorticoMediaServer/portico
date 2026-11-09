package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// IA-13 statement family. Every statement is a point read, a PK-range read or
// one finite insert/CAS on the exact group C draft relations plus the embedded
// catalogue tables and the group C author-key column. There is no OFFSET,
// temporary sort, library-wide scan or caller-selected SQL. The typed member
// cohort is read only through its own receipt/kind prefix.
const (
	iaBookOwner              stmt.ID = "ia.audio.book.owner"
	iaBookHeader             stmt.ID = "ia.audio.book.header"
	iaBookMemberBytes        stmt.ID = "ia.audio.book.member_bytes"
	iaBookMembers            stmt.ID = "ia.audio.book.members"
	iaBookLibrary            stmt.ID = "ia.audio.book.library"
	iaBookSource             stmt.ID = "ia.audio.book.source"
	iaBookDirectory          stmt.ID = "ia.audio.book.directory"
	iaBookAsset              stmt.ID = "ia.audio.book.asset"
	iaBookIdentity           stmt.ID = "ia.audio.book.identity"
	iaBookEntity             stmt.ID = "ia.audio.book.entity"
	iaBookDirectoryCAS       stmt.ID = "ia.audio.book.directory_cas"
	iaBookRunCAS             stmt.ID = "ia.audio.book.run_cas"
	iaBookSubjectInsert      stmt.ID = "ia.audio.book.subject_insert"
	iaBookIdentityInsert     stmt.ID = "ia.audio.book.identity_insert"
	iaBookEntityInsert       stmt.ID = "ia.audio.book.entity_insert"
	iaBookLinkInsert         stmt.ID = "ia.audio.book.link_insert"
	iaBookContributionInsert stmt.ID = "ia.audio.book.contribution_insert"
	iaBookReceiptInsert      stmt.ID = "ia.audio.book.receipt_insert"
	iaBookMemberInsert       stmt.ID = "ia.audio.book.member_insert"
)

const iaBookReceiptKind = "audio_inventory_accept"

func iaBookStatements() []stmt.Definition {
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
		{ID: iaBookOwner, Kind: stmt.Read,
			SQL:    `SELECT command_incarnation,last_command_sequence,acknowledged_command_sequence FROM inventory_runs WHERE job_id=?`,
			Params: []stmt.Param{t("run_id")},
			Cohorts: []stmt.Cohort{
				{Name: "current_owner", Bindings: []any{"books-run"}},
				{Name: "missing_owner", Bindings: []any{"books-missing"}},
			}},
		{ID: iaBookHeader, Kind: stmt.Read,
			SQL: `SELECT operation_id,request_digest,command_kind,result_count,result_digest,accepted_revision,applied_ms,source_id,source_incarnation,
ia_media_result_version,ia_media_member_bytes,ia_media_object_id,ia_media_object_revision,ia_media_cursor_kind,
ia_media_directory_path,ia_media_directory_identity,ia_media_directory_revision,ia_media_expected_payload,ia_media_next_payload
FROM scan_command_receipts WHERE run_id=? AND command_incarnation=? AND command_sequence=?`,
			Params: key,
			Cohorts: []stmt.Cohort{
				{Name: "saved_book_receipt", Bindings: []any{"books-run", inc, int64(2)}},
				{Name: "missing_receipt", Bindings: []any{"books-missing", inc, int64(2)}},
			}},
		{ID: iaBookMemberBytes, Kind: stmt.Read,
			SQL: `SELECT ordinal,retained_bytes FROM scan_media_result_members
WHERE run_id=? AND command_incarnation=? AND command_sequence=? AND command_kind='audio_inventory_accept'
ORDER BY ordinal LIMIT ?`,
			Params: append(append([]stmt.Param{}, key...), i("row_limit")),
			Cohorts: []stmt.Cohort{
				{Name: "saved_member_bytes", Bindings: []any{"books-run", inc, int64(2), int64(9)}},
				{Name: "missing_member_bytes", Bindings: []any{"books-missing", inc, int64(2), int64(9)}},
			}},
		{ID: iaBookMembers, Kind: stmt.Read,
			SQL: `SELECT ordinal,member_kind,library_id,subject_id,subject_kind,subject_revision,metadata_incarnation,
parent_id,parent_kind,ancestor_id,ancestor_kind,asset_id,asset_info_epoch,part_index,link_origin_revision,link_origin_incarnation,
edition,link_effect,cue_ordinal,start_seconds,end_seconds,episode_boundary_status,reading_format,reading_path_head,reading_path_sequence,extra_kind
FROM scan_media_result_members
WHERE run_id=? AND command_incarnation=? AND command_sequence=? AND command_kind='audio_inventory_accept'
ORDER BY ordinal LIMIT ?`,
			Params: append(append([]stmt.Param{}, key...), i("row_limit")),
			Cohorts: []stmt.Cohort{
				{Name: "saved_members", Bindings: []any{"books-run", inc, int64(2), int64(9)}},
				{Name: "missing_members", Bindings: []any{"books-missing", inc, int64(2), int64(9)}},
			}},
		{ID: iaBookLibrary, Kind: stmt.Read,
			SQL:    `SELECT id FROM catalog_libraries WHERE id=? AND library_id=? AND retired=0`,
			Params: []stmt.Param{i("library_id"), t("library_key")},
			Cohorts: []stmt.Cohort{
				{Name: "live_library", Bindings: []any{int64(7), "books-library"}},
				{Name: "retired_library", Bindings: []any{int64(8), "books-retired"}},
			}},
		{ID: iaBookSource, Kind: stmt.Read,
			SQL: `SELECT id FROM library_sources
WHERE id=? AND library_id=? AND generation=? AND incarnation=? AND root_identity=? AND root=? AND follow_symlinks=?
AND kind='local' AND classification='local' AND enabled=1 AND identity_confirmed=1 AND health='ready' AND remote_source_id IS NULL`,
			Params: []stmt.Param{t("source_id"), t("library_key"), i("generation"), t("incarnation"), t("root_identity"), t("resolved_root"), boolean("follow_symlinks")},
			Cohorts: []stmt.Cohort{
				{Name: "current_source", Bindings: []any{"books-source", "books-library", int64(3), "books-incarnation", "books-root-identity", "/media/books", false}},
				{Name: "stale_source", Bindings: []any{"books-source", "books-library", int64(4), "books-incarnation", "books-root-identity", "/media/books", false}},
			}},
		{ID: iaBookDirectory, Kind: stmt.Read,
			SQL: `SELECT pages,skipped FROM inventory_directories
WHERE job_id=? AND relative_path=? AND identity=? AND revision=? AND cursor=? AND pages=? AND skipped=? AND state='pending'`,
			Params: []stmt.Param{t("run_id"), t("relative_path"), t("identity"), t("revision"), t("cursor"), i("pages"), i("skipped")},
			Cohorts: []stmt.Cohort{
				{Name: "pending_directory", Bindings: []any{"books-run", "Author/Book", "books-directory-identity", "books-directory-revision", "", int64(2), int64(0)}},
				{Name: "stale_directory", Bindings: []any{"books-run", "Author/Book", "books-directory-identity", "books-directory-revision", "9", int64(2), int64(0)}},
			}},
		{ID: iaBookAsset, Kind: stmt.Read,
			SQL:    `SELECT id,token,info_epoch,file_size_revision FROM catalog_assets WHERE library_id=? AND path=? AND size=? AND modified_ns=? AND available=1`,
			Params: []stmt.Param{i("library_id"), t("path"), i("size"), i("modified_ns")},
			Cohorts: []stmt.Cohort{
				{Name: "observed_asset", Bindings: []any{int64(7), "/media/books/Author/Book/part1.m4b", int64(42), int64(7)}},
				{Name: "changed_asset", Bindings: []any{int64(7), "/media/books/Author/Book/part1.m4b", int64(43), int64(7)}},
			}},
		{ID: iaBookIdentity, Kind: stmt.Read,
			SQL:    `SELECT subject_id FROM catalog_identities WHERE library_id=? AND source_key=?`,
			Params: []stmt.Param{i("library_id"), t("source_key")},
			Cohorts: []stmt.Cohort{
				{Name: "reused_identity", Bindings: []any{int64(7), "book-key"}},
				{Name: "new_identity", Bindings: []any{int64(7), "book-new-key"}},
			}},
		{ID: iaBookEntity, Kind: stmt.Read,
			SQL:    `SELECT kind,parent_id,book_id,author,book_author_identity_key,narrator,series,series_position,retired,revision,metadata_incarnation FROM catalog_entities WHERE id=? AND library_id=?`,
			Params: []stmt.Param{i("subject_id"), i("library_id")},
			Cohorts: []stmt.Cohort{
				{Name: "reused_entity", Bindings: []any{int64(31), int64(7)}},
				{Name: "missing_entity", Bindings: []any{int64(32), int64(7)}},
			}},
		{ID: iaBookDirectoryCAS, Kind: stmt.Write,
			SQL: `UPDATE inventory_directories SET cursor=?,pages=?
WHERE job_id=? AND relative_path=? AND identity=? AND revision=? AND cursor=? AND pages=? AND skipped=? AND state='pending'
RETURNING cursor,pages,skipped`,
			Params: []stmt.Param{t("next_cursor"), i("next_pages"), t("run_id"), t("relative_path"), t("identity"), t("revision"), t("cursor"), i("pages"), i("skipped")},
			Cohorts: []stmt.Cohort{
				{Name: "pending_to_next", Bindings: []any{"42", int64(3), "books-run", "Author/Book", "books-directory-identity", "books-directory-revision", "", int64(2), int64(0)}},
				{Name: "stale_directory_cas", Bindings: []any{"42", int64(3), "books-run", "Author/Book", "books-directory-identity", "books-directory-revision", "9", int64(2), int64(0)}},
			}},
		{ID: iaBookRunCAS, Kind: stmt.Write,
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
				{Name: "actual_post_accept_revision", Bindings: []any{int64(6), int64(2), "books-run", inc, int64(7), "books-library", "/media/books", false,
					"books-source", int64(3), "books-incarnation", "books-root-identity", int64(4), "inventory", "first-import",
					int64(11), int64(13), int64(4000000000000), int64(5), int64(1), int64(1)}},
				{Name: "stale_run_cas", Bindings: []any{int64(6), int64(2), "books-run", inc, int64(7), "books-library", "/media/books", false,
					"books-source", int64(3), "books-incarnation", "books-root-identity", int64(4), "inventory", "first-import",
					int64(11), int64(13), int64(4000000000000), int64(5), int64(1), int64(0)}},
			}},
		{ID: iaBookSubjectInsert, Kind: stmt.Write,
			SQL:    `INSERT INTO catalog_subjects(public_id,library_id,kind) VALUES(?,?,?) RETURNING id`,
			Params: []stmt.Param{b("public_id"), i("library_id"), i("kind")},
			Cohorts: []stmt.Cohort{
				{Name: "new_author_subject", Bindings: []any{inc, int64(7), int64(13)}},
				{Name: "new_book_subject", Bindings: []any{operation, int64(7), int64(8)}},
			}},
		{ID: iaBookIdentityInsert, Kind: stmt.Write,
			SQL:    `INSERT INTO catalog_identities(library_id,source_key,subject_id) VALUES(?,?,?) ON CONFLICT(library_id,source_key) DO NOTHING`,
			Params: []stmt.Param{i("library_id"), t("source_key"), i("subject_id")},
			Cohorts: []stmt.Cohort{
				{Name: "first_identity", Bindings: []any{int64(7), "book-key", int64(31)}},
				{Name: "concurrent_identity", Bindings: []any{int64(7), "book-key", int64(32)}},
			}},
		{ID: iaBookEntityInsert, Kind: stmt.Write,
			SQL: `INSERT INTO catalog_entities(
id,public_id,library_id,kind,parent_id,title,sort_title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,
random_key,year,added_at,metadata_language,book_id,author,narrator,series,series_position,book_author_identity_key,
metadata_incarnation,artwork_incarnation,playback_origin_incarnation,playback_origin_revision)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
RETURNING id,revision,metadata_incarnation`,
			Params: []stmt.Param{i("subject_id"), b("public_id"), i("library_id"), i("kind"), ni("parent_id"), t("title"), t("sort_title"), t("sort_key"), t("sort_head"),
				t("sort_key_keep_articles"), t("sort_head_keep_articles"), i("random_key"), i("year"), t("added_at"), t("metadata_language"),
				ni("book_id"), t("author"), t("narrator"), t("series"), t("series_position"), nt("book_author_identity_key"),
				b("metadata_incarnation"), b("artwork_incarnation"), b("playback_origin_incarnation"), i("playback_origin_revision")},
			Cohorts: []stmt.Cohort{
				{Name: "new_author_entity", Bindings: []any{int64(31), inc, int64(7), int64(13), nil, "Author", "", "author", "a", "author", "a",
					int64(1), int64(0), "2026-11-08T00:00:00.000000000Z", "en-US", nil, "", "", "", "", nil,
					inc, operation, digest, int64(1)}},
				{Name: "new_book_entity", Bindings: []any{int64(32), inc, int64(7), int64(8), int64(31), "Book", "", "book", "b", "book", "b",
					int64(1), int64(2020), "2026-11-08T00:00:00.000000000Z", "en-US", nil, "Author", "Reader", "Series", "1", "author:author",
					inc, operation, digest, int64(1)}},
				{Name: "new_file_entity", Bindings: []any{int64(33), inc, int64(7), int64(9), int64(32), "Part 1", "", "part 1", "p", "part 1", "p",
					int64(1), int64(2020), "2026-11-08T00:00:00.000000000Z", "en-US", int64(32), "", "", "", "", nil,
					inc, operation, digest, int64(1)}},
			}},
		{ID: iaBookLinkInsert, Kind: stmt.Write,
			SQL: `INSERT INTO catalog_asset_links(entity_id,asset_id,part_index,available,start_seconds,end_seconds,edition,playback_origin_incarnation,playback_origin_revision,cue_ordinal,library_id)
VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(entity_id,asset_id) DO NOTHING`,
			Params: []stmt.Param{i("entity_id"), i("asset_id"), i("part_index"), i("available"), r("start_seconds"), nr("end_seconds"), t("edition"), b("playback_incarnation"), i("playback_revision"), ni("cue_ordinal"), i("library_id")},
			Cohorts: []stmt.Cohort{
				{Name: "whole_source_link", Bindings: []any{int64(33), int64(41), int64(0), int64(1), float64(0), nil, "", inc, int64(1), nil, int64(7)}},
				{Name: "cue_range_link", Bindings: []any{int64(33), int64(41), int64(0), int64(1), float64(0), float64(42), "", inc, int64(1), int64(1), int64(7)}},
			}},
		{ID: iaBookContributionInsert, Kind: stmt.Write,
			SQL:    `INSERT INTO catalog_asset_contributions(library_id,parent_id,asset_id,membership_refs,authored_refs,file_size,file_size_revision) VALUES(?,?,?,?,?,?,?)`,
			Params: []stmt.Param{i("library_id"), i("parent_id"), i("asset_id"), i("membership_refs"), i("authored_refs"), i("file_size"), i("file_size_revision")},
			Cohorts: []stmt.Cohort{
				{Name: "one_file_membership", Bindings: []any{int64(7), int64(33), int64(41), int64(1), int64(0), int64(42), int64(1)}},
			}},
	}
	// The book accept appends the shared search-intent family. applyIAMediaSearchInput
	// names these exact IDs, so the family is registered here instead of
	// duplicating the append protocol.
	defs = append(defs, iaBookSearchStatements()...)
	// IA-14 publishes the accepted local book-series projection inside the same
	// command; its exact head/member family is composed here, not registered
	// twice.
	defs = append(defs, iaBookSeriesStatements()...)
	defs = append(defs,
		stmt.Definition{ID: iaBookReceiptInsert, Kind: stmt.Write,
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
				{Name: "new_book_receipt", Bindings: []any{"books-run", inc, int64(2), operation, digest, iaBookReceiptKind, int64(6), int64(1),
					"books-source", "books-incarnation", nil, nil, "", int64(4), digest,
					int64(1), int64(900), "book-object", "book-revision", "local_getdents64_cookie_v1",
					[]byte("Author/Book"), []byte("books-directory-identity"), []byte("books-directory-revision"), []byte(""), []byte("42")}},
			}},
		stmt.Definition{ID: iaBookMemberInsert, Kind: stmt.Write,
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
				{Name: "new_book_author_identity", Bindings: []any{"books-run", inc, int64(2), iaBookReceiptKind, int64(0), "identity", int64(7), int64(31), int64(13), int64(1), operation,
					nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil}},
				{Name: "new_book_membership", Bindings: []any{"books-run", inc, int64(2), iaBookReceiptKind, int64(3), "membership", int64(7), int64(33), int64(9), int64(1), operation,
					nil, nil, nil, nil, int64(41), int64(1), int64(0), int64(1), inc,
					"", "present", nil, float64(0), nil, nil, nil, nil, nil, nil}},
			}},
	)
	for n := range defs {
		defs[n].Owner = "store/writer IA audiobook accept"
		defs[n].CallSites = []string{"store/writer/ia_audiobook_accept.go"}
		defs[n].Plan = stmt.Plan{Hot: true}
		switch defs[n].ID {
		case iaBookOwner:
			defs[n].Plan.Indexes = []string{"sqlite_autoindex_inventory_runs_1"}
			defs[n].Plan.Seeks = []string{"job_id=?"}
		case iaBookHeader:
			defs[n].Plan.PrimaryKeys = []string{"scan_command_receipts"}
			defs[n].Plan.Seeks = []string{"run_id=? AND command_incarnation=? AND command_sequence=?"}
		case iaBookMemberBytes, iaBookMembers:
			defs[n].Plan.PrimaryKeys = []string{"scan_media_result_members"}
			defs[n].Plan.Seeks = []string{"run_id=? AND command_incarnation=? AND command_sequence=? AND command_kind=?"}
		case iaBookLibrary:
			defs[n].Plan.PrimaryKeys = []string{"catalog_libraries"}
			defs[n].Plan.Seeks = []string{"rowid=?"}
		case iaBookSource:
			defs[n].Plan.Indexes = []string{"sqlite_autoindex_library_sources_2"}
			defs[n].Plan.Seeks = []string{"library_id=? AND root=?"}
		case iaBookDirectory:
			defs[n].Plan.PrimaryKeys = []string{"inventory_directories"}
			defs[n].Plan.Seeks = []string{"job_id=? AND relative_path=?"}
		case iaBookAsset:
			defs[n].Plan.Indexes = []string{"sqlite_autoindex_catalog_assets_3"}
			defs[n].Plan.Seeks = []string{"library_id=? AND path=?"}
		case iaBookIdentity:
			defs[n].Plan.PrimaryKeys = []string{"catalog_identities"}
			defs[n].Plan.Seeks = []string{"library_id=? AND source_key=?"}
		case iaBookEntity:
			defs[n].Plan.PrimaryKeys = []string{"catalog_entities"}
			defs[n].Plan.Seeks = []string{"rowid=?"}
		case iaBookDirectoryCAS:
			defs[n].Plan.PrimaryKeys = []string{"inventory_directories"}
			defs[n].Plan.Seeks = []string{"job_id=? AND relative_path=?"}
		case iaBookRunCAS:
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

// iaBookSearchStatements is the exact IA-11 search-intent family under the
// shared IDs applyIAMediaSearchInput names. It reuses IA-11's single definition
// of that family instead of keeping a second copy of the SQL and cohorts.
func iaBookSearchStatements() []stmt.Definition {
	var defs []stmt.Definition
	for _, def := range iaEpisodeStatements() {
		switch def.ID {
		case iaEpisodeSearchHead, iaEpisodeSearchBank, iaEpisodeSearchExisting, iaEpisodeSearchInsert,
			iaEpisodeSearchPending, iaEpisodeSearchSequence, iaEpisodeSearchBuilding:
			defs = append(defs, def)
		}
	}
	return defs
}

// Complete private family: current US owner authority, the finite book
// points/CAS writes, the shared search-intent append and the typed receipt
// cohort. No configuration, ordinary-child or legacy result definition is
// composed here.
func iaBookDefinitions() []stmt.Definition {
	return append(usAuthorityDefinitions(), iaBookStatements()...)
}
