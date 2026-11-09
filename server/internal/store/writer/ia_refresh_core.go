package writer

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// runIARefresh is a private SAME-TURN component, not a runtime handler or an
// Applied API. One Step journals/mutates one typed row; one Rollback restores
// one journalled key. IA21/22 add bounded keyset walks and dependency order;
// IA23 alone may complete the family cohort and clear its publication fence.
// No function here clears a fence, retires a journal, refunds ACK debt or starts
// another input. A source retry must allocate a new input after full restore.
func runIARefresh(ctx context.Context, turn *commandTurn, q iaRefreshRequest, p *usOwnerAuthorityProof, lifetime context.Context, b iaEntryBudget) (out iaRefreshOutcome, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	entry, e := enterIARefresh(ctx, turn, q, p, lifetime, b)
	if e != nil {
		return out, e
	}
	if entry.decision == receiptRetired {
		return iaRefreshOutcome{decision: receiptRetired}, nil
	}
	if entry.decision != receiptFresh {
		return iaRefreshOutcome{entry.decision, entry.saved, entry.digest}, nil
	}
	now, e := entry.checkIARefreshCurrent(ctx, turn)
	if e != nil {
		return out, e
	}
	if q.purpose == IARefreshBegin {
		s := q.scope
		if e = iaRefreshNoRows(ctx, turn, iaRefreshInputUsed, s.changeID, s.assetID, s.token, s.epoch, s.inputRevision, s.fingerprint[:]); e != nil {
			return out, e
		}
		if e = iaRefreshExec(ctx, turn, iaRefreshFence, s.assetID, s.token, s.libraryID, s.epoch); e != nil {
			return out, e
		}
		row, e := basicStageRead(ctx, turn, iaRefreshBirth, s.changeID, s.runID, q.reference.Identity.Incarnation[:], s.assetID, s.token, s.epoch, s.inputRevision, s.fingerprint[:], "copying", int64(1))
		if e != nil {
			return out, e
		}
		if e = row.Scan(&entry.refreshID); e != nil {
			return out, basicCopyInsertError(e)
		}
	} else {
		if q.purpose == IARefreshStep && entry.state != "copying" || q.purpose == IARefreshRollback && entry.state != "copying" && entry.state != "restoring" {
			return out, ErrConflict
		}
		if e = entry.changeIARefreshRow(ctx, turn); e != nil {
			return out, e
		}
	}
	// Current owner/source/lease/fence are checked again AFTER every mutation,
	// before its receipt. This also makes a late same-turn denial roll back the
	// first capture, canonical write, MP invalidation and debt together.
	if _, e = entry.checkIARefreshCurrent(ctx, turn); e != nil {
		return out, e
	}
	out.result = iaRefreshResult{refreshID: entry.refreshID, revision: entry.revision + 1, appliedMS: now, state: entry.state, rows: entry.rows + 1}
	if q.purpose != IARefreshBegin {
		out.result.family = int64(q.key.family)
		out.result.key = q.key.cursor()
	}
	// Receipt debt is a fixed160 bytes plus exact kind/state/key text bytes.
	receiptBytes := int64(160 + len(iaRefreshKind(q.purpose)) + len(out.result.state) + len(out.result.key))
	out.result.bytes = entry.bytes + receiptBytes
	if e = entry.chargeIARefresh(ctx, turn, receiptBytes); e != nil {
		return iaRefreshOutcome{}, e
	}
	out.digest, e = iaRefreshResultDigest(q, out.result)
	if e != nil {
		return iaRefreshOutcome{}, e
	}
	id := q.reference.Identity
	if e = iaRefreshExec(ctx, turn, iaRefreshAdvance, out.result.revision, out.result.state, id.Sequence, out.result.family, out.result.key, out.result.rows, out.result.bytes, entry.refreshID, id.Incarnation[:], entry.revision, entry.originalState(), entry.owner.lastSequence, entry.owner.acknowledgedSequence, entry.originalRows(), entry.originalBytes()); e != nil {
		return iaRefreshOutcome{}, e
	}
	if e = iaRefreshExec(ctx, turn, iaRefreshReceipt, entry.refreshID, id.Incarnation[:], id.Sequence, id.OperationID[:], q.reference.RequestDigest[:], iaRefreshKind(q.purpose), out.result.revision, now, out.result.state, out.result.family, out.result.key, out.result.rows, out.result.bytes, out.digest[:]); e != nil {
		return iaRefreshOutcome{}, e
	}
	if _, e = entry.checkIARefreshCurrent(ctx, turn); e != nil {
		return iaRefreshOutcome{}, e
	}
	out.decision = receiptFresh
	return out, nil
}

