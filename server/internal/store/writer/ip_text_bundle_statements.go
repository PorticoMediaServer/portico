package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	ipTextEditOwner             stmt.ID = "ip.text_edit.owner"
	ipTextEditOwnerInsert       stmt.ID = "ip.text_edit.owner_insert"
	ipTextEditOwnerAdvance      stmt.ID = "ip.text_edit.owner_advance"
	ipTextEditFieldRow          stmt.ID = "ip.text_edit.field"
	ipTextEditFieldInsert       stmt.ID = "ip.text_edit.field_insert"
	ipTextEditFieldUpdate       stmt.ID = "ip.text_edit.field_update"
	ipTextEditSubjectRow        stmt.ID = "ip.text_edit.source"
	ipTextEditPersonSource      stmt.ID = "ip.text_edit.person_source"
	ipTextEditProjectionCurrent stmt.ID = "ip.text_edit.projection_current"
	ipTextEditProjectionUpdate  stmt.ID = "ip.text_edit.projection_update"
	ipTextEditScalar            stmt.ID = "ip.text_edit.scalar_current"
	ipTextEditScalarUpdate      stmt.ID = "ip.text_edit.scalar_update"
	ipTextEditAuthor            stmt.ID = "ip.text_edit.author_current"
	ipTextEditAuthorUpdate      stmt.ID = "ip.text_edit.author_update"
	ipTextEditPersonCurrent     stmt.ID = "ip.text_edit.person_current"
	ipTextEditPersonUpdate      stmt.ID = "ip.text_edit.person_update"
	ipTextEditSubjectAdvance    stmt.ID = "ip.text_edit.subject_advance"
	ipTextEditPersonAdvance     stmt.ID = "ip.text_edit.person_advance"
	ipTextEditHistoryInsert     stmt.ID = "ip.text_edit.history"
	ipTextEditHistoryField      stmt.ID = "ip.text_edit.history_field"
	ipTextEditReceipt           stmt.ID = "ip.text_edit.receipt"
	ipTextEditReceiptField      stmt.ID = "ip.text_edit.receipt_field"
	ipTextEditNoNext            stmt.ID = "ip.text_edit.no_next"
	ipTextEditReceiptInsert     stmt.ID = "ip.text_edit.receipt_insert"
	ipTextEditReceiptFieldWrite stmt.ID = "ip.text_edit.receipt_field_insert"
)

