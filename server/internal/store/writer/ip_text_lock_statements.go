package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// Lock and restore are the single-field companions of the typed owner edit.
// Each command kind owns a closed statement set over E's owner/receipt/history
// tables plus the accepted descriptive columns. Nothing here is shared with the
// multi-field edit family or with automatic observation: a lock retains the
// automatic source while it changes only the lock bit, and a restore substitutes
// that retained automatic source for the effective value.
type ipTextFieldMutationSet struct {
	Owner, OwnerInsert, OwnerAdvance stmt.ID
	Field, FieldInsert, FieldUpdate  stmt.ID
	Catalog, PersonCatalog           stmt.ID
	SubjectAdvance, PersonAdvance    stmt.ID
	ProjectionUpdate                 stmt.ID
	ScalarUpdate, PersonUpdate       stmt.ID
	HistoryInsert, HistoryField      stmt.ID
	Receipt, ReceiptInsert           stmt.ID
}

func ipTextFieldMutationStatements(kind CommandID) ipTextFieldMutationSet {
	prefix := "ip.lock"
	if kind == IPFieldRestore {
		prefix = "ip.restore"
	}
	id := func(name string) stmt.ID { return stmt.ID(prefix + "." + name) }
	return ipTextFieldMutationSet{
		Owner:            id("owner"),
		OwnerInsert:      id("owner_insert"),
		OwnerAdvance:     id("owner_advance"),
		Field:            id("field"),
		FieldInsert:      id("field_insert"),
		FieldUpdate:      id("field_update"),
		Catalog:          id("catalog"),
		PersonCatalog:    id("person_catalog"),
		SubjectAdvance:   id("subject_advance"),
		PersonAdvance:    id("person_advance"),
		ProjectionUpdate: id("projection_update"),
		ScalarUpdate:     id("scalar_update"),
		PersonUpdate:     id("person_update"),
		HistoryInsert:    id("history"),
		HistoryField:     id("history_field"),
		Receipt:          id("receipt"),
		ReceiptInsert:    id("receipt_insert"),
	}
}

// ipTextLockDefinitions returns the shared IP entry family plus both closed
// single-field families. The full set is one W plan for tests and, later, for a
// dispatcher; production registration stays with the installed command plan.
func ipTextLockDefinitions() []stmt.Definition {
	defs := append(ipTextFieldMutationDefinitions(IPFieldLock), ipTextFieldMutationDefinitions(IPFieldRestore)...)
	return append(ipEntryDefinitions(), defs...)
}