// These frozen entry facts stay separate from the growing debt/state in this
// turn. The owner CAS must match the pre-turn row, not its newly computed result.
func (e *iaRefreshEntry) originalState() string {
	if e.request.purpose == IARefreshBegin {
		return "copying"
	}
	return e.beforeState
}
func (e *iaRefreshEntry) originalRows() int64  { return e.beforeRows }
func (e *iaRefreshEntry) originalBytes() int64 { return e.beforeBytes }
func iaRefreshExec(ctx context.Context, turn *commandTurn, id stmt.ID, args ...any) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	step, ok := turn.steps[id]
	if !ok {
		return invalid("unregistered_ia_refresh_step")
	}
	r, err := turn.exec(ctx, step.statementStep, args...)
	if err != nil {
		return basicCopyInsertError(err)
	}
	n, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}

func (e *iaRefreshEntry) chargeIARefresh(ctx context.Context, turn *commandTurn, n int64) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if n <= 0 || e.bytes > maxCanonicalResultBytes-n {
		return &Refusal{Code: RefusalCapacity}
	}
	row, err := basicStageRead(ctx, turn, basicStageBudget, int64(1))
	if err != nil {
		return err
	}
	var used, cap int64
	if err = row.Scan(&used, &cap); err != nil {
		return basicStageError(err)
	}
	if used < 0 || cap < 0 || used > cap || n > cap-used {
		return &Refusal{Code: RefusalCapacity}
	}
	row, err = basicStageRead(ctx, turn, basicStageBudgetCAS, used+n, int64(1), used, cap)
	if err != nil {
		return err
	}
	var got int64
	if err = row.Scan(&got); err != nil {
		return basicStageError(err)
	}
	if got != used+n {
		return ErrConflict
	}
	return nil
}

func iaRefreshReadImage(ctx context.Context, turn *commandTurn, e *iaRefreshEntry, old bool) (image iaRefreshImage, written int64, digest [32]byte, exists bool, err error) {
	defer func() {
		v, x := finishCommand(ctx, commandLifetime(turn), struct {
			Image   iaRefreshImage
			Written int64
			Digest  [32]byte
			Exists  bool
		}{image, written, digest, exists}, err)
		image, written, digest, exists, err = v.Image, v.Written, v.Digest, v.Exists, x
	}()
	k := e.request.key
	args := k.args()
	op, bytesOp := "current", "bytes"
	if old {
		op, bytesOp = "old", "old_bytes"
		args = append([]any{e.refreshID}, args...)
	}
	if err = e.authority.budget.check(ctx, turn, iaRefreshRowCap); err != nil {
		return
	}
	row, x := basicStageRead(ctx, turn, iaRefreshID(k.family, bytesOp), args...)
	if x != nil {
		err = x
		return
	}
	var bytes int64
	var lengths []sql.NullInt64
	for _, c := range iaRefreshShapes[k.family].columns {
		if c.kind == "TEXT" {
			lengths = append(lengths, sql.NullInt64{})
		}
	}
	var lengthDest []any
	for j := range lengths {
		lengthDest = append(lengthDest, &lengths[j])
	}
	x = row.Scan(lengthDest...)
	for _, n := range lengths {
		if n.Valid {
			if n.Int64 < 0 || n.Int64 > iaRefreshRowCap-bytes {
				err = &Refusal{Code: RefusalCapacity}
				return
			}
			bytes += n.Int64
		}
	}
	if errors.Is(x, sql.ErrNoRows) {
		image = iaRefreshAbsent(k)
		return
	}
	if x != nil {
		err = x
		return
	}
	if bytes < 0 || bytes > iaRefreshRowCap {
		err = &Refusal{Code: RefusalCapacity}
		return
	}
	row, x = basicStageRead(ctx, turn, iaRefreshID(k.family, op), args...)
	if x != nil {
		err = x
		return
	}
	image.values = make([]any, len(iaRefreshShapes[k.family].columns))
	var dest []any
	var present int64
	var saved []byte
	if old {
		dest = []any{&present, &written, &saved}
	}
	for j := range image.values {
		dest = append(dest, &image.values[j])
	}
	if x = row.Scan(dest...); x != nil {
		err = basicStageError(x)
		return
	}
	exists = true
	image.present = true
	if old {
		if present != 0 && present != 1 || written <= 0 || written > e.revision || len(saved) != 32 {
			err = ErrResolveRequired
			return
		}
		image.present = present == 1
		copy(digest[:], saved)
	}
	// Verify the variable-byte charge on the SAME transaction's actual payload.
	var actual int64
	for _, v := range image.values {
		if t, ok := v.(string); ok {
			actual += int64(len(t))
		}
	}
	if actual != bytes {
		err = ErrResolveRequired
		return
	}
	_, _, err = iaRefreshImageDigest(k, image)
	return
}

