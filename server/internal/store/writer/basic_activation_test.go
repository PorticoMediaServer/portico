package writer

import (
	"database/sql"
	"errors"
	"math"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func basicActivationFixture(t *testing.T) (*executor, basicActivationEffects) {
	t.Helper()
	w, in := basicOrdinaryInputAcceptFixture(t)
	for _, q := range []string{
		`INSERT INTO library_sources(id,library_id,configured_root,root,incarnation,root_identity) VALUES('basic-source','basic-library','/media','/media','basic-incarnation','native-root')`,
		`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns) VALUES('basic-object','basic-source','basic-asset','basic-incarnation','Arrival.mkv','object:019','{"Path":"Arrival.mkv"}',42,-11)`,
		`UPDATE catalog_assets SET info_publication_pending=1 WHERE id=41`,
		`UPDATE catalog_info_changes SET phase=3,copy_family=0,target_available=1,target_owner_object=NULL,target_owner_revision=NULL WHERE id=51`,
		`INSERT INTO asset_stream_facts VALUES('basic-asset',31,42,-11,'probe-fingerprint',1)`,
	} {
		if _, err := w.db.ExecContext(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}
	return w, basicActivationEffects{source: in.source, changeRevision: 13, familyMask: 7, shapeID: 61, invalidationRevision: 1, fileSizeRevision: 1}
}
func basicActivationTestDefs() []stmt.Definition {
	return append(basicStreamStageDefinitions(), basicActivationDefinitions()...)
}
func TestBasicActivationActualEpochIndependentOfChangeInput(t *testing.T) {
	w, in := basicActivationFixture(t)
	turn := basicBoundedComponentTurn(t, w, basicActivationTestDefs())
	got, err := activateBasicAsset(t.Context(), turn, in)
	if err != nil || got != (basicActivatedAsset{41, 18, 14, 4, 1, sql.NullString{}, sql.NullString{}}) {
		t.Fatal(got, err)
	}
	basicMediaCommit(t, turn)
	var epoch, pending, expected, input, phase, changeRevision int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT info_epoch,info_publication_pending FROM catalog_assets WHERE id=41`).Scan(&epoch, &pending); err != nil {
		t.Fatal(err)
	}
	if err := w.db.QueryRowContext(t.Context(), `SELECT expected_epoch,input_revision,phase,job_revision FROM catalog_info_changes WHERE id=51`).Scan(&expected, &input, &phase, &changeRevision); err != nil {
		t.Fatal(err)
	}
	// Intermediate activation intentionally keeps the foreground fence set.
	if epoch != 18 || pending != 1 || expected != 17 || input != 7 || phase != 4 || changeRevision != 14 {
		t.Fatal(epoch, pending, expected, input, phase, changeRevision)
	}
}
func TestBasicActivationRefusesIncompleteCopyAndProbeDrift(t *testing.T) {
	for _, name := range []string{"copy_cursor", "probe_revision", "unfenced_asset"} {
		t.Run(name, func(t *testing.T) {
			w, in := basicActivationFixture(t)
			switch name {
			case "copy_cursor":
				if _, err := w.db.ExecContext(t.Context(), `UPDATE catalog_info_changes SET copy_family=3,copy_id=2 WHERE id=51`); err != nil {
					t.Fatal(err)
				}
			case "probe_revision":
				if _, err := w.db.ExecContext(t.Context(), `UPDATE asset_stream_facts SET revision=32 WHERE asset_id='basic-asset'`); err != nil {
					t.Fatal(err)
				}
			case "unfenced_asset":
				if _, err := w.db.ExecContext(t.Context(), `UPDATE catalog_assets SET info_publication_pending=0 WHERE id=41`); err != nil {
					t.Fatal(err)
				}
			}
			turn := basicBoundedComponentTurn(t, w, basicActivationTestDefs())
			got, err := activateBasicAsset(t.Context(), turn, in)
			if !errors.Is(err, ErrConflict) || got != (basicActivatedAsset{}) {
				t.Fatal(got, err)
			}
		})
	}
}
func TestBasicActivationLateFailureRollsBackNativeEpoch(t *testing.T) {
	w, in := basicActivationFixture(t)
	turn := basicBoundedComponentTurn(t, w, basicActivationTestDefs())
	delete(turn.steps, basicActivationChangeCAS)
	got, err := activateBasicAsset(t.Context(), turn, in)
	if err == nil || got != (basicActivatedAsset{}) {
		t.Fatal(got, err)
	}
	_ = turn.closeRows()
	_, _ = turn.transaction.rollback(err)
	var epoch, phase int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT info_epoch FROM catalog_assets WHERE id=41`).Scan(&epoch); err != nil {
		t.Fatal(err)
	}
	if err := w.db.QueryRowContext(t.Context(), `SELECT phase FROM catalog_info_changes WHERE id=51`).Scan(&phase); err != nil {
		t.Fatal(err)
	}
	if epoch != 17 || phase != 3 {
		t.Fatal(epoch, phase)
	}
}

func TestBasicActivationPreservesNullableChoiceIndependently(t *testing.T) {
	for _, choice := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary_null", true: "retained_other_choice"}[choice], func(t *testing.T) {
			w, in := basicActivationFixture(t)
			in.ownerObject, in.ownerRevision = sql.NullString{}, sql.NullString{}
			if _, err := w.db.ExecContext(t.Context(), `UPDATE catalog_info_changes SET target_owner_object=NULL,target_owner_revision=NULL WHERE id=51`); err != nil {
				t.Fatal(err)
			}
			if choice {
				if _, err := w.db.ExecContext(t.Context(), `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns) VALUES('retained-choice','basic-source','basic-asset','basic-incarnation','choice.iso','choice:777','{"Path":"choice.iso"}',42,-11)`); err != nil {
					t.Fatal(err)
				}
				if _, err := w.db.ExecContext(t.Context(), `UPDATE catalog_info_changes SET target_owner_object='retained-choice',target_owner_revision='choice:777' WHERE id=51`); err != nil {
					t.Fatal(err)
				}
				in.ownerObject, in.ownerRevision = sql.NullString{String: "retained-choice", Valid: true}, sql.NullString{String: "choice:777", Valid: true}
			}
			turn := basicBoundedComponentTurn(t, w, basicActivationTestDefs())
			got, err := activateBasicAsset(t.Context(), turn, in)
			if choice {
				var refusal *Refusal
				if !errors.As(err, &refusal) || refusal.Code != RefusalUnqualified || got != (basicActivatedAsset{}) {
					t.Fatal("unsupported disc choice changed", got, err)
				}
				_ = turn.closeRows()
				_, _ = turn.transaction.rollback(err)
				return
			}
			if err != nil || got.infoEpoch != 18 || got.ownerObject != in.ownerObject || got.ownerRevision != in.ownerRevision {
				t.Fatal("probe witness replaced native choice", got, err)
			}
			basicMediaCommit(t, turn)
			var object, revision sql.NullString
			if err = w.db.QueryRowContext(t.Context(), `SELECT info_owner_object,info_owner_revision FROM catalog_assets WHERE id=41`).Scan(&object, &revision); err != nil || object != in.ownerObject || revision != in.ownerRevision {
				t.Fatal(object, revision, err)
			}
		})
	}
}

