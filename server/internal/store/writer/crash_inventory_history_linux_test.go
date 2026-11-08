package writer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// These are historical SQL-fact populations, NOT fresh Inventory acceptance or
// source/lease/profile qualification. Only the target owner's ACK floor may
// change. Every column/row of these native tables is checkpointed independently.
var inventoryHistoryStateTables = []string{
	"inventory_runs", "jobs", "library_sources", "catalog_libraries", "libraries",
	"catalog_subjects", "catalog_entities", "catalog_assets", "catalog_asset_links", "inventory_objects",
	"inventory_directories", "inventory_strict_intents", "scan_command_receipts", "scan_command_results",
	"search_revision", "search_generations", "search_publish_jobs", "search_publications", "search_fragments",
	"catalog_scalar_contributions", "catalog_asset_contributions", "catalog_age_item_counts",
}

// Type tags retain SQL integer/REAL/TEXT/BLOB/NULL distinctions; decimal integer
// strings preserve nanoseconds beyond JSON's float precision. Lengths/escaping
// come from JSON arrays, not ambiguous concatenation. No production codec runs.
func inventoryHistoryCell(v any) (string, error) {
	switch v := v.(type) {
	case nil:
		return "n", nil
	case int64:
		return "i" + strconv.FormatInt(v, 10), nil
	case float64:
		if math.IsInf(v, 0) || math.IsNaN(v) {
			return "", errors.New("unsupported nonfinite native value")
		}
		return "r" + strconv.FormatFloat(v, 'g', -1, 64), nil
	case string:
		return "t" + v, nil
	case []byte:
		return "b" + hex.EncodeToString(v), nil
	default:
		return "", fmt.Errorf("unsupported history cell %T", v)
	}
}

func readInventoryHistoryState(ctx context.Context, reader inventoryCrashReader, q ScanReceiptRequest, floor int64) (map[string][32]byte, error) {
	out := map[string][32]byte{}
	ownerFound := false
	for _, name := range inventoryHistoryStateTables {
		rows, err := reader.QueryContext(ctx, "SELECT * FROM "+name)
		if err != nil {
			return nil, err
		}
		columns, err := rows.Columns()
		if err != nil {
			rows.Close()
			return nil, err
		}
		var encoded []string
		for rows.Next() {
			values, dest := make([]any, len(columns)), make([]any, len(columns))
			for i := range dest {
				dest[i] = &values[i]
			}
			if err = rows.Scan(dest...); err != nil {
				break
			}
			target := false
			if name == "inventory_runs" {
				for i, column := range columns {
					if column == "job_id" {
						target = values[i] == q.RunID
					}
				}
			}
			cells := make([]string, len(values))
			for i, value := range values {
				if target && columns[i] == "acknowledged_command_sequence" {
					if value != floor {
						err = fmt.Errorf("history ACK floor got %v, want %d", value, floor)
						break
					}
					ownerFound, value = true, int64(0) // ONLY this independently asserted transition is normalized.
				}
				cells[i], err = inventoryHistoryCell(value)
				if err != nil {
					break
				}
			}
			if err != nil {
				break
			}
			row, marshalErr := json.Marshal(cells)
			if marshalErr != nil {
				err = marshalErr
				break
			}
			encoded = append(encoded, string(row))
		}
		err = errors.Join(err, rows.Err(), rows.Close())
		if err != nil {
			return nil, err
		}
		sort.Strings(encoded) // A multiset checkpoint, not an accidental native row order.
		data, err := json.Marshal(struct{ Columns, Rows []string }{columns, encoded})
		if err != nil {
			return nil, err
		}
		out[name] = sha256.Sum256(data)
	}
	if !ownerFound {
		return nil, errors.New("history checkpoint lost original owner")
	}
	return out, nil
}

func compareInventoryHistoryState(want, got map[string][32]byte) error {
	if len(want) != len(inventoryHistoryStateTables) || len(got) != len(want) {
		return errors.New("unsupported history table inventory")
	}
	for _, name := range inventoryHistoryStateTables {
		before, exists := want[name]
		after, present := got[name]
		if !exists || !present || before != after {
			return fmt.Errorf("history native %s checkpoint changed", name)
		}
	}
	return nil
}

