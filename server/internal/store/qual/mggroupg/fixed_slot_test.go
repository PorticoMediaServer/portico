package mggroupg

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// Native protocol model only. No W capability, authority, codec or real COMMIT
// qualification is inferred from these explicit candidate-table operations.
func updateNative(t *testing.T, f *fixture, table, key string, values map[string]any) {
	t.Helper()
	names := make([]string, 0, len(values))
	for n := range values {
		names = append(names, n)
	}
	slices.Sort(names)
	sets := make([]string, len(names))
	args := make([]any, 0, len(names)+1)
	for i, n := range names {
		sets[i] = n + "=?"
		args = append(args, values[n])
	}
	args = append(args, key)
	f.exec(t, "UPDATE "+table+" SET "+strings.Join(sets, ",")+" WHERE request_id=?", args...)
}
func gcSlot(seq, ack, old, removed, variable int64) map[string]any {
	var removedKey any
	count := int64(0)
	after := old
	if removed > 0 {
		removedKey = removed
		count = 1
		after = removed
	}
	return map[string]any{
		"last_command_sequence": seq, "acknowledged_command_sequence": ack,
		"receipt_reclaim_after_sequence": after, "receipt_reclaim_due_ms": nil,
		"reclaim_command_sequence": seq, "reclaim_operation_id": bytes.Repeat([]byte{byte(seq)}, 16),
		"reclaim_request_digest": bytes.Repeat([]byte{byte(seq + 16)}, 32), "reclaim_command_kind": "guide_receipt_reclaim",
		"reclaim_result_version": int64(1), "reclaim_captured_acknowledged_sequence": ack, "reclaim_old_after_sequence": old,
		"reclaim_removed_sequence": removedKey, "reclaim_removed_count": count,
		"reclaim_removed_variable_bytes": variable, "reclaim_result_after_sequence": after, "reclaim_result_no_next": int64(1),
		"reclaim_applied_ms": int64(1000) + seq, "reclaim_result_count": int64(1), "reclaim_result_digest": bytes.Repeat([]byte{byte(seq + 32)}, 32),
	}
}
func TestINTGroupGFixedSlotFloorAndDebt011(t *testing.T) {
	f := native(t, candidate)
	f.guide(t, "gc-generation", "source-a")
	if err := f.insert(t, "live_operations", map[string]any{"request_id": "gc", "source_id": "source-a", "generation_id": "gc-generation", "expected_revision": int64(0), "expected_generation": "", "status": "staging", "last_command_sequence": int64(2), "acknowledged_command_sequence": int64(1)}); err != nil {
		t.Fatal(err)
	}
	for seq := int64(1); seq <= 2; seq++ {
		row := publishReceipt()
		row["request_id"] = "gc"
		row["command_sequence"] = seq
		row["accepted_acknowledged_sequence"] = seq - 1
		row["command_kind"] = "channels"
		row["operation_id"] = bytes.Repeat([]byte{byte(seq + 70)}, 16)
		if err := f.insert(t, "live_operation_command_receipts", row); err != nil {
			t.Fatal(err)
		}
	}
	f.exec(t, "INSERT INTO live_operation_receipt_diagnostics(request_id,command_incarnation,command_sequence,ordinal,message) VALUES('gc',zeroblob(16),1,0,'one diagnostic')")
	var first, second, diagnostic int64
	for _, p := range []struct {
		q   string
		out *int64
	}{{"SELECT variable_bytes FROM live_operation_command_receipts WHERE request_id='gc' AND command_sequence=1", &first}, {"SELECT variable_bytes FROM live_operation_command_receipts WHERE request_id='gc' AND command_sequence=2", &second}, {"SELECT variable_bytes FROM live_operation_receipt_diagnostics WHERE request_id='gc'", &diagnostic}} {
		if err := f.c.QueryRowContext(t.Context(), p.q).Scan(p.out); err != nil {
			t.Fatal(err)
		}
	}
	total := first + second + diagnostic
	updateNative(t, f, "live_operations", "gc", map[string]any{"receipt_rows": int64(2), "receipt_variable_bytes": total})
	// ACK refunds zero, including native diagnostic rows.
	updateNative(t, f, "live_operations", "gc", map[string]any{"acknowledged_command_sequence": int64(2)})
	var beforeBytes int64
	if err := f.c.QueryRowContext(t.Context(), "SELECT receipt_variable_bytes FROM live_operations WHERE request_id='gc'").Scan(&beforeBytes); err != nil || beforeBytes != total {
		t.Fatal(beforeBytes, err)
	}
	// A failed result/ledger publication rolls the preceding child deletes back.
	f.exec(t, "BEGIN")
	f.exec(t, "DELETE FROM live_operation_receipt_diagnostics WHERE request_id='gc' AND command_sequence=1")
	f.exec(t, "DELETE FROM live_operation_command_receipts WHERE request_id='gc' AND command_sequence=1")
	f.reject(t, "UPDATE live_operations SET receipt_rows=0 WHERE request_id='gc'", "CHECK")
	f.exec(t, "ROLLBACK")
	var count int
	if err := f.c.QueryRowContext(t.Context(), "SELECT count(*) FROM live_operation_command_receipts WHERE request_id='gc'").Scan(&count); err != nil || count != 2 {
		t.Fatal(count, err)
	}
	// One bounded cohort: header plus its one diagnostic. Slot is outside history.
	f.exec(t, "BEGIN")
	f.exec(t, "DELETE FROM live_operation_receipt_diagnostics WHERE request_id='gc' AND command_sequence=1")
	f.exec(t, "DELETE FROM live_operation_command_receipts WHERE request_id='gc' AND command_sequence=1")
	slot := gcSlot(3, 2, 0, 1, first+diagnostic)
	slot["receipt_rows"] = int64(1)
	slot["receipt_variable_bytes"] = second
	updateNative(t, f, "live_operations", "gc", slot)
	f.exec(t, "COMMIT")
	// No Applied decoding is attempted after ACK; inspect retained physical facts.
	updateNative(t, f, "live_operations", "gc", map[string]any{"acknowledged_command_sequence": int64(3)})
	f.exec(t, "BEGIN")
	f.exec(t, "DELETE FROM live_operation_command_receipts WHERE request_id='gc' AND command_sequence=2")
	slot = gcSlot(4, 3, 1, 2, second)
	slot["receipt_rows"] = int64(0)
	slot["receipt_variable_bytes"] = int64(0)
	updateNative(t, f, "live_operations", "gc", slot)
	f.exec(t, "COMMIT")
	updateNative(t, f, "live_operations", "gc", map[string]any{"acknowledged_command_sequence": int64(4)})
	var rows, varbytes, control int64
	var due any
	if err := f.c.QueryRowContext(t.Context(), "SELECT receipt_rows,receipt_variable_bytes,control_variable_bytes,receipt_reclaim_due_ms FROM live_operations WHERE request_id='gc'").Scan(&rows, &varbytes, &control, &due); err != nil || rows != 0 || varbytes != 0 || control <= 0 || due != nil {
		t.Fatal(rows, varbytes, control, due, err)
	}
	// Retained floor is this actual owner's longer lifetime, not a new worker.
	floor := map[string]any{"request_id": "gc", "source_id": "source-a", "final_sequence": int64(4), "final_result_digest": slot["reclaim_result_digest"]}
	for k, v := range slot {
		floor[k] = v
	}
	floor["acknowledged_command_sequence"] = int64(4)
	f.exec(t, "BEGIN")
	if err := f.insert(t, "live_operation_retirements", floor); err != nil {
		t.Fatal(err)
	}
	f.exec(t, "DELETE FROM live_operations WHERE request_id='gc'")
	f.exec(t, "COMMIT")
	var floorControl int64
	if err := f.c.QueryRowContext(t.Context(), "SELECT control_variable_bytes FROM live_operation_retirements WHERE request_id='gc'").Scan(&floorControl); err != nil || floorControl <= 0 {
		t.Fatal(floorControl, err)
	}
	t.Logf("transfer receipt refund=0; OLD-control=%d NEW-floor=%d actual-control-delta=%d", control, floorControl, floorControl-control)
	// Truthful empty floor quantum retains exactly one result slot, no child.
	empty := gcSlot(5, 4, 2, 0, 0)
	updateNative(t, f, "live_operation_retirements", "gc", empty)
	updateNative(t, f, "live_operation_retirements", "gc", map[string]any{"acknowledged_command_sequence": int64(5)})
	if err := f.c.QueryRowContext(t.Context(), "SELECT count(*) FROM live_operation_command_receipts WHERE request_id='gc'").Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	f.reject(t, "UPDATE live_operation_retirements SET reclaim_removed_count=2 WHERE request_id='gc'", "receipt_slot")
	f.reject(t, "UPDATE live_operation_retirements SET reclaim_command_kind='lc_receipt_reclaim' WHERE request_id='gc'", "receipt_slot")
	f.reject(t, "UPDATE live_operation_retirements SET receipt_reclaim_after_sequence=3 WHERE request_id='gc'", "receipt_slot")
	t.Log("last header drain and ACK create no child or due work; floor/slot remains charged")
}
func TestINTGroupGFixedSlotMutant011(t *testing.T) {
	for _, mutant := range []bool{false, true} {
		ddl := candidate
		if mutant {
			ddl = stripConstraint(t, "live_operations_receipt_slot")
		}
		f := native(t, ddl)
		f.guide(t, "mutant-generation", "source-a")
		row := map[string]any{"request_id": "gc", "source_id": "source-a", "generation_id": "mutant-generation", "expected_revision": int64(0), "expected_generation": "", "status": "staging", "last_command_sequence": int64(1), "acknowledged_command_sequence": int64(1)}
		if err := f.insert(t, "live_operations", row); err != nil {
			t.Fatal(err)
		}
		values := gcSlot(2, 1, 0, 1, 100)
		values["reclaim_removed_count"] = int64(2)
		names := make([]string, 0, len(values))
		for k := range values {
			names = append(names, k)
		}
		slices.Sort(names)
		sets := make([]string, len(names))
		args := make([]any, 0, len(names))
		for i, n := range names {
			sets[i] = n + "=?"
			args = append(args, values[n])
		}
		_, err := f.c.ExecContext(t.Context(), "UPDATE live_operations SET "+strings.Join(sets, ",")+" WHERE request_id='gc'", args...)
		if (err != nil) == mutant {
			t.Fatal("fixed slot mutant not distinguished", mutant, err)
		}
		t.Log(fmt.Sprintf("M3 source_mutated=%v multi-header-cohort-rejected=%v", mutant, err != nil))
	}
}

