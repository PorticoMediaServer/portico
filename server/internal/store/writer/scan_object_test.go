package writer

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

// The immutable seed below is a native SQL-fact fixture, not adapter evidence,
// a valid original command/result manifest or source admission. In particular
// its opaque revision is not a claim of producer JSON/revision agreement.
func seedScanObject(t *testing.T, w *executor) inventoryObjectFacts {
	t.Helper()
	for _, sql := range []string{
		`INSERT INTO libraries(id,name,kind,root) VALUES('scan-object-library','Movies','movie','/media')`,
		`INSERT INTO catalog_libraries(id,library_id,root) VALUES(7,'scan-object-library','/media')`,
		`INSERT INTO library_sources(id,library_id,configured_root,root,incarnation,root_identity)
VALUES('scan-object-source','scan-object-library','/media','/media','source-incarnation','root')`,
		`INSERT INTO jobs(id,library_id,status,created_at)
VALUES('scan-object-run','scan-object-library','running','2026-11-05T00:00:00Z')`,
		`INSERT INTO catalog_assets(id,token,library_id,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration)
VALUES(41,'scan-object-asset',7,'/media/Arrival.mkv',42,-11,'','','',0,0,0)`,
	} {
		if _, err := w.db.ExecContext(t.Context(), sql); err != nil {
			t.Fatal(err)
		}
	}
	return inventoryObjectFacts{
		objectID: "scan-object-new", sourceID: "scan-object-source", assetToken: "scan-object-asset", incarnation: "source-incarnation",
		relativePath: "Arrival.mkv", objectIdentity: "ino:31", revision: "opaque:observation:019",
		evidenceJSON: `{"kind":"local","scope":"scan-object-source:source-incarnation","object":"ino:31","size":42,"modifiedNs":-11,"changeToken":"change:29"}`,
		size:         42, modifiedNS: -11, runID: "scan-object-run", directoryPath: ".",
	}
}

func scanObjectTestTurn(t *testing.T, w *executor, definitions []stmt.Definition) *commandTurn {
	t.Helper()
	tx, err := w.begin(t.Context(), InventoryAccept, testRecipe(), &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = tx.rollback(nil) })
	config := w.config
	config.Synchronous = sqliteconn.Normal
	registry := cachedNativeFixtureRegistry(t, tx.conn, config, definitions, "scan_object_test.go")
	return scanObjectCommandTurn(t, tx, registry)
}

// RETURNING literals, ignored/two-row INSERTs deliberately violate the static
// product grammar. Keep those decoder faults separate from native receipts.
func scanObjectFaultTurn(t *testing.T, w *executor, definitions []stmt.Definition) *commandTurn {
	t.Helper()
	registry, err := stmttest.New(t, definitions)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := w.begin(t.Context(), InventoryAccept, testRecipe(), &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = tx.rollback(nil) })
	return scanObjectCommandTurn(t, tx, registry)
}

func scanObjectCommandTurn(t *testing.T, tx *transaction, registry *stmt.Registry) *commandTurn {
	t.Helper()
	var steps []statementStep
	for _, entry := range registry.Manifest() {
		// Synthetic component budget; not production Inventory admission/HOLD/WAL.
		steps = append(steps, statementStep{entry.ID, Work{4, 100, 4, 4096, time.Millisecond}})
	}
	turn, err := newCommandTurn(tx, registry, steps)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = turn.closeRows() })
	return turn
}

