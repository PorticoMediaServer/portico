package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"unicode/utf8"
)

// The asset state the probe left behind, re-read by the producer: nothing in
// the database records it. Ignored once the header carries the abandon marker.
type basicAbandonExpectation struct {
	after                              basicProbeScalars
	playbackRevision, preparedRevision int64
}

func validateBasicAbandonRequest(in basicControlRequest, purpose BasicPurpose) error {
	a := in.abandon
	if purpose != BasicPurposeStage || a == nil || in.probe != nil || in.family != 0 || in.rawChapters != 0 || in.rows != nil || in.start != 0 || in.end != 0 ||
		in.entityID != 0 || in.shapeID != 0 || in.copy != (basicCopyCursor{}) || in.changeRevision <= 0 || in.changeRevision == math.MaxInt64 ||
		a.playbackRevision <= 0 || a.preparedRevision <= 0 ||
		!utf8.ValidString(a.after.container) || !utf8.ValidString(a.after.videoCodec) || !utf8.ValidString(a.after.audioCodec) {
		return ErrInvalid
	}
	return nil
}

// One abandon turn of a FIRST input whose copy has not started. Copy commits
// canonical rows turn by turn and nothing captures the old ones, so once it
// starts the walk must resume from its cursor: clearing the fences then would
// show new rows under old scalars. The first turn restores the probed scalars,
// clears both fences, releases the pin's shape ref and sets the marker, all in
// one transaction. Every later turn only reclaims staged rows, and the header
// goes last. removed reports that it has.
func abandonBasicInput(ctx context.Context, turn *commandTurn, in basicControlRequest) (removed bool, err error) {
	defer func() { removed, err = finishCommand(ctx, commandLifetime(turn), removed, err) }()
	s := in.source
	row, err := basicStageRead(ctx, turn, basicAbandonHeader, s.changeID, s.assetID, s.assetToken, s.expectedEpoch, s.inputRevision, s.witnessFingerprint[:])
	if err != nil {
		return false, err
	}
	var h basicAbandonHeaderState
	if err = row.Scan(&h.phase, &h.aux, &h.table, &h.copyPhase, &h.copyID, &h.key, &h.key2, &h.key3, &h.revision, &h.shape, &h.hasAfter, &h.afterEntity); err != nil {
		return false, basicStageError(err)
	}
	switch {
	case h.aux == -1:
		return reclaimBasicAbandoned(ctx, turn, s, h)
	case h.beforeCopy():
		if h.revision != in.changeRevision {
			return false, ErrConflict
		}
		return false, restoreBasicAbandoned(ctx, turn, s, *in.abandon, h)
	}
	return false, ErrConflict
}

type basicAbandonHeaderState struct {
	phase, aux, table, copyPhase, copyID, revision, hasAfter, afterEntity int64
	key, key2, key3                                                       string
	shape                                                                 sql.NullInt64
}

// Entity leaves phase 2 at the zero cursor; the first copy turn moves it.
func (h basicAbandonHeaderState) beforeCopy() bool {
	if h.aux < 0 || h.phase < 0 || h.phase > 2 {
		return false
	}
	return h.phase < 2 || (h.table == 0 && h.copyPhase == 0 && h.copyID == 0 && h.aux == 0 && h.key == "" && h.key2 == "" && h.key3 == "")
}

func restoreBasicAbandoned(ctx context.Context, turn *commandTurn, s basicStreamFactStage, a basicAbandonExpectation, h basicAbandonHeaderState) error {
	if h.revision == math.MaxInt64 || (h.phase >= 2 && (h.hasAfter != 1 || h.afterEntity <= 0)) {
		return ErrConflict
	}
	target, playback, prepared, captured, err := basicAbandonRestoreTarget(ctx, turn, s, a)
	if err != nil {
		return err
	}
	if captured {
		if err = writeBasicAbandonRestore(ctx, turn, s, a, target, playback, prepared); err != nil {
			return err
		}
	}
	return clearBasicAbandoned(ctx, turn, s, h)
}

