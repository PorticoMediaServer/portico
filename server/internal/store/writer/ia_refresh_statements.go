package writer

import (
	"fmt"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"strings"
)

const (
	iaRefreshInputUsed    stmt.ID = "ia.refresh.input_used"
	iaRefreshInputCurrent stmt.ID = "ia.refresh.input_current"
	iaRefreshOriginal     stmt.ID = "ia.refresh.original"
	iaRefreshOwner        stmt.ID = "ia.refresh.owner"
	iaRefreshHeader       stmt.ID = "ia.refresh.header"
	iaRefreshSource       stmt.ID = "ia.refresh.source"
	iaRefreshChange       stmt.ID = "ia.refresh.change"
	iaRefreshAsset        stmt.ID = "ia.refresh.asset"
	iaRefreshBirth        stmt.ID = "ia.refresh.birth"
	iaRefreshFence        stmt.ID = "ia.refresh.fence"
	iaRefreshAdvance      stmt.ID = "ia.refresh.advance"
	iaRefreshReceipt      stmt.ID = "ia.refresh.receipt"
	iaRefreshInvalidate   stmt.ID = "ia.refresh.invalidate"
	iaRefreshShared       stmt.ID = "ia.refresh.shared"
	iaRefreshReferences   stmt.ID = "ia.refresh.references"
)

