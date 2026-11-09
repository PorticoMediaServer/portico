package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// IA-17 statement family. Every statement is a point read, a bounded indexed
// window or one finite CAS/insert on the exact group C draft receipt/child
// relations and the embedded inventory relations. There is no OFFSET, temporary
// sort, library-wide scan or caller-selected SQL. The reconcile child cohort is
// read only through its own receipt/kind prefix.
const (
	iaReconcileOwner          stmt.ID = "ia.reconcile.object.owner"
	iaReconcileHeader         stmt.ID = "ia.reconcile.object.header"
	iaReconcileMembers        stmt.ID = "ia.reconcile.object.members"
	iaReconcileLibrary        stmt.ID = "ia.reconcile.object.library"
	iaReconcileSource         stmt.ID = "ia.reconcile.object.source"
	iaReconcileObject         stmt.ID = "ia.reconcile.object.object"
	iaReconcilePathLive       stmt.ID = "ia.reconcile.object.path_live"
	iaReconcileIdentityWindow stmt.ID = "ia.reconcile.object.identity_window"
	iaReconcileRunCAS         stmt.ID = "ia.reconcile.object.run_cas"
	iaReconcileReceiptInsert  stmt.ID = "ia.reconcile.object.receipt_insert"
	iaReconcileMemberInsert   stmt.ID = "ia.reconcile.object.member_insert"
)

const iaReconcileReceiptKind = "inventory_reconcile"