// A mismatch is ErrConflict and is not terminal: the producer re-reads the
// asset row for its expectation, so a retry compares against the current state.
func writeBasicAbandonRestore(ctx context.Context, turn *commandTurn, s basicStreamFactStage, a basicAbandonExpectation, target basicProbeScalars, playback, prepared int64) error {
	args := []any{target.container, target.videoCodec, target.audioCodec, target.width, target.height, target.duration, playback, prepared,
		s.assetID, s.assetToken, s.libraryID, s.expectedEpoch,
		a.after.container, a.after.videoCodec, a.after.audioCodec, a.after.width, a.after.height, a.after.duration, a.playbackRevision, a.preparedRevision}
	row, err := basicStageRead(ctx, turn, basicAbandonRestore, args...)
	if err != nil {
		return err
	}
	var id, gotPlayback, gotPrepared int64
	var got basicProbeScalars
	if err = row.Scan(&id, &got.container, &got.videoCodec, &got.audioCodec, &got.width, &got.height, &got.duration, &gotPlayback, &gotPrepared); err != nil {
		return basicStageError(err)
	}
	if id != s.assetID || got != target || gotPlayback != playback || gotPrepared != prepared {
		return ErrConflict
	}
	return nil
}

func clearBasicAbandoned(ctx context.Context, turn *commandTurn, s basicStreamFactStage, h basicAbandonHeaderState) error {
	var id int64
	if h.phase >= 1 {
		row, err := basicStageRead(ctx, turn, basicAbandonAsset, s.assetID, s.assetToken, s.libraryID, s.expectedEpoch)
		if err != nil {
			return err
		}
		var pending int64
		if err = row.Scan(&id, &pending); err != nil {
			return basicStageError(err)
		}
		if id != s.assetID || pending != 0 {
			return ErrConflict
		}
	}
	if h.phase >= 2 {
		row, err := basicStageRead(ctx, turn, basicAbandonLink, nil, h.afterEntity, s.assetID, s.libraryID, s.changeID)
		if err != nil {
			return err
		}
		var entity int64
		var link sql.NullInt64
		if err = row.Scan(&entity, &id, &link); err != nil {
			return basicStageError(err)
		}
		if entity != h.afterEntity || id != s.assetID || link.Valid {
			return ErrConflict
		}
	}
	// The marker is written last, so it is only ever seen after the release.
	if h.shape.Valid {
		row, err := basicStageRead(ctx, turn, basicAbandonShape, h.shape.Int64)
		if err != nil {
			return err
		}
		var refs int64
		if err = row.Scan(&id, &refs); err != nil {
			return basicStageError(err)
		}
		if id != h.shape.Int64 || refs < 0 {
			return ErrConflict
		}
	}
	row, err := basicStageRead(ctx, turn, basicAbandonMark, h.revision+1, s.changeID, s.assetID, s.assetToken, h.phase, h.revision)
	if err != nil {
		return err
	}
	var aux, table, revision int64
	if err = row.Scan(&aux, &table, &revision); err != nil {
		return basicStageError(err)
	}
	if aux != -1 || table != 0 || revision != h.revision+1 {
		return ErrConflict
	}
	return nil
}

// Only fields with an OLD row changed. Both counters advanced exactly when the
// container did (basic_probe.go), so an OLD container row is that predicate.
func basicAbandonRestoreTarget(ctx context.Context, turn *commandTurn, s basicStreamFactStage, a basicAbandonExpectation) (target basicProbeScalars, playback, prepared int64, captured bool, err error) {
	target, playback, prepared = a.after, a.playbackRevision, a.preparedRevision
	rows, err := basicRows(ctx, turn, basicAbandonOldRows, s.changeID)
	if err != nil {
		return target, 0, 0, false, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		captured = true
		var v basicFirstOldScalar
		if err = rows.Scan(&v.field, &v.kind, &v.integer, &v.real, &v.text); err != nil {
			return target, 0, 0, false, err
		}
		switch {
		case v.field == 4 && v.kind == 3 && v.text.Valid:
			target.container = v.text.String
			if playback <= 1 || prepared <= 1 {
				return target, 0, 0, false, ErrConflict
			}
			playback, prepared = playback-1, prepared-1
		case v.field == 5 && v.kind == 3 && v.text.Valid:
			target.videoCodec = v.text.String
		case v.field == 6 && v.kind == 3 && v.text.Valid:
			target.audioCodec = v.text.String
		case v.field == 7 && v.kind == 1 && v.integer.Valid:
			target.width = v.integer.Int64
		case v.field == 8 && v.kind == 1 && v.integer.Valid:
			target.height = v.integer.Int64
		case v.field == 9 && v.kind == 2 && v.real.Valid:
			target.duration = v.real.Float64
		default:
			return target, 0, 0, false, ErrConflict
		}
	}
	return target, playback, prepared, captured, rows.Err()
}