func (e *iaRefreshEntry) changeIARefreshRow(ctx context.Context, turn *commandTurn) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	k := e.request.key
	s := e.request.scope
	if k.family <= 5 && k.token != s.token {
		return ErrConflict
	}
	current, _, _, _, err := iaRefreshReadImage(ctx, turn, e, false)
	if err != nil {
		return err
	}
	if k.family == 7 && current.present && current.values[1] != s.token {
		return ErrConflict
	}
	currentDigest, _, err := iaRefreshImageDigest(k, current)
	if err != nil {
		return err
	}
	old, written, postDigest, hasOld, err := iaRefreshReadImage(ctx, turn, e, true)
	if err != nil {
		return err
	}
	if hasOld && currentDigest != postDigest {
		return ErrConflict
	} // never clobber a newer publication
	var next iaRefreshImage
	if e.request.purpose == IARefreshRollback {
		if !hasOld {
			return ErrConflict
		}
		next = old
		e.state = "restoring"
	} else {
		next = e.desired
	}
	if k.family == 7 && next.present && next.values[1] != s.token {
		return ErrConflict
	}
	nextDigest, _, err := iaRefreshImageDigest(k, next)
	if err != nil {
		return err
	}
	if !hasOld {
		_, debt, x := iaRefreshImageDigest(k, current)
		if x != nil {
			return x
		}
		if err = e.chargeIARefresh(ctx, turn, debt); err != nil {
			return err
		}
		present := int64(0)
		if current.present {
			present = 1
		}
		args := []any{e.refreshID, present, e.revision, currentDigest[:]}
		args = append(args, current.values...)
		if err = iaRefreshExec(ctx, turn, iaRefreshID(k.family, "capture"), args...); err != nil {
			return err
		}
		e.rows++
		e.bytes += debt
		written = e.revision
		postDigest = currentDigest
	}
	// No parent delete can cascade away an uncaptured child. Shared sidecars
	// cannot change other assets' accepted rows or be unlinked while referenced.
	if current.present && !next.present && (k.family == 1 || k.family == 3 || k.family == 5) {
		if err = iaRefreshNoRows(ctx, turn, iaRefreshID(k.family, "children"), s.token); err != nil {
			return err
		}
	}
	if k.family == 6 && currentDigest != nextDigest {
		if err = iaRefreshNoRows(ctx, turn, iaRefreshShared, k.token, s.token); err != nil {
			return err
		}
		if !next.present {
			if err = iaRefreshNoRows(ctx, turn, iaRefreshReferences, k.token); err != nil {
				return err
			}
		}
	}
	if currentDigest != nextDigest {
		spec := iaRefreshShapes[k.family]
		switch {
		case !next.present:
			err = iaRefreshExec(ctx, turn, iaRefreshID(k.family, "delete"), k.args()...)
		case !current.present:
			err = iaRefreshExec(ctx, turn, iaRefreshID(k.family, "insert"), next.values...)
		default:
			args := append(append([]any(nil), next.values[spec.keys:]...), k.args()...)
			err = iaRefreshExec(ctx, turn, iaRefreshID(k.family, "update"), args...)
		}
		if err != nil {
			return err
		}
		if err = iaRefreshExec(ctx, turn, iaRefreshInvalidate, e.assetRevisions[0]+1, e.assetRevisions[1]+1, e.assetRevisions[2]+1, s.assetID, s.token, s.epoch, e.assetRevisions[0], e.assetRevisions[1], e.assetRevisions[2]); err != nil {
			return err
		}
	}
	// Post-write digest is recomputed from SQLite, not trusted from the caller.
	got, _, _, _, err := iaRefreshReadImage(ctx, turn, e, false)
	if err != nil {
		return err
	}
	gotDigest, _, err := iaRefreshImageDigest(k, got)
	if err != nil {
		return err
	}
	if gotDigest != nextDigest {
		return ErrConflict
	}
	args := []any{e.revision + 1, nextDigest[:], e.refreshID}
	args = append(args, k.args()...)
	args = append(args, written, postDigest[:])
	return iaRefreshExec(ctx, turn, iaRefreshID(k.family, "written"), args...)
}
func iaRefreshNoRows(ctx context.Context, turn *commandTurn, id stmt.ID, args ...any) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	row, err := basicStageRead(ctx, turn, id, args...)
	if err != nil {
		return err
	}
	var v int64
	err = row.Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return ErrConflict
}
