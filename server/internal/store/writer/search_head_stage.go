package writer

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchpolicy"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Native historical scalars only. W alone supplies physical Outcome and current
// disclosure; no serving revision or old-head value is reconstructed on replay.
type searchHeadPublishResult struct {
	Outcome                                                        ReceiptOutcome
	JobID, AcceptedJobRevision, AppliedMS, ImageID                 int64
	InputGeneration, PublishedGeneration                           int64
	AfterField, AfterOrdinal, AfterFragmentID                      int64
	StagedFragments, StagedBytes, VerifiedFragments, VerifiedBytes int64
	SealChain                                                      [32]byte
	SealAlgorithm                                                  string
}

func applySearchHeadPublish(ctx context.Context, turn *commandTurn, c *searchHeadPublishCommand) (out searchHeadPublishResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if ctx == nil || turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != SearchHeadPublish || c == nil || !c.valid {
		return out, ErrInvalid
	}
	defer turn.transaction.guardPanic()
	s := c.request.snapshot.stage
	q := PublisherReceiptRequest{JobID: s.job.id, LibraryID: s.source.libraryID, EntityID: s.source.entityID, Receipt: ReceiptReference{c.identity, c.digest}}
	saved, decision, err := lookupSearchHeadPublish(ctx, turn, q)
	if err != nil {
		return out, err
	}
	if decision == receiptReplay {
		return saved, nil
	}
	if decision != receiptFresh {
		return out, ErrResolveRequired
	}
	resources, ok := any(turn).(searchPublisherTurnResources)
	if !ok {
		return out, ErrNotImplemented
	}
	required, err := searchHeadPreparationBytes(c.request)
	if err != nil {
		return out, err
	}
	if err = resources.requireSearchPublisherMemory(ctx, SearchHeadPublish, required); err != nil {
		return out, err
	}
	digest, err := canonicalRequestDigest(SearchHeadPublish, 1, c.identity, c.request)
	if err != nil || digest != c.digest {
		return out, ErrConflict
	}
	native, err := searchHeadPublishFreshFacts(ctx, turn, c.request)
	if err != nil {
		return out, err
	}
	engine, err := resources.searchPublisherEngine(ctx, SearchHeadPublish)
	if err != nil {
		return out, err
	}
	if searchpolicy.Compare(native.snapshot.stage.bank.profile, engine).State != searchpolicy.Current {
		return out, ErrConflict
	}
	appliedMS, err := resources.searchPublisherTimeMS(ctx, SearchHeadPublish, s.job.leaseExpiresMS)
	if err != nil {
		return out, err
	}
	out, err = writeSearchHeadPublish(ctx, turn, c, native, appliedMS)
	if err != nil {
		return out, err
	}
	_, err = resources.searchPublisherTimeMS(ctx, SearchHeadPublish, s.job.leaseExpiresMS)
	return out, err // W's independent terminal/physical projection still required.
}

