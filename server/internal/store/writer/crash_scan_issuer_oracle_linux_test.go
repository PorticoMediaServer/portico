package writer

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
)

// Schema defaults are immutable canonical INPUT, read before the command.
// Only literal defaults are interpreted; required/dynamic fields must be
// supplied explicitly. No candidate row or production reducer fills a field.
func scanCrashDefaults(t *testing.T, e *executor) map[string]map[string]effectiveRawCell {
	t.Helper()
	out := map[string]map[string]effectiveRawCell{}
	for _, name := range []string{"jobs", "inventory_runs", "inventory_directories", "inventory_source_active", "scan_command_receipts"} {
		rows, err := e.db.QueryContext(t.Context(), "PRAGMA table_xinfo('"+name+"')")
		if err != nil {
			t.Fatal(err)
		}
		fields := map[string]effectiveRawCell{}
		for rows.Next() {
			var cid, required, pk, hidden int64
			var column, kind string
			var literal sql.NullString
			if err := rows.Scan(&cid, &column, &kind, &required, &literal, &pk, &hidden); err != nil {
				t.Fatal(err)
			}
			value := effectiveRawCell{Kind: "required"}
			if !literal.Valid && required == 0 {
				value = effectiveRawCell{Kind: "null"}
			}
			if literal.Valid {
				s := literal.String
				switch {
				case strings.EqualFold(s, "NULL"):
					value = effectiveRawCell{Kind: "null"}
				case len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\'':
					value = effectiveRawCell{Kind: "text", Text: strings.ReplaceAll(s[1:len(s)-1], "''", "'")}
				default:
					if n, err := strconv.ParseInt(s, 10, 64); err == nil {
						value = effectiveRawCell{Kind: "integer", Int: n}
					}
				}
			}
			fields[column] = value
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		out[name] = fields
	}
	return out
}

func scanCrashRow(source scanIssuerPreparedCrashSource, table string, values map[string]effectiveRawCell) map[string]effectiveRawCell {
	row := map[string]effectiveRawCell{}
	for k, v := range source.Defaults[table] {
		row[k] = v
	}
	for k, v := range values {
		_, ok := row[k]
		progressRequire(ok, "unknown scan column "+table+"."+k)
		row[k] = v
	}
	for k, v := range row {
		progressRequire(v.Kind != "required", "unmodeled scan default "+table+"."+k)
	}
	return row
}

func scanCrashFind(j effectiveRawJournal, table, column, value string) map[string]effectiveRawCell {
	var found map[string]effectiveRawCell
	for _, row := range j[table].Rows {
		if row[column].Text == value {
			progressRequire(found == nil, "duplicate scan row")
			found = row
		}
	}
	progressRequire(found != nil, "missing scan row "+table)
	return found
}

func scanCrashReceipt(j effectiveRawJournal, ref ScanReceiptRequest) map[string]effectiveRawCell {
	var found map[string]effectiveRawCell
	for _, row := range j["scan_command_receipts"].Rows {
		if row["run_id"].Text == ref.RunID && row["command_sequence"].Int == ref.Receipt.Identity.Sequence && bytes.Equal(row["command_incarnation"].Blob, ref.Receipt.Identity.Incarnation[:]) {
			progressRequire(found == nil, "duplicate original receipt")
			found = row
		}
	}
	progressRequire(found != nil, "missing original scan receipt")
	progressRequire(bytes.Equal(found["operation_id"].Blob, ref.Receipt.Identity.OperationID[:]) && bytes.Equal(found["request_digest"].Blob, ref.Receipt.RequestDigest[:]), "original receipt identity/digest")
	return found
}

// Independent v1 wire encoder: fixed octets, big-endian scalars and UTF8
// length-prefixed strings. It calls no writer codec/validator/result reducer.
func scanCrashResultDigest(source scanIssuerPreparedCrashSource, run, root, job map[string]effectiveRawCell, applied int64) ResultDigest {
	h := sha256.New()
	put := func(p []byte) { _, _ = h.Write(p) }
	num := func(n int64) { var p [8]byte; binary.BigEndian.PutUint64(p[:], uint64(n)); put(p[:]) }
	text := func(s string) { num(int64(len(s))); put([]byte(s)) }
	put([]byte("portico-writer-result-sha256-v1\x00"))
	put([]byte{0, 1})
	name := "inventory_create"
	if source.Reference.Purpose == InventoryClaim {
		name = "inventory_claim"
	}
	text(name)
	text("run_lifecycle")
	text("applied")
	text(source.Reference.Scan.RunID)
	i := source.Reference.Scan.Receipt.Identity
	put(i.Incarnation[:])
	num(i.Sequence)
	put(i.OperationID[:])
	put(source.Reference.Scan.Receipt.RequestDigest[:])
	num(run["original_library_id"].Int)
	text(run["original_library_key"].Text)
	text(run["source_id"].Text)
	num(run["source_generation"].Int)
	text(run["root_incarnation"].Text)
	text(run["root_identity"].Text)
	raw := []byte(run["original_resolved_root"].Text)
	num(int64(len(raw)))
	put(raw)
	flag := byte(0)
	if run["original_follow_symlinks"].Int == 1 {
		flag = 1
	}
	put([]byte{flag})
	num(run["policy_revision"].Int)
	text(run["scan_mode"].Text)
	put(run["create_operation_id"].Blob)
	put(run["create_request_digest"].Blob)
	num(run["job_revision"].Int)
	num(applied)
	num(run["job_lease_owner"].Int)
	num(run["job_lease_generation"].Int)
	num(run["job_lease_expires_ms"].Int)
	num(run["acknowledged_command_sequence"].Int)
	num(run["discovered"].Int)
	num(job["processed"].Int)
	text(root["identity"].Text)
	text(root["revision"].Text)
	text(root["cursor"].Text)
	num(root["pages"].Int)
	num(root["skipped"].Int)
	put([]byte{0})
	text("")
	num(0)
	num(0)
	var result ResultDigest
	copy(result[:], h.Sum(nil))
	return result
}

func expectedScanIssuer(source scanIssuerPreparedCrashSource) (want effectiveRawJournal, value ScanRunSnapshot, err error) {
	defer func() {
		if v := recover(); v != nil {
			want, value, err = nil, ScanRunSnapshot{}, fmt.Errorf("scan issuer oracle: %v", v)
		}
	}()
	data, err := json.Marshal(source.Initial)
	progressRequire(err == nil && json.Unmarshal(data, &want) == nil, "clone scan source")
	progressRequire(source.Reference.Purpose == InventoryCreate || source.Reference.Purpose == InventoryClaim, "scan purpose")
	ref := source.Reference.Scan
	progressRequire(ref.Receipt.Identity.Sequence > 0 && ref.Receipt.RequestDigest != (RequestDigest{}) && ref.RunID != "", "original scan reference")
	var run, job, root, receipt map[string]effectiveRawCell
	switch source.Action {
	case "create":
		run = scanCrashRow(source, "inventory_runs", source.RunInsert)
		job = scanCrashRow(source, "jobs", source.JobInsert)
		root = scanCrashRow(source, "inventory_directories", source.RootInsert)
		progressAppend(want, "jobs", job)
		progressAppend(want, "inventory_runs", run)
		progressAppend(want, "inventory_directories", root)
		progressAppend(want, "inventory_source_active", scanCrashRow(source, "inventory_source_active", effectiveModelRow(map[string]any{"source_id": run["source_id"].Text, "job_id": ref.RunID})))
	case "claim":
		run = scanCrashFind(want, "inventory_runs", "job_id", ref.RunID)
		job = scanCrashFind(want, "jobs", "id", ref.RunID)
		root = scanCrashFind(want, "inventory_directories", "job_id", ref.RunID)
		for k, v := range source.RunInsert {
			run[k] = v
		}
		for k, v := range source.JobInsert {
			job[k] = v
		}
	case "resolve", "retired", "ack":
		run = scanCrashFind(want, "inventory_runs", "job_id", ref.RunID)
		job = scanCrashFind(want, "jobs", "id", ref.RunID)
		root = scanCrashFind(want, "inventory_directories", "job_id", ref.RunID)
		if source.Action == "retired" {
			progressRequire(run["acknowledged_command_sequence"].Int >= ref.Receipt.Identity.Sequence, "retired floor proof")
			value = ScanRunSnapshot{Outcome: ReceiptOutcome{State: ResultRetired, Reference: ref.Receipt}}
			return want, value, nil
		}
		receipt = scanCrashReceipt(want, ref)
		if source.Action == "ack" {
			progressSet(run, "acknowledged_command_sequence", ref.Receipt.Identity.Sequence)
			// Reviewed lifecycle ACK co-commits floor + exact fully validated
			// zero-cohort header removal. N1 child-bearing receipts are separate.
			for _, child := range want["scan_command_results"].Rows {
				progressRequire(child["run_id"].Text != ref.RunID || child["command_sequence"].Int != ref.Receipt.Identity.Sequence, "lifecycle cohort must be empty")
			}
			table := want["scan_command_receipts"]
			var kept []map[string]effectiveRawCell
			for _, r := range table.Rows {
				if r["run_id"].Text != ref.RunID || r["command_sequence"].Int != ref.Receipt.Identity.Sequence || !bytes.Equal(r["command_incarnation"].Blob, ref.Receipt.Identity.Incarnation[:]) {
					kept = append(kept, r)
				}
			}
			table.Rows = kept
			want["scan_command_receipts"] = table
		}
	default:
		progressRequire(false, "unsupported scan action")
	}
	if source.Action == "create" || source.Action == "claim" {
		digest := scanCrashResultDigest(source, run, root, job, source.AppliedMS)
		kind := "inventory_create"
		if source.Action == "claim" {
			kind = "inventory_claim"
		}
		receipt = scanCrashRow(source, "scan_command_receipts", effectiveModelRow(map[string]any{
			"run_id": ref.RunID, "command_incarnation": ref.Receipt.Identity.Incarnation[:], "command_sequence": ref.Receipt.Identity.Sequence,
			"operation_id": ref.Receipt.Identity.OperationID[:], "request_digest": ref.Receipt.RequestDigest[:], "command_kind": kind,
			"accepted_revision": run["job_revision"].Int, "applied_ms": source.AppliedMS, "source_id": run["source_id"].Text, "source_incarnation": run["root_incarnation"].Text,
			"source_revision": nil, "next_cursor": "", "result_count": int64(0), "result_digest": digest[:],
			"result_lease_owner": run["job_lease_owner"].Int, "result_lease_generation": run["job_lease_generation"].Int, "result_lease_expires_ms": run["job_lease_expires_ms"].Int,
			"result_acknowledged_sequence": run["acknowledged_command_sequence"].Int, "result_discovered": run["discovered"].Int, "result_processed": job["processed"].Int,
			"result_root_identity": root["identity"].Text, "result_root_revision": root["revision"].Text, "result_root_cursor": root["cursor"].Text,
			"result_root_pages": root["pages"].Int, "result_root_skipped": root["skipped"].Int,
		}))
		progressAppend(want, "scan_command_receipts", receipt)
	}
	value = ScanRunSnapshot{Outcome: ReceiptOutcome{State: Applied, Reference: ref.Receipt, Replayed: source.Action == "resolve"}, RunID: ref.RunID,
		AcceptedRunRevision: receipt["accepted_revision"].Int, AppliedMS: receipt["applied_ms"].Int,
		LeaseOwner: receipt["result_lease_owner"].Int, LeaseGeneration: receipt["result_lease_generation"].Int, LeaseExpiresMS: receipt["result_lease_expires_ms"].Int,
		AcknowledgedSequence: receipt["result_acknowledged_sequence"].Int, Discovered: receipt["result_discovered"].Int, Processed: receipt["result_processed"].Int,
		RootIdentity: receipt["result_root_identity"].Text, RootRevision: receipt["result_root_revision"].Text, RootCursor: receipt["result_root_cursor"].Text,
		RootPages: receipt["result_root_pages"].Int, RootSkipped: receipt["result_root_skipped"].Int}
	progressRequire(copy(value.Digest[:], receipt["result_digest"].Blob) == 32, "receipt digest width")
	// Historical fixture receipts were independently verified at their original
	// Apply before becoming this source; their complete payload stays immutable.
	if source.Action == "retired" {
		value = ScanRunSnapshot{Outcome: ReceiptOutcome{State: ResultRetired, Reference: ref.Receipt}}
	}
	for name, table := range want {
		sortEffectiveRows(&table)
		want[name] = table
	}
	return want, value, nil
}
