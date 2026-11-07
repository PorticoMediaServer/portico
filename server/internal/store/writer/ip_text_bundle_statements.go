package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	ipBundleOwner         stmt.ID = "ip.text_bundle.owner"
	ipBundleReceipt       stmt.ID = "ip.text_bundle.receipt"
	ipBundleFieldReceipt  stmt.ID = "ip.text_bundle.field_receipt"
	ipBundleNoNext        stmt.ID = "ip.text_bundle.no_next"
	ipBundleOwnerAdvance  stmt.ID = "ip.text_bundle.owner_advance"
	ipBundleSave          stmt.ID = "ip.text_bundle.save"
	ipBundleSaveField     stmt.ID = "ip.text_bundle.save_field"
	ipBundlePersonSource  stmt.ID = "ip.text_bundle.person_source"
	ipBundlePersonCurrent stmt.ID = "ip.text_bundle.person_current"
	ipBundlePersonUpdate  stmt.ID = "ip.text_bundle.person_update"
	ipBundlePersonAdvance stmt.ID = "ip.text_bundle.person_advance"
)

func ipTextBundleDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	n := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer, Nullable: true} }
	defs := []stmt.Definition{
		{ID: ipBundleOwner, Kind: stmt.Read, SQL: `SELECT revision,command_incarnation,last_command_sequence,acknowledged_command_sequence FROM metadata_edit_owners WHERE id=? AND entity_id IS ? AND person_id IS ? AND kind=?`, Params: []stmt.Param{i("owner"), n("entity"), n("person"), s("kind")}},
		{ID: ipBundleReceipt, Kind: stmt.Read, SQL: `SELECT operation_id,request_digest,result_digest,CASE command_kind WHEN 'field_edit' THEN 240 WHEN 'field_lock' THEN 241 WHEN 'field_restore' THEN 242 ELSE 0 END,accepted_revision,subject_revision,applied_ms,changed,effective_changed,history_id,result_count,(field='' AND value='' AND automatic_value='' AND value_present=0 AND automatic_present=0 AND locked=0) FROM metadata_edit_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`, Params: []stmt.Param{i("owner"), b("incarnation"), i("sequence")}},
		{ID: ipBundleFieldReceipt, Kind: stmt.Read, SQL: `SELECT 1 FROM metadata_edit_receipt_fields WHERE owner_id=? AND command_incarnation=? AND command_sequence=? AND ordinal=? AND field=? AND value=? AND automatic_value=? AND value_present=? AND automatic_present=? AND locked=? AND changed=? AND effective_changed=?`, Params: []stmt.Param{i("owner"), b("incarnation"), i("sequence"), i("ordinal"), s("field"), s("value"), s("automatic"), i("value_present"), i("automatic_present"), i("locked"), i("changed"), i("effective")}},
		{ID: ipBundleNoNext, Kind: stmt.Read, SQL: `SELECT 1 FROM metadata_edit_receipt_fields WHERE owner_id=? AND command_incarnation=? AND command_sequence=? AND ordinal>=? ORDER BY ordinal LIMIT 1`, Params: []stmt.Param{i("owner"), b("incarnation"), i("sequence"), i("count")}},
		{ID: ipBundleOwnerAdvance, Kind: stmt.Write, SQL: `UPDATE metadata_edit_owners SET revision=?,last_command_sequence=? WHERE id=? AND entity_id IS ? AND person_id IS ? AND kind=? AND command_incarnation=? AND revision=? AND last_command_sequence=? AND acknowledged_command_sequence=? AND retired_ms IS NULL RETURNING 1`, Params: []stmt.Param{i("new_revision"), i("sequence"), i("owner"), n("entity"), n("person"), s("kind"), b("incarnation"), i("old_revision"), i("last"), i("ack")}},
		{ID: ipBundleSave, Kind: stmt.Write, SQL: `INSERT INTO metadata_edit_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,applied_ms,result_digest,command_kind,field,result_count,accepted_revision,subject_revision,value,value_present,automatic_value,automatic_present,locked,changed,effective_changed,history_id) VALUES(?,?,?,?,?,?,?,?,'',?,?,?,'',0,'',0,0,?,?,?)`, Params: []stmt.Param{i("owner"), b("incarnation"), i("sequence"), b("operation"), b("request"), i("applied"), b("result"), s("kind"), i("count"), i("revision"), i("subject"), i("changed"), i("effective"), n("history")}},
		{ID: ipBundleSaveField, Kind: stmt.Write, SQL: `INSERT INTO metadata_edit_receipt_fields(owner_id,command_incarnation,command_sequence,ordinal,field,value,automatic_value,value_present,automatic_present,locked,changed,effective_changed) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, Params: []stmt.Param{i("owner"), b("incarnation"), i("sequence"), i("ordinal"), s("field"), s("value"), s("automatic"), i("value_present"), i("automatic_present"), i("locked"), i("changed"), i("effective")}},
		{ID: ipBundlePersonSource, Kind: stmt.Read, SQL: `SELECT 1 FROM catalog_people WHERE id=? AND revision=?`, Params: []stmt.Param{i("person"), i("revision")}},
		{ID: ipBundlePersonCurrent, Kind: stmt.Read, SQL: `SELECT 1 FROM catalog_people WHERE id=? AND biography=?`, Params: []stmt.Param{i("person"), s("biography")}},
		{ID: ipBundlePersonUpdate, Kind: stmt.Write, SQL: `UPDATE catalog_people SET biography=? WHERE id=? RETURNING 1`, Params: []stmt.Param{s("biography"), i("person")}},
		{ID: ipBundlePersonAdvance, Kind: stmt.Write, SQL: `UPDATE catalog_people SET revision=revision+1 WHERE id=? AND revision=? RETURNING 1`, Params: []stmt.Param{i("person"), i("revision")}},
	}
	defs = append(defs, ipTextLockDefinitions()...)
	for j := range defs {
		defs[j].Owner = "writer.ip.text_bundle"
		defs[j].CallSites = []string{"writer/ip_text_bundle_native.go"}
		var bindings []any
		for _, p := range defs[j].Params {
			if p.Nullable {
				bindings = append(bindings, nil)
			} else {
				switch p.Type {
				case stmt.Text:
					bindings = append(bindings, "")
				case stmt.Blob:
					bindings = append(bindings, make([]byte, 16))
				default:
					bindings = append(bindings, int64(0))
				}
			}
		}
		defs[j].Cohorts = []stmt.Cohort{{Name: "missing_native_owner", Bindings: bindings}}
	}
	return defs
}
