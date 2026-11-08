package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// The ONE leaf entity linked to the FIRST asset. A second link refuses: FIRST
// is a single ordinary leaf and never coalesces entities.
func readBasicSoleLeaf(ctx context.Context, turn *commandTurn, s basicStreamFactStage) (entity int64, err error) {
	defer func() { entity, err = finishCommand(ctx, commandLifetime(turn), entity, err) }()
	rows, err := basicRows(ctx, turn, basicFreezeLinks, s.assetID)
	if err != nil {
		return 0, err
	}
	defer func() {
		if e := rows.Close(); err == nil {
			err = e
		}
	}()
	n := 0
	for rows.Next() {
		if err = rows.Scan(&entity); err != nil {
			return 0, basicStageError(err)
		}
		n++
	}
	if err = rows.Err(); err != nil {
		return 0, err
	}
	if n != 1 || entity <= 0 {
		return 0, ErrConflict
	}
	return entity, nil
}

// A leaf settle would refuse (a cue or ranged link, a child or aggregate-owned
// entity) must refuse here: past this point the asset is hidden from playback.
func checkBasicLeafSettleable(ctx context.Context, turn *commandTurn, s basicStreamFactStage, entity int64) error {
	for _, read := range []struct {
		id   stmt.ID
		args []any
	}{
		{basicFreezeLink, []any{entity, s.assetID, s.libraryID}},
		{basicFreezeEntity, []any{entity, s.libraryID}},
	} {
		row, err := basicStageRead(ctx, turn, read.id, read.args...)
		if err != nil {
			return err
		}
		var got int64
		if err = row.Scan(&got); errors.Is(err, sql.ErrNoRows) {
			return &Refusal{Code: RefusalUnqualified}
		} else if err != nil {
			return err
		}
		if got != entity {
			return ErrConflict
		}
	}
	return nil
}

// freezeBasicFirstInput moves a pinned FIRST input 0->1 after all nine masked
// families are sealed at their exact staged counts. It freezes the target as
// available with the genuine NULL/NULL owner and bounds the walk at the sole
// leaf entity; it does not select the entity or copy anything.
func freezeBasicFirstInput(ctx context.Context, turn *commandTurn, in basicControlRequest) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	s := in.source
	if ctx == nil || turn == nil || in.changeRevision <= 0 || in.changeRevision == math.MaxInt64 || in.shapeID <= 0 || in.entityID <= 0 {
		return ErrInvalid
	}
	if err = checkBasicStreamInput(ctx, turn, s); err != nil {
		return err
	}
	if err = checkBasicFamiliesSealed(ctx, turn, s.changeID); err != nil {
		return err
	}
	leaf, err := readBasicSoleLeaf(ctx, turn, s)
	if err != nil {
		return err
	}
	if leaf != in.entityID {
		return ErrConflict
	}
	if err = checkBasicLeafSettleable(ctx, turn, s, leaf); err != nil {
		return err
	}
	row, err := basicStageRead(ctx, turn, basicFreezeCAS, in.entityID, in.changeRevision+1, s.changeID, s.assetID, s.assetToken, s.expectedEpoch, s.inputRevision, s.witnessFingerprint[:], in.changeRevision, in.shapeID)
	if err != nil {
		return err
	}
	var phase, hasAfter, available, upper, revision int64
	if err = row.Scan(&phase, &hasAfter, &available, &upper, &revision); err != nil {
		return basicStageError(err)
	}
	if phase != 1 || hasAfter != 0 || available != 1 || upper != in.entityID || revision != in.changeRevision+1 {
		return ErrConflict
	}
	// Begin and the probe require this obligation clear, so it opens here and
	// holds through copy and activation until the whole publication completes.
	row, err = basicStageRead(ctx, turn, basicFreezeAsset, s.assetID, s.assetToken, s.libraryID, s.expectedEpoch)
	if err != nil {
		return err
	}
	var asset, pending int64
	if err = row.Scan(&asset, &pending); err != nil {
		return basicStageError(err)
	}
	if asset != s.assetID || pending != 1 {
		return ErrConflict
	}
	return nil
}

// selectBasicFirstEntity moves a frozen FIRST input 1->2: the walk's after
// bound becomes the sole leaf entity (== upper_entity) and copy starts at
// family 0 with a zeroed cursor.
func selectBasicFirstEntity(ctx context.Context, turn *commandTurn, in basicControlRequest) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	s := in.source
	if ctx == nil || turn == nil || in.changeRevision <= 0 || in.changeRevision == math.MaxInt64 || in.shapeID <= 0 || in.entityID <= 0 {
		return ErrInvalid
	}
	if err = checkBasicInputAtPhase(ctx, turn, s, 1); err != nil {
		return err
	}
	leaf, err := readBasicSoleLeaf(ctx, turn, s)
	if err != nil {
		return err
	}
	if leaf != in.entityID {
		return ErrConflict
	}
	row, err := basicStageRead(ctx, turn, basicEntityCAS, in.entityID, in.changeRevision+1, s.changeID, s.assetID, s.assetToken, s.expectedEpoch, s.inputRevision, s.witnessFingerprint[:], in.changeRevision, in.shapeID, in.entityID)
	if err != nil {
		return err
	}
	var phase, hasAfter, after, family, revision int64
	if err = row.Scan(&phase, &hasAfter, &after, &family, &revision); err != nil {
		return basicStageError(err)
	}
	if phase != 2 || hasAfter != 1 || after != in.entityID || family != 0 || revision != in.changeRevision+1 {
		return ErrConflict
	}
	// Pre-fence the sole leaf before any copy: settle and publish require it.
	row, err = basicStageRead(ctx, turn, basicEntityLink, s.changeID, in.entityID, s.assetID, s.libraryID)
	if err != nil {
		return err
	}
	var entity, asset, pending int64
	if err = row.Scan(&entity, &asset, &pending); err != nil {
		return basicStageError(err)
	}
	if entity != in.entityID || asset != s.assetID || pending != s.changeID {
		return ErrConflict
	}
	return nil
}

// Exactly the nine FIRST families 1..9, each sealed at its exact staged count.
func checkBasicFamiliesSealed(ctx context.Context, turn *commandTurn, change int64) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	rows, err := basicRows(ctx, turn, basicFreezeSealed, change)
	if err != nil {
		return err
	}
	defer func() {
		if e := rows.Close(); err == nil {
			err = e
		}
	}()
	want := int64(1)
	for rows.Next() {
		var family, expected, staged, sealed int64
		if err = rows.Scan(&family, &expected, &staged, &sealed); err != nil {
			return basicStageError(err)
		}
		if family != want || expected < 0 || staged != expected || sealed != 1 {
			return ErrConflict
		}
		want++
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if want != 10 {
		return ErrConflict
	}
	return nil
}