func inventoryHistoryStepIDs(operation string) []stmt.ID {
	ids := []stmt.ID{scanReceiptOwner, inventoryDisclosureJob, inventoryDisclosureLibrary, scanReceiptOwner, scanReceiptIdentityPrefix}
	switch operation {
	case "resolve":
		return append(ids, scanReceiptByteLengths, scanMemberKeys, scanMemberByteLengths, scanReceiptLookup, scanReceiptResults)
	case "ack":
		return append(ids, scanReceiptByteLengths, scanMemberKeys, scanMemberByteLengths, scanReceiptLookup, scanReceiptResults, scanReceiptAck)
	case "resolve_retired", "ack_retired":
		return ids // Floor precedes cohort decode and an idempotent ACK writes nothing.
	default:
		return nil
	}
}

func inventoryHistoryPhases(operation string) []string {
	var out []string
	for i, id := range inventoryHistoryStepIDs(operation) {
		out = append(out, fmt.Sprintf("native_%02d_%s", i, id))
	}
	return out
}

func inventoryHistoryFloor(c crashCase) (int64, error) {
	valid := c.Domain == "inventory_native" && len(inventoryHistoryStepIDs(c.Operation)) != 0
	if c.CommitFault != "" {
		valid = valid && c.Boundary == "uncertain_return" && (c.CommitFault == "before" || c.CommitFault == "after")
	} else {
		found := false
		for _, phase := range append([]string{"before_begin", "after_begin", "before_commit", "after_commit", "physical_return", "api_return"}, inventoryHistoryPhases(c.Operation)...) {
			found = found || c.Boundary == phase
		}
		valid = valid && found
	}
	if !valid {
		return 0, fmt.Errorf("unsupported inventory history cell %+v", c)
	}
	if strings.HasSuffix(c.Operation, "_retired") || c.Operation == "ack" && crashCommitted(c) {
		return 1, nil
	}
	return 0, nil
}

func checkInventoryHistoryReturn(t *testing.T, w *Writer, e *executor, q ScanReceiptRequest, journal inventoryCrashJournal, floor int64, operation string, result InventoryResult, ack AckResult) {
	t.Helper()
	evidence := result.Outcome.Evidence
	if strings.HasPrefix(operation, "ack") {
		evidence = ack.Evidence
	}
	if evidence == nil || evidence.Purpose != InventoryAccept || evidence.Class != governor.ScanMinimum || evidence.Durability != Normal || evidence.Commit != CommitSucceeded ||
		evidence.PhysicalReturnedAt == nil || evidence.Policy == nil || evidence.WALBefore == nil || evidence.WALAfter == nil ||
		w.effective.authority.Usage() != ([4]governor.Amounts{}) || e.db.Stats().InUse != 0 {
		t.Fatal("missing real history frame/custody evidence", evidence)
	}
	if strings.HasPrefix(operation, "ack") {
		ack.Evidence = nil
		if !reflect.DeepEqual(ack, AckResult{AcknowledgedThrough: 1}) || !reflect.DeepEqual(result, InventoryResult{}) {
			t.Fatal(result, ack)
		}
	} else {
		want, err := inventoryCrashProjection(journal, floor == 1)
		if err != nil {
			t.Fatal(err)
		}
		if err := compareInventoryCrashProjection(want, result); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(ack, AckResult{}) {
			t.Fatal(ack)
		}
	}
	var actual int64
	if err := e.db.QueryRowContext(t.Context(), "SELECT acknowledged_command_sequence FROM inventory_runs WHERE job_id=?", q.RunID).Scan(&actual); err != nil || actual != floor {
		t.Fatal(actual, floor, err)
	}
}

