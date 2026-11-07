package writer

import (
	"context"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Inert captured rows, never authority. The caller owns phase5 retirement,
// admission and the durable cursor in this SAME turn, and must roll back the
// whole turn on error. Retire children (notably family8 before7) first; native
// foreign keys remain authoritative. No manifest, header or witness is removed.
type basicMediaReclaimInput struct {
	family                                 *basicMediaFamily
	start, end, jobRevision, targetShapeID int64
}
type basicMediaReclaimResult struct{ deleted, refunded int64 }

func reclaimBasicMediaRows(ctx context.Context, turn *commandTurn, in basicMediaReclaimInput) (deleted, refunded int64, err error) {
	defer func() {
		v, e := finishCommand(ctx, commandLifetime(turn), basicMediaReclaimResult{deleted, refunded}, err)
		deleted, refunded, err = v.deleted, v.refunded, e
	}()
	f := in.family
	if ctx == nil || turn == nil || turn.transaction == nil || f == nil || !f.valid || f.family < 2 || f.family > 9 || in.start < 0 || in.end <= in.start || in.end-in.start > 8 || in.end > int64(len(f.rows)) || in.jobRevision <= 0 || in.targetShapeID <= 0 {
		return 0, 0, ErrInvalid
	}
	s := f.source
	row, err := basicStageRead(ctx, turn, basicReclaimMediaHeader, s.changeID, s.assetID, s.assetToken, s.expectedEpoch, s.inputRevision, s.witnessFingerprint[:], in.jobRevision, in.targetShapeID)
	if err != nil {
		return 0, 0, err
	}
	var id int64
	if err = row.Scan(&id); err != nil {
		return 0, 0, basicStageError(err)
	}
	if id != s.changeID {
		return 0, 0, ErrConflict
	}
	var charge int64
	for n := in.start; n < in.end; n++ {
		family, _, _, c, e := basicMediaValues(s, n, f.rows[n])
		if e != nil {
			return 0, 0, e
		}
		if family != f.family {
			return 0, 0, ErrInvalid
		}
		if c < 0 || c > 33554432-charge {
			return 0, 0, ErrCap
		}
		charge += c
	}
	for n := in.start; n < in.end; n++ {
		_, _, args, _, e := basicMediaValues(s, n, f.rows[n])
		if e != nil {
			return 0, 0, e
		}
		want := append([]any{s.changeID}, args...)
		rows, e := basicRows(ctx, turn, basicReclaimMediaID(f.family), want...)
		if e != nil {
			return 0, 0, e
		}
		if e = basicMediaExactRow(rows, want); e != nil {
			return 0, 0, e
		}
	}
	// Refund only physical deletions, never an acknowledgement. Exact old debt
	// and cap participate in the CAS; any failure requires caller rollback.
	row, err = basicStageRead(ctx, turn, basicStageBudget, int64(1))
	if err != nil {
		return 0, 0, err
	}
	var used, cap int64
	if err = row.Scan(&used, &cap); err != nil {
		return 0, 0, basicStageError(err)
	}
	if cap != 33554432 || used < charge || used > cap {
		return 0, 0, ErrConflict
	}
	row, err = basicStageRead(ctx, turn, basicStageBudgetCAS, used-charge, int64(1), used, cap)
	if err != nil {
		return 0, 0, err
	}
	var actual int64
	if err = row.Scan(&actual); err != nil {
		return 0, 0, basicStageError(err)
	}
	if actual != used-charge {
		return 0, 0, ErrConflict
	}
	return in.end - in.start, charge, nil
}
func basicReclaimMediaID(family int64) stmt.ID {
	switch family {
	case 2:
		return "scan.basic_reclaim.catalog_info_new_streams"
	case 3:
		return "scan.basic_reclaim.catalog_info_new_stream_breadth"
	case 4:
		return "scan.basic_reclaim.catalog_info_new_chapter_facts"
	case 5:
		return "scan.basic_reclaim.catalog_info_new_chapters"
	case 6:
		return "scan.basic_reclaim.catalog_info_new_subtitle_facts"
	case 7:
		return "scan.basic_reclaim.catalog_info_new_subtitle_sidecars"
	case 8:
		return "scan.basic_reclaim.catalog_info_new_subtitles"
	case 9:
		return "scan.basic_reclaim.catalog_info_new_subtitle_inventory_evidence"
	}
	return ""
}
