package writer

import (
	"context"
	"database/sql"
	"errors"
)

// FIRST cannot begin a replacement of an already published link. Refuse before
// allocating staging debt or setting either pending fence, so an unsupported
// refresh cannot strand a previously usable asset behind a new publication.
func checkBasicBeginFirst(ctx context.Context, turn *commandTurn, source basicStreamFactStage) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	row, err := basicStageRead(ctx, turn, basicBeginFirstAsset, source.assetID, source.assetToken, source.libraryID, source.expectedEpoch)
	if err != nil {
		return err
	}
	var id int64
	if err = row.Scan(&id); errors.Is(err, sql.ErrNoRows) {
		return &Refusal{Code: RefusalUnqualified}
	}
	if err != nil {
		return err
	}
	if id != source.assetID {
		return ErrConflict
	}
	rows, err := basicRows(ctx, turn, basicBeginFirstOwners, source.assetID)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	if !rows.Next() {
		if err = rows.Err(); err != nil {
			return err
		}
		return &Refusal{Code: RefusalUnqualified}
	}
	var entity, library, kind, retired, subject, subjectLibrary, subjectKind int64
	var shape, pending sql.NullInt64
	var duration float64
	if err = rows.Scan(&entity, &library, &shape, &duration, &pending, &kind, &retired, &subject, &subjectLibrary, &subjectKind); err != nil {
		return err
	}
	if entity <= 0 || library != source.libraryID || shape.Valid || duration != 0 || pending.Valid || kind != 1 || retired != 0 || subject != entity || subjectLibrary != library || subjectKind != kind {
		return &Refusal{Code: RefusalUnqualified}
	}
	if rows.Next() {
		return &Refusal{Code: RefusalUnqualified}
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	assets, e := basicRows(ctx, turn, basicBeginFirstEntityAssets, entity)
	if e != nil {
		return e
	}
	if e = basicMediaExactRow(assets, []any{source.assetID}); e != nil {
		if errors.Is(e, ErrConflict) {
			return &Refusal{Code: RefusalUnqualified}
		}
		return e
	}
	return nil
}
