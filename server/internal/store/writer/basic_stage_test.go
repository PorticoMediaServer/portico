package writer

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func basicStreamFixture(t *testing.T) (*executor, basicStreamFactStage) {
	t.Helper()
	w, _, _ := testExecutor(t)
	for _, q := range []string{
		`INSERT INTO libraries(id,name,kind,root) VALUES('basic-library','Movies','movie','/media')`,
		`INSERT INTO catalog_libraries(id,library_id,root) VALUES(7,'basic-library','/media')`,
		`INSERT INTO catalog_assets(id,token,library_id,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,info_epoch)
VALUES(41,'basic-asset',7,'/media/Arrival.mkv',42,-11,'','','',0,0,0,17)`,
		`INSERT INTO catalog_info_input_budget(singleton,used_bytes) VALUES(1,1000)`,
	} {
		if _, err := w.db.ExecContext(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}
	fingerprint := bytes.Repeat([]byte{0x31}, 32)
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO catalog_info_changes(id,asset_id,asset_token,slot,expected_epoch,input_revision,family_mask,fingerprint,created_ms)
VALUES(51,41,'basic-asset',0,17,7,7,?,1000)`, fingerprint); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO catalog_info_input_witnesses(change_id,ordinal,kind,job_id,source_id,source_generation,root_incarnation,object_id,object_revision,policy_revision,fingerprint)
VALUES(51,0,0,'basic-run','basic-source',23,'basic-incarnation','basic-object','object:019',29,?)`, fingerprint); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO catalog_info_input_families(change_id,family,witness_ordinal,rows_expected) VALUES(51,1,0,1)`); err != nil {
		t.Fatal(err)
	}
	return w, basicStreamFactStage{changeID: 51, inputRevision: 7, libraryID: 7, assetID: 41, expectedEpoch: 17, assetToken: "basic-asset",
		runID: "basic-run", sourceID: "basic-source", incarnation: "basic-incarnation", objectID: "basic-object", objectRevision: "object:019",
		sourceGeneration: 23, policyRevision: 29, witnessFingerprint: [32]byte(fingerprint), factRevision: 31, size: 42, modifiedNS: -11, detailVersion: 1, fingerprint: "probe-fingerprint"}
}

func basicStreamComponentTurn(t *testing.T, w *executor) *commandTurn {
	t.Helper()
	return basicComponentTurn(t, w, basicStreamStageDefinitions())
}

func basicComponentTurn(t *testing.T, w *executor, defs []stmt.Definition) *commandTurn {
	t.Helper()
	// This is the EXISTING BasicAccept synthetic component frame, not a new
	// BasicStage/Seal command, grant, issuer, recipe or durable receipt. The
	// private effect bodies are not wired; W's draft dedicated purposes and
	// complete source/history/time/memory/terminal integration remain separate.
	tx, err := w.begin(t.Context(), BasicAccept, testRecipe(), &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = tx.rollback(nil) })
	policy := w.config
	policy.Synchronous = sqliteconn.Normal
	registry := nativeFixtureRegistry(t, tx.conn, policy, defs, "basic_stage_test.go")
	var steps []statementStep
	for _, d := range defs {
		steps = append(steps, statementStep{d.ID, Work{Rows: 4, VMOps: 500, Pages: 4, WALBytes: 4096, Hold: time.Millisecond}})
	}
	turn, err := newCommandTurn(tx, registry, steps)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = turn.closeRows() })
	return turn
}

func TestBasicStreamNativeStageAndSealStayUnpublished(t *testing.T) {
	w, in := basicStreamFixture(t)
	turn := basicStreamComponentTurn(t, w)
	got, err := stageBasicStreamFact(t.Context(), turn, in)
	if err != nil || got != (basicFamilyProgress{51, 7, 1, 0}) {
		t.Fatal(got, err)
	}
	got, err = sealBasicStreamFact(t.Context(), turn, in)
	if err != nil || got != (basicFamilyProgress{51, 7, 1, 1}) {
		t.Fatal(got, err)
	}
	if err = turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	var used, phase, epoch, staged, sealed int64
	if err = w.db.QueryRowContext(t.Context(), `SELECT used_bytes FROM catalog_info_input_budget WHERE singleton=1`).Scan(&used); err != nil {
		t.Fatal(err)
	}
	if err = w.db.QueryRowContext(t.Context(), `SELECT phase FROM catalog_info_changes WHERE id=51`).Scan(&phase); err != nil {
		t.Fatal(err)
	}
	if err = w.db.QueryRowContext(t.Context(), `SELECT info_epoch FROM catalog_assets WHERE id=41`).Scan(&epoch); err != nil {
		t.Fatal(err)
	}
	if err = w.db.QueryRowContext(t.Context(), `SELECT rows_staged,sealed FROM catalog_info_input_families WHERE change_id=51 AND family=1`).Scan(&staged, &sealed); err != nil {
		t.Fatal(err)
	}
	if used != 1000+192+int64(len(in.assetToken)+len(in.fingerprint)) || phase != 0 || epoch != 17 || staged != 1 || sealed != 1 {
		t.Fatal(used, phase, epoch, staged, sealed)
	}
}

func TestBasicStreamNativeFailureRollsBackWholeUnit(t *testing.T) {
	for _, scenario := range []string{"foreign_library", "debt_capacity", "duplicate_row", "duplicate_fact_key"} {
		t.Run(scenario, func(t *testing.T) {
			w, in := basicStreamFixture(t)
			seedBytes := int64(1000)
			seedRows := int64(0)
			switch scenario {
			case "foreign_library":
				in.libraryID = 8
			case "debt_capacity":
				seedBytes = 33554432
				if _, err := w.db.ExecContext(t.Context(), `UPDATE catalog_info_input_budget SET used_bytes=33554432 WHERE singleton=1`); err != nil {
					t.Fatal(err)
				}
			case "duplicate_row", "duplicate_fact_key":
				seedRows = 1
				ordinal, token := int64(0), "other-asset"
				if scenario == "duplicate_fact_key" {
					ordinal, token = 1, "basic-asset"
				}
				if _, err := w.db.ExecContext(t.Context(), `INSERT INTO catalog_info_new_stream_facts VALUES(51,?,?,31,42,-11,'probe-fingerprint',1)`, ordinal, token); err != nil {
					t.Fatal(err)
				}
			}
			turn := basicStreamComponentTurn(t, w)
			got, err := stageBasicStreamFact(t.Context(), turn, in)
			if err == nil || got != (basicFamilyProgress{}) {
				t.Fatal("failed unit leaked output", got, err)
			}
			if (scenario == "duplicate_row" || scenario == "duplicate_fact_key") && !errors.Is(err, ErrConflict) {
				t.Fatal("native duplicate must be a typed conflict", err)
			}
			if _, err = turn.transaction.rollback(err); err == nil {
				t.Fatal("rollback lost failure")
			}
			var used, staged, rows int64
			if err = w.db.QueryRowContext(t.Context(), `SELECT used_bytes FROM catalog_info_input_budget WHERE singleton=1`).Scan(&used); err != nil {
				t.Fatal(err)
			}
			if err = w.db.QueryRowContext(t.Context(), `SELECT rows_staged FROM catalog_info_input_families WHERE change_id=51 AND family=1`).Scan(&staged); err != nil {
				t.Fatal(err)
			}
			if err = w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM catalog_info_new_stream_facts WHERE change_id=51`).Scan(&rows); err != nil {
				t.Fatal(err)
			}
			if used != seedBytes || staged != 0 || rows != seedRows {
				t.Fatal("partial debt/count/row escaped", used, staged, rows)
			}
		})
	}
}

