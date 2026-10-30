package compactcatalog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"strconv"
)

// The existing coalesced source row owns this bit. It is a real per-file input
// invalidation, never source offline/online work or a per-query job.
const sourceInfoInvalidated int64 = 128
const infoUnknownMask int64 = 1 | 2 | 4 | 8 | 16 | 32 | 128 | 256
const infoInvalidationWitness = "info-invalidation-v1"

// InvalidateInfoTechnicalTx preserves OLD canonical child rows until every
// holder has been pre-fenced. Same-size/mtime replacements also require this
// explicit observation invalidation; stat equality cannot prove old tracks.
func InvalidateInfoTechnicalTx(ctx context.Context, tx *sql.Tx, asset int64) error {
	var token string
	if e := tx.QueryRowContext(ctx, `SELECT token FROM catalog_assets WHERE id=?`, asset).Scan(&token); e != nil {
		return e
	}
	// A newly observed physical revision supersedes unaccepted input only.
	// Accepted input is immutable and is finished before the unknown baseline.
	if _, e := tx.ExecContext(ctx, `UPDATE catalog_info_changes SET fault='',copy_aux=-1,input_revision=input_revision+1,copy_family=0 WHERE asset_id=? AND asset_token=? AND phase=0 AND copy_aux<>-1`, asset, token); e != nil {
		return e
	}
	return queueSource(ctx, tx, DomainAssetSources, asset, token, sourceInfoInvalidated|sourceInfo|sourceMetadata|sourceRevision)
}

// advanceInfoInvalidationTx is entered only from A5's coherent captured point.
// A prior accepted frame advances normally. Otherwise it creates one exact
// unknown technical baseline and seals at most one empty family per quantum.
// No canonical family changes before normal pre-fence/materialize/activation.
func advanceInfoInvalidationTx(ctx context.Context, tx *sql.Tx, asset int64, token string) (handled, accepted bool, err error) {
	c, e := activeInfoChange(ctx, tx, asset, token)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return false, false, e
	}
	if e == nil {
		if c.phase != 0 {
			return false, false, nil
		}
		var fault string
		if e = tx.QueryRowContext(ctx, `SELECT CASE copy_aux WHEN -1 THEN 'unaccepted-reclaim' WHEN -2 THEN 'old-frozen' ELSE fault END FROM catalog_info_changes WHERE id=?`, c.id).Scan(&fault); e != nil {
			return false, false, e
		}
		if fault == infoReclaim {
			_, _, e = recoverInfoInputTx(ctx, tx, c)
			return true, false, e
		}
		var hasWitness bool
		if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_info_input_witnesses WHERE change_id=? AND ordinal=0)`, c.id).Scan(&hasWitness); e != nil {
			return false, false, e
		}
		if hasWitness {
			w, we := infoInputWitnessAt(ctx, tx, c.id, 0)
			if we != nil {
				return false, false, we
			}
			if w.JobID != infoInvalidationWitness {
				h, he := infoInputHeaderByID(ctx, tx, c.id)
				if he != nil {
					return false, false, he
				}
				_, he = AbandonInfoInputTx(ctx, tx, h.ref())
				return true, false, he
			}
		}
	}
	var epoch, size, modified int64
	if e = tx.QueryRowContext(ctx, `SELECT info_epoch,size,modified_ns FROM catalog_assets WHERE id=? AND token=?`, asset, token).Scan(&epoch, &size, &modified); e != nil {
		return false, false, e
	}
	fingerprint := sha256.Sum256([]byte(infoInvalidationWitness + ":" + token + ":" + strconv.FormatInt(size, 10) + ":" + strconv.FormatInt(modified, 10)))
	spec := InfoInputSpec{AssetID: asset, Token: token, ExpectedEpoch: epoch, FamilyMask: infoUnknownMask, Fingerprint: fingerprint, Witnesses: []InfoWitness{{Kind: InfoWitnessAsset, JobID: infoInvalidationWitness, Fingerprint: fingerprint}}}
	// Bypass only the public invalidation admission guard. The worker's coherent
	// A5 source revision is validated by its caller in this same transaction.
	input, e := beginInfoInputTx(ctx, tx, spec)
	if e != nil {
		return true, false, e
	}
	if input.Ref.Slot != 0 {
		return true, false, ErrInfoInvariant
	}
	for family := 1; family <= 18; family++ {
		if infoUnknownMask&(1<<uint(family-1)) == 0 {
			continue
		}
		_, _, sealed, _, se := infoInputManifest(ctx, tx, input.Ref.ChangeID, family)
		if se != nil && !errors.Is(se, sql.ErrNoRows) {
			return true, false, se
		}
		if se == nil && sealed != 0 {
			continue
		}
		_, e = SealInfoFamilyTx(ctx, tx, input.Ref, family, 0)
		return true, false, e
	}
	_, e = AcceptInfoInputTx(ctx, tx, input.Ref)
	return true, e == nil, e
}
