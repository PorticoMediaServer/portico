package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"time"
)

// Intrinsic input identity is retained in the journal. Lease witnesses belong
// to the EXISTING catalog_info_changes owner, not a new scheduler or lease.
type iaRefreshScope struct {
	size, modifiedNS                                            int64
	sourceID, rootIncarnation, objectID, objectRevision         string
	sourceGeneration, policyRevision                            int64
	changeID, assetID, libraryID, epoch, inputRevision          int64
	token, runID, libraryKey                                    string
	fingerprint                                                 [32]byte
	changeRevision, leaseOwner, leaseGeneration, leaseExpiresMS int64
}
type iaRefreshRequest struct {
	purpose                     CommandID
	action                      iaEntryAction
	reference                   ReceiptReference
	scope                       iaRefreshScope
	refreshID, expectedRevision int64
	key                         iaRefreshKey
	row                         iaRefreshRecord
}
type iaRefreshEntry struct {
	desired                          iaRefreshImage
	assetRevisions                   [3]int64
	authority                        *iaEntryAuthorization
	request                          iaRefreshRequest
	decision                         receiptDecision
	refreshID, revision, rows, bytes int64
	state                            string
	owner                            receiptOwnerFacts
	saved                            iaRefreshResult
	digest                           ResultDigest
	beforeState                      string
	beforeRows, beforeBytes          int64
}
type iaRefreshOutcome struct {
	decision receiptDecision
	result   iaRefreshResult
	digest   ResultDigest
}