func reclaimBasicAbandoned(ctx context.Context, turn *commandTurn, s basicStreamFactStage, h basicAbandonHeaderState) (removed bool, err error) {
	if h.table < 0 || h.table > int64(len(basicAbandonTables)) || h.revision == math.MaxInt64 {
		return false, ErrConflict
	}
	table, deleted := int(h.table), 0
	var refund int64
	for table < len(basicAbandonTables) && deleted < basicAbandonRowsPerTurn {
		limit := basicAbandonRowsPerTurn - deleted
		charge, n, e := reclaimBasicAbandonedTable(ctx, turn, s, table, limit)
		if e != nil {
			return false, e
		}
		refund, deleted = refund+charge, deleted+n
		if n < limit {
			table++
		}
	}
	removed = table == len(basicAbandonTables)
	if removed {
		fixed, e := basicAbandonHeaderCharge(s)
		if e != nil {
			return false, e
		}
		refund += fixed
		row, e := basicStageRead(ctx, turn, basicAbandonDeleteRow, s.changeID, s.assetID, s.assetToken, h.table, h.revision)
		if e != nil {
			return false, e
		}
		var id int64
		if e = row.Scan(&id); e != nil {
			return false, basicStageError(e)
		}
		if id != s.changeID {
			return false, ErrConflict
		}
	} else {
		row, e := basicStageRead(ctx, turn, basicAbandonCursor, int64(table), h.revision+1, s.changeID, s.assetID, s.assetToken, h.table, h.revision)
		if e != nil {
			return false, e
		}
		var gotTable, revision int64
		if e = row.Scan(&gotTable, &revision); e != nil {
			return false, basicStageError(e)
		}
		if gotTable != int64(table) || revision != h.revision+1 {
			return false, ErrConflict
		}
	}
	if refund == 0 {
		return removed, nil
	}
	row, err := basicStageRead(ctx, turn, basicStageBudget, int64(1))
	if err != nil {
		return false, err
	}
	var used, cap int64
	if err = row.Scan(&used, &cap); err != nil {
		return false, basicStageError(err)
	}
	if cap != 33554432 || used < refund || used > cap {
		return false, ErrConflict
	}
	row, err = basicStageRead(ctx, turn, basicStageBudgetCAS, used-refund, int64(1), used, cap)
	if err != nil {
		return false, err
	}
	var actual int64
	if err = row.Scan(&actual); err != nil {
		return false, basicStageError(err)
	}
	if actual != used-refund {
		return false, ErrConflict
	}
	return removed, nil
}

// Reads up to limit rows of one table, then deletes each by its exact key.
func reclaimBasicAbandonedTable(ctx context.Context, turn *commandTurn, s basicStreamFactStage, table, limit int) (charge int64, n int, err error) {
	spec := basicAbandonTables[table]
	rows, err := basicRows(ctx, turn, basicAbandonReadID(table), s.changeID, int64(limit))
	if err != nil {
		return 0, 0, err
	}
	keys := make([]int64, 0, limit)
	texts := make([]sql.NullString, len(spec.texts))
	targets := make([]any, 0, len(spec.texts)+1)
	var key int64
	targets = append(targets, &key)
	for n := range texts {
		targets = append(targets, &texts[n])
	}
	for rows.Next() {
		if err = rows.Scan(targets...); err != nil {
			return 0, 0, errors.Join(err, rows.Close())
		}
		if len(keys) == limit {
			return 0, 0, errors.Join(ErrConflict, rows.Close())
		}
		keys = append(keys, key)
		charge += spec.base
		for _, v := range texts {
			charge += int64(len(v.String))
		}
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return 0, 0, err
	}
	for _, k := range keys {
		row, e := basicStageRead(ctx, turn, basicAbandonDeleteID(table), s.changeID, k)
		if e != nil {
			return 0, 0, e
		}
		var got int64
		if e = row.Scan(&got); e != nil {
			return 0, 0, basicStageError(e)
		}
		if got != k {
			return 0, 0, ErrConflict
		}
	}
	return charge, len(keys), nil
}

// What Begin charged for the header, its witness and the nine manifests: the
// same formula as basicRetireFirstCharge without the family-1 stream fact,
// which is refunded with its own row here.
func basicAbandonHeaderCharge(s basicStreamFactStage) (int64, error) {
	charge := int64(344 + 32 + 224 + 32 + 9*176)
	for _, v := range [...]string{s.assetToken, s.runID, s.sourceID, s.incarnation, s.objectID, s.objectRevision} {
		if int64(len(v)) > 33554432-charge {
			return 0, ErrCap
		}
		charge += int64(len(v))
	}
	return charge, nil
}