func ipTextFieldMutationDefinitions(kind CommandID) []stmt.Definition {
	ids := ipTextFieldMutationStatements(kind)
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	n := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer, Nullable: true} }
	defs := []stmt.Definition{
		{ID: ids.Owner, Kind: stmt.Read, SQL: `SELECT revision FROM metadata_edit_owners WHERE id=? AND kind=? AND entity_id IS ? AND person_id IS ?`, Params: []stmt.Param{i("owner"), s("kind"), n("entity"), n("person")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_edit_owners"}}},
		{ID: ids.OwnerInsert, Kind: stmt.Write, SQL: `INSERT INTO metadata_edit_owners(entity_id,person_id,kind,command_incarnation) VALUES(?,?,?,?) RETURNING id`, Params: []stmt.Param{n("entity"), n("person"), s("kind"), b("incarnation")}},
		{ID: ids.OwnerAdvance, Kind: stmt.Write, SQL: `UPDATE metadata_edit_owners SET revision=?,last_command_sequence=? WHERE id=? AND kind=? AND entity_id IS ? AND person_id IS ? AND command_incarnation=? AND revision=? AND last_command_sequence=? AND acknowledged_command_sequence=? AND retired_ms IS NULL RETURNING 1`, Params: []stmt.Param{i("new_revision"), i("sequence"), i("owner"), s("kind"), n("entity"), n("person"), b("incarnation"), i("old_revision"), i("old_last"), i("old_ack")}},
		{ID: ids.Field, Kind: stmt.Read, SQL: `SELECT value,automatic_value,value_present,automatic_present,locked FROM metadata_owner_fields WHERE kind=? AND entity_id=? AND field=?`, Params: []stmt.Param{s("kind"), i("entity"), s("field")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_owner_fields"}}},
		{ID: ids.FieldInsert, Kind: stmt.Write, SQL: `INSERT INTO metadata_owner_fields(kind,entity_id,field,value,automatic_value,value_present,automatic_present,locked,actor,observed_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, Params: []stmt.Param{s("kind"), i("entity"), s("field"), s("value"), s("automatic"), i("value_present"), i("automatic_present"), i("locked"), s("actor"), s("at")}},
		{ID: ids.FieldUpdate, Kind: stmt.Write, SQL: `UPDATE metadata_owner_fields SET value=?,automatic_value=?,value_present=?,automatic_present=?,locked=?,actor=?,observed_at=? WHERE kind=? AND entity_id=? AND field=? AND value=? AND automatic_value=? AND value_present=? AND automatic_present=? AND locked=? RETURNING 1`, Params: []stmt.Param{s("value"), s("automatic"), i("value_present"), i("automatic_present"), i("locked"), s("actor"), s("at"), s("kind"), i("entity"), s("field"), s("old_value"), s("old_automatic"), i("old_value_present"), i("old_automatic_present"), i("old_locked")}},
		{ID: ids.Catalog, Kind: stmt.Read, SQL: `SELECT title,sort_title,metadata_language,overview,original_title,tagline,studio,network,country,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles FROM catalog_entities WHERE id=? AND kind=? AND library_id=? AND retired=0`, Params: []stmt.Param{i("entity"), i("kind"), i("library")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_entities"}}},
		{ID: ids.PersonCatalog, Kind: stmt.Read, SQL: `SELECT biography FROM catalog_people WHERE id=?`, Params: []stmt.Param{i("person")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_people"}}},
		{ID: ids.SubjectAdvance, Kind: stmt.Write, SQL: `UPDATE catalog_entities SET revision=revision+1,metadata_revision=metadata_revision+1 WHERE id=? AND library_id=? AND metadata_incarnation=? AND revision=? AND metadata_revision=? AND retired=0 RETURNING 1`, Params: []stmt.Param{i("entity"), i("library"), b("incarnation"), i("subject"), i("metadata")}},
		{ID: ids.PersonAdvance, Kind: stmt.Write, SQL: `UPDATE catalog_people SET revision=revision+1 WHERE id=? AND revision=? RETURNING 1`, Params: []stmt.Param{i("person"), i("revision")}},
		{ID: ids.ProjectionUpdate, Kind: stmt.Write, SQL: `UPDATE catalog_entities SET title=?,sort_title=?,metadata_language=?,sort_key=?,sort_head=?,sort_key_keep_articles=?,sort_head_keep_articles=? WHERE id=? AND title=? AND sort_title=? AND metadata_language=? AND sort_key=? COLLATE BINARY AND sort_head=? AND sort_key_keep_articles=? COLLATE BINARY AND sort_head_keep_articles=? RETURNING 1`, Params: []stmt.Param{s("title"), s("sort_title"), s("language"), s("key"), s("head"), s("keep_key"), s("keep_head"), i("entity"), s("old_title"), s("old_sort_title"), s("old_language"), s("old_key"), s("old_head"), s("old_keep_key"), s("old_keep_head")}},
		{ID: ids.ScalarUpdate, Kind: stmt.Write, SQL: `UPDATE catalog_entities SET overview=CASE WHEN ?='description' THEN ? ELSE overview END,original_title=CASE WHEN ?='originalTitle' THEN ? ELSE original_title END,tagline=CASE WHEN ?='tagline' THEN ? ELSE tagline END,studio=CASE WHEN ?='studio' THEN ? ELSE studio END,network=CASE WHEN ?='network' THEN ? ELSE network END,country=CASE WHEN ?='country' THEN ? ELSE country END WHERE id=? AND CASE ? WHEN 'description' THEN overview WHEN 'originalTitle' THEN original_title WHEN 'tagline' THEN tagline WHEN 'studio' THEN studio WHEN 'network' THEN network WHEN 'country' THEN country ELSE NULL END=? RETURNING 1`, Params: []stmt.Param{s("f1"), s("v1"), s("f2"), s("v2"), s("f3"), s("v3"), s("f4"), s("v4"), s("f5"), s("v5"), s("f6"), s("v6"), i("entity"), s("field"), s("old")}},
		{ID: ids.PersonUpdate, Kind: stmt.Write, SQL: `UPDATE catalog_people SET biography=? WHERE id=? AND biography=? RETURNING 1`, Params: []stmt.Param{s("biography"), i("person"), s("old")}},
		{ID: ids.HistoryInsert, Kind: stmt.Write, SQL: `INSERT INTO metadata_owner_history(kind,entity_id,base_revision,revision,trigger,actor,observed_at,before_json,after_json) VALUES(?,?,?,?,?,?,?,?,?) RETURNING id`, Params: []stmt.Param{s("kind"), i("entity"), s("base_revision"), s("revision"), s("trigger"), s("actor"), s("at"), s("before_audit"), s("after_audit")}},
		{ID: ids.HistoryField, Kind: stmt.Write, SQL: `INSERT INTO metadata_owner_history_fields(history_id,field,before_value,before_value_present,before_automatic,before_automatic_present,before_locked,after_value,after_value_present,after_automatic,after_automatic_present,after_locked) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, Params: []stmt.Param{i("history"), s("field"), s("before_value"), i("before_present"), s("before_automatic"), i("before_automatic_present"), i("before_locked"), s("after_value"), i("after_present"), s("after_automatic"), i("after_automatic_present"), i("after_locked")}},
		{ID: ids.Receipt, Kind: stmt.Read, SQL: `SELECT operation_id,request_digest,result_digest,command_kind,result_count,accepted_revision,subject_revision,applied_ms,field,value,value_present,automatic_value,automatic_present,locked,changed,effective_changed,history_id FROM metadata_edit_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`, Params: []stmt.Param{i("owner"), b("incarnation"), i("sequence")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_edit_receipts"}}},
		{ID: ids.ReceiptInsert, Kind: stmt.Write, SQL: `INSERT INTO metadata_edit_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,applied_ms,result_digest,command_kind,field,result_count,accepted_revision,subject_revision,value,value_present,automatic_value,automatic_present,locked,changed,effective_changed,history_id) VALUES(?,?,?,?,?,?,?,?,?,1,?,?,?,?,?,?,?,?,?,?)`, Params: []stmt.Param{i("owner"), b("incarnation"), i("sequence"), b("operation"), b("request"), i("applied"), b("result"), s("kind"), s("field"), i("accepted"), i("subject"), s("value"), i("value_present"), s("automatic"), i("automatic_present"), i("locked"), i("changed"), i("effective"), n("history")}},
	}
	for x := range defs {
		defs[x].Owner = "store/writer IP field lock"
		defs[x].CallSites = []string{"store/writer/ip_text_lock.go"}
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
	return defs
}
