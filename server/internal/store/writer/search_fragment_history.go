package writer

import (
	"context"
	"database/sql"
	"errors"
)

// W alone supplies current opaque/native disclosure before this domain lookup.
// The common receipt classifier, not a later cursor/head, selects replay/floor.
func lookupSearchFragmentStage(ctx context.Context, turn *commandTurn, q PublisherReceiptRequest) (out searchFragmentStageResult, decision receiptDecision, err error) {
	defer func() {
		value, e := finishCommand(ctx, commandLifetime(turn), struct {
			Result   searchFragmentStageResult
			Decision receiptDecision
		}{out, decision}, err)
		out, decision, err = value.Result, value.Decision, e
	}()
	if ctx == nil || turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != SearchFragmentStage || q.JobID <= 0 || q.LibraryID <= 0 || q.EntityID <= 0 {
		return out, 0, ErrInvalid
	}
	defer turn.transaction.guardPanic()
	step, err := searchPublisherStep(turn, searchPublisherOwnerID)
	if err != nil {
		return out, 0, err
	}
	row, err := turn.queryRow(ctx, step, q.JobID)
	if err != nil {
		return out, 0, err
	}
	var entity, person sql.NullInt64
	var incarnation []byte
	owner := receiptOwnerFacts{}
	err = row.Scan(&entity, &person, &incarnation, &owner.lastSequence, &owner.acknowledgedSequence)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, 0, err
	}
	if err == nil {
		if len(incarnation) != 16 {
			return out, 0, ErrResolveRequired
		}
		owner.exists = true
		copy(owner.incarnation[:], incarnation)
		if !entity.Valid || entity.Int64 != q.EntityID || person.Valid {
			return out, 0, ErrDisclosureDenied
		}
	}
	step, err = searchPublisherStep(turn, searchPublisherIdentity)
	if err != nil {
		return out, 0, err
	}
	row, err = turn.queryRow(ctx, step, q.JobID, q.Receipt.Identity.Incarnation[:], q.Receipt.Identity.Sequence)
	if err != nil {
		return out, 0, err
	}
	facts := receiptFacts{}
	var operation, digest []byte
	var kind string
	err = row.Scan(&operation, &digest, &kind)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, 0, err
	}
	if err == nil {
		if len(operation) != 16 || len(digest) != 32 {
			return out, 0, ErrResolveRequired
		}
		facts.exists = true
		facts.identity.command = q.Receipt.Identity
		copy(facts.identity.command.OperationID[:], operation)
		copy(facts.identity.digest[:], digest)
		if kind == searchFragmentReceiptKind {
			facts.kind = SearchFragmentStage
		}
	}
	decision, err = classifyReceipt(receiptIdentity{q.Receipt.Identity, q.Receipt.RequestDigest}, SearchFragmentStage, owner, facts)
	if err != nil || decision != receiptReplay {
		return out, decision, err
	}
	out, err = readSearchFragmentStageResult(ctx, turn, q)
	return out, decision, err
}
