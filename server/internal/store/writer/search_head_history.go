package writer

import (
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchmanifest"
)

// W alone supplies current opaque/native disclosure before this domain lookup.
// The common receipt classifier, not a later cursor/head, selects replay/floor.
func lookupSearchHeadPublish(ctx context.Context, turn *commandTurn, q PublisherReceiptRequest) (out searchHeadPublishResult, decision receiptDecision, err error) {
	defer func() {
		value, e := finishCommand(ctx, commandLifetime(turn), struct {
			Result   searchHeadPublishResult
			Decision receiptDecision
		}{out, decision}, err)
		out, decision, err = value.Result, value.Decision, e
	}()
	if ctx == nil || turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != SearchHeadPublish || q.JobID <= 0 || q.LibraryID <= 0 || q.EntityID <= 0 {
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
		if kind == searchHeadReceiptKind {
			facts.kind = SearchHeadPublish
		}
	}
	decision, err = classifyReceipt(receiptIdentity{q.Receipt.Identity, q.Receipt.RequestDigest}, SearchHeadPublish, owner, facts)
	if err != nil || decision != receiptReplay {
		return out, decision, err
	}
	out, err = readSearchHeadPublishResult(ctx, turn, q)
	return out, decision, err
}

func readSearchHeadPublishResult(ctx context.Context, turn *commandTurn, q PublisherReceiptRequest) (out searchHeadPublishResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if ctx == nil || turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != SearchHeadPublish {
		return out, ErrInvalid
	}
	step, err := searchPublisherStep(turn, searchHeadReceiptRead)
	if err != nil {
		return out, err
	}
	row, err := turn.queryRow(ctx, step, q.JobID, q.Receipt.Identity.Incarnation[:], q.Receipt.Identity.Sequence, searchmanifest.Algorithm)
	if err != nil {
		return out, err
	}
	var image, fragment sql.NullInt64
	var chain []byte
	err = row.Scan(&out.AcceptedJobRevision, &out.AppliedMS, &out.InputGeneration, &image, &fragment, &out.AfterField, &out.AfterOrdinal, &out.AfterFragmentID, &chain, &out.PublishedGeneration, &out.StagedFragments, &out.StagedBytes, &out.VerifiedFragments, &out.VerifiedBytes, &out.SealAlgorithm)
	if err != nil {
		return out, errors.Join(ErrResolveRequired, err)
	}
	if out.AcceptedJobRevision <= 0 || out.AppliedMS < 0 || out.InputGeneration <= 0 || out.PublishedGeneration != out.InputGeneration || !image.Valid || image.Int64 <= 0 || fragment.Valid || out.StagedFragments < 0 || out.StagedBytes < 0 || out.VerifiedFragments != out.StagedFragments || out.VerifiedBytes != out.StagedBytes || len(chain) != 32 || out.SealAlgorithm != searchmanifest.Algorithm {
		return out, ErrResolveRequired
	}
	if out.StagedFragments == 0 {
		if out.StagedBytes != 0 || out.AfterField != 0 || out.AfterOrdinal != 0 || out.AfterFragmentID != 0 {
			return out, ErrResolveRequired
		}
	} else if out.AfterField != 1 || out.AfterOrdinal != out.StagedFragments-1 || out.AfterFragmentID <= 0 {
		return out, ErrResolveRequired
	}
	out.JobID, out.ImageID = q.JobID, image.Int64
	copy(out.SealChain[:], chain)
	return out, nil
}