func iaRefreshID(f uint8, op string) stmt.ID {
	return stmt.ID(fmt.Sprintf("ia.refresh.row%d.%s", f, op))
}
func iaRefreshWhere(s iaRefreshShape) string {
	var a []string
	for _, c := range s.columns[:s.keys] {
		a = append(a, c.name+"=?")
	}
	return strings.Join(a, " AND ")
}
func iaRefreshRowDefinitions() []stmt.Definition {
	var defs []stmt.Definition
	for f := uint8(1); f <= 7; f++ {
		s := iaRefreshShapes[f]
		undo := "catalog_info_undo_" + s.table
		var names, marks, octets []string
		var params []stmt.Param
		for _, c := range s.columns {
			names = append(names, c.name)
			marks = append(marks, "?")
			typ := stmt.Integer
			if c.kind == "TEXT" {
				typ = stmt.Text
				octets = append(octets, "octet_length("+c.name+")")
			}
			if c.kind == "REAL" {
				typ = stmt.Real
			}
			params = append(params, stmt.Param{Name: c.name, Type: typ, Nullable: c.nullable})
		}
		keys := append([]stmt.Param(nil), params[:s.keys]...)
		where := iaRefreshWhere(s)
		cols := strings.Join(names, ",")
		pk := make([]string, s.keys)
		for j := range pk {
			pk[j] = s.columns[j].name
		}
		plan := stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_" + s.table + "_1"}}
		up := stmt.Plan{Hot: true, PrimaryKeys: []string{undo}}
		byteSQL := "SELECT " + strings.Join(octets, ",") + " FROM " + s.table + " WHERE " + where
		defs = append(defs,
			stmt.Definition{ID: iaRefreshID(f, "bytes"), Kind: stmt.Read, SQL: byteSQL, Params: keys, Plan: plan},
			stmt.Definition{ID: iaRefreshID(f, "current"), Kind: stmt.Read, SQL: "SELECT " + cols + " FROM " + s.table + " WHERE " + where, Params: keys, Plan: plan},
			stmt.Definition{ID: iaRefreshID(f, "old_bytes"), Kind: stmt.Read, SQL: "SELECT " + strings.Join(octets, ",") + " FROM " + undo + " WHERE refresh_id=? AND " + where, Params: append([]stmt.Param{{Name: "refresh", Type: stmt.Integer}}, keys...), Plan: up},
			stmt.Definition{ID: iaRefreshID(f, "old"), Kind: stmt.Read, SQL: "SELECT old_exists,written_revision,write_digest," + cols + " FROM " + undo + " WHERE refresh_id=? AND " + where, Params: append([]stmt.Param{{Name: "refresh", Type: stmt.Integer}}, keys...), Plan: up},
		)
		captureParams := []stmt.Param{{Name: "refresh", Type: stmt.Integer}, {Name: "exists", Type: stmt.Integer}, {Name: "written", Type: stmt.Integer}, {Name: "post_digest", Type: stmt.Blob}}
		for j, p := range params {
			if j >= s.keys {
				p.Nullable = true
			}
			captureParams = append(captureParams, p)
		}
		defs = append(defs, stmt.Definition{ID: iaRefreshID(f, "capture"), Kind: stmt.Write, SQL: "INSERT INTO " + undo + "(refresh_id,old_exists,written_revision,write_digest," + cols + ") VALUES(?,?,?,?," + strings.Join(marks, ",") + ")", Params: captureParams, Plan: stmt.Plan{Hot: true}})
		var set []string
		for _, c := range names[s.keys:] {
			set = append(set, c+"=?")
		}
		defs = append(defs,
			stmt.Definition{ID: iaRefreshID(f, "insert"), Kind: stmt.Write, SQL: "INSERT INTO " + s.table + "(" + cols + ") VALUES(" + strings.Join(marks, ",") + ")", Params: params, Plan: stmt.Plan{Hot: true}},
			stmt.Definition{ID: iaRefreshID(f, "update"), Kind: stmt.Write, SQL: "UPDATE " + s.table + " SET " + strings.Join(set, ",") + " WHERE " + where, Params: append(append([]stmt.Param(nil), params[s.keys:]...), keys...), Plan: plan},
			stmt.Definition{ID: iaRefreshID(f, "delete"), Kind: stmt.Write, SQL: "DELETE FROM " + s.table + " WHERE " + where, Params: keys, Plan: plan},
			stmt.Definition{ID: iaRefreshID(f, "written"), Kind: stmt.Write, SQL: "UPDATE " + undo + " SET written_revision=?,write_digest=? WHERE refresh_id=? AND " + where + " AND written_revision=? AND write_digest=?", Params: append(append([]stmt.Param{{Name: "next", Type: stmt.Integer}, {Name: "next_digest", Type: stmt.Blob}, {Name: "refresh", Type: stmt.Integer}}, keys...), stmt.Param{Name: "old_revision", Type: stmt.Integer}, stmt.Param{Name: "old_digest", Type: stmt.Blob}), Plan: up},
		)
		child := ""
		switch f {
		case 1:
			child = "asset_streams"
		case 3:
			child = "asset_chapters"
		case 5:
			child = "asset_subtitles"
		}
		if child != "" {
			defs = append(defs, stmt.Definition{ID: iaRefreshID(f, "children"), Kind: stmt.Read, SQL: "SELECT 1 FROM " + child + " WHERE asset_id=? LIMIT 1", Params: []stmt.Param{{Name: "asset", Type: stmt.Text}}, Plan: stmt.Plan{Hot: true}})
		}
	}
	return defs
}
func iaRefreshDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	defs := []stmt.Definition{
		{ID: iaRefreshInputUsed, Kind: stmt.Read, SQL: `SELECT 1 FROM catalog_info_refresh_undo INDEXED BY catalog_info_refresh_original WHERE change_id=? AND asset_id=? AND asset_token=? AND expected_epoch=? AND input_revision=? AND fingerprint=? LIMIT 1`, Params: []stmt.Param{i("change"), i("asset"), t("token"), i("epoch"), i("input"), b("fingerprint")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"catalog_info_refresh_original"}}},
		{ID: iaRefreshOriginal, Kind: stmt.Read, SQL: `SELECT refresh_id FROM catalog_info_refresh_undo WHERE change_id=? AND command_incarnation=?`, Params: []stmt.Param{i("change"), b("incarnation")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"catalog_info_refresh_original"}}},
		{ID: iaRefreshOwner, Kind: stmt.Read, SQL: `SELECT u.change_id,u.original_run_id,u.command_incarnation,u.asset_id,u.asset_token,u.expected_epoch,u.input_revision,u.fingerprint,u.state,u.revision,u.last_command_sequence,u.acknowledged_command_sequence,u.retained_rows,u.retained_bytes FROM catalog_info_refresh_undo AS u INNER JOIN inventory_runs AS r ON r.job_id=u.original_run_id WHERE u.refresh_id=? AND r.original_library_id=? AND r.original_library_key=? AND u.asset_token=? AND u.original_run_id=?`, Params: []stmt.Param{i("refresh"), i("library"), t("key"), t("token"), t("run")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_info_refresh_undo"}}},
		{ID: iaRefreshHeader, Kind: stmt.Read, SQL: `SELECT operation_id,request_digest,command_kind,accepted_revision,applied_ms,result_state,after_family,substr(after_key,1,274),result_rows,result_bytes,result_digest FROM catalog_info_refresh_receipts WHERE refresh_id=? AND command_incarnation=? AND command_sequence=?`, Params: []stmt.Param{i("refresh"), b("incarnation"), i("sequence")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_info_refresh_receipts"}}},
		{ID: iaRefreshSource, Kind: stmt.Read, SQL: `SELECT 1 FROM inventory_runs AS r INNER JOIN jobs AS j ON j.id=r.job_id INNER JOIN catalog_libraries AS cl ON cl.id=r.original_library_id INNER JOIN library_sources AS s ON s.id=r.source_id INNER JOIN libraries AS l ON l.id=s.library_id INNER JOIN inventory_source_active AS a ON a.source_id=s.id INNER JOIN library_scan_policies AS p ON p.library_id=l.id WHERE r.job_id=? AND r.original_library_id=? AND r.original_library_key=? AND cl.library_id=? AND cl.retired=0 AND j.status='running' AND a.job_id=r.job_id AND s.library_id=r.original_library_key AND s.enabled=1 AND s.identity_confirmed=1 AND s.incarnation=r.root_incarnation AND s.generation=r.source_generation AND s.root_identity=r.root_identity AND s.root=r.original_resolved_root AND p.revision=r.policy_revision`, Params: []stmt.Param{t("run"), i("library"), t("library_key"), t("namespace")}, Plan: stmt.Plan{Hot: true}},
		{ID: iaRefreshChange, Kind: stmt.Read, SQL: `SELECT c.job_revision,c.job_lease_owner,c.job_lease_generation,c.job_lease_expires_ms FROM catalog_info_changes AS c INNER JOIN catalog_info_input_witnesses AS w ON w.change_id=c.id AND w.ordinal=0 INNER JOIN inventory_runs AS r ON r.job_id=w.job_id WHERE c.id=? AND c.asset_id=? AND c.asset_token=? AND c.slot=1 AND c.expected_epoch=? AND c.input_revision=? AND c.fingerprint=? AND c.fault='' AND c.phase BETWEEN 0 AND 3 AND w.kind=0 AND w.job_id=? AND w.source_id=r.source_id AND w.source_generation=r.source_generation AND w.root_incarnation=r.root_incarnation AND w.policy_revision=r.policy_revision AND w.fingerprint=c.fingerprint AND w.source_id=? AND w.source_generation=? AND w.root_incarnation=? AND w.object_id=? AND w.object_revision=? AND w.policy_revision=?`, Params: []stmt.Param{i("change"), i("asset"), t("token"), i("epoch"), i("input"), b("fingerprint"), t("run"), t("source"), i("generation"), t("incarnation"), t("object"), t("object_revision"), i("policy")}, Plan: stmt.Plan{Hot: true}},
		{ID: iaRefreshAsset, Kind: stmt.Read, SQL: `SELECT info_publication_pending,playback_origin_revision,prepared_source_revision,info_invalidation_revision FROM catalog_assets WHERE id=? AND token=? AND library_id=? AND info_epoch=?`, Params: []stmt.Param{i("asset"), t("token"), i("library"), i("epoch")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_assets"}}},
		{ID: iaRefreshFence, Kind: stmt.Write, SQL: `UPDATE catalog_assets SET info_publication_pending=1 WHERE id=? AND token=? AND library_id=? AND info_epoch=? AND info_publication_pending=0`, Params: []stmt.Param{i("asset"), t("token"), i("library"), i("epoch")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_assets"}}},
		{ID: iaRefreshBirth, Kind: stmt.Write, SQL: `INSERT INTO catalog_info_refresh_undo(change_id,original_run_id,command_incarnation,asset_id,asset_token,expected_epoch,input_revision,fingerprint,state,revision) VALUES(?,?,?,?,?,?,?,?,?,?) RETURNING refresh_id`, Params: []stmt.Param{i("change"), t("run"), b("incarnation"), i("asset"), t("token"), i("epoch"), i("input"), b("fingerprint"), t("state"), i("revision")}, Plan: stmt.Plan{Hot: true}},
		{ID: iaRefreshAdvance, Kind: stmt.Write, SQL: `UPDATE catalog_info_refresh_undo SET revision=?,state=?,last_command_sequence=?,after_family=?,after_key=?,retained_rows=?,retained_bytes=? WHERE refresh_id=? AND command_incarnation=? AND revision=? AND state=? AND last_command_sequence=? AND acknowledged_command_sequence=? AND retained_rows=? AND retained_bytes=?`, Params: []stmt.Param{i("revision"), t("state"), i("sequence"), i("family"), t("key"), i("rows"), i("bytes"), i("refresh"), b("incarnation"), i("old_revision"), t("old_state"), i("last"), i("ack"), i("old_rows"), i("old_bytes")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_info_refresh_undo"}}},
		{ID: iaRefreshReceipt, Kind: stmt.Write, SQL: `INSERT INTO catalog_info_refresh_receipts(refresh_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,accepted_revision,applied_ms,result_state,after_family,after_key,result_rows,result_bytes,result_digest) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, Params: []stmt.Param{i("refresh"), b("incarnation"), i("sequence"), b("operation"), b("request"), t("kind"), i("revision"), i("applied"), t("state"), i("family"), t("key"), i("rows"), i("bytes"), b("digest")}, Plan: stmt.Plan{Hot: true}},
		{ID: iaRefreshInvalidate, Kind: stmt.Write, SQL: `UPDATE catalog_assets SET playback_origin_revision=?,prepared_source_revision=?,info_invalidation_revision=? WHERE id=? AND token=? AND info_epoch=? AND info_publication_pending=1 AND playback_origin_revision=? AND prepared_source_revision=? AND info_invalidation_revision=?`, Params: []stmt.Param{i("next_playback"), i("next_prepared"), i("next_invalidation"), i("asset"), t("token"), i("epoch"), i("playback"), i("prepared"), i("invalidation")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_assets"}}},
		{ID: iaRefreshShared, Kind: stmt.Read, SQL: `SELECT 1 FROM asset_subtitles INDEXED BY asset_subtitles_sidecar_id_fk WHERE sidecar_id=? AND asset_id<>? LIMIT 1`, Params: []stmt.Param{t("sidecar"), t("asset")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"asset_subtitles_sidecar_id_fk"}}},
		{ID: iaRefreshReferences, Kind: stmt.Read, SQL: `SELECT 1 FROM asset_subtitles INDEXED BY asset_subtitles_sidecar_id_fk WHERE sidecar_id=? LIMIT 1`, Params: []stmt.Param{t("sidecar")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"asset_subtitles_sidecar_id_fk"}}},
	}
	defs = append(defs, stmt.Definition{ID: iaRefreshInputCurrent, Kind: stmt.Read, SQL: `SELECT 1 FROM catalog_info_input_witnesses AS w INNER JOIN inventory_objects AS o ON o.id=w.object_id INNER JOIN catalog_assets AS a ON a.id=? WHERE w.change_id=? AND w.ordinal=0 AND w.kind=0 AND o.source_id=w.source_id AND o.root_incarnation=w.root_incarnation AND o.revision=w.object_revision AND o.retired=0 AND o.asset_id=a.token AND o.size=a.size AND o.modified_ns=a.modified_ns AND a.size=? AND a.modified_ns=?`, Params: []stmt.Param{i("asset"), i("change"), i("size"), i("modified")}, Plan: stmt.Plan{Hot: true}})

	defs = append(defs, iaRefreshRowDefinitions()...)
	for j := range defs {
		var bindings []any
		for _, p := range defs[j].Params {
			var v any
			switch p.Type {
			case stmt.Integer:
				v = int64(1)
			case stmt.Real:
				v = float64(1)
			case stmt.Text:
				v = "absent"
			case stmt.Blob:
				v = make([]byte, 32)
			}
			bindings = append(bindings, v)
		}
		defs[j].Cohorts = []stmt.Cohort{{Name: "absent", Bindings: bindings}}
		defs[j].Owner = "store/writer IA refresh core"
		defs[j].CallSites = []string{"store/writer/ia_refresh_core.go", "store/writer/ia_refresh_entry.go"}
	}
	for _, d := range basicStreamStageDefinitions() {
		if d.ID == basicStageBudget || d.ID == basicStageBudgetCAS {
			defs = append(defs, d)
		}
	}
	for _, d := range iaEntryDefinitions() {
		if d.ID == iaEntryNamespace {
			defs = append(defs, d)
		}
	}
	return append(usAuthorityDefinitions(), defs...)
}