func writeSearchHeadPublish(ctx context.Context, turn *commandTurn, c *searchHeadPublishCommand, native searchHeadPublishRequest, appliedMS int64) (out searchHeadPublishResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if ctx == nil || turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != SearchHeadPublish || c == nil || !c.valid || appliedMS < 0 {
		return out, ErrInvalid
	}
	defer turn.transaction.guardPanic()
	if native != c.request {
		return out, ErrConflict
	}
	next, err := planSearchHeadPublish(native.snapshot)
	if err != nil {
		return out, err
	}
	for _, id := range []stmt.ID{searchHeadOwnInsert, searchHeadOwnDelete, searchHeadWakeNoDue, searchHeadWakeDue, searchHeadPendingCAS, searchHeadClockOnly, searchHeadClockDual, searchHeadReceiptPut, searchHeadNoHeadNoDue, searchHeadNoHeadDue, searchHeadHasHeadNoDue, searchHeadHasHeadDue} {
		if _, e := searchPublisherStep(turn, id); e != nil {
			return out, e
		}
	}
	exec := func(id stmt.ID, args ...any) error {
		step, e := searchPublisherStep(turn, id)
		if e != nil {
			return e
		}
		_, e = turn.exec(ctx, step, args...)
		return e
	}
	cas := func(id stmt.ID, args ...any) error {
		step, e := searchPublisherStep(turn, id)
		if e != nil {
			return e
		}
		result, e := turn.exec(ctx, step, args...)
		if e != nil {
			return e
		}
		rows, e := result.RowsAffected()
		if e != nil {
			return e
		}
		if rows != 1 {
			return ErrConflict
		}
		return nil
	}
	s := native.snapshot.stage
	j, b, seal := s.job, s.bank, s.seal
	// Old ORIGINAL payload leaves native custody before its accepted pointer
	// moves. The old image and its strings remain available to the reclaimer.
	if j.publishedImage.Valid {
		if err = exec(searchHeadOwnDelete, "delete", s.source.entityID, native.old.prefix, native.old.initial); err != nil {
			return out, err
		}
	}
	if err = exec(searchHeadOwnInsert, s.source.entityID, native.next.prefix, native.next.initial); err != nil {
		return out, err
	}
	id, args := searchHeadJobValues(c, next)
	step, _ := searchPublisherStep(turn, id)
	row, err := turn.queryRow(ctx, step, args...)
	if err != nil {
		return out, err
	}
	var accepted int64
	if err = row.Scan(&accepted); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return out, ErrConflict
		}
		return out, err
	}
	if accepted != next.acceptedJobRevision {
		return out, ErrResolveRequired
	}
	if err = cas(searchHeadPendingCAS, next.pending, b.id, b.key, b.role, b.profile.RecipeVersion, b.profile.EngineVersion, b.profile.UnicodeFingerprint, native.snapshot.pending); err != nil {
		return out, err
	}
	if b.role == "active" {
		id := searchHeadClockOnly
		args := []any{next.servingRevision, int64(1), native.snapshot.servingRevision, s.active.Int64}
		if s.building.Valid {
			id = searchHeadClockDual
			args = append(args, s.building.Int64)
		}
		if err = cas(id, args...); err != nil {
			return out, err
		}
	}
	if j.publishedImage.Valid {
		o := native.retirement
		revision, e := nextSearchInputCounter(o.revision, 1)
		if e != nil {
			return out, e
		}
		due := appliedMS
		if o.nextAttempt.Valid {
			due = min(due, o.nextAttempt.Int64)
		}
		id := searchHeadWakeNoDue
		args := []any{"ready", revision, due, o.id, o.publicationID, o.livePublicationID.Int64, o.phase, o.incarnation[:], o.revision, o.afterFragmentID, o.lastSequence, o.acknowledgedSequence, o.leaseOwner, o.leaseGeneration, o.leaseExpiresMS, o.attempts}
		if o.nextAttempt.Valid {
			id = searchHeadWakeDue
			args = append(args, o.nextAttempt.Int64)
		}
		if err = cas(id, args...); err != nil {
			return out, err
		}
	}
	out = searchHeadPublishResult{JobID: j.id, AcceptedJobRevision: accepted, AppliedMS: appliedMS, ImageID: s.image.id, InputGeneration: j.inputGeneration, PublishedGeneration: j.inputGeneration,
		AfterField: seal.afterField, AfterOrdinal: seal.afterOrdinal, AfterFragmentID: seal.afterFragmentID, StagedFragments: seal.stagedFragments, StagedBytes: seal.stagedBytes, VerifiedFragments: seal.verifiedFragments, VerifiedBytes: seal.verifiedBytes, SealChain: seal.chain, SealAlgorithm: seal.algorithm}
	err = cas(searchHeadReceiptPut, j.id, c.identity.Incarnation[:], c.identity.Sequence, c.identity.OperationID[:], c.digest[:], searchHeadReceiptKind, accepted, appliedMS, j.inputGeneration, s.image.id, nil, seal.afterField, seal.afterOrdinal, seal.afterFragmentID, seal.chain[:], j.inputGeneration, seal.stagedFragments, seal.stagedBytes, seal.verifiedFragments, seal.verifiedBytes, seal.algorithm)
	return out, err
}