func TestBasicStreamNativeSealRejectsChangedFactsAndCancellation(t *testing.T) {
	w, in := basicStreamFixture(t)
	turn := basicStreamComponentTurn(t, w)
	if _, err := stageBasicStreamFact(t.Context(), turn, in); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE catalog_info_new_stream_facts SET modified_ns=-12 WHERE change_id=51 AND asset_id='basic-asset'`); err != nil {
		t.Fatal(err)
	}
	if got, err := sealBasicStreamFact(t.Context(), turn, in); !errors.Is(err, ErrConflict) || got != (basicFamilyProgress{}) {
		t.Fatal(got, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if got, err := sealBasicStreamFact(ctx, turn, in); !errors.Is(err, context.Canceled) || got != (basicFamilyProgress{}) {
		t.Fatal(got, err)
	}
}

func TestBasicStreamNativeRejectsHiddenWitnessOrFamilyRow(t *testing.T) {
	for _, name := range []string{"witness", "family"} {
		t.Run(name, func(t *testing.T) {
			w, in := basicStreamFixture(t)
			turn := basicStreamComponentTurn(t, w)
			if name == "witness" {
				if _, err := turn.transaction.conn.ExecContext(t.Context(), `INSERT INTO catalog_info_input_witnesses
SELECT change_id,1,kind,job_id,source_id,source_generation,root_incarnation,object_id,object_revision,policy_revision,expected_choice_revision,fingerprint
FROM catalog_info_input_witnesses WHERE change_id=51 AND ordinal=0`); err != nil {
					t.Fatal(err)
				}
				if got, err := stageBasicStreamFact(t.Context(), turn, in); !errors.Is(err, ErrConflict) || got != (basicFamilyProgress{}) {
					t.Fatal(got, err)
				}
			} else {
				if _, err := stageBasicStreamFact(t.Context(), turn, in); err != nil {
					t.Fatal(err)
				}
				if _, err := turn.transaction.conn.ExecContext(t.Context(), `INSERT INTO catalog_info_new_stream_facts VALUES(51,1,'other-asset',31,42,-11,'probe-fingerprint',1)`); err != nil {
					t.Fatal(err)
				}
				if got, err := sealBasicStreamFact(t.Context(), turn, in); !errors.Is(err, ErrConflict) || got != (basicFamilyProgress{}) {
					t.Fatal(got, err)
				}
			}
			var sealed int64
			if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT sealed FROM catalog_info_input_families WHERE change_id=51 AND family=1`).Scan(&sealed); err != nil || sealed != 0 {
				t.Fatal(sealed, err)
			}
		})
	}
}