// The edit family is a closed native statement set over E's owner/receipt/
// history tables plus the existing descriptive and reading-author columns.
// No statement is shared with lock/restore or automatic observation.
func ipTextEditDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	n := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer, Nullable: true} }
	nt := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text, Nullable: true} }
	defs := []stmt.Definition{
		{ID: ipTextEditOwner, Kind: stmt.Read, SQL: `SELECT revision FROM metadata_edit_owners WHERE id=? AND kind=? AND entity_id IS ? AND person_id IS ?`, Params: []stmt.Param{i("owner"), s("kind"), n("entity"), n("person")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_edit_owners"}}},
		{ID: ipTextEditOwnerInsert, Kind: stmt.Write, SQL: `INSERT INTO metadata_edit_owners(entity_id,person_id,kind,command_incarnation) VALUES(?,?,?,?) RETURNING id`, Params: []stmt.Param{n("entity"), n("person"), s("kind"), b("incarnation")}},
		{ID: ipTextEditOwnerAdvance, Kind: stmt.Write, SQL: `UPDATE metadata_edit_owners SET revision=?,last_command_sequence=? WHERE id=? AND kind=? AND entity_id IS ? AND person_id IS ? AND command_incarnation=? AND revision=? AND last_command_sequence=? AND acknowledged_command_sequence=? AND retired_ms IS NULL RETURNING 1`, Params: []stmt.Param{i("new_revision"), i("sequence"), i("owner"), s("kind"), n("entity"), n("person"), b("incarnation"), i("old_revision"), i("old_last"), i("old_ack")}},
		{ID: ipTextEditFieldRow, Kind: stmt.Read, SQL: `SELECT value,automatic_value,value_present,automatic_present,locked FROM metadata_owner_fields WHERE kind=? AND entity_id=? AND field=?`, Params: []stmt.Param{s("kind"), i("entity"), s("field")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_owner_fields"}}},
		{ID: ipTextEditFieldInsert, Kind: stmt.Write, SQL: `INSERT INTO metadata_owner_fields(kind,entity_id,field,value,automatic_value,value_present,automatic_present,locked,actor,observed_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, Params: []stmt.Param{s("kind"), i("entity"), s("field"), s("value"), s("automatic"), i("value_present"), i("automatic_present"), i("locked"), s("actor"), s("at")}},
		{ID: ipTextEditFieldUpdate, Kind: stmt.Write, SQL: `UPDATE metadata_owner_fields SET value=?,automatic_value=?,value_present=?,automatic_present=?,locked=?,actor=?,observed_at=? WHERE kind=? AND entity_id=? AND field=? AND value=? AND automatic_value=? AND value_present=? AND automatic_present=? AND locked=? RETURNING 1`, Params: []stmt.Param{s("value"), s("automatic"), i("value_present"), i("automatic_present"), i("locked"), s("actor"), s("at"), s("kind"), i("entity"), s("field"), s("old_value"), s("old_automatic"), i("old_value_present"), i("old_automatic_present"), i("old_locked")}},
		{ID: ipTextEditSubjectRow, Kind: stmt.Read, SQL: `SELECT 1 FROM catalog_entities WHERE id=? AND library_id=? AND metadata_incarnation=? AND source_revision=? AND identity_revision=? AND revision=? AND metadata_revision=? AND retired=0`, Params: []stmt.Param{i("entity"), i("library"), b("incarnation"), i("source"), i("identity"), i("subject"), i("metadata")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_entities"}}},
		{ID: ipTextEditPersonSource, Kind: stmt.Read, SQL: `SELECT 1 FROM catalog_people WHERE id=? AND token=? AND revision=?`, Params: []stmt.Param{i("person"), s("token"), i("revision")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_people"}}},
		{ID: ipTextEditProjectionCurrent, Kind: stmt.Read, SQL: `SELECT 1 FROM catalog_entities WHERE id=? AND title=? AND sort_title=? AND metadata_language=? AND sort_key=? COLLATE BINARY AND sort_head=? AND sort_key_keep_articles=? COLLATE BINARY AND sort_head_keep_articles=?`, Params: []stmt.Param{i("entity"), s("title"), s("sort_title"), s("language"), s("key"), s("head"), s("keep_key"), s("keep_head")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_entities"}}},
		{ID: ipTextEditProjectionUpdate, Kind: stmt.Write, SQL: `UPDATE catalog_entities SET title=?,sort_title=?,metadata_language=?,sort_key=?,sort_head=?,sort_key_keep_articles=?,sort_head_keep_articles=? WHERE id=? RETURNING 1`, Params: []stmt.Param{s("title"), s("sort_title"), s("language"), s("key"), s("head"), s("keep_key"), s("keep_head"), i("entity")}},
		{ID: ipTextEditScalar, Kind: stmt.Read, SQL: `SELECT CASE ? WHEN 'description' THEN overview WHEN 'originalTitle' THEN original_title WHEN 'tagline' THEN tagline WHEN 'studio' THEN studio WHEN 'network' THEN network WHEN 'country' THEN country ELSE NULL END FROM catalog_entities WHERE id=?`, Params: []stmt.Param{s("field"), i("entity")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_entities"}}},
		{ID: ipTextEditScalarUpdate, Kind: stmt.Write, SQL: `UPDATE catalog_entities SET overview=CASE WHEN ?='description' THEN ? ELSE overview END,original_title=CASE WHEN ?='originalTitle' THEN ? ELSE original_title END,tagline=CASE WHEN ?='tagline' THEN ? ELSE tagline END,studio=CASE WHEN ?='studio' THEN ? ELSE studio END,network=CASE WHEN ?='network' THEN ? ELSE network END,country=CASE WHEN ?='country' THEN ? ELSE country END WHERE id=? AND CASE ? WHEN 'description' THEN overview WHEN 'originalTitle' THEN original_title WHEN 'tagline' THEN tagline WHEN 'studio' THEN studio WHEN 'network' THEN network WHEN 'country' THEN country ELSE NULL END=? RETURNING 1`, Params: []stmt.Param{s("f1"), s("v1"), s("f2"), s("v2"), s("f3"), s("v3"), s("f4"), s("v4"), s("f5"), s("v5"), s("f6"), s("v6"), i("entity"), s("field"), s("old")}},
		{ID: ipTextEditAuthor, Kind: stmt.Read, SQL: `SELECT author,reading_author_value,reading_author_key,reading_author_source FROM catalog_entities WHERE id=?`, Params: []stmt.Param{i("entity")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_entities"}}},
		{ID: ipTextEditAuthorUpdate, Kind: stmt.Write, SQL: `UPDATE catalog_entities SET author=?,reading_author_value=?,reading_author_key=?,reading_author_source='owner' WHERE id=? AND author=? AND reading_author_value IS ? AND reading_author_key IS ? RETURNING 1`, Params: []stmt.Param{s("author"), s("reading_value"), s("reading_key"), i("entity"), s("old_author"), nt("old_reading_value"), nt("old_reading_key")}},
		{ID: ipTextEditPersonCurrent, Kind: stmt.Read, SQL: `SELECT biography FROM catalog_people WHERE id=?`, Params: []stmt.Param{i("person")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_people"}}},
		{ID: ipTextEditPersonUpdate, Kind: stmt.Write, SQL: `UPDATE catalog_people SET biography=? WHERE id=? AND biography=? RETURNING 1`, Params: []stmt.Param{s("biography"), i("person"), s("old")}},
		{ID: ipTextEditSubjectAdvance, Kind: stmt.Write, SQL: `UPDATE catalog_entities SET revision=revision+1,metadata_revision=metadata_revision+1 WHERE id=? AND library_id=? AND metadata_incarnation=? AND revision=? AND metadata_revision=? AND retired=0 RETURNING 1`, Params: []stmt.Param{i("entity"), i("library"), b("incarnation"), i("subject"), i("metadata")}},
		{ID: ipTextEditPersonAdvance, Kind: stmt.Write, SQL: `UPDATE catalog_people SET revision=revision+1 WHERE id=? AND revision=? RETURNING 1`, Params: []stmt.Param{i("person"), i("revision")}},
		{ID: ipTextEditHistoryInsert, Kind: stmt.Write, SQL: `INSERT INTO metadata_owner_history(kind,entity_id,base_revision,revision,trigger,actor,observed_at,before_json,after_json) VALUES(?,?,?,?,?,?,?,?,?) RETURNING id`, Params: []stmt.Param{s("kind"), i("entity"), s("base_revision"), s("revision"), s("trigger"), s("actor"), s("at"), s("before_audit"), s("after_audit")}},
		{ID: ipTextEditHistoryField, Kind: stmt.Write, SQL: `INSERT INTO metadata_owner_history_fields(history_id,field,before_value,before_value_present,before_automatic,before_automatic_present,before_locked,after_value,after_value_present,after_automatic,after_automatic_present,after_locked) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, Params: []stmt.Param{i("history"), s("field"), s("before_value"), i("before_present"), s("before_automatic"), i("before_automatic_present"), i("before_locked"), s("after_value"), i("after_present"), s("after_automatic"), i("after_automatic_present"), i("after_locked")}},
		{ID: ipTextEditReceipt, Kind: stmt.Read, SQL: `SELECT operation_id,request_digest,result_digest,command_kind,result_count,accepted_revision,subject_revision,applied_ms,changed,effective_changed,history_id,field,value,value_present,automatic_value,automatic_present,locked FROM metadata_edit_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`, Params: []stmt.Param{i("owner"), b("incarnation"), i("sequence")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_edit_receipts"}}},
		{ID: ipTextEditReceiptField, Kind: stmt.Read, SQL: `SELECT ordinal,field,value,value_present,automatic_value,automatic_present,locked,changed,effective_changed FROM metadata_edit_receipt_fields WHERE owner_id=? AND command_incarnation=? AND command_sequence=? ORDER BY ordinal`, Params: []stmt.Param{i("owner"), b("incarnation"), i("sequence")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_edit_receipt_fields"}}},
		{ID: ipTextEditNoNext, Kind: stmt.Read, SQL: `SELECT 1 FROM metadata_edit_receipt_fields WHERE owner_id=? AND command_incarnation=? AND command_sequence=? AND ordinal>=? ORDER BY ordinal LIMIT 1`, Params: []stmt.Param{i("owner"), b("incarnation"), i("sequence"), i("count")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_edit_receipt_fields"}}},
		{ID: ipTextEditReceiptInsert, Kind: stmt.Write, SQL: `INSERT INTO metadata_edit_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,applied_ms,result_digest,command_kind,field,result_count,accepted_revision,subject_revision,value,value_present,automatic_value,automatic_present,locked,changed,effective_changed,history_id) VALUES(?,?,?,?,?,?,?,'field_edit','',?,?,?,'',0,'',0,0,?,?,?)`, Params: []stmt.Param{i("owner"), b("incarnation"), i("sequence"), b("operation"), b("request"), i("applied"), b("result"), i("count"), i("accepted"), i("subject"), i("changed"), i("effective"), n("history")}},
		{ID: ipTextEditReceiptFieldWrite, Kind: stmt.Write, SQL: `INSERT INTO metadata_edit_receipt_fields(owner_id,command_incarnation,command_sequence,ordinal,field,value,value_present,automatic_value,automatic_present,locked,changed,effective_changed) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, Params: []stmt.Param{i("owner"), b("incarnation"), i("sequence"), i("ordinal"), s("field"), s("value"), i("value_present"), s("automatic"), i("automatic_present"), i("locked"), i("changed"), i("effective")}},
	}
	for x := range defs {
		defs[x].Owner = "store/writer IP text edit"
		defs[x].CallSites = []string{"store/writer/ip_text_edit.go", "store/writer/ip_text_bundle_native.go"}
		var args []any
		for _, p := range defs[x].Params {
			switch p.Type {
			case stmt.Text:
				args = append(args, "")
			case stmt.Blob:
				args = append(args, make([]byte, 16))
			default:
				if p.Nullable {
					args = append(args, nil)
				} else {
					args = append(args, int64(0))
				}
			}
		}
		defs[x].Cohorts = []stmt.Cohort{{Name: "missing_native_keys", Bindings: args}}
	}
	return append(ipEntryDefinitions(), defs...)
}