func scanObjectAssertAbsent(t *testing.T, w *executor) {
	t.Helper()
	var count, processed, receipts int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT
(SELECT count(*) FROM inventory_objects),
(SELECT processed FROM jobs WHERE id='scan-object-run'),
(SELECT count(*) FROM scan_command_receipts)`).Scan(&count, &processed, &receipts); err != nil {
		t.Fatal(err)
	}
	if count != 0 || processed != 0 || receipts != 0 {
		t.Fatalf("partial fixture escaped rollback: objects=%d processed=%d receipts=%d", count, processed, receipts)
	}
}

func TestScanObjectClosedStatement(t *testing.T) {
	definitions := scanObjectDefinitions()
	if len(definitions) != 1 || definitions[0].ID != scanInventoryObject || !definitions[0].Plan.Hot || len(definitions[0].Plan.Waivers) != 0 {
		t.Fatal("object statement escaped the closed inventory")
	}
	d := definitions[0]
	if issues := plan.LintSQL(d.SQL); len(issues) != 0 {
		t.Fatalf("closed DML grammar: %v", issues)
	}
	if len(d.Params) != 16 || len(d.Cohorts) != 1 {
		t.Fatal("incomplete object binding inventory")
	}
	insert, _, ok := strings.Cut(d.SQL, "RETURNING ")
	if !ok || strings.Contains(insert, "retired") {
		t.Fatal("object INSERT must keep unconditional retired DEFAULT 0")
	}
	// Describe validates all declared cohort bindings without authorizing SQL.
	catalogue, err := stmt.Describe(definitions)
	if err != nil || len(catalogue.Manifest()) != 1 {
		t.Fatal(err)
	}
	// HN owns the separate fresh VM/INSERT-destination/FK capture. No golden
	// recording or old-capture reinterpretation is performed by this test.
}

func TestScanObjectNativeTokenEchoAndPending(t *testing.T) {
	w, _, _ := testExecutor(t)
	in := seedScanObject(t, w)
	for _, identity := range []string{"ino:31", "", "opaque:\x00identity"} {
		t.Run(identity, func(t *testing.T) {
			in.objectIdentity = identity // Prepared projections, not raw-normalization tests.
			turn := scanObjectTestTurn(t, w, scanObjectDefinitions())
			got, err := insertInventoryObject(t.Context(), turn, in)
			if err != nil || got != in {
				t.Fatalf("native fact projection: got=%+v err=%v", got, err)
			}
			var token, revision, analysis string
			var number, binds, pendingRevision int64
			if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT o.asset_id,a.id,o.revision,o.analysis_state,
o.has_analysis_probe_bindings,length(o.analysis_revision)
FROM inventory_objects o JOIN catalog_assets a ON a.token=o.asset_id WHERE o.id=?`, got.objectID).
				Scan(&token, &number, &revision, &analysis, &binds, &pendingRevision); err != nil {
				t.Fatal(err)
			}
			if token != "scan-object-asset" || number != 41 || revision != in.revision || analysis != "pending" || binds != 0 || pendingRevision != 0 {
				t.Fatal("numeric asset alias or fabricated Basic facts")
			}
			var indexedID string
			if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT id FROM inventory_objects
INDEXED BY inventory_objects_separate_folder
WHERE asset_id=? AND source_id=? AND root_incarnation=? AND directory_path=? AND retired=0
ORDER BY id LIMIT 1`, in.assetToken, in.sourceID, in.incarnation, in.directoryPath).Scan(&indexedID); err != nil || indexedID != got.objectID {
				t.Fatalf("default-unretired INSERT missing partial index entry: id=%q err=%v", indexedID, err)
			}
			if err := turn.closeRows(); err != nil {
				t.Fatal(err)
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}
			scanObjectAssertAbsent(t, w)
		})
	}
}

func TestScanObjectAllReturningFieldsRejectDrift(t *testing.T) {
	w, _, _ := testExecutor(t)
	in := seedScanObject(t, w)
	base := scanObjectDefinitions()[0]
	parts := strings.Split(base.SQL, "RETURNING ")
	columns := strings.Split(strings.ReplaceAll(parts[1], "\n", ""), ",")
	for i, column := range columns {
		t.Run(column, func(t *testing.T) {
			mutated := append([]string(nil), columns...)
			mutated[i] = "'wrong'"
			switch column {
			case "size", "modified_ns", "retired", "has_analysis_probe_bindings":
				mutated[i] = "999"
			}
			definition := base
			// Test-only RETURNING fault; actual INSERT stays unchanged. Not a
			// production registry extension, grammar waiver or HN capture.
			definition.SQL = parts[0] + "RETURNING " + strings.Join(mutated, ",")
			turn := scanObjectFaultTurn(t, w, []stmt.Definition{definition})
			if _, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE jobs SET processed=9 WHERE id='scan-object-run'`); err != nil {
				t.Fatal(err)
			}
			got, err := insertInventoryObject(t.Context(), turn, in)
			if !errors.Is(err, ErrInvalid) || got != (inventoryObjectFacts{}) {
				t.Fatalf("returned field drift escaped: got=%+v err=%v", got, err)
			}
			var changed int64
			if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT count(*) FROM inventory_objects`).Scan(&changed); err != nil || changed != 1 {
				t.Fatalf("fault did not follow actual native INSERT: count=%d err=%v", changed, err)
			}
			if err := turn.closeRows(); err != nil {
				t.Fatal(err)
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}
			scanObjectAssertAbsent(t, w)
		})
	}
}

func TestScanObjectNativeFailuresRollbackPrefix(t *testing.T) {
	w, _, _ := testExecutor(t)
	in := seedScanObject(t, w)
	for _, failure := range []string{"missing source", "numeric asset alias", "duplicate id", "duplicate live path"} {
		t.Run(failure, func(t *testing.T) {
			turn := scanObjectTestTurn(t, w, scanObjectDefinitions())
			if _, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE jobs SET processed=9 WHERE id='scan-object-run'`); err != nil {
				t.Fatal(err)
			}
			bad := in
			switch failure {
			case "missing source":
				bad.sourceID = "missing"
			case "numeric asset alias":
				bad.assetToken = "41"
			default:
				if _, err := insertInventoryObject(t.Context(), turn, in); err != nil {
					t.Fatal(err)
				}
				if failure == "duplicate live path" {
					bad.objectID = "other-object"
				}
			}
			if got, err := insertInventoryObject(t.Context(), turn, bad); err == nil || got != (inventoryObjectFacts{}) {
				t.Fatalf("native constraint failure escaped: got=%+v err=%v", got, err)
			}
			_ = turn.closeRows()
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}
			scanObjectAssertAbsent(t, w)
		})
	}
}

