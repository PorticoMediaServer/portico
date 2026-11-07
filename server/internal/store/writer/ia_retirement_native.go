package writer

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Private storage adapters against IA-SCHEMA-001. The owning W composition
// must establish current conjunctive disclosure/owner authority and admit the
// complete native row/copy bytes before entry. Neither these functions nor a
// Capture receipt mint ingress, a lease, resource authority or Applied truth.
func iaRetirementPoint(ctx context.Context, turn *commandTurn, id stmt.ID, args []any, dest ...any) error {
	if ctx == nil || turn == nil || turn.transaction == nil {
		return ErrInvalid
	}
	step, ok := turn.steps[id]
	if !ok {
		return invalid("ia_retirement_statement")
	}
	rows, err := turn.query(ctx, step.statementStep, args...)
	if err != nil {
		return err
	}
	if !rows.Next() {
		err = rows.Err()
		if err == nil {
			err = sql.ErrNoRows
		}
	} else {
		err = rows.Scan(dest...)
		if err == nil && rows.Next() {
			err = ErrResolveRequired
		}
	}
	return errors.Join(err, rows.Err(), rows.Close())
}

func iaRetirementKey(q iaRetirementReference) []any {
	return []any{q.kind, q.target, q.originalOperation[:], q.identity.Incarnation[:]}
}

func loadIARetirementOwner(ctx context.Context, turn *commandTurn, q iaRetirementReference) (iaRetirementOwner, error) {
	var owner iaRetirementOwner
	o := &owner.original
	o.kind, o.target = q.kind, q.target
	var operation, digest, inc []byte
	var sourceIncarnation, rootIdentity sql.NullString
	var sourceGeneration sql.NullInt64
	err := iaRetirementPoint(ctx, turn, iaRetirementOwnerPoint, iaRetirementKey(q), &operation, &digest, &inc,
		&o.libraryID, &o.libraryKey, &o.resolvedRoot, &sourceIncarnation, &sourceGeneration, &rootIdentity,
		&owner.last, &owner.acknowledged, &owner.reclaimed, &owner.retainedCount, &owner.retainedBytes)
	if errors.Is(err, sql.ErrNoRows) {
		return iaRetirementOwner{}, nil
	}
	if err != nil {
		return iaRetirementOwner{}, err
	}
	if len(operation) != 16 || len(digest) != 32 || len(inc) != 16 ||
		sourceIncarnation.Valid != (q.kind == "folder") || sourceGeneration.Valid != sourceIncarnation.Valid || rootIdentity.Valid != sourceIncarnation.Valid {
		return iaRetirementOwner{}, ErrResolveRequired
	}
	copy(o.operationID[:], operation)
	copy(o.requestDigest[:], digest)
	copy(owner.incarnation[:], inc)
	o.sourceIncarnation, o.sourceGeneration, o.rootIdentity = sourceIncarnation.String, sourceGeneration.Int64, rootIdentity.String
	owner.exists = true
	return owner, nil
}

func lookupIARetirement(ctx context.Context, turn *commandTurn, q iaRetirementReference) (value iaRetirementHistory, owner iaRetirementOwner, err error) {
	defer func() {
		value, err = finishCommand(ctx, commandLifetime(turn), value, err)
		if err != nil {
			owner = iaRetirementOwner{}
		}
	}()
	if err = validateIARetirementReference(q); err != nil {
		return value, owner, err
	}
	if ctx == nil || turn == nil || turn.transaction == nil || (turn.transaction.descriptor.ID != q.purpose && turn.transaction.descriptor.ID != IARetireStep) {
		return value, owner, ErrInvalid
	}
	owner, err = loadIARetirementOwner(ctx, turn, q)
	if err != nil {
		return value, owner, err
	}
	var h iaRetirementReceipt
	if !owner.exists {
		value, err = classifyIARetirementHistory(q, owner, h)
		return
	}
	args := append(iaRetirementKey(q), q.identity.Sequence)
	var operation, request, digest []byte
	var kind string
	err = iaRetirementPoint(ctx, turn, iaRetirementReceiptPoint, args, &operation, &request, &kind,
		&h.version, &h.resultCount, &digest, &h.retainedBytes)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return value, owner, err
	}
	if err == nil {
		if len(operation) != 16 || len(request) != 32 || len(digest) != 32 {
			return value, owner, ErrResolveRequired
		}
		h.exists, h.identity.command = true, q.identity
		copy(h.identity.command.OperationID[:], operation)
		copy(h.identity.digest[:], request)
		copy(h.digest[:], digest)
		switch kind {
		case "retire_library":
			h.purpose = IARetireLibrary
		case "retire_source":
			h.purpose = IARetireSource
		case "retire_step":
			h.purpose = IARetireStep
		default:
			return value, owner, ErrResolveRequired
		}
	}
	// The common classifier is used only to decide whether the large body may
	// be fetched. The final IA classifier below proves original scope and the
	// complete result. ACKed bodies are never queried, decoded or reconstructed.
	decision, classifyErr := classifyReceipt(receiptIdentity{q.identity, q.request}, q.purpose,
		receiptOwnerFacts{owner.exists, owner.incarnation, owner.last, owner.acknowledged},
		receiptFacts{exists: h.exists, identity: h.identity, kind: h.purpose, resultCount: h.resultCount, resultDigest: h.digest})
	if classifyErr == nil && decision == receiptReplay {
		h.result.original = owner.original
		var removed, kept int64
		err = iaRetirementPoint(ctx, turn, iaRetirementBodyPoint, args, &h.result.acceptedRevision, &h.result.appliedMS,
			&h.result.phase, &h.result.entityKind, &h.result.entityCursor, &h.result.objectCursor, &removed, &kept, &h.result.state)
		if err != nil {
			return value, owner, errors.Join(ErrResolveRequired, err)
		}
		if (removed != 0 && removed != 1) || (kept != 0 && kept != 1) {
			return value, owner, ErrResolveRequired
		}
		h.result.walkRemoved, h.result.walkKept = removed == 1, kept == 1
	}
	value, err = classifyIARetirementHistory(q, owner, h)
	return
}