func enterIARefresh(ctx context.Context, turn *commandTurn, q iaRefreshRequest, p *usOwnerAuthorityProof, lifetime context.Context, b iaEntryBudget) (out *iaRefreshEntry, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	s := q.scope
	id := q.reference.Identity
	if turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != q.purpose || (q.purpose != IARefreshBegin && q.purpose != IARefreshStep && q.purpose != IARefreshRollback && q.purpose != IARefreshFinalize) ||
		(q.action != iaEntryApply && q.action != iaEntryResolve) || s.changeID <= 0 || s.assetID <= 0 || s.libraryID <= 0 || s.epoch <= 0 || s.inputRevision <= 0 || s.size < 0 || s.fingerprint == [32]byte{} ||
		!iaEntryText(s.token) || !iaEntryText(s.runID) || !iaEntryText(s.libraryKey) || q.refreshID < 0 || id.Incarnation == [16]byte{} || id.OperationID == [16]byte{} || id.Sequence <= 0 || id.Sequence == math.MaxInt64 {
		return nil, ErrInvalid
	}
	if q.purpose == IARefreshBegin {
		if q.refreshID != 0 || q.key != (iaRefreshKey{}) || q.row != nil || id.Sequence != 1 || q.expectedRevision != 0 {
			return nil, ErrInvalid
		}
	} else if q.purpose == IARefreshFinalize {
		// IA23's finish turn names the journal, never a row: the journal cursor
		// and every before-image are read from the same snapshot, not the caller.
		if q.refreshID <= 0 || q.key != (iaRefreshKey{}) || q.row != nil || q.expectedRevision <= 0 || q.expectedRevision == math.MaxInt64 {
			return nil, ErrInvalid
		}
	} else if q.refreshID <= 0 || !q.key.valid() || q.expectedRevision <= 0 || q.expectedRevision == math.MaxInt64 || q.purpose == IARefreshRollback && q.row != nil {
		return nil, ErrInvalid
	}
	if !iaEntryText(s.sourceID) || !iaEntryText(s.rootIncarnation) || !iaEntryText(s.objectID) || !iaEntryText(s.objectRevision) || s.sourceGeneration <= 0 || s.policyRevision <= 0 {
		return nil, ErrInvalid
	}
	fp, x := iaRefreshInputFingerprint(s)
	if x != nil {
		return nil, x
	}
	if fp != s.fingerprint {
		return nil, ErrConflict
	}
	if err = b.check(ctx, turn, iaRefreshRowCap); err != nil {
		return nil, err
	}
	var desired iaRefreshImage
	if q.purpose == IARefreshStep {
		var x error
		desired, x = iaRefreshReplacement(q.key, q.row)
		if x != nil {
			return nil, x
		}
	}
	digest, e := iaRefreshRequestDigestWithImage(q, desired)
	if e != nil {
		return nil, e
	}
	if digest != q.reference.RequestDigest {
		return nil, ErrConflict
	}
	auth, e := authorizeIAEntryOwner(ctx, turn, p, lifetime, b)
	if e != nil {
		return nil, e
	}
	out = &iaRefreshEntry{authority: auth, request: q, refreshID: q.refreshID, desired: desired}
	if q.purpose == IARefreshBegin {
		row, e := basicStageRead(ctx, turn, iaRefreshOriginal, s.changeID, id.Incarnation[:])
		if e != nil {
			return nil, e
		}
		if e = row.Scan(&out.refreshID); e != nil && !errors.Is(e, sql.ErrNoRows) {
			return nil, e
		}
	}
	if out.refreshID == 0 {
		if q.action == iaEntryResolve {
			return nil, ErrResolveRequired
		}
		out.owner = receiptOwnerFacts{exists: true, incarnation: id.Incarnation}
		out.state = "copying"
		out.revision = 1
	} else {
		row, e := basicStageRead(ctx, turn, iaRefreshOwner, out.refreshID, s.libraryID, s.libraryKey, s.token, s.runID)
		if e != nil {
			return nil, e
		}
		var got iaRefreshScope
		var incarnation, fingerprint []byte
		if e = row.Scan(&got.changeID, &got.runID, &incarnation, &got.assetID, &got.token, &got.epoch, &got.inputRevision, &fingerprint, &out.state, &out.revision, &out.owner.lastSequence, &out.owner.acknowledgedSequence, &out.rows, &out.bytes); e != nil {
			return nil, basicStageError(e)
		}
		if got.changeID != s.changeID || got.runID != s.runID || got.assetID != s.assetID || got.token != s.token || got.epoch != s.epoch || got.inputRevision != s.inputRevision || len(incarnation) != 16 || len(fingerprint) != 32 || [16]byte(incarnation) != id.Incarnation || [32]byte(fingerprint) != s.fingerprint || out.revision <= 0 || out.revision == math.MaxInt64 || out.rows < 0 || out.bytes < 0 || out.bytes > maxCanonicalResultBytes || out.rows > maxCanonicalResultBytes/96 {
			return nil, ErrConflict
		}
		out.owner.exists = true
		copy(out.owner.incarnation[:], incarnation)
	}
	// Namespace is retained scope, not fresh eligibility, and precedes receipt
	// disclosure. A retired library may replay under current independent owner.
	row, e := basicStageRead(ctx, turn, iaEntryNamespace, s.libraryKey, s.libraryID)
	if e != nil {
		return nil, e
	}
	var same, retired int64
	if e = row.Scan(&same, &retired); e != nil {
		return nil, basicStageError(e)
	}
	if same != 1 {
		return nil, ErrConflict
	}
	var found receiptFacts
	if out.refreshID != 0 {
		row, e = basicStageRead(ctx, turn, iaRefreshHeader, out.refreshID, id.Incarnation[:], id.Sequence)
		if e != nil {
			return nil, e
		}
		var operation, request, result []byte
		var kind string
		v := iaRefreshResult{refreshID: out.refreshID}
		e = row.Scan(&operation, &request, &kind, &v.revision, &v.appliedMS, &v.state, &v.family, &v.key, &v.rows, &v.bytes, &result)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return nil, e
		}
		if e == nil {
			if len(operation) != 16 || len(request) != 32 || len(result) != 32 || kind != iaRefreshKind(q.purpose) {
				return nil, ErrConflict
			}
			found.exists = true
			found.kind = q.purpose
			found.identity.command = id
			copy(found.identity.command.OperationID[:], operation)
			copy(found.identity.digest[:], request)
			found.resultCount = v.rows
			copy(found.resultDigest[:], result)
			out.saved = v
			copy(out.digest[:], result)
		}
		if q.purpose == IARefreshFinalize && !found.exists {
			// A 127 cleanup turn appends no receipt: its saved result lives in the
			// permanent owner's fixed slot, so the same original request replays
			// from that exact slot until ACK retires it.
			found, out.saved, out.digest, e = readIARefreshSlot(ctx, turn, out.refreshID, id)
			if e != nil {
				return nil, e
			}
		}
	}
	out.beforeState, out.beforeRows, out.beforeBytes = out.state, out.rows, out.bytes
	out.decision, err = classifyReceipt(receiptIdentity{id, q.reference.RequestDigest}, q.purpose, out.owner, found)
	if err != nil {
		return nil, err
	}
	if out.decision == receiptReplay {
		wantKey := ""
		wantFamily := int64(0)
		if q.purpose == IARefreshFinalize {
			// A finish result names the journal's own cursor (accept/restore) or the
			// retirement position (cleanup); the caller never supplies either.
			wantKey = out.saved.key
			wantFamily = out.saved.family
			if wantFamily < 0 || wantFamily > 7 || len(wantKey) > 273 {
				return nil, ErrResolveRequired
			}
		} else if q.purpose != IARefreshBegin {
			wantKey = q.key.cursor()
			wantFamily = int64(q.key.family)
		}
		if out.saved.key != wantKey || out.saved.family != wantFamily {
			return nil, ErrResolveRequired
		}
		expected, e := iaRefreshResultDigest(q, out.saved)
		if e != nil || expected != out.digest {
			return nil, ErrResolveRequired
		}
	}
	if q.action == iaEntryResolve && out.decision == receiptFresh {
		return nil, ErrResolveRequired
	}
	if out.decision == receiptFresh && (retired != 0 || q.purpose != IARefreshBegin && q.expectedRevision != out.revision) {
		return nil, ErrConflict
	}
	if err = auth.recheck(ctx, turn); err != nil {
		return nil, err
	}
	return out, nil
}
func iaRefreshKind(p CommandID) string {
	switch p {
	case IARefreshBegin:
		return "refresh_begin"
	case IARefreshStep:
		return "refresh_step"
	case IARefreshRollback:
		return "refresh_rollback"
	case IARefreshFinalize:
		return "refresh_finalize"
	}
	return ""
}
func (e *iaRefreshEntry) checkIARefreshCurrent(ctx context.Context, turn *commandTurn) (nowMS int64, err error) {
	defer func() { nowMS, err = finishCommand(ctx, commandLifetime(turn), nowMS, err) }()
	if e == nil || e.authority == nil || e.decision != receiptFresh || e.authority.turn != turn {
		return 0, ErrInvalid
	}
	if err = e.authority.recheck(ctx, turn); err != nil {
		return 0, err
	}
	s := e.request.scope
	row, x := basicStageRead(ctx, turn, iaRefreshSource, s.runID, s.libraryID, s.libraryKey, s.libraryKey)
	if x != nil {
		return 0, x
	}
	var yes int64
	if x = row.Scan(&yes); x != nil {
		return 0, basicStageError(x)
	}
	if yes != 1 {
		return 0, ErrConflict
	}
	row, x = basicStageRead(ctx, turn, iaRefreshChange, s.changeID, s.assetID, s.token, s.epoch, s.inputRevision, s.fingerprint[:], s.runID, s.sourceID, s.sourceGeneration, s.rootIncarnation, s.objectID, s.objectRevision, s.policyRevision)
	if x != nil {
		return 0, x
	}
	var revision, owner, generation, expiry int64
	if x = row.Scan(&revision, &owner, &generation, &expiry); x != nil {
		return 0, basicStageError(x)
	}
	if revision != s.changeRevision || owner != s.leaseOwner || generation != s.leaseGeneration || expiry != s.leaseExpiresMS || owner <= 0 || generation <= 0 {
		return 0, ErrConflict
	}
	now := turn.transaction.writer.now()
	if err = turn.transaction.observeTime(now); err != nil {
		return 0, err
	}
	deadline, ok := turn.transaction.ctx.Deadline()
	if !ok || !now.Before(deadline) || !now.Before(time.UnixMilli(expiry)) || deadline.After(time.UnixMilli(expiry)) {
		return 0, ErrConflict
	}
	row, x = basicStageRead(ctx, turn, iaRefreshAsset, s.assetID, s.token, s.libraryID, s.epoch)
	if x != nil {
		return 0, x
	}
	var pending int64
	if x = row.Scan(&pending, &e.assetRevisions[0], &e.assetRevisions[1], &e.assetRevisions[2]); x != nil {
		return 0, basicStageError(x)
	}
	want := int64(1)
	if e.refreshID == 0 {
		want = 0
	}
	for _, n := range e.assetRevisions {
		if n <= 0 || n == math.MaxInt64 {
			return 0, ErrConflict
		}
	}
	if pending != want {
		return 0, ErrConflict
	}
	if e.request.purpose != IARefreshRollback && !(e.request.purpose == IARefreshFinalize && e.state == "restoring") {
		row, x = basicStageRead(ctx, turn, iaRefreshInputCurrent, s.assetID, s.changeID, s.size, s.modifiedNS)
		if x != nil {
			return 0, x
		}
		if x = row.Scan(&yes); x != nil {
			return 0, basicStageError(x)
		}
		if yes != 1 {
			return 0, ErrConflict
		}
	}
	return now.UnixMilli(), e.authority.recheck(ctx, turn)
}