func TestScanObjectReturningRequiresExactlyOneRow(t *testing.T) {
	w, _, _ := testExecutor(t)
	in := seedScanObject(t, w)
	for _, count := range []int64{0, 2} {
		t.Run(string(rune('0'+count)), func(t *testing.T) {
			definition := scanObjectDefinitions()[0]
			if count == 0 {
				// Test-only ignored INSERT: the result decoder must not invent
				// a witness from input when RETURNING produces no row.
				definition.SQL = strings.Replace(definition.SQL, "INSERT INTO", "INSERT OR IGNORE INTO", 1)
			} else {
				// Test-only two-row INSERT preserving the 16 native bindings.
				// This is a decoder fault control, NOT N2 admission support.
				definition.SQL = strings.Replace(definition.SQL, "VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
					`SELECT ?||suffix,?,?,?,?||suffix,?,?,?,?,?,?,?,?,?,?,?
FROM (SELECT '' AS suffix UNION ALL SELECT '-other')`, 1)
			}
			turn := scanObjectFaultTurn(t, w, []stmt.Definition{definition})
			if count == 0 {
				// Seed the collision through the canonical statement, then use
				// the private test-only ignored variant on this same turn.
				original := scanObjectDefinitions()[0]
				config := w.config
				config.Synchronous = sqliteconn.Normal
				r := cachedNativeFixtureRegistry(t, turn.transaction.conn, config, []stmt.Definition{original}, "scan_object_test.go")
				saved := turn.registry
				turn.registry = r
				if _, err := insertInventoryObject(t.Context(), turn, in); err != nil {
					t.Fatal(err)
				}
				turn.registry = saved
			}
			got, err := insertInventoryObject(t.Context(), turn, in)
			want := ErrInvalid
			if count == 0 {
				want = sql.ErrNoRows
			}
			if !errors.Is(err, want) || got != (inventoryObjectFacts{}) {
				t.Fatalf("RETURNING cardinality=%d: got=%+v err=%v", count, got, err)
			}
			if err := turn.closeRows(); err != nil {
				t.Fatal(err)
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}
			scanObjectAssertAbsent(t, w)
		})
	}
}

func TestScanObjectBeforeSQLRefusals(t *testing.T) {
	w, _, _ := testExecutor(t)
	in := seedScanObject(t, w)
	for _, failure := range []string{"unregistered", "canceled", "quantum", "bad UTF8", "wrong parent", "empty evidence"} {
		t.Run(failure, func(t *testing.T) {
			turn := scanObjectTestTurn(t, w, scanObjectDefinitions())
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			bad, want := in, ErrInvalid
			switch failure {
			case "unregistered":
				delete(turn.steps, scanInventoryObject)
			case "canceled":
				cancel()
				want = context.Canceled
			case "quantum":
				step := turn.steps[scanInventoryObject]
				step.work.Rows = 101
				turn.steps[scanInventoryObject] = step
				want = ErrQuantum
			case "bad UTF8":
				bad.objectIdentity = string([]byte{0xff})
			case "wrong parent":
				bad.directoryPath = "other"
			case "empty evidence":
				bad.evidenceJSON = ""
			}
			if got, err := insertInventoryObject(ctx, turn, bad); !errors.Is(err, want) || got != (inventoryObjectFacts{}) {
				t.Fatalf("pre-SQL refusal: got=%+v err=%v want=%v", got, err, want)
			}
			var count int64
			if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT count(*) FROM inventory_objects`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("refused call mutated: count=%d err=%v", count, err)
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}
			scanObjectAssertAbsent(t, w)
		})
	}
}