func TestBasicStreamDefinitionsAreClosed(t *testing.T) {
	defs := basicStreamStageDefinitions()
	catalogue, err := stmt.Describe(defs)
	if err != nil || len(catalogue.Manifest()) != 10 {
		t.Fatal(catalogue, err)
	}
}

// These cases retain exactly one witness. Adding another row would test only
// cardinality and would not detect removal of the shared value comparison.
func TestBasicStreamNativeSoleWitnessDrift(t *testing.T) {
	for _, operation := range []string{"stage", "seal"} {
		t.Run(operation, func(t *testing.T) {
			w, in := basicStreamFixture(t)
			turn := basicStreamComponentTurn(t, w)
			if operation == "seal" {
				if _, err := stageBasicStreamFact(t.Context(), turn, in); err != nil {
					t.Fatal(err)
				}
			}
			in.runID = "other-run"
			var got basicFamilyProgress
			var err error
			if operation == "stage" {
				got, err = stageBasicStreamFact(t.Context(), turn, in)
			} else {
				got, err = sealBasicStreamFact(t.Context(), turn, in)
			}
			if !errors.Is(err, ErrConflict) || got != (basicFamilyProgress{}) {
				t.Fatal("sole-witness drift admitted", got, err)
			}
			if _, err = turn.transaction.rollback(err); !errors.Is(err, ErrConflict) {
				t.Fatal("rollback lost conflict", err)
			}
			var used, staged, sealed, rows int64
			if err = w.db.QueryRowContext(t.Context(), `SELECT used_bytes FROM catalog_info_input_budget WHERE singleton=1`).Scan(&used); err != nil {
				t.Fatal(err)
			}
			if err = w.db.QueryRowContext(t.Context(), `SELECT rows_staged,sealed FROM catalog_info_input_families WHERE change_id=51 AND family=1`).Scan(&staged, &sealed); err != nil {
				t.Fatal(err)
			}
			if err = w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM catalog_info_new_stream_facts WHERE change_id=51`).Scan(&rows); err != nil {
				t.Fatal(err)
			}
			if used != 1000 || staged != 0 || sealed != 0 || rows != 0 {
				t.Fatal("witness failure leaked effects", used, staged, sealed, rows)
			}
		})
	}
}

type basicFactTestError int

func (e basicFactTestError) Error() string { return "native fact insert failure" }
func (e basicFactTestError) Code() int     { return int(e) }

func TestBasicStreamFactInsertErrorClassification(t *testing.T) {
	for _, code := range []int{1555, 2067, 19, 787, 275, 5, 9, 10} {
		native := basicFactTestError(code)
		err := basicStreamFactInsertError(native)
		wantConflict := code == 1555 || code == 2067
		if errors.Is(err, ErrConflict) != wantConflict || !errors.Is(err, native) {
			t.Fatal(code, err)
		}
	}
	for _, original := range []error{nil, context.Canceled, context.DeadlineExceeded, ErrCap} {
		if got := basicStreamFactInsertError(original); got != original {
			t.Fatal("unrelated error changed", original, got)
		}
	}
}
