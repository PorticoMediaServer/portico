package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// These statements belong to the scan owner adapter. They supply facts to W's
// common receipt classifier; neither registration nor SQL success constructs an
// Applied result. Current disclosure authority must be checked before reading
// historical results, and the ACK floor before returning any result cohort.
const (
	scanReceiptOwner         stmt.ID = "scan.inventory_accept.receipt_owner"
	scanReceiptLookup        stmt.ID = "scan.inventory_accept.receipt_lookup"
	scanReceiptResults       stmt.ID = "scan.inventory_accept.receipt_results"
	scanReceiptInsert        stmt.ID = "scan.inventory_accept.receipt_insert"
	scanResultInsert         stmt.ID = "scan.inventory_accept.result_insert"
	scanReceiptAck           stmt.ID = "scan.inventory_accept.receipt_ack"
	scanReceiptReclaimKey    stmt.ID = "scan.inventory_accept.receipt_reclaim_key"
	scanReceiptReclaimResult stmt.ID = "scan.inventory_accept.receipt_reclaim_result"
	scanReceiptReclaimHeader stmt.ID = "scan.inventory_accept.receipt_reclaim_header"
)

// Handler-owned constants are bound, never selected by caller payloads.
const scanInventoryReceiptKind = "inventory_accept"

const scanOrdinaryChildKind, scanOrdinaryChildOrdinal int64 = 0, 0

