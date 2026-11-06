package writer

import (
	"bytes"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

func TestScanStatementClosedGrammar(t *testing.T) {
	for _, d := range append(scanReceiptDefinitions(), scanInventoryDefinitions()...) {
		t.Run(string(d.ID), func(t *testing.T) {
			if issues := plan.LintSQL(d.SQL); len(issues) != 0 {
				t.Fatalf("scan statement escaped merged closed grammar: %v", issues)
			}
		})
	}
	// This checks the registered SQL's grammar only. Native destination/FK/EQP
	// capture and reviewed full goldens remain HN's separate qualification.
}

func TestScanReceiptDefinitionsNativeCompile(t *testing.T) {
	definitions := append(scanReceiptDefinitions(), scanInventoryDefinitions()...)
	registry, err := stmttest.New(t, definitions)
	if err != nil {
		t.Fatal(err)
	}
	w, _ := testCanonicalExecutor(t)
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var version string
	if err := conn.QueryRowContext(t.Context(), "SELECT sqlite_version()").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != "3.53.4" {
		t.Fatalf("unqualified native engine %s", version)
	}
	t.Logf("native SQLite=%s schema=%s", version, schema.SHA256)
	for _, d := range definitions {
		t.Run(string(d.ID), func(t *testing.T) {
			if !strings.HasPrefix(string(d.ID), "scan.inventory_accept.") || !d.Plan.Hot {
				t.Fatal("statement escaped the closed hot scan namespace")
			}
			s, err := registry.Lookup(d.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, cohort := range d.Cohorts {
				if _, err := s.Bind(cohort.Bindings...); err != nil {
					t.Fatal(err)
				}
				// This is native compilation diagnostics, NOT a command, plan
				// golden, transaction or Applied receipt. EXPLAIN executes no
				// mutation. HN's reviewed capture remains a separate PR gate.
				rows, err := conn.QueryContext(t.Context(), "EXPLAIN "+d.SQL, cohort.Bindings...)
				if err != nil {
					t.Fatal(err)
				}
				count := 0
				for rows.Next() {
					count++
				}
				err = rows.Err()
				closeErr := rows.Close()
				if err != nil || closeErr != nil || count == 0 {
					t.Fatalf("native compilation rows=%d err=%v close=%v", count, err, closeErr)
				}
			}
		})
	}
	var receipts, results int
	if err := conn.QueryRowContext(t.Context(), "SELECT count(*) FROM scan_command_receipts").Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRowContext(t.Context(), "SELECT count(*) FROM scan_command_results").Scan(&results); err != nil {
		t.Fatal(err)
	}
	if receipts != 0 || results != 0 {
		t.Fatal("native compilation executed receipt mutations")
	}
}

// This journal is the immutable seed/ID witness for HN, rather than cohort names
// that may hit either population. It is a SQL-fact fixture, not a producer of
// valid command manifests/results: request/result digests are synthetic bytes.
// Common-classifier integration and exact result decoding remain separate tests
// when W's reviewed shared seam is on main.
type scanNativePopulation struct {
	name                          string
	last, ack, count, rows, first int64
	operation, digest             []byte
}

func seedScanReceiptCohorts(t *testing.T, w *executor) []scanNativePopulation {
	t.Helper()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := w.db.ExecContext(t.Context(), query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO libraries(id,name,kind,root) VALUES('scan-library','Movies','movie','/media')`)
	exec(`INSERT INTO catalog_libraries(id,library_id,root) VALUES(7,'scan-library','/media')`)
	exec(`INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(1,'movie',1,0,1,1,0)`)
	exec(`INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(31,?,7,1)`, bytes.Repeat([]byte{0x31}, 16))
	exec(`INSERT INTO catalog_assets(id,token,library_id,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration)
VALUES(41,'scan-fixture-asset',7,'/media/Arrival.mkv',42,1,'','','',0,0,0)`)
	populations := []scanNativePopulation{
		{name: "hit", last: 1, count: 1, rows: 1},
		{name: "missing"},
		{name: "replay", last: 1, count: 1, rows: 1},
		{name: "conflict", last: 1, count: 1, rows: 1},
		{name: "stale", last: 1, count: 1, rows: 1},
		{name: "floor", last: 1, ack: 1, count: 1, rows: 1},
		{name: "drained", last: 1, ack: 1, count: 1},
		{name: "remaining", last: 1, ack: 1, count: 2, rows: 1, first: 1},
		{name: "new"},
	}
	for i := range populations {
		p := &populations[i]
		// operation_id is globally unique, including across run owners.
		p.operation = bytes.Repeat([]byte{byte(0x20 + i)}, 16)
		p.digest = make([]byte, 32)
		if p.name == "conflict" {
			p.digest[0] = 0x7f // differs from the captured all-zero request digest
		}
		source, run := "scan-source-"+p.name, "scan-"+p.name
		generation, leaseGeneration, currentIncarnation := int64(1), int64(1), "source-incarnation"
		if p.name == "stale" {
			generation, leaseGeneration, currentIncarnation = 2, 2, "changed-source-incarnation"
		}
		exec(`INSERT INTO library_sources(id,library_id,configured_root,root,generation,incarnation,root_identity)
VALUES(?,'scan-library',?,?,?,?,'root')`, source, "/media/"+p.name, "/media/"+p.name, generation, currentIncarnation)
		exec(`INSERT INTO jobs(id,library_id,status,created_at) VALUES(?,'scan-library','running','2026-11-05T00:00:00Z')`, run)
		// Explicit immutable creation anchors for these SQL-fact populations,
		// not a lifecycle request digest or a lookup of mutable parent scope.
		// Creation operations are distinct from every N1 receipt operation.
		createOperation := bytes.Repeat([]byte{byte(0x60 + i)}, 16)
		createDigest := bytes.Repeat([]byte{byte(0xc0 + i)}, 32)
		exec(`INSERT INTO inventory_runs(job_id,source_id,source_generation,root_incarnation,root_identity,policy_revision,
command_incarnation,last_command_sequence,acknowledged_command_sequence,job_lease_owner,job_lease_generation,job_lease_expires_ms,
original_library_id,original_library_key,original_resolved_root,original_follow_symlinks,create_operation_id,create_request_digest)
VALUES(?,?,1,'source-incarnation','root',1,?,?,?,?,?,200,7,'scan-library',?,0,?,?)`,
			run, source, make([]byte, 16), p.last, p.ack, int64(1), leaseGeneration, "/media/"+p.name, createOperation, createDigest)
		if p.last == 0 {
			continue
		}
		exec(`INSERT INTO scan_command_receipts(run_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,
accepted_revision,applied_ms,source_id,source_incarnation,source_revision,next_cursor,result_count,result_digest)
VALUES(?,?,1,?,?,'inventory_accept',2,100,?,'source-incarnation','opaque:007','native-next',?,?)`,
			run, make([]byte, 16), p.operation, p.digest, source, p.count, make([]byte, 32))
		for n := int64(0); n < p.rows; n++ {
			exec(`INSERT INTO scan_command_results(run_id,command_incarnation,command_sequence,input_ordinal,child_kind,child_ordinal,
object_id,library_id,subject_id,asset_id,accepted_asset_revision) VALUES(?,?,1,?,0,0,?,7,31,41,1)`,
				run, make([]byte, 16), p.first+n, "object-"+p.name)
		}
	}
	return populations
}

func scanFixtureTurn(t *testing.T, w *executor) (*transaction, *stmt.Registry) {
	t.Helper()
	r, err := stmttest.New(t, scanReceiptDefinitions())
	if err != nil {
		t.Fatal(err)
	}
	tx, err := w.begin(t.Context(), InventoryAccept, testRecipe(), &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tx.rollback(nil) })
	return tx, r
}

func scanFixtureBind(t *testing.T, tx *transaction, registry *stmt.Registry, id stmt.ID, args ...any) stmt.Bound {
	t.Helper()
	s, err := registry.Lookup(id)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Bind(args...)
	if err != nil {
		t.Fatal(err)
	}
	// Synthetic clock/work, exactly as the frame controls: no production recipe,
	// command admission, HOLD/WAL measurement or durable outcome is qualified.
	if err := tx.before(Work{Rows: 1, VMOps: 100, Pages: 1, WALBytes: 100, Hold: time.Nanosecond}); err != nil {
		t.Fatal(err)
	}
	return b
}

func scanFixtureExec(t *testing.T, tx *transaction, r *stmt.Registry, id stmt.ID, want int64, args ...any) {
	t.Helper()
	result, err := scanFixtureBind(t, tx, r, id, args...).ExecContext(tx.ctx, tx.conn)
	if err != nil {
		t.Fatal(err)
	}
	n, err := result.RowsAffected()
	if err != nil || n != want {
		t.Fatalf("%s effects=%d want=%d err=%v", id, n, want, err)
	}
}

func TestScanReceiptDistinctNativeReadCohorts(t *testing.T) {
	w, _, _ := testExecutor(t)
	populations := seedScanReceiptCohorts(t, w)
	tx, r := scanFixtureTurn(t, w)
	for _, p := range populations[:8] {
		t.Run(p.name, func(t *testing.T) {
			run, inc := "scan-"+p.name, make([]byte, 16)
			var gotInc []byte
			var last, ack int64
			if err := scanFixtureBind(t, tx, r, scanReceiptOwner, run).QueryRowContext(tx.ctx, tx.conn).Scan(&gotInc, &last, &ack); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(gotInc, inc) || last != p.last || ack != p.ack {
				t.Fatalf("owner facts incarnation=%x last=%d ack=%d", gotInc, last, ack)
			}
			var operation, digest, resultDigest []byte
			var kind, source, sourceInc, revision, cursor string
			var acceptedRevision, appliedMS, count int64
			err := scanFixtureBind(t, tx, r, scanReceiptLookup, run, inc, int64(1)).QueryRowContext(tx.ctx, tx.conn).Scan(
				&operation, &digest, &kind, &acceptedRevision, &appliedMS, &source, &sourceInc, &revision, &cursor, &count, &resultDigest)
			if p.last == 0 {
				if !errors.Is(err, sql.ErrNoRows) {
					t.Fatalf("missing receipt: %v", err)
				}
			} else if err != nil || !bytes.Equal(operation, p.operation) || !bytes.Equal(digest, p.digest) || kind != scanInventoryReceiptKind ||
				source != "scan-source-"+p.name || sourceInc != "source-incarnation" || revision != "opaque:007" || cursor != "native-next" ||
				count != p.count || acceptedRevision != 2 || appliedMS != 100 || !bytes.Equal(resultDigest, make([]byte, 32)) {
				t.Fatalf("historical header mismatch: operation=%x digest=%x count=%d err=%v", operation, digest, count, err)
			}
			if p.name == "conflict" && bytes.Equal(digest, make([]byte, 32)) {
				t.Fatal("conflict seed did not differ from captured digest")
			}
			if p.name == "stale" {
				var generation, lease int64
				var currentIncarnation string
				// Test-only witness read; production current-source statements
				// have not yet been introduced into InventoryAccept's recipe.
				if err := tx.conn.QueryRowContext(tx.ctx, `SELECT generation,incarnation FROM library_sources WHERE id=?`, source).Scan(&generation, &currentIncarnation); err != nil {
					t.Fatal(err)
				}
				if err := tx.conn.QueryRowContext(tx.ctx, `SELECT job_lease_generation FROM inventory_runs WHERE job_id=?`, run).Scan(&lease); err != nil {
					t.Fatal(err)
				}
				if generation != 2 || lease != 2 || currentIncarnation == sourceInc {
					t.Fatal("stale fixture has no changed current source/lease witness")
				}
			}
			rows, err := scanFixtureBind(t, tx, r, scanReceiptResults, run, inc, int64(1), int64(3)).QueryContext(tx.ctx, tx.conn)
			if err != nil {
				t.Fatal(err)
			}
			var actual int64
			for rows.Next() {
				var ordinal, childKind, childOrdinal, library, subject, asset, epoch int64
				var object string
				if err := rows.Scan(&ordinal, &childKind, &childOrdinal, &object, &library, &subject, &asset, &epoch); err != nil {
					rows.Close()
					t.Fatal(err)
				}
				if ordinal != p.first+actual || childKind != 0 || childOrdinal != 0 || object != "object-"+p.name || library != 7 || subject != 31 || asset != 41 || epoch != 1 {
					rows.Close()
					t.Fatal("result did not preserve its full native mapping")
				}
				actual++
			}
			if err, closeErr := rows.Err(), rows.Close(); err != nil || closeErr != nil || actual != p.rows {
				t.Fatalf("result rows=%d want=%d err=%v close=%v", actual, p.rows, err, closeErr)
			}
			var ordinal, childKind, childOrdinal int64
			err = scanFixtureBind(t, tx, r, scanReceiptReclaimKey, run, inc, int64(1)).QueryRowContext(tx.ctx, tx.conn).Scan(&ordinal, &childKind, &childOrdinal)
			if p.rows == 0 {
				if !errors.Is(err, sql.ErrNoRows) {
					t.Fatalf("drained/missing key: %v", err)
				}
			} else if err != nil || ordinal != p.first || childKind != 0 || childOrdinal != 0 {
				t.Fatalf("bounded first key=%d/%d/%d err=%v", ordinal, childKind, childOrdinal, err)
			}
		})
	}
}

func TestScanReceiptScalarCASAndFullKeyEffects(t *testing.T) {
	w, _, _ := testExecutor(t)
	seedScanReceiptCohorts(t, w)
	tx, r := scanFixtureTurn(t, w)
	inc := make([]byte, 16)
	var gotInc []byte
	var last, ack int64
	if err := scanFixtureBind(t, tx, r, scanReceiptOwner, "scan-hit").QueryRowContext(tx.ctx, tx.conn).Scan(&gotInc, &last, &ack); err != nil {
		t.Fatal(err)
	}
	// This establishes the captured owner CAS only; wrong-op/digest decisions
	// must use W's common classifier before these writes, never these scalars.
	scanFixtureExec(t, tx, r, scanReceiptAck, 0, int64(1), "scan-hit", inc, ack, last+1)
	scanFixtureExec(t, tx, r, scanReceiptAck, 0, int64(1), "scan-hit", bytes.Repeat([]byte{1}, 16), ack, last)
	scanFixtureExec(t, tx, r, scanReceiptAck, 0, int64(1), "missing-owner", inc, ack, last)
	scanFixtureExec(t, tx, r, scanReceiptAck, 1, int64(1), "scan-hit", gotInc, ack, last)
	scanFixtureExec(t, tx, r, scanReceiptAck, 0, int64(1), "scan-hit", gotInc, ack, last)
	// The remaining fixture is already acknowledged. Point-read its exact
	// first key in this same turn; wrong/full keys affect zero/one rows.
	if err := scanFixtureBind(t, tx, r, scanReceiptOwner, "scan-remaining").QueryRowContext(tx.ctx, tx.conn).Scan(&gotInc, &last, &ack); err != nil {
		t.Fatal(err)
	}
	if ack != 1 || last != 1 {
		t.Fatal("reclaim fixture is not acknowledged")
	}
	var ordinal, childKind, childOrdinal int64
	if err := scanFixtureBind(t, tx, r, scanReceiptReclaimKey, "scan-remaining", inc, int64(1)).QueryRowContext(tx.ctx, tx.conn).Scan(&ordinal, &childKind, &childOrdinal); err != nil {
		t.Fatal(err)
	}
	scanFixtureExec(t, tx, r, scanReceiptReclaimResult, 0, "scan-remaining", inc, int64(1), ordinal-1, childKind, childOrdinal)
	scanFixtureExec(t, tx, r, scanReceiptReclaimResult, 0, "missing-owner", inc, int64(1), ordinal, childKind, childOrdinal)
	scanFixtureExec(t, tx, r, scanReceiptReclaimResult, 1, "scan-remaining", inc, int64(1), ordinal, childKind, childOrdinal)
	if err := scanFixtureBind(t, tx, r, scanReceiptReclaimKey, "scan-remaining", inc, int64(1)).QueryRowContext(tx.ctx, tx.conn).Scan(&ordinal, &childKind, &childOrdinal); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected a drained point read: %v", err)
	}
	scanFixtureExec(t, tx, r, scanReceiptReclaimHeader, 1, "scan-remaining", inc, int64(1))
	scanFixtureExec(t, tx, r, scanReceiptReclaimHeader, 0, "scan-remaining", inc, int64(1))
	if _, err := tx.commit(); err != nil {
		t.Fatal(err)
	}
	for run, want := range map[string]int64{"scan-hit": 1, "scan-replay": 0, "scan-remaining": 1} {
		if err := w.db.QueryRowContext(t.Context(), `SELECT acknowledged_command_sequence FROM inventory_runs WHERE job_id=?`, run).Scan(&ack); err != nil || ack != want {
			t.Fatalf("owner %s floor=%d want=%d err=%v", run, ack, want, err)
		}
	}
	var remaining, unrelated int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM scan_command_receipts WHERE run_id='scan-remaining'`).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM scan_command_results WHERE run_id='scan-replay'`).Scan(&unrelated); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 || unrelated != 1 {
		t.Fatalf("full-key delete scope remaining=%d unrelated=%d", remaining, unrelated)
	}
}

func TestScanReceiptHeaderRejectsRemainingChild(t *testing.T) {
	w, _, _ := testExecutor(t)
	seedScanReceiptCohorts(t, w)
	tx, r := scanFixtureTurn(t, w)
	_, err := scanFixtureBind(t, tx, r, scanReceiptReclaimHeader, "scan-remaining", make([]byte, 16), int64(1)).ExecContext(tx.ctx, tx.conn)
	if err == nil || !strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
		t.Fatalf("remaining child did not prevent header deletion: %v", err)
	}
	if result, rollbackErr := tx.rollback(err); result.state != rolledBack || !errors.Is(rollbackErr, err) {
		t.Fatalf("rollback state=%v err=%v", result.state, rollbackErr)
	}
	var headers, children int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM scan_command_receipts WHERE run_id='scan-remaining'`).Scan(&headers); err != nil {
		t.Fatal(err)
	}
	if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM scan_command_results WHERE run_id='scan-remaining'`).Scan(&children); err != nil {
		t.Fatal(err)
	}
	if headers != 1 || children != 1 {
		t.Fatalf("failed header deletion changed cohort: %d/%d", headers, children)
	}
}

func TestScanReceiptNativeInsertRollback(t *testing.T) {
	for _, wrongLibrary := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "wrong_library"}[wrongLibrary], func(t *testing.T) {
			w, _, _ := testExecutor(t)
			seedScanReceiptCohorts(t, w)
			tx, r := scanFixtureTurn(t, w)
			inc, operation, digest := make([]byte, 16), make([]byte, 16), make([]byte, 32)
			scanFixtureExec(t, tx, r, scanReceiptInsert, 1, "scan-new", inc, int64(1), operation, digest, scanInventoryReceiptKind,
				int64(2), int64(1), "scan-source-new", "source-incarnation", "opaque:007", "native-next", int64(1), digest)
			library := int64(7)
			if wrongLibrary {
				library = 8
			}
			_, err := scanFixtureBind(t, tx, r, scanResultInsert, "scan-new", inc, int64(1), int64(0), scanOrdinaryChildKind, scanOrdinaryChildOrdinal,
				"object-new", library, int64(31), int64(41), int64(1)).ExecContext(tx.ctx, tx.conn)
			if wrongLibrary {
				if err == nil || !strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
					t.Fatalf("wrong-library result: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			// Both the complete fixture and late-FK failure roll back. Neither
			// is a public command or evidence that a source became visible.
			if result, rollbackErr := tx.rollback(err); result.state != rolledBack || !errors.Is(rollbackErr, err) {
				t.Fatalf("rollback state=%v err=%v", result.state, rollbackErr)
			}
			var headers, children int64
			if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM scan_command_receipts WHERE run_id='scan-new'`).Scan(&headers); err != nil {
				t.Fatal(err)
			}
			if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM scan_command_results WHERE run_id='scan-new'`).Scan(&children); err != nil {
				t.Fatal(err)
			}
			if headers != 0 || children != 0 {
				t.Fatalf("rolled-back insert left a prefix: %d/%d", headers, children)
			}
		})
	}
}

func TestScanReceiptNewOrdinarySourceStatements(t *testing.T) {
	w, _, _ := testExecutor(t)
	// Source SQL fixture only: no source/lease/cursor/age/search/receipt handler
	// exists here yet, and committing these fixture rows is NOT InventoryAccept.
	for _, query := range []string{
		`INSERT INTO catalog_libraries(id,library_id,root) VALUES(1,'scan-library','/media')`,
		`INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(1,'movie',1,0,1,1,0)`,
	} {
		if _, err := w.db.ExecContext(t.Context(), query); err != nil {
			t.Fatal(err)
		}
	}
	registry, err := stmttest.New(t, scanInventoryDefinitions())
	if err != nil {
		t.Fatal(err)
	}
	tx, err := w.begin(t.Context(), InventoryAccept, testRecipe(), &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.rollback(nil)
	bind := func(id stmt.ID, args ...any) stmt.Bound {
		t.Helper()
		s, err := registry.Lookup(id)
		if err != nil {
			t.Fatal(err)
		}
		b, err := s.Bind(args...)
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.before(Work{Rows: 1, VMOps: 100, Pages: 1, WALBytes: 100, Hold: time.Nanosecond}); err != nil {
			t.Fatal(err)
		}
		return b
	}
	public, metadata, artwork, origin := bytes.Repeat([]byte{1}, 16), bytes.Repeat([]byte{2}, 16), bytes.Repeat([]byte{3}, 16), bytes.Repeat([]byte{4}, 16)
	var subjectID, assetID, epoch, entityID, revision, originRevision int64
	var token string
	var sourceIncarnation, playbackIncarnation []byte
	if err := bind(scanInventorySubject, public, int64(1), scanNewMovieKind).QueryRowContext(tx.ctx, tx.conn).Scan(&subjectID); err != nil {
		t.Fatal(err)
	}
	if _, err := bind(scanInventoryAlias, int64(1), "source-key", subjectID).ExecContext(tx.ctx, tx.conn); err != nil {
		t.Fatal(err)
	}
	if err := bind(scanInventoryNewAsset, "asset-token", int64(1), "/media/Arrival.mkv", int64(42), int64(1), "", "", "", int64(0), int64(0), float64(0)).QueryRowContext(tx.ctx, tx.conn).Scan(&assetID, &token, &epoch); err != nil {
		t.Fatal(err)
	}
	if err := bind(scanInventoryEntity, subjectID, public, int64(1), scanNewMovieKind, "Arrival", "Arrival", "arrival", "a", "arrival", "a",
		int64(1), int64(2016), "2026-11-05T00:00:00.000000000Z", "en-US", metadata, artwork, origin, scanInitialPlaybackOriginRevision).QueryRowContext(tx.ctx, tx.conn).Scan(&entityID, &sourceIncarnation, &revision, &playbackIncarnation, &originRevision); err != nil {
		t.Fatal(err)
	}
	if _, err := bind(scanInventoryLink, int64(1), subjectID, assetID, int64(0)).ExecContext(tx.ctx, tx.conn); err != nil {
		t.Fatal(err)
	}
	if entityID != subjectID || token != "asset-token" || epoch != 1 || revision != 1 || !bytes.Equal(sourceIncarnation, metadata) || bytes.Equal(sourceIncarnation, artwork) ||
		originRevision != 1 || !bytes.Equal(playbackIncarnation, origin) || bytes.Equal(playbackIncarnation, sourceIncarnation) {
		t.Fatalf("wrong new native source witnesses: subject/entity=%d/%d token=%s epoch=%d revision=%d incarnation=%x", subjectID, entityID, token, epoch, revision, sourceIncarnation)
	}
	if _, err := tx.commit(); err != nil {
		t.Fatal(err)
	}
}
