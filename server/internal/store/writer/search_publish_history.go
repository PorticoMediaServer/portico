package writer

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchmanifest"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Return the frozen step installed by W, never synthesize or amend a forecast.
func searchPublisherStep(turn *commandTurn, id stmt.ID) (statementStep, error) {
	if turn == nil {
		return statementStep{}, ErrInvalid
	}
	step, ok := turn.steps[id]
	if !ok {
		return statementStep{}, invalid("unregistered_search_publisher_step")
	}
	return step.statementStep, nil
}

// W proves CURRENT disclosure/native scope before this entry. The floor and
// ORIGINAL identity are resolved before fresh source/profile/lease/overflow or
// reading the result body. No mutable head reconstructs an earlier result.
func lookupSearchImageStage(ctx context.Context, turn *commandTurn, q PublisherReceiptRequest) (out publisherReceiptLookup, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if ctx == nil || turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != SearchImageStage ||
		q.JobID <= 0 || q.LibraryID <= 0 || q.EntityID <= 0 {
		return out, ErrInvalid
	}
	defer turn.transaction.guardPanic()
	step, err := searchPublisherStep(turn, searchPublisherOwnerID)
	if err != nil {
		return out, err
	}
	row, err := turn.queryRow(ctx, step, q.JobID)
	if err != nil {
		return out, err
	}
	var incarnation []byte
	var entity, person sql.NullInt64
	err = row.Scan(&entity, &person, &incarnation, &out.owner.lastSequence, &out.owner.acknowledgedSequence)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	if err == nil {
		if len(incarnation) != 16 {
			return out, ErrResolveRequired
		}
		out.owner.exists = true
		copy(out.owner.incarnation[:], incarnation)
		if !entity.Valid || entity.Int64 != q.EntityID || person.Valid {
			return out, ErrDisclosureDenied
		}
	}
	step, err = searchPublisherStep(turn, searchPublisherIdentity)
	if err != nil {
		return out, err
	}
	row, err = turn.queryRow(ctx, step, q.JobID, q.Receipt.Identity.Incarnation[:], q.Receipt.Identity.Sequence)
	if err != nil {
		return out, err
	}
	facts := receiptFacts{}
	var operation, digest []byte
	var kind string
	err = row.Scan(&operation, &digest, &kind)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	if err == nil {
		if len(operation) != 16 || len(digest) != 32 {
			return out, ErrResolveRequired
		}
		facts.exists = true
		facts.identity.command = q.Receipt.Identity
		copy(facts.identity.command.OperationID[:], operation)
		copy(facts.identity.digest[:], digest)
		if kind == searchImageReceiptKind {
			facts.kind = SearchImageStage
		}
	}
	out.decision, err = classifyReceipt(receiptIdentity{q.Receipt.Identity, q.Receipt.RequestDigest}, SearchImageStage, out.owner, facts)
	if err != nil {
		return out, err
	}
	if out.decision != receiptReplay {
		return out, nil // Fresh/retired carry no result, and never read its body.
	}
	step, err = searchPublisherStep(turn, searchPublisherImageRead)
	if err != nil {
		return out, err
	}
	row, err = turn.queryRow(ctx, step, q.JobID, q.Receipt.Identity.Incarnation[:], q.Receipt.Identity.Sequence, searchmanifest.Algorithm)
	if err != nil {
		return out, err
	}
	var image, fragment sql.NullInt64
	var chain []byte
	v := &out.result
	err = row.Scan(&v.AcceptedJobRevision, &v.AppliedMS, &v.InputGeneration, &image, &fragment,
		&v.AfterField, &v.AfterOrdinal, &v.AfterFragmentID, &chain, &v.PublishedGeneration,
		&v.StagedFragments, &v.StagedBytes, &v.VerifiedFragments, &v.VerifiedBytes, &v.SealAlgorithm)
	if err != nil {
		return out, errors.Join(ErrResolveRequired, err)
	}
	if v.AcceptedJobRevision <= 0 || v.AppliedMS < 0 || v.InputGeneration <= 0 || !image.Valid || image.Int64 <= 0 || fragment.Valid ||
		v.PublishedGeneration < 0 || v.PublishedGeneration >= v.InputGeneration || v.AfterField != 0 || v.AfterOrdinal != 0 || v.AfterFragmentID != 0 ||
		v.StagedFragments != 0 || v.StagedBytes != 0 || v.VerifiedFragments != 0 || v.VerifiedBytes != 0 ||
		len(chain) != 32 || v.SealAlgorithm != searchmanifest.Algorithm {
		return out, ErrResolveRequired
	}
	v.JobID, v.ImageID = q.JobID, image.Int64
	copy(v.SealChain[:], chain)
	return out, nil
}

func acknowledgeSearchPublisher(ctx context.Context, turn *commandTurn, q PublisherReceiptRequest, lookup publisherReceiptLookup) (out int64, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != SearchImageStage || q.JobID <= 0 || q.LibraryID <= 0 || q.EntityID <= 0 || q.Receipt.Identity.Sequence <= 0 {
		return 0, ErrInvalid
	}
	if err := turn.checkLifetimes(ctx); err != nil {
		return 0, err
	}
	return acknowledgeSearchPublisherFacts(ctx, turn, q, lookup.owner, lookup.decision, lookup.result.JobID)
}

// Shared scalar ACK kernel; native typed lookup supplies the saved job ID.
// Historical identities are never inferred from a later mutable head.
func acknowledgeSearchPublisherFacts(ctx context.Context, turn *commandTurn, q PublisherReceiptRequest, owner receiptOwnerFacts, decision receiptDecision, savedJobID int64) (out int64, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if ctx == nil || turn == nil || turn.transaction == nil || (!searchPublisherPurpose(turn.transaction.descriptor.ID) && turn.transaction.descriptor.ID != SearchImageStage) {
		return 0, ErrInvalid
	}
	if err := turn.checkLifetimes(ctx); err != nil {
		return 0, err
	}
	// W passes the lookup for this exact immutable invocation on this SAME
	// turn, after current disclosure and original history. Protocol facts are
	// not a caller-supplied permission or a substitute for that composition.
	if !owner.exists || owner.incarnation != q.Receipt.Identity.Incarnation {
		return 0, ErrResolveRequired
	}
	if decision == receiptRetired {
		if q.Receipt.Identity.Sequence > owner.acknowledgedSequence {
			return 0, ErrResolveRequired
		}
		return owner.acknowledgedSequence, nil
	}
	if decision != receiptReplay || savedJobID != q.JobID || q.Receipt.Identity.Sequence != owner.lastSequence {
		return 0, ErrResolveRequired
	}
	step, err := searchPublisherStep(turn, searchPublisherAck)
	if err != nil {
		return 0, err
	}
	result, err := turn.exec(ctx, step, q.Receipt.Identity.Sequence, q.JobID, q.Receipt.Identity.Incarnation[:], owner.lastSequence, owner.acknowledgedSequence)
	if err != nil {
		return 0, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if n != 1 {
		return 0, ErrConflict
	}
	return q.Receipt.Identity.Sequence, nil
}