// scanReceiptDefinitions is composed only by W's finite registry. It exposes no
// registration hook, SQL executor, connection, classifier or independent writer.
// InventoryRefuse is deliberately absent until INT's separate packet is adopted.
func scanReceiptDefinitions() []stmt.Definition {
	text := func(name string) stmt.Param { return stmt.Param{Name: name, Type: stmt.Text} }
	integer := func(name string) stmt.Param { return stmt.Param{Name: name, Type: stmt.Integer} }
	blob := func(name string) stmt.Param { return stmt.Param{Name: name, Type: stmt.Blob} }
	inc, operation, digest := make([]byte, 16), make([]byte, 16), make([]byte, 32)
	definitions := []stmt.Definition{
		{
			ID: scanReceiptOwner, Kind: stmt.Read,
			SQL: `SELECT command_incarnation,last_command_sequence,acknowledged_command_sequence
FROM inventory_runs WHERE job_id=?`,
			Params:  []stmt.Param{text("run_id")},
			Cohorts: scanReceiptReadCohorts(scanReceiptOwner, inc),
		},
		{
			ID: scanReceiptLookup, Kind: stmt.Read,
			SQL: `SELECT operation_id,request_digest,command_kind,accepted_revision,applied_ms,
source_id,source_incarnation,source_revision,next_cursor,result_count,result_digest
FROM scan_command_receipts
WHERE run_id=? AND command_incarnation=? AND command_sequence=?`,
			Params:  []stmt.Param{text("run_id"), blob("command_incarnation"), integer("command_sequence")},
			Cohorts: scanReceiptReadCohorts(scanReceiptLookup, inc),
		},
		{
			ID: scanReceiptResults, Kind: stmt.Read,
			SQL: `SELECT input_ordinal,child_kind,child_ordinal,object_id,library_id,subject_id,asset_id,accepted_asset_revision
FROM scan_command_results
WHERE run_id=? AND command_incarnation=? AND command_sequence=?
ORDER BY input_ordinal,child_kind,child_ordinal LIMIT ?`,
			Params: []stmt.Param{text("run_id"), blob("command_incarnation"), integer("command_sequence"), integer("result_limit")},
			// The decoder must request count+1 within the admitted result bound,
			// verify exact count/digest, and never serve a truncated prefix.
			Cohorts: scanReceiptReadCohorts(scanReceiptResults, inc),
		},
		{
			ID: scanReceiptInsert, Kind: stmt.Write,
			SQL: `INSERT INTO scan_command_receipts(
run_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,
accepted_revision,applied_ms,source_id,source_incarnation,source_revision,next_cursor,result_count,result_digest)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			Params: []stmt.Param{text("run_id"), blob("command_incarnation"), integer("command_sequence"), blob("operation_id"), blob("request_digest"), text("command_kind"),
				integer("accepted_revision"), integer("applied_ms"), text("source_id"), text("source_incarnation"), text("source_revision"), text("next_cursor"), integer("result_count"), blob("result_digest")},
			Cohorts: []stmt.Cohort{{Name: "new_ordinary_receipt", Bindings: []any{"scan-new", inc, int64(1), operation, digest, scanInventoryReceiptKind,
				int64(2), int64(1), "scan-source-new", "source-incarnation", "opaque:007", "native-next", int64(1), digest}}},
		},
		{
			ID: scanResultInsert, Kind: stmt.Write,
			SQL: `INSERT INTO scan_command_results(
run_id,command_incarnation,command_sequence,input_ordinal,child_kind,child_ordinal,
object_id,library_id,subject_id,asset_id,accepted_asset_revision)
VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
			Params: []stmt.Param{text("run_id"), blob("command_incarnation"), integer("command_sequence"), integer("input_ordinal"), integer("child_kind"), integer("child_ordinal"),
				text("object_id"), integer("library_id"), integer("subject_id"), integer("asset_id"), integer("accepted_asset_revision")},
			Cohorts: []stmt.Cohort{{Name: "new_ordinary_child_zero", Bindings: []any{"scan-new", inc, int64(1), int64(0), scanOrdinaryChildKind, scanOrdinaryChildOrdinal, "object-new", int64(7), int64(31), int64(41), int64(1)}}},
		},
		{
			ID: scanReceiptAck, Kind: stmt.Write,
			// Same-turn authority/owner/header reads and W's common decision
			// precede this CAS. No SQL subquery duplicates the classifier.
			SQL: `UPDATE inventory_runs SET acknowledged_command_sequence=?
WHERE job_id=? AND command_incarnation=? AND acknowledged_command_sequence=? AND last_command_sequence=?`,
			Params: []stmt.Param{integer("acknowledged_sequence"), text("run_id"), blob("command_incarnation"), integer("expected_acknowledged_sequence"), integer("expected_last_sequence")},
			Cohorts: []stmt.Cohort{
				{Name: "ack_hit", Bindings: []any{int64(1), "scan-hit", inc, int64(0), int64(1)}},
				{Name: "ack_stale_cas", Bindings: []any{int64(1), "scan-stale", inc, int64(0), int64(2)}},
			},
		},
		{
			ID: scanReceiptReclaimKey, Kind: stmt.Read,
			SQL: `SELECT input_ordinal,child_kind,child_ordinal FROM scan_command_results
WHERE run_id=? AND command_incarnation=? AND command_sequence=?
ORDER BY input_ordinal,child_kind,child_ordinal LIMIT 1`,
			Params:  []stmt.Param{text("run_id"), blob("command_incarnation"), integer("command_sequence")},
			Cohorts: scanReceiptReadCohorts(scanReceiptReclaimKey, inc),
		},
		{
			ID: scanReceiptReclaimResult, Kind: stmt.Write,
			// Reclamation requires same-turn owner/floor and full result-key
			// reads. Their captured values select exactly one row here.
			SQL: `DELETE FROM scan_command_results
WHERE run_id=? AND command_incarnation=? AND command_sequence=? AND input_ordinal=? AND child_kind=? AND child_ordinal=?`,
			Params: []stmt.Param{text("run_id"), blob("command_incarnation"), integer("command_sequence"), integer("input_ordinal"), integer("child_kind"), integer("child_ordinal")},
			Cohorts: []stmt.Cohort{
				{Name: "acknowledged_remaining_result", Bindings: []any{"scan-remaining", inc, int64(1), int64(1), scanOrdinaryChildKind, scanOrdinaryChildOrdinal}},
				{Name: "missing_full_result_key", Bindings: []any{"scan-remaining", inc, int64(1), int64(0), scanOrdinaryChildKind, scanOrdinaryChildOrdinal}},
			},
		},
		{
			ID: scanReceiptReclaimHeader, Kind: stmt.Write,
			// Only after the same-turn floor proof and a bounded no-result
			// point read. The FK independently rejects a remaining child.
			SQL: `DELETE FROM scan_command_receipts
WHERE run_id=? AND command_incarnation=? AND command_sequence=?`,
			Params: []stmt.Param{text("run_id"), blob("command_incarnation"), integer("command_sequence")},
			Cohorts: []stmt.Cohort{
				{Name: "acknowledged_drained_header", Bindings: []any{"scan-drained", inc, int64(1)}},
				{Name: "remaining_child_fk_refusal", Bindings: []any{"scan-remaining", inc, int64(1)}},
			},
		},
	}
	for i := range definitions {
		definitions[i].Owner = "store/writer scan owner adapter"
		definitions[i].CallSites = []string{"store/writer/scan_receipts.go"}
		definitions[i].Plan = stmt.Plan{Hot: true}
		switch definitions[i].ID {
		case scanReceiptOwner, scanReceiptAck:
			definitions[i].Plan.Indexes = []string{"sqlite_autoindex_inventory_runs_1"}
			definitions[i].Plan.Seeks = []string{"job_id=?"}
		case scanReceiptLookup:
			definitions[i].Plan.PrimaryKeys = []string{"scan_command_receipts"}
			definitions[i].Plan.Seeks = []string{"run_id=? AND command_incarnation=? AND command_sequence=?"}
		case scanReceiptResults, scanReceiptReclaimKey:
			definitions[i].Plan.PrimaryKeys = []string{"scan_command_results"}
			definitions[i].Plan.Seeks = []string{"run_id=? AND command_incarnation=? AND command_sequence=?"}
		case scanReceiptReclaimResult:
			definitions[i].Plan.PrimaryKeys = []string{"scan_command_results"}
			definitions[i].Plan.Seeks = []string{"run_id=? AND command_incarnation=? AND command_sequence=? AND input_ordinal=? AND child_kind=? AND child_ordinal=?"}
		case scanReceiptReclaimHeader:
			definitions[i].Plan.PrimaryKeys = []string{"scan_command_receipts", "scan_command_results"}
			definitions[i].Plan.Seeks = []string{"run_id=? AND command_incarnation=? AND command_sequence=?"}
		}
	}
	return definitions
}

// These cohorts map to distinct native fixtures in scan_receipts_test.go. A
// conflict uses an actually different stored digest; stale changes the current
// source/lease facts, not the historical result. No "hit_or_missing" population.
func scanReceiptReadCohorts(id stmt.ID, inc []byte) []stmt.Cohort {
	var out []stmt.Cohort
	for _, name := range []string{"hit", "missing", "replay", "conflict", "stale", "floor", "drained", "remaining"} {
		args := []any{"scan-" + name}
		if id != scanReceiptOwner {
			args = append(args, inc, int64(1))
		}
		if id == scanReceiptResults {
			args = append(args, int64(3))
		}
		out = append(out, stmt.Cohort{Name: name, Bindings: args})
	}
	return out
}