func iaRetirementOneEffect(ctx context.Context, turn *commandTurn, id stmt.ID, args ...any) error {
	if ctx == nil || turn == nil || turn.transaction == nil {
		return ErrInvalid
	}
	step, ok := turn.steps[id]
	if !ok {
		return invalid("ia_retirement_effect")
	}
	changed, err := turn.exec(ctx, step.statementStep, args...)
	if err != nil {
		return err
	}
	if changed == nil {
		return ErrResolveRequired
	}
	n, err := changed.RowsAffected()
	if err != nil || n != 1 {
		return errors.Join(ErrConflict, err)
	}
	return nil
}

// ACK advances only the consumer floor. Scalar receipt bytes and the actual
// original driver remain retained; there is no deletion/refund in this recipe.
func acknowledgeIARetirement(ctx context.Context, turn *commandTurn, q iaRetirementReference) (value int64, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	history, owner, err := lookupIARetirement(ctx, turn, q)
	if err != nil {
		return 0, err
	}
	if history.decision == receiptRetired {
		return owner.acknowledged, nil
	}
	if history.decision != receiptReplay || q.identity.Sequence != owner.last {
		return 0, ErrResolveRequired
	}
	args := append([]any{q.identity.Sequence}, append(iaRetirementKey(q), owner.last, owner.acknowledged)...)
	if err = iaRetirementOneEffect(ctx, turn, iaRetirementAckCAS, args...); err != nil {
		return 0, err
	}
	return q.identity.Sequence, nil
}

// One scalar header is reclaimed per turn, after current authority and owner
// proof. Native CHECKs bind retained_bytes to actual payload. Full-key delete
// plus exact ledger CAS share the caller's rollback/COMMIT frame. No live target
// is deleted, no lease is replaced, and the terminal driver remains a tombstone.
func reclaimIARetirement(ctx context.Context, turn *commandTurn, q iaRetirementReference) (value bool, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	history, owner, err := lookupIARetirement(ctx, turn, q)
	if err != nil {
		return false, err
	}
	if history.decision != receiptRetired {
		return false, ErrResolveRequired
	}
	var sequence, retained int64
	err = iaRetirementPoint(ctx, turn, iaRetirementReclaimPoint, append(iaRetirementKey(q), owner.reclaimed, owner.acknowledged), &sequence, &retained)
	if errors.Is(err, sql.ErrNoRows) {
		if owner.reclaimed != owner.acknowledged {
			return false, ErrResolveRequired
		}
		return false, nil
	}
	if err != nil {
		return false, err
	}
	next, err := iaRetirementReclaimDebt(owner, sequence, retained)
	if err != nil {
		return false, err
	}
	if err = iaRetirementOneEffect(ctx, turn, iaRetirementReclaimDelete, append(iaRetirementKey(q), sequence, retained)...); err != nil {
		return false, err
	}
	args := append([]any{next.reclaimed, next.retainedCount, next.retainedBytes}, append(iaRetirementKey(q), owner.last, owner.acknowledged, owner.reclaimed, owner.retainedCount, owner.retainedBytes)...)
	if err = iaRetirementOneEffect(ctx, turn, iaRetirementReclaimCAS, args...); err != nil {
		return false, err
	}
	return true, nil
}