func TestBasicActivationPreservesIndependentFences(t *testing.T) {
	for _, scenario := range []string{"invalidation_drift", "size_revision_drift", "owner_current_drift", "preserved_max"} {
		t.Run(scenario, func(t *testing.T) {
			w, in := basicActivationFixture(t)
			q := ""
			switch scenario {
			case "invalidation_drift":
				q = `UPDATE catalog_assets SET info_invalidation_revision=2 WHERE id=41`
			case "size_revision_drift":
				q = `UPDATE catalog_assets SET file_size_revision=2 WHERE id=41`
			case "owner_current_drift":
				q = `UPDATE catalog_assets SET info_owner_object='basic-object',info_owner_revision='object:019' WHERE id=41`
			case "preserved_max":
				q = `UPDATE catalog_assets SET info_invalidation_revision=9223372036854775807,file_size_revision=9223372036854775807 WHERE id=41`
				in.invalidationRevision, in.fileSizeRevision = math.MaxInt64, math.MaxInt64
			}
			if _, err := w.db.ExecContext(t.Context(), q); err != nil {
				t.Fatal(err)
			}
			turn := basicBoundedComponentTurn(t, w, basicActivationTestDefs())
			got, err := activateBasicAsset(t.Context(), turn, in)
			if scenario == "preserved_max" {
				if err != nil || got.infoEpoch != 18 {
					t.Fatal("preserved counter wrongly overflowed", got, err)
				}
				basicMediaCommit(t, turn)
				var inv, size int64
				if err = w.db.QueryRowContext(t.Context(), `SELECT info_invalidation_revision,file_size_revision FROM catalog_assets WHERE id=41`).Scan(&inv, &size); err != nil || inv != math.MaxInt64 || size != math.MaxInt64 {
					t.Fatal(inv, size, err)
				}
				return
			}
			if !errors.Is(err, ErrConflict) || got != (basicActivatedAsset{}) {
				t.Fatal("stale native fence activated", got, err)
			}
			_ = turn.closeRows()
			_, _ = turn.transaction.rollback(err)
			var epoch int64
			if err = w.db.QueryRowContext(t.Context(), `SELECT info_epoch FROM catalog_assets WHERE id=41`).Scan(&epoch); err != nil || epoch != 17 {
				t.Fatal(epoch, err)
			}
		})
	}
}