func TestINTGroupGReclaimDuePaths011(t *testing.T) {
	f := native(t, candidate)
	for _, tc := range []struct{ table, key string }{{"live_operations", "request_id"}, {"lc_generations", "id"}, {"dvr_recordings", "id"}, {"live_operation_retirements", "request_id"}, {"lc_generation_retirements", "generation_id"}, {"dvr_capture_retirements", "recording_id"}} {
		q := "EXPLAIN QUERY PLAN SELECT " + tc.key + " FROM " + tc.table + " INDEXED BY " + tc.table + "_receipt_due WHERE receipt_reclaim_due_ms IS NOT NULL AND receipt_reclaim_due_ms<=? ORDER BY receipt_reclaim_due_ms," + tc.key + " LIMIT 1"
		rows, err := f.c.QueryContext(t.Context(), q, int64(1000))
		if err != nil {
			t.Fatal(err)
		}
		var details []string
		for rows.Next() {
			var a, b, c int
			var d string
			if err := rows.Scan(&a, &b, &c, &d); err != nil {
				t.Fatal(err)
			}
			details = append(details, d)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		if len(details) != 1 || !strings.HasPrefix(details[0], "SEARCH "+tc.table+" USING") || !strings.Contains(details[0], tc.table+"_receipt_due") || strings.Contains(details[0], "TEMP B-TREE") {
			t.Fatal(tc, details)
		}
		t.Log(tc.table, details)
	}
}
