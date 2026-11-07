package writer

import (
	"context"
	"errors"
	"reflect"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Complete immutable family prepared OUTSIDE SQL HOLD. The trusted Basic
// preparation owns funding/lifetime before this constructor; it grants none.
type basicMediaFamily struct {
	source                  basicStreamFactStage
	family, rawChapterCount int64
	rows                    []basicMediaRow
	valid                   bool
}

func prepareBasicMediaFamily(source basicStreamFactStage, family, rawChapters int64, rows []basicMediaRow) (*basicMediaFamily, error) {
	limit := basicMediaFamilyLimit(family)
	if limit == 0 || rawChapters < 0 {
		return nil, ErrInvalid
	}
	if int64(len(rows)) > limit || rawChapters > MaxRawChapters {
		return nil, ErrCap
	}
	if family == 5 && int64(len(rows)) > rawChapters {
		return nil, ErrInvalid
	}
	if (family == 4 || family == 6 || family == 9) && len(rows) != 1 {
		return nil, ErrInvalid
	}
	// Complete preflight before slice, clones, args, maps or fingerprint work.
	remaining := int64(33554432)
	for _, s := range [...]string{source.assetToken, source.runID, source.sourceID, source.incarnation, source.objectID, source.objectRevision, source.fingerprint} {
		if int64(len(s)) > remaining {
			return nil, ErrCap
		}
		remaining -= int64(len(s))
	}
	for _, row := range rows {
		texts, f, _, base, err := basicMediaTextFields(source, row)
		if err != nil || f != family {
			return nil, ErrInvalid
		}
		if base > remaining {
			return nil, ErrCap
		}
		remaining -= base
		for _, s := range texts {
			if int64(len(s)) > remaining {
				return nil, ErrCap
			}
			remaining -= int64(len(s))
		}
	}
	// Check cross-row source geometry after full admission but before clones.
	keys := make(map[any]bool, len(rows))
	paths := make(map[string]bool, len(rows))
	lastEnd := float64(0)
	for n, row := range rows {
		var key any
		switch r := row.(type) {
		case basicStreamRow:
			key = r.index
		case basicStreamBreadth:
			key = r.index
		case basicChapter:
			key = r.index
			if n > 0 && r.start < lastEnd {
				return nil, ErrInvalid
			}
			lastEnd = r.end
		case basicSubtitleSidecar:
			key = r.id
			if paths[r.path] {
				return nil, ErrInvalid
			}
			paths[r.path] = true
		case basicSubtitle:
			key = r.id
			pair := struct{ origin, locator string }{r.origin, r.locator}
			if keys[pair] {
				return nil, ErrInvalid
			}
			keys[pair] = true
		default:
			key = n
		}
		if keys[key] {
			return nil, ErrInvalid
		}
		keys[key] = true
	}
	if family == 2 {
		shape := ScanShape{LibraryID: source.libraryID, Streams: make([]Stream, len(rows))}
		for n, row := range rows {
			r := row.(basicStreamRow)
			shape.Streams[n] = Stream{int(r.index), basicStreamClass(r.streamType)}
		}
		if err := ValidateScanShape(shape); err != nil {
			return nil, err
		}
	}
	for n, row := range rows {
		if f, _, _, _, err := basicMediaValues(source, int64(n), row); err != nil {
			return nil, err
		} else if f != family {
			return nil, ErrInvalid
		}
	}
	source.assetToken = strings.Clone(source.assetToken)
	source.runID = strings.Clone(source.runID)
	source.sourceID = strings.Clone(source.sourceID)
	source.incarnation = strings.Clone(source.incarnation)
	source.objectID = strings.Clone(source.objectID)
	source.objectRevision = strings.Clone(source.objectRevision)
	source.fingerprint = strings.Clone(source.fingerprint)
	out := &basicMediaFamily{source: source, family: family, rawChapterCount: rawChapters, rows: make([]basicMediaRow, len(rows)), valid: true}
	for n, row := range rows {
		out.rows[n] = cloneBasicMediaRow(row)
	}
	return out, nil
}

func checkBasicMediaFamily(ctx context.Context, turn *commandTurn, in *basicMediaFamily, wantStaged int64) (err error) {
	if in == nil || !in.valid || wantStaged < 0 || wantStaged > int64(len(in.rows)) {
		return ErrInvalid
	}
	if err = checkBasicStreamInput(ctx, turn, in.source); err != nil {
		return err
	}
	// The base probe witness alone does not authorize an unmasked family.
	row, err := basicStageRead(ctx, turn, basicStageHeader, in.source.changeID)
	if err != nil {
		return err
	}
	var asset, slot, phase, epoch, revision, mask, aux int64
	var token, fault string
	var fingerprint []byte
	if err = row.Scan(&asset, &token, &slot, &phase, &epoch, &revision, &mask, &fingerprint, &aux, &fault); err != nil {
		return basicStageError(err)
	}
	if mask&(1<<(in.family-1)) == 0 {
		return ErrConflict
	}
	row, err = basicStageRead(ctx, turn, basicStageFamily, in.source.changeID, in.family)
	if err != nil {
		return err
	}
	var witness, expected, staged, sealed int64
	if err = row.Scan(&witness, &expected, &staged, &sealed); err != nil {
		return basicStageError(err)
	}
	if witness != 0 || expected != int64(len(in.rows)) || staged != wantStaged || sealed != 0 {
		return ErrConflict
	}
	return nil
}

// stageBasicMediaRows inserts at most8 raw rows. Exact debt + rows + count CAS
// are ONE caller transaction; all errors require WHOLE command rollback.
func stageBasicMediaRows(ctx context.Context, turn *commandTurn, in *basicMediaFamily, start, end int64) (value basicFamilyProgress, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if ctx == nil || turn == nil || turn.transaction == nil || in == nil || !in.valid || start < 0 || end <= start || end-start > 8 || end > int64(len(in.rows)) {
		return value, ErrInvalid
	}
	if err = checkBasicMediaFamily(ctx, turn, in, start); err != nil {
		return value, err
	}
	var charge int64
	for n := start; n < end; n++ {
		_, _, _, c, e := basicMediaValues(in.source, n, in.rows[n])
		if e != nil {
			return value, e
		}
		charge += c
	}
	row, err := basicStageRead(ctx, turn, basicStageBudget, int64(1))
	if err != nil {
		return value, err
	}
	var used, cap int64
	if err = row.Scan(&used, &cap); err != nil {
		return value, basicStageError(err)
	}
	if cap != 33554432 || used < 0 || used > cap || charge > cap-used {
		return value, ErrCap
	}
	row, err = basicStageRead(ctx, turn, basicStageBudgetCAS, used+charge, int64(1), used, cap)
	if err != nil {
		return value, err
	}
	var actual int64
	if err = row.Scan(&actual); err != nil {
		return value, basicStageError(err)
	}
	if actual != used+charge {
		return value, ErrConflict
	}
	for n := start; n < end; n++ {
		_, id, args, _, e := basicMediaValues(in.source, n, in.rows[n])
		if e != nil {
			return value, e
		}
		step, ok := turn.steps[id]
		if !ok {
			return value, invalid("unregistered_basic_media_step")
		}
		rows, e := turn.query(ctx, step.statementStep, append([]any{in.source.changeID}, args...)...)
		if e != nil {
			return value, e
		}
		if e = basicMediaExactRow(rows, args); e != nil {
			return value, e
		}
	}
	row, err = basicStageRead(ctx, turn, basicStageFamilyCAS, end, in.source.changeID, in.family, int64(0), int64(len(in.rows)), start, int64(0))
	if err != nil {
		return value, err
	}
	value = basicFamilyProgress{changeID: in.source.changeID, inputRevision: in.source.inputRevision}
	if err = row.Scan(&value.rowsStaged, &value.sealed); err != nil {
		return value, basicStageError(err)
	}
	if value.rowsStaged != end || value.sealed != 0 {
		return value, ErrConflict
	}
	return value, nil
}

// sealBasicMediaFamily proves the COMPLETE bounded family, including empty
// changed sets. Budget the entire LIMIT+1 native read; never accept a prefix.
func sealBasicMediaFamily(ctx context.Context, turn *commandTurn, in *basicMediaFamily) (value basicFamilyProgress, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if ctx == nil || turn == nil || turn.transaction == nil || in == nil || !in.valid {
		return value, ErrInvalid
	}
	if err = checkBasicMediaFamily(ctx, turn, in, int64(len(in.rows))); err != nil {
		return value, err
	}
	id := basicMediaRowsID(in.family)
	step, ok := turn.steps[id]
	if !ok {
		return value, invalid("unregistered_basic_media_step")
	}
	rows, err := turn.query(ctx, step.statementStep, in.source.changeID, basicMediaFamilyLimit(in.family)+1)
	if err != nil {
		return value, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	var n int64
	for rows.Next() {
		if n >= int64(len(in.rows)) {
			return value, ErrConflict
		}
		_, _, args, _, e := basicMediaValues(in.source, n, in.rows[n])
		if e != nil {
			return value, e
		}
		if e = basicMediaScan(rows, args); e != nil {
			return value, e
		}
		n++
	}
	if err = rows.Err(); err != nil {
		return value, err
	}
	if n != int64(len(in.rows)) {
		return value, ErrConflict
	}
	if err = rows.Close(); err != nil {
		return value, err
	}
	row, err := basicStageRead(ctx, turn, basicSealFamilyCAS, int64(1), in.source.changeID, in.family, int64(0), n, n, int64(0))
	if err != nil {
		return value, err
	}
	value = basicFamilyProgress{changeID: in.source.changeID, inputRevision: in.source.inputRevision}
	if err = row.Scan(&value.rowsStaged, &value.sealed); err != nil {
		return value, basicStageError(err)
	}
	if value.rowsStaged != n || value.sealed != 1 {
		return value, ErrConflict
	}
	return value, nil
}

func basicMediaScan(rows *commandRows, want []any) error {
	var got [32]any
	var dest [32]any
	if len(want) > len(got) {
		return ErrInvalid
	}
	for n := range want {
		dest[n] = &got[n]
	}
	if err := rows.Scan(dest[:len(want)]...); err != nil {
		return err
	}
	for n := range want {
		if !reflect.DeepEqual(got[n], want[n]) {
			return ErrConflict
		}
	}
	return nil
}
func basicMediaExactRow(rows *commandRows, want []any) (err error) {
	defer func() { err = errors.Join(err, rows.Close()) }()
	if !rows.Next() {
		return errors.Join(ErrConflict, rows.Err())
	}
	if err = basicMediaScan(rows, want); err != nil {
		return err
	}
	if rows.Next() {
		return ErrConflict
	}
	return rows.Err()
}

func basicRows(ctx context.Context, turn *commandTurn, id stmt.ID, args ...any) (*commandRows, error) {
	step, ok := turn.steps[id]
	if !ok {
		return nil, invalid("unregistered_basic_step")
	}
	return turn.query(ctx, step.statementStep, args...)
}
