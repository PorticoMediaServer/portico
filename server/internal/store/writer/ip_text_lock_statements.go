package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	ipTextOwner          stmt.ID = "ip.text_lock.owner"
	ipTextReceiptHeader  stmt.ID = "ip.text_lock.receipt_header"
	ipTextSource         stmt.ID = "ip.text_lock.source"
	ipTextField          stmt.ID = "ip.text_lock.field"
	ipTextLockUpdate     stmt.ID = "ip.text_lock.update"
	ipTextSubjectAdvance stmt.ID = "ip.text_lock.subject_advance"
	ipTextHistoryInsert  stmt.ID = "ip.text_lock.history"
	ipTextHistoryField   stmt.ID = "ip.text_lock.history_field"
	ipTextOwnerAdvance   stmt.ID = "ip.text_lock.owner_advance"
	ipTextReceiptInsert  stmt.ID = "ip.text_lock.receipt_insert"
	ipTextReceiptBody    stmt.ID = "ip.text_lock.receipt_body"
)

func ipTextLockDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	n := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer, Nullable: true} }
	defs := []stmt.Definition{
		{ID: ipTextOwner, Kind: stmt.Read, SQL: `SELECT revision,command_incarnation,last_command_sequence,acknowledged_command_sequence FROM metadata_edit_owners WHERE id=? AND entity_id=? AND kind=?`, Params: []stmt.Param{i("owner"), i("entity"), s("kind")}},
		{ID: ipTextReceiptHeader, Kind: stmt.Read, SQL: `SELECT operation_id,request_digest,result_digest,accepted_revision,subject_revision,applied_ms,changed,effective_changed,history_id,CASE command_kind WHEN 'field_edit' THEN 240 WHEN 'field_lock' THEN 241 WHEN 'field_restore' THEN 242 ELSE 0 END FROM metadata_edit_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`, Params: []stmt.Param{i("owner"), b("incarnation"), i("sequence")}},
		{ID: ipTextSource, Kind: stmt.Read, SQL: `SELECT 1 FROM catalog_entities WHERE id=? AND library_id=? AND metadata_incarnation=? AND revision=? AND metadata_revision=? AND retired=0`, Params: []stmt.Param{i("entity"), i("library"), b("metadata_incarnation"), i("subject_revision"), i("metadata_revision")}},
		{ID: ipTextField, Kind: stmt.Read, SQL: `SELECT 1 FROM metadata_owner_fields WHERE kind=? AND entity_id=? AND field=? AND value=? AND automatic_value=? AND value_present=? AND automatic_present=? AND locked=?`, Params: []stmt.Param{s("kind"), i("entity"), s("field"), s("value"), s("automatic"), i("value_present"), i("automatic_present"), i("locked")}},
		{ID: ipTextLockUpdate, Kind: stmt.Write, SQL: `UPDATE metadata_owner_fields SET locked=?,actor=?,observed_at=? WHERE kind=? AND entity_id=? AND field=? AND locked=? RETURNING 1`, Params: []stmt.Param{i("locked"), s("actor"), s("at"), s("kind"), i("entity"), s("field"), i("old_locked")}},
		{ID: ipTextSubjectAdvance, Kind: stmt.Write, SQL: `UPDATE catalog_entities SET revision=revision+1,metadata_revision=metadata_revision+1 WHERE id=? AND library_id=? AND metadata_incarnation=? AND revision=? AND metadata_revision=? AND retired=0 RETURNING 1`, Params: []stmt.Param{i("entity"), i("library"), b("metadata_incarnation"), i("subject_revision"), i("metadata_revision")}},
		{ID: ipTextHistoryInsert, Kind: stmt.Write, SQL: `INSERT INTO metadata_owner_history(kind,entity_id,base_revision,revision,trigger,actor,observed_at,before_json,after_json) VALUES(?,?,?,?,?,?,?,?,?) RETURNING id`, Params: []stmt.Param{s("kind"), i("entity"), s("base_revision"), s("revision"), s("trigger"), s("actor"), s("at"), s("before_audit"), s("after_audit")}},
		{ID: ipTextHistoryField, Kind: stmt.Write, SQL: `INSERT INTO metadata_owner_history_fields(history_id,field,before_value,before_value_present,before_automatic,before_automatic_present,before_locked,after_value,after_value_present,after_automatic,after_automatic_present,after_locked) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, Params: []stmt.Param{i("history"), s("field"), s("before_value"), i("before_present"), s("before_automatic"), i("before_automatic_present"), i("before_locked"), s("after_value"), i("after_present"), s("after_automatic"), i("after_automatic_present"), i("after_locked")}},
		{ID: ipTextOwnerAdvance, Kind: stmt.Write, SQL: `UPDATE metadata_edit_owners SET revision=?,last_command_sequence=? WHERE id=? AND entity_id=? AND kind=? AND command_incarnation=? AND revision=? AND last_command_sequence=? AND acknowledged_command_sequence=? AND retired_ms IS NULL RETURNING 1`, Params: []stmt.Param{i("new_revision"), i("next"), i("owner"), i("entity"), s("kind"), b("incarnation"), i("old_revision"), i("old_last"), i("old_ack")}},
		{ID: ipTextReceiptInsert, Kind: stmt.Write, SQL: `INSERT INTO metadata_edit_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,applied_ms,result_digest,command_kind,field,accepted_revision,subject_revision,value,value_present,automatic_value,automatic_present,locked,changed,effective_changed,history_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, Params: []stmt.Param{i("owner"), b("incarnation"), i("sequence"), b("operation"), b("request"), i("applied_ms"), b("result"), s("kind"), s("field"), i("accepted_revision"), i("subject_revision"), s("value"), i("value_present"), s("automatic"), i("automatic_present"), i("locked"), i("changed"), i("effective_changed"), n("history")}},
		{ID: ipTextReceiptBody, Kind: stmt.Read, SQL: `SELECT 1 FROM metadata_edit_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=? AND command_kind=? AND field=? AND value=? AND value_present=? AND automatic_value=? AND automatic_present=? AND locked=? AND history_id IS ?`, Params: []stmt.Param{i("owner"), b("incarnation"), i("sequence"), s("kind"), s("field"), s("value"), i("value_present"), s("automatic"), i("automatic_present"), i("locked"), n("history")}},
	}
	defs = append(defs, ipTextEditDefinitions()...)
	for x := range defs {
		defs[x].Owner = "writer.ip.text_lock"
		defs[x].CallSites = []string{"writer/ip_text_lock.go:applyIPTextLock"}
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
