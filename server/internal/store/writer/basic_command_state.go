package writer

import (
	"context"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const basicControlChange stmt.ID = "scan.basic_control.change"

// Initial input staging and sealing require the native COPY cursor at zero. Check its bounded empty
// state in SQL before the result decoder can allocate any returned cursor TEXT.
// The separately captured CJR job revision is not the scan run revision.
func checkBasicControlChange(ctx context.Context, turn *commandTurn, source basicStreamFactStage, expectedRevision int64) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if expectedRevision <= 0 {
		return ErrInvalid
	}
	row, err := basicStageRead(ctx, turn, basicControlChange, source.changeID, source.assetID, source.assetToken, source.inputRevision, source.expectedEpoch, expectedRevision, source.witnessFingerprint[:])
	if err != nil {
		return err
	}
	var actual int64
	if err = row.Scan(&actual); err != nil {
		return basicStageError(err)
	}
	if actual != expectedRevision {
		return ErrConflict
	}
	return nil
}

func basicCommandStateDefinitions() []stmt.Definition {
	return []stmt.Definition{{ID: basicControlChange, Kind: stmt.Read, Owner: "store/writer Basic domain", CallSites: []string{"store/writer/basic_handlers.go"},
		SQL: `SELECT job_revision FROM catalog_info_changes WHERE id=? AND asset_id=? AND asset_token=? AND input_revision=? AND expected_epoch=? AND job_revision=? AND fingerprint=?
AND slot=0 AND phase=0 AND family_mask=511 AND has_after=0 AND after_entity=0 AND upper_entity=0
AND copy_family=0 AND copy_phase=0 AND copy_id=0 AND copy_aux=0 AND copy_key='' AND copy_key2='' AND copy_key3=''
AND job_lease_owner=0 AND job_lease_generation=0 AND job_lease_expires_ms=0 AND fault='' AND target_available IS NULL AND target_owner_object IS NULL AND target_owner_revision IS NULL`,
		Params:  []stmt.Param{{Name: "change_id", Type: stmt.Integer}, {Name: "asset_id", Type: stmt.Integer}, {Name: "asset_token", Type: stmt.Text}, {Name: "input_revision", Type: stmt.Integer}, {Name: "expected_epoch", Type: stmt.Integer}, {Name: "change_revision", Type: stmt.Integer}, {Name: "fingerprint", Type: stmt.Blob}},
		Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_info_changes"}, Seeks: []string{"rowid=?"}},
		Cohorts: []stmt.Cohort{{Name: "staging_change_exact_cursor_and_revision", Bindings: []any{int64(51), int64(41), "basic-asset", int64(7), int64(17), int64(1), []byte("11111111111111111111111111111111")}}},
	}}
}
