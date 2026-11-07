package writer

import (
	"database/sql"
	"errors"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func TestN1RecordWorkIsReservedOnEveryRewrite(t *testing.T) {
	work := Work{16, 2000, 16, 65536, time.Microsecond}
	p := effectivePlan{inventoryPageSize: 4096, inventoryRecords: inventoryRecordEnvelope{32 << 10, work}}
	for _, id := range []stmt.ID{scanFreshRunCAS, scanFreshJobCAS, scanFreshDirectoryCAS, scanFreshLibraryCAS, scanFreshProgressCAS} {
		p.steps = append(p.steps, statementStep{id, work})
	}
	if err := validateInventoryRecordEnvelope(p); err != nil {
		t.Fatal(err)
	}
	for i := range p.steps {
		p.steps[i].work.Pages--
		if err := validateInventoryRecordEnvelope(p); !errors.Is(err, ErrQuantum) {
			t.Fatal("another unit lent pages to a native rewrite", p.steps[i].id, err)
		}
		p.steps[i].work.Pages++
	}
}

func TestN1RecordStorageConsumesAssertedGeometry(t *testing.T) {
	for _, size := range []int32{512, 4096, 65536} {
		work, err := inventoryRecordStorageWork(size, 64<<10)
		if err != nil {
			t.Fatal(size, err)
		}
		usable := uint64(size) - 255
		pages := uint64(1) + (64<<10)/(usable-4)
		if (64<<10)%(usable-4) != 0 {
			pages++
		}
		if work.Pages != pages || work.WALBytes != 32+pages*(uint64(size)+24) {
			t.Fatal(size, work, pages)
		}
	}
	if work, err := inventoryRecordStorageWork(4096, math.MaxUint64); err == nil || work != (Work{}) {
		t.Fatal("overflow became a storage allowance", work, err)
	}
	if work, err := inventoryRecordStorageWork(0, 1); err == nil || work != (Work{}) {
		t.Fatal("unobserved geometry sized a record", work, err)
	}
}

func TestN1ExistingRecordByteArithmetic(t *testing.T) {
	got, err := inventoryRecordActualBytes([]string{"run", "."}, scanByteVector{exists: true, total: 19})
	if err != nil || got != inventoryRecordFixedBytes+23 {
		t.Fatal(got, err)
	}
	for _, vectors := range [][]scanByteVector{{{}}, {{exists: true, total: math.MaxUint64}}} {
		if got, err := inventoryRecordActualBytes(nil, vectors...); err == nil || got != 0 {
			t.Fatal("invalid vector produced a record allowance", got, err)
		}
	}
	// A preserved native field is charged for a fresh rewrite, without changing
	// the existing history reader's twelve-field payload-copy budget.
	run := scanByteVector{exists: true, count: 17, total: 1003}
	run.cells[0] = sql.NullInt64{Int64: 3, Valid: true}
	run.cells[15] = sql.NullInt64{Int64: 1000, Valid: true}
	if copied := run.runCopiedPayload(); copied.total != 3 || copied.count != 12 || !copied.exists || run.total != 1003 {
		t.Fatal("native rewrite bound changed history copy budget", copied, run)
	}
}

func TestN1ExistingRecordSchemaCoverage(t *testing.T) {
	d, _ := n1ComposedDriverFixture(t)
	// Every variable-width physical column must be covered by a native length
	// or an already frozen exact PK. Integer/generated scalars and headers fit
	// the fixed term; this fails on schema additions instead of assuming zero.
	for table, want := range map[string][]string{
		"library_sources":       {"id", "library_id", "kind", "root", "incarnation", "root_identity", "health", "classification", "name", "configured_root", "next_scan_at", "last_complete_job", "last_complete_at", "last_integrity_at", "remote_source_id", "double_check_job", "progress_last_progress_at"},
		"libraries":             {"id", "name", "kind", "root", "preference_local_tags", "preference_lyrics_source", "preference_title_source", "preference_episode_numbering", "playback_origin_incarnation", "metadata_language"},
		"inventory_runs":        {"job_id", "original_library_key", "original_resolved_root", "source_id", "root_incarnation", "root_identity", "phase", "finished_at", "scan_mode", "create_operation_id", "create_request_digest", "command_incarnation", "reconcile_cursor", "verify_cursor", "last_turn", "pause_reason", "inventory_completed_at"},
		"jobs":                  {"id", "library_id", "status", "created_at", "error"},
		"inventory_directories": {"job_id", "relative_path", "identity", "revision", "cursor", "state"},
	} {
		rows, err := d.opening.executor.db.QueryContext(t.Context(), "PRAGMA main.table_xinfo("+table+")")
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		count := 0
		for rows.Next() {
			var cid, notNull, pk, hidden int
			var name, kind string
			var defaultValue any
			if err := rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &pk, &hidden); err != nil {
				t.Fatal(err)
			}
			count++
			switch kind {
			case "TEXT", "BLOB":
				names = append(names, name)
			case "INTEGER", "REAL":
			default:
				t.Fatal("unbounded scalar type", table, name, kind)
			}
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			t.Fatal(err)
		}
		slices.Sort(names)
		slices.Sort(want)
		if count > 64 || !slices.Equal(names, want) {
			t.Fatal("record formula no longer covers schema", table, count, names, want)
		}
	}
}

func TestN1ExistingRecordCapacityBeforeEffects(t *testing.T) {
	for _, field := range []string{"library_name", "run_pause_reason"} {
		t.Run(field, func(t *testing.T) {
			d, preparation, command, original := n1ComposedDriverPrepared(t)
			query := `UPDATE libraries SET name=? WHERE id='lifecycle-library'`
			if field == "run_pause_reason" {
				query = `UPDATE inventory_runs SET pause_reason=? WHERE job_id=?`
			}
			args := []any{strings.Repeat("x", 128<<10)}
			if field == "run_pause_reason" {
				args = append(args, original.RunID)
			}
			if _, err := d.opening.executor.db.ExecContext(t.Context(), query, args...); err != nil {
				t.Fatal(err)
			}
			got, err := preparation.Accept(t.Context(), command)
			var refusal *Refusal
			if !errors.As(err, &refusal) || refusal.Code != RefusalCapacity || !reflect.DeepEqual(got, InventoryResult{}) {
				t.Fatal("uncovered old row reached effects", got, err)
			}
			var assets, receipts int
			if err := d.opening.executor.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM catalog_assets),(SELECT count(*) FROM scan_command_receipts WHERE run_id=? AND command_kind='inventory_accept')`, original.RunID).Scan(&assets, &receipts); err != nil || assets != 0 || receipts != 0 {
				t.Fatal("capacity refusal left native effects", assets, receipts, err)
			}
		})
	}
}