func iaReconcileObjectStatements() []stmt.Definition {
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	nt := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text, Nullable: true} }
	ni := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer, Nullable: true} }
	boolean := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Boolean} }
	inc, operation, digest := make([]byte, 16), make([]byte, 16), make([]byte, 32)
	key := []stmt.Param{t("run_id"), b("command_incarnation"), i("command_sequence")}
	defs := []stmt.Definition{
		{ID: iaReconcileOwner, Kind: stmt.Read,
			SQL:    `SELECT command_incarnation,last_command_sequence,acknowledged_command_sequence FROM inventory_runs WHERE job_id=?`,
			Params: []stmt.Param{t("run_id")},
			Cohorts: []stmt.Cohort{
				{Name: "current_owner", Bindings: []any{"reconcile-run"}},
				{Name: "missing_owner", Bindings: []any{"reconcile-missing"}},
			}},
		{ID: iaReconcileHeader, Kind: stmt.Read,
			SQL: `SELECT operation_id,request_digest,command_kind,result_count,result_digest,accepted_revision,applied_ms,
source_id,source_incarnation,ia_before_key,ia_after_key,ia_phase,ia_authoritative,ia_processed,ia_analyzed,ia_warnings,
ia_finished_ms,ia_source_health
FROM scan_command_receipts WHERE run_id=? AND command_incarnation=? AND command_sequence=?`,
			Params: key,
			Cohorts: []stmt.Cohort{
				{Name: "saved_reconcile_receipt", Bindings: []any{"reconcile-run", inc, int64(2)}},
				{Name: "missing_receipt", Bindings: []any{"reconcile-missing", inc, int64(2)}},
			}},
		{ID: iaReconcileMembers, Kind: stmt.Read,
			SQL: `SELECT ordinal,object_id,prior_object_id,before_revision,after_revision,before_path,after_path,disposition
FROM scan_reconcile_members
WHERE run_id=? AND command_incarnation=? AND command_sequence=? AND command_kind='inventory_reconcile'
ORDER BY ordinal LIMIT ?`,
			Params: append(append([]stmt.Param{}, key...), i("row_limit")),
			Cohorts: []stmt.Cohort{
				{Name: "saved_members", Bindings: []any{"reconcile-run", inc, int64(2), int64(2)}},
				{Name: "missing_members", Bindings: []any{"reconcile-missing", inc, int64(2), int64(2)}},
			}},
		{ID: iaReconcileLibrary, Kind: stmt.Read,
			SQL:    `SELECT id FROM catalog_libraries WHERE id=? AND library_id=? AND retired=0`,
			Params: []stmt.Param{i("library_id"), t("library_key")},
			Cohorts: []stmt.Cohort{
				{Name: "live_library", Bindings: []any{int64(7), "reconcile-library"}},
				{Name: "retired_library", Bindings: []any{int64(8), "reconcile-retired"}},
			}},
		{ID: iaReconcileSource, Kind: stmt.Read,
			SQL: `SELECT id,health FROM library_sources
WHERE id=? AND library_id=? AND generation=? AND incarnation=? AND root_identity=? AND root=? AND follow_symlinks=?
AND kind='local' AND classification='local' AND enabled=1 AND identity_confirmed=1 AND health='ready' AND remote_source_id IS NULL`,
			Params: []stmt.Param{t("source_id"), t("library_key"), i("generation"), t("incarnation"), t("root_identity"), t("resolved_root"), boolean("follow_symlinks")},
			Cohorts: []stmt.Cohort{
				{Name: "current_source", Bindings: []any{"reconcile-source", "reconcile-library", int64(3), "reconcile-incarnation", "reconcile-root-identity", "/media/reconcile", false}},
				{Name: "stale_source", Bindings: []any{"reconcile-source", "reconcile-library", int64(4), "reconcile-incarnation", "reconcile-root-identity", "/media/reconcile", false}},
			}},
		{ID: iaReconcileObject, Kind: stmt.Read,
			SQL:    `SELECT object_identity,revision,size,modified_ns FROM inventory_objects WHERE id=? AND source_id=? AND relative_path=? AND state='available' AND retired=0`,
			Params: []stmt.Param{t("object_id"), t("source_id"), t("relative_path")},
			Cohorts: []stmt.Cohort{
				{Name: "retained_object", Bindings: []any{"object:reconcile:1", "reconcile-source", "Reconcile/Movie.mkv"}},
				{Name: "missing_object", Bindings: []any{"object:reconcile:missing", "reconcile-source", "Reconcile/Movie.mkv"}},
			}},
		{ID: iaReconcilePathLive, Kind: stmt.Read,
			SQL:    `SELECT id FROM inventory_objects INDEXED BY inventory_objects_path WHERE source_id=? AND relative_path=? AND retired=0`,
			Params: []stmt.Param{t("source_id"), t("relative_path")},
			Cohorts: []stmt.Cohort{
				{Name: "free_path", Bindings: []any{"reconcile-source", "Reconcile/Moved.mkv"}},
				{Name: "occupied_path", Bindings: []any{"reconcile-source", "Reconcile/Movie.mkv"}},
			}},
		{ID: iaReconcileIdentityWindow, Kind: stmt.Read,
			SQL: `SELECT id,relative_path,object_identity,revision,size,modified_ns
FROM inventory_objects INDEXED BY inventory_objects_identity
WHERE source_id=? AND object_identity=? AND size=? AND modified_ns=? AND retired=0
ORDER BY asset_id LIMIT ?`,
			Params: []stmt.Param{t("source_id"), t("object_identity"), i("size"), i("modified_ns"), i("row_limit")},
			Cohorts: []stmt.Cohort{
				{Name: "identity_match", Bindings: []any{"reconcile-source", "ino:41", int64(42), int64(7), int64(3)}},
				{Name: "identity_miss", Bindings: []any{"reconcile-source", "ino:missing", int64(42), int64(7), int64(3)}},
			}},
		{ID: iaReconcileRunCAS, Kind: stmt.Write,
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
				{Name: "actual_post_reconcile_revision", Bindings: []any{int64(6), int64(2), "reconcile-run", inc, int64(7), "reconcile-library", "/media/reconcile", false,
					"reconcile-source", int64(3), "reconcile-incarnation", "reconcile-root-identity", int64(4), "inventory", "first-import",
					int64(11), int64(13), int64(4000000000000), int64(5), int64(1), int64(1)}},
				{Name: "stale_run_cas", Bindings: []any{int64(6), int64(2), "reconcile-run", inc, int64(7), "reconcile-library", "/media/reconcile", false,
					"reconcile-source", int64(3), "reconcile-incarnation", "reconcile-root-identity", int64(4), "inventory", "first-import",
					int64(11), int64(13), int64(4000000000000), int64(5), int64(1), int64(0)}},
			}},
		{ID: iaReconcileReceiptInsert, Kind: stmt.Write,
			SQL: `INSERT INTO scan_command_receipts(
run_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,accepted_revision,applied_ms,
source_id,source_incarnation,source_revision,change_id,next_cursor,result_count,result_digest,
ia_before_key,ia_after_key,ia_phase,ia_authoritative,ia_processed,ia_analyzed,ia_warnings,ia_finished_ms,ia_source_health)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			Params: []stmt.Param{t("run_id"), b("command_incarnation"), i("command_sequence"), b("operation_id"), b("request_digest"), t("command_kind"),
				i("accepted_revision"), i("applied_ms"), t("source_id"), t("source_incarnation"), nt("source_revision"), ni("change_id"), t("next_cursor"),
				i("result_count"), b("result_digest"), t("before_key"), t("after_key"), t("phase"), i("authoritative"), i("processed"), i("analyzed"),
				i("warnings"), i("finished_ms"), t("source_health")},
			Cohorts: []stmt.Cohort{
				{Name: "new_reconcile_receipt", Bindings: []any{"reconcile-run", inc, int64(2), operation, digest, iaReconcileReceiptKind, int64(6), int64(1),
					"reconcile-source", "reconcile-incarnation", nil, nil, "", int64(1), digest,
					"object:reconcile:1", "object:reconcile:1", "reconciling", int64(0), int64(1), int64(1), int64(0), int64(0), "ready"}},
			}},
		{ID: iaReconcileMemberInsert, Kind: stmt.Write,
			SQL: `INSERT INTO scan_reconcile_members(
run_id,command_incarnation,command_sequence,command_kind,ordinal,object_id,prior_object_id,
before_revision,after_revision,before_path,after_path,disposition)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
			Params: []stmt.Param{t("run_id"), b("command_incarnation"), i("command_sequence"), t("command_kind"), i("ordinal"),
				t("object_id"), nt("prior_object_id"), t("before_revision"), t("after_revision"), t("before_path"), t("after_path"), t("disposition")},
			Cohorts: []stmt.Cohort{
				{Name: "new_changed_member", Bindings: []any{"reconcile-run", inc, int64(2), iaReconcileReceiptKind, int64(0), "object:reconcile:1", nil,
					"object-revision:1", "object-revision:2", "Reconcile/Movie.mkv", "Reconcile/Movie.mkv", "changed"}},
				{Name: "new_replaced_member", Bindings: []any{"reconcile-run", inc, int64(2), iaReconcileReceiptKind, int64(0), "object:reconcile:2", "object:reconcile:1",
					"object-revision:1", "object-revision:2", "Reconcile/Movie.mkv", "Reconcile/Movie.mkv", "replaced"}},
			}},
	}
	for n := range defs {
		defs[n].Owner = "store/writer IA reconcile object"
		defs[n].CallSites = []string{"store/writer/ia_reconcile_object.go"}
		defs[n].Plan = stmt.Plan{Hot: true}
		switch defs[n].ID {
		case iaReconcileOwner:
			defs[n].Plan.Indexes = []string{"sqlite_autoindex_inventory_runs_1"}
			defs[n].Plan.Seeks = []string{"job_id=?"}
		case iaReconcileHeader:
			defs[n].Plan.PrimaryKeys = []string{"scan_command_receipts"}
			defs[n].Plan.Seeks = []string{"run_id=? AND command_incarnation=? AND command_sequence=?"}
		case iaReconcileMembers:
			defs[n].Plan.PrimaryKeys = []string{"scan_reconcile_members"}
			defs[n].Plan.Seeks = []string{"run_id=? AND command_incarnation=? AND command_sequence=? AND command_kind=?"}
		case iaReconcileLibrary:
			defs[n].Plan.PrimaryKeys = []string{"catalog_libraries"}
			defs[n].Plan.Seeks = []string{"rowid=?"}
		case iaReconcileSource:
			defs[n].Plan.Indexes = []string{"sqlite_autoindex_library_sources_2"}
			defs[n].Plan.Seeks = []string{"library_id=? AND root=?"}
		case iaReconcileObject:
			defs[n].Plan.Indexes = []string{"sqlite_autoindex_inventory_objects_1"}
			defs[n].Plan.Seeks = []string{"id=?"}
		case iaReconcilePathLive:
			defs[n].Plan.Indexes = []string{"inventory_objects_path"}
			defs[n].Plan.Seeks = []string{"source_id=? AND relative_path=?"}
		case iaReconcileIdentityWindow:
			defs[n].Plan.Indexes = []string{"inventory_objects_identity"}
			defs[n].Plan.Seeks = []string{"object_identity=? AND size=? AND modified_ns=?"}
		case iaReconcileRunCAS:
			defs[n].Plan.Indexes = []string{"sqlite_autoindex_inventory_runs_2"}
			defs[n].Plan.Seeks = []string{"job_id=? AND command_incarnation=?"}
		}
	}
	return defs
}

// Complete private family: current US owner authority plus the finite reconcile
// points/CAS writes. No configuration, media-child or legacy result definition is
// composed here.
func iaReconcileObjectDefinitions() []stmt.Definition {
	return append(usAuthorityDefinitions(), iaReconcileObjectStatements()...)
}