func crashInventoryNativeFixture(t *testing.T, witness *crashWitness) (*Writer, *executor, ScanReceiptRequest, inventoryCrashJournal, []string) {
	t.Helper()
	if len(inventoryHistoryStepIDs(witness.Case.Operation)) == 0 {
		t.Fatal("unsupported history operation", witness.Case)
	}
	w, requests, e, _ := inventoryFacadeFixture(t)
	e.now = func() time.Time { return crashClock }
	w = bindCrashInventory(t, e, w.effective.path, *w.effective.inventoryPlan)
	q := requests["hit"]
	cap := inventoryTestDisclosure(t, w, q)
	witness.Path, witness.Reference = w.effective.path, q.Receipt
	journal, err := readInventoryCrashJournal(t.Context(), e.db, q.RunID)
	if err != nil {
		t.Fatal(err)
	}
	witness.HistoryState, err = readInventoryHistoryState(t.Context(), e.db, q, 0)
	if err != nil {
		t.Fatal(err)
	}
	floor := int64(0)
	if strings.HasSuffix(witness.Case.Operation, "_retired") {
		ack, err := w.AckScan(t.Context(), cap, q)
		if err != nil {
			t.Fatal(err)
		}
		checkInventoryHistoryReturn(t, w, e, q, journal, 1, "ack", InventoryResult{}, ack)
		floor = 1
	}
	prepared, err := readInventoryHistoryState(t.Context(), e.db, q, floor)
	if err != nil {
		t.Fatal(err)
	}
	if err := compareInventoryHistoryState(witness.HistoryState, prepared); err != nil {
		t.Fatal("preparatory ACK changed original state", err)
	}
	defs := inventoryHistoryDefinitions()
	ids := map[string]stmt.ID{}
	for _, d := range defs {
		ids[strings.TrimSpace(d.SQL)] = d.ID
	}
	var phases []string
	installCrashDriverAt(t, e, witness.Path, witness, crashClock, func(query string) {
		if id, ok := ids[strings.TrimSpace(query)]; ok {
			phase := fmt.Sprintf("native_%02d_%s", len(phases), id)
			phases = append(phases, phase)
			crashPoint(t, witness, phase)
		}
	})
	var result InventoryResult
	var ack AckResult
	if strings.HasPrefix(witness.Case.Operation, "ack") {
		ack, err = w.AckScan(t.Context(), cap, q)
	} else {
		result, err = w.ResolveScan(t.Context(), cap, q)
	}
	if witness.Case.CommitFault != "" {
		if !errors.Is(err, ErrResolveRequired) || !errors.Is(err, errCrashCommitReply) || !reflect.DeepEqual(result, InventoryResult{}) || !reflect.DeepEqual(ack, AckResult{}) || !e.ambiguous || !w.effective.fenced {
			t.Fatal("history unknown COMMIT escaped quarantine", result, ack, err)
		}
		witness.Unknown = true
		crashPoint(t, witness, "uncertain_return")
	} else {
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(witness.Case.Operation, "ack") {
			floor = 1
		}
		checkInventoryHistoryReturn(t, w, e, q, journal, floor, witness.Case.Operation, result, ack)
	}
	after, err := readInventoryHistoryState(t.Context(), e.db, q, floor)
	if err != nil {
		t.Fatal(err)
	}
	if err := compareInventoryHistoryState(witness.HistoryState, after); err != nil {
		t.Fatal(err)
	}
	saved, err := readInventoryCrashJournal(t.Context(), e.db, q.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if err := compareInventoryCrashJournal(journal, saved); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(phases, inventoryHistoryPhases(witness.Case.Operation)) {
		t.Fatal("history native sequence changed", phases)
	}
	crashPoint(t, witness, "api_return")
	if witness.Case.Boundary != "trace" {
		t.Fatal("history boundary never reached", witness.Case)
	}
	return w, e, q, journal, phases
}

func verifyInventoryNativeCrash(t *testing.T, profile effectivePlan, q ScanReceiptRequest, original inventoryCrashJournal, witness crashWitness) {
	t.Helper()
	floor, err := inventoryHistoryFloor(witness.Case)
	if err != nil {
		t.Fatal(err)
	}
	if witness.Reference != q.Receipt || witness.Unknown != (witness.Case.CommitFault != "") {
		t.Fatal("history original identity/outcome changed", witness)
	}
	e := reopenCrashExecutor(t, witness.Path)
	check := func(floor int64) {
		t.Helper()
		got, err := readInventoryHistoryState(t.Context(), e.db, q, floor)
		if err != nil {
			t.Fatal(err)
		}
		if err := compareInventoryHistoryState(witness.HistoryState, got); err != nil {
			t.Fatal(err)
		}
		journal, err := readInventoryCrashJournal(t.Context(), e.db, q.RunID)
		if err != nil {
			t.Fatal(err)
		}
		if err := compareInventoryCrashJournal(original, journal); err != nil {
			t.Fatal(err)
		}
	}
	check(floor)
	w := bindCrashInventory(t, e, witness.Path, profile)
	cap := inventoryTestDisclosure(t, w, q)
	for range 2 {
		result, err := w.ResolveScan(t.Context(), cap, q)
		if err != nil {
			t.Fatal(err)
		}
		checkInventoryHistoryReturn(t, w, e, q, original, floor, "resolve", result, AckResult{})
	}
	for range 2 {
		ack, err := w.AckScan(t.Context(), cap, q)
		if err != nil {
			t.Fatal(err)
		}
		checkInventoryHistoryReturn(t, w, e, q, original, 1, "ack", InventoryResult{}, ack)
	}
	check(1)
	result, err := w.ResolveScan(t.Context(), cap, q)
	if err != nil {
		t.Fatal(err)
	}
	checkInventoryHistoryReturn(t, w, e, q, original, 1, "resolve_retired", result, AckResult{})
	cap.scope.revoked.Store(true)
	denied, err := w.ResolveScan(t.Context(), cap, q)
	if !errors.Is(err, ErrDisclosureDenied) || !reflect.DeepEqual(denied, InventoryResult{}) {
		t.Fatal("retired history bypassed current disclosure", denied, err)
	}
	check(1)
}

func TestCrashReplayInventoryNativeHistory(t *testing.T) {
	t.Helper()
	if raceDetector {
		// This sequential child matrix exceeds one shard's race budget. The
		// same cells run without -race; a skipped opt-in target is not evidence.
		t.Skip("crash replay is covered without -race; race child matrix exceeds the shard budget")
	}

	for _, operation := range []string{"resolve", "ack", "resolve_retired", "ack_retired"} {
		t.Run(operation, func(t *testing.T) {
			witness := crashWitness{Case: crashCase{"inventory_native", operation, "trace", ""}}
			w, e, q, original, phases := crashInventoryNativeFixture(t, &witness)
			profile := *w.effective.inventoryPlan
			if err := e.close(); err != nil {
				t.Fatal(err)
			}
			if e.db.Stats().OpenConnections != 0 || w.effective.authority.Usage() != ([4]governor.Amounts{}) {
				t.Fatal("history prototype did not join")
			}
			for _, boundary := range append(append([]string{"before_begin", "after_begin"}, phases...), "before_commit", "after_commit", "physical_return", "api_return") {
				t.Run(boundary, func(t *testing.T) {
					verifyInventoryNativeCrash(t, profile, q, original, runCrashChild(t, crashCase{"inventory_native", operation, boundary, ""}))
				})
			}
			for _, fault := range []string{"before", "after"} {
				t.Run("uncertain_return/"+fault, func(t *testing.T) {
					verifyInventoryNativeCrash(t, profile, q, original, runCrashChild(t, crashCase{"inventory_native", operation, "uncertain_return", fault}))
				})
			}
		})
	}
}

func TestCrashInventoryHistoryRejectsNativeCorruption(t *testing.T) {
	witness := crashWitness{Case: crashCase{"inventory_native", "resolve", "trace", ""}}
	w, e, q, original, _ := crashInventoryNativeFixture(t, &witness)
	profile := *w.effective.inventoryPlan
	if err := e.close(); err != nil {
		t.Fatal(err)
	}
	e = reopenCrashExecutor(t, witness.Path)
	w = bindCrashInventory(t, e, witness.Path, profile)
	cap := inventoryTestDisclosure(t, w, q)
	for _, retired := range []bool{false, true} {
		floor := int64(0)
		if retired {
			ack, err := w.AckScan(t.Context(), cap, q)
			if err != nil {
				t.Fatal(err)
			}
			checkInventoryHistoryReturn(t, w, e, q, original, 1, "ack", InventoryResult{}, ack)
			floor = 1
		}
		out, err := w.ResolveScan(t.Context(), cap, q)
		if err != nil {
			t.Fatal(err)
		}
		checkInventoryHistoryReturn(t, w, e, q, original, floor, "resolve", out, AckResult{})
		conn, err := e.db.Conn(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer conn.Close()
			policy := e.config
			policy.Synchronous = sqliteconn.Normal // asserted after the actual Resolve frame
			if _, err := sqliteconn.Assert(t.Context(), conn, policy); err != nil {
				t.Fatal(err)
			}
			before, err := readInventoryHistoryState(t.Context(), conn, q, floor)
			if err != nil {
				t.Fatal(err)
			}
			if err := compareInventoryHistoryState(witness.HistoryState, before); err != nil {
				t.Fatal(err)
			}
			for _, mutation := range []struct{ name, sql string }{
				{"owner_revision", `UPDATE inventory_runs SET job_revision=job_revision+1 WHERE job_id='scan-hit'`},
				{"owner_lease", `UPDATE inventory_runs SET job_lease_generation=job_lease_generation+1 WHERE job_id='scan-hit'`},
				{"wrong_floor", fmt.Sprintf(`UPDATE inventory_runs SET acknowledged_command_sequence=%d WHERE job_id='scan-hit'`, 1-floor)},
				{"other_owner_floor", `UPDATE inventory_runs SET acknowledged_command_sequence=1 WHERE job_id='scan-replay'`},
				{"receipt_identity", `UPDATE scan_command_receipts SET operation_id=zeroblob(16) WHERE run_id='scan-hit'`},
				{"receipt_digest", `UPDATE scan_command_receipts SET result_digest=zeroblob(32) WHERE run_id='scan-hit'`},
				{"receipt_carrier", `UPDATE scan_command_receipts SET change_id=1 WHERE run_id='scan-hit'`},
				{"other_receipt", `UPDATE scan_command_receipts SET source_revision='WRONG-OTHER-RECEIPT' WHERE run_id='scan-replay'`},
				{"child", `UPDATE scan_command_results SET object_id='WRONG-CHILD' WHERE run_id='scan-hit'`},
				{"other_child", `DELETE FROM scan_command_results WHERE run_id='scan-replay'`},
				{"asset_epoch", `UPDATE catalog_assets SET info_epoch=info_epoch+1 WHERE id=41`},
				{"source_incarnation", `UPDATE library_sources SET incarnation='WRONG-CURRENT-SOURCE' WHERE id='scan-source-hit'`},
			} {
				t.Run(fmt.Sprintf("retired_%t/%s", retired, mutation.name), func(t *testing.T) {
					if _, err := conn.ExecContext(t.Context(), "SAVEPOINT corrupt_history"); err != nil {
						t.Fatal(err)
					}
					defer func() {
						if _, err := conn.ExecContext(t.Context(), "ROLLBACK TO corrupt_history"); err != nil {
							t.Error(err)
						}
						if _, err := conn.ExecContext(t.Context(), "RELEASE corrupt_history"); err != nil {
							t.Error(err)
						}
					}()
					result, err := conn.ExecContext(t.Context(), mutation.sql)
					if err != nil {
						t.Fatal("corruption failed before oracle", err)
					}
					if n, err := result.RowsAffected(); err != nil || n != 1 {
						t.Fatal("corruption did not change exactly one native row", n, err)
					}
					got, err := readInventoryHistoryState(t.Context(), conn, q, floor)
					if err == nil && compareInventoryHistoryState(witness.HistoryState, got) == nil {
						t.Fatal("history checkpoint accepted native corruption")
					}
				})
			}
		}()
	}
	for _, bad := range []crashCase{
		{"inventory_native", "future_accept", "api_return", ""}, {"inventory_native", "ack", "inside_fsync", ""},
		{"inventory_native", "ack", "after_commit", "before"}, {"inventory_native", "ack", "uncertain_return", "guess"},
	} {
		if _, err := inventoryHistoryFloor(bad); err == nil {
			t.Fatal("unsupported journal became a passing expectation", bad)
		}
	}
	seen := map[string]bool{}
	for _, value := range []any{nil, int64(1), float64(1), int64(9007199254740992), int64(9007199254740993), "9007199254740993", []byte("9007199254740993"), "", []byte{}} {
		encoded, err := inventoryHistoryCell(value)
		if err != nil || seen[encoded] {
			t.Fatal("SQL type/precision identity lost", encoded, err)
		}
		seen[encoded] = true
	}
}
