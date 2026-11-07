package writer

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchpolicy"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

type searchFragmentReclaimResult struct {
	Outcome                                                                           ReceiptOutcome
	JobID, AcceptedJobRevision, AppliedMS, PublicationID, FragmentID, AfterFragmentID int64
	Complete                                                                          bool
}

func readSearchReclaimOwner(ctx context.Context, turn *commandTurn, job int64) (out searchFragmentReclaimOwner, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	step, err := searchPublisherStep(turn, searchReclaimOwner)
	if err != nil {
		return out, err
	}
	row, err := turn.queryRow(ctx, step, job)
	if err != nil {
		return out, err
	}
	var inc []byte
	err = row.Scan(&out.id, &out.publicationID, &out.livePublicationID, &out.phase, &inc, &out.revision, &out.afterFragmentID, &out.lastSequence, &out.acknowledgedSequence, &out.leaseOwner, &out.leaseGeneration, &out.leaseExpiresMS, &out.attempts, &out.nextAttempt)
	if err != nil {
		return out, err
	}
	if len(inc) != 16 {
		return out, ErrResolveRequired
	}
	copy(out.incarnation[:], inc)
	return out, nil
}

// W's current opaque/native disclosure precedes this private domain entry.
// The reclaimer is its OWN native sequence/ACK owner, not the publisher job.
func lookupSearchFragmentReclaim(ctx context.Context, turn *commandTurn, job int64, ref ReceiptReference) (out searchFragmentReclaimResult, decision receiptDecision, err error) {
	defer func() {
		v, e := finishCommand(ctx, commandLifetime(turn), struct {
			Result   searchFragmentReclaimResult
			Decision receiptDecision
		}{out, decision}, err)
		out, decision, err = v.Result, v.Decision, e
	}()
	if ctx == nil || turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != SearchFragmentReclaim || job <= 0 {
		return out, 0, ErrInvalid
	}
	defer turn.transaction.guardPanic()
	o, e := readSearchReclaimOwner(ctx, turn, job)
	owner := receiptOwnerFacts{}
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return out, 0, e
	}
	if e == nil {
		owner = receiptOwnerFacts{true, o.incarnation, o.lastSequence, o.acknowledgedSequence}
	}
	step, e := searchPublisherStep(turn, searchReclaimIdentity)
	if e != nil {
		return out, 0, e
	}
	row, e := turn.queryRow(ctx, step, job, ref.Identity.Incarnation[:], ref.Identity.Sequence)
	if e != nil {
		return out, 0, e
	}
	var operation, digest []byte
	var kind string
	facts := receiptFacts{}
	e = row.Scan(&operation, &digest, &kind)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return out, 0, e
	}
	if e == nil {
		if len(operation) != 16 || len(digest) != 32 {
			return out, 0, ErrResolveRequired
		}
		facts.exists = true
		facts.identity.command = ref.Identity
		copy(facts.identity.command.OperationID[:], operation)
		copy(facts.identity.digest[:], digest)
		if kind == searchReclaimFragmentKind {
			facts.kind = SearchFragmentReclaim
		}
	}
	decision, err = classifyReceipt(receiptIdentity{ref.Identity, ref.RequestDigest}, SearchFragmentReclaim, owner, facts)
	if err != nil || decision != receiptReplay {
		return out, decision, err
	}
	step, err = searchPublisherStep(turn, searchReclaimResult)
	if err != nil {
		return out, 0, err
	}
	row, err = turn.queryRow(ctx, step, job, ref.Identity.Incarnation[:], ref.Identity.Sequence)
	if err != nil {
		return out, 0, err
	}
	var fragment sql.NullInt64
	var complete int64
	err = row.Scan(&out.AcceptedJobRevision, &out.AppliedMS, &out.PublicationID, &fragment, &out.AfterFragmentID, &complete)
	if err != nil {
		return out, 0, errors.Join(ErrResolveRequired, err)
	}
	if out.AcceptedJobRevision <= 0 || out.AppliedMS < 0 || out.PublicationID <= 0 || !fragment.Valid || fragment.Int64 <= 0 || out.AfterFragmentID != fragment.Int64 || complete != 0 {
		return out, 0, ErrResolveRequired
	}
	out.JobID, out.FragmentID = job, fragment.Int64
	return out, decision, nil
}

func searchFragmentReclaimFreshFacts(ctx context.Context, turn *commandTurn, want searchFragmentReclaimRequest) (out searchFragmentReclaimRequest, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if ctx == nil || turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != SearchFragmentReclaim {
		return out, ErrInvalid
	}
	defer turn.transaction.guardPanic()
	out = want
	o, err := readSearchReclaimOwner(ctx, turn, want.snapshot.owner.id)
	if err != nil {
		return out, err
	}
	out.snapshot.owner = o
	query := func(id stmt.ID, args ...any) (commandRow, error) {
		step, e := searchPublisherStep(turn, id)
		if e != nil {
			return commandRow{}, e
		}
		return turn.queryRow(ctx, step, args...)
	}
	f := &out.snapshot.fragment
	row, err := query(searchReclaimPublication, o.publicationID)
	if err != nil {
		return out, err
	}
	var publication int64
	err = row.Scan(&publication, &f.entityID, &f.libraryID, &f.kind, &f.bankID, &f.inputGeneration)
	if err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	if publication != o.publicationID {
		return out, ErrConflict
	}
	row, err = query(searchReclaimSubject, f.entityID, f.libraryID, f.kind)
	if err != nil {
		return out, err
	}
	var id int64
	if err = row.Scan(&id); err != nil || id != f.entityID {
		return out, errors.Join(ErrConflict, err)
	}
	row, err = query(searchReclaimBank, f.bankID, want.bankKey, want.engineVersion)
	if err != nil {
		return out, err
	}
	if err = row.Scan(&id); err != nil || id != f.bankID {
		return out, errors.Join(ErrConflict, err)
	}
	row, err = query(searchReclaimPointers, f.entityID, f.bankID)
	if err != nil {
		return out, err
	}
	out.snapshot.currentHead, out.snapshot.currentStage = sql.NullInt64{}, sql.NullInt64{}
	err = row.Scan(&out.snapshot.currentHead, &out.snapshot.currentStage)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	row, err = query(searchReclaimNext, o.publicationID, o.afterFragmentID, int64(1))
	if err != nil {
		return out, err
	}
	var next int64
	if err = row.Scan(&next); err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	if next != want.snapshot.fragment.id {
		return out, ErrConflict
	}
	row, err = query(searchReclaimFragment, next, o.publicationID)
	if err != nil {
		return out, err
	}
	err = row.Scan(&f.id, &f.publicationID, &f.bankID, &f.inputGeneration, &f.entityID, &f.row.Field, &f.row.Ordinal, &f.row.Revision, &f.row.Prefix, &f.row.Lexical, &f.row.Normalized)
	if err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	row, err = query(searchReclaimSeal, o.publicationID)
	if err != nil {
		return out, err
	}
	if err = row.Scan(&id); err != nil || id != o.publicationID {
		return out, errors.Join(ErrConflict, err)
	}
	if out != want {
		return out, ErrConflict
	}
	return out, nil
}

func applySearchFragmentReclaim(ctx context.Context, turn *commandTurn, c *searchFragmentReclaimCommand) (out searchFragmentReclaimResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if ctx == nil || turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != SearchFragmentReclaim || c == nil || !c.valid {
		return out, ErrInvalid
	}
	defer turn.transaction.guardPanic()
	o := c.request.snapshot.owner
	saved, decision, err := lookupSearchFragmentReclaim(ctx, turn, o.id, ReceiptReference{c.identity, c.digest})
	if err != nil {
		return out, err
	}
	if decision == receiptReplay {
		return saved, nil
	}
	if decision != receiptFresh {
		return out, ErrResolveRequired
	}
	r, ok := any(turn).(searchPublisherTurnResources)
	if !ok {
		return out, ErrNotImplemented
	}
	required, err := searchReclaimPreparationBytes(c.request)
	if err != nil {
		return out, err
	}
	if err = r.requireSearchPublisherMemory(ctx, SearchFragmentReclaim, required); err != nil {
		return out, err
	}
	digest, err := canonicalRequestDigest(SearchFragmentReclaim, 1, c.identity, c.request)
	if err != nil || digest != c.digest {
		return out, ErrConflict
	}
	native, err := searchFragmentReclaimFreshFacts(ctx, turn, c.request)
	if err != nil {
		return out, err
	}
	engine, err := r.searchPublisherEngine(ctx, SearchFragmentReclaim)
	if err != nil {
		return out, err
	}
	if engine.Version != searchpolicy.SQLiteVersion || engine.SourceID != searchpolicy.SQLiteSourceID {
		return out, ErrConflict
	}
	// Original bounded TEXT, not the compiled normalizer's output, is retired.
	// A Unicode/prep-profile mismatch does not authorize re-normalization.
	ms, err := r.searchPublisherTimeMS(ctx, SearchFragmentReclaim, o.leaseExpiresMS)
	if err != nil {
		return out, err
	}
	out, err = writeSearchFragmentReclaim(ctx, turn, c, native, ms)
	if err != nil {
		return out, err
	}
	_, err = r.searchPublisherTimeMS(ctx, SearchFragmentReclaim, o.leaseExpiresMS)
	return out, err
}

func writeSearchFragmentReclaim(ctx context.Context, turn *commandTurn, c *searchFragmentReclaimCommand, native searchFragmentReclaimRequest, ms int64) (out searchFragmentReclaimResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if ctx == nil || turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != SearchFragmentReclaim || c == nil || !c.valid || ms < 0 {
		return out, ErrInvalid
	}
	defer turn.transaction.guardPanic()
	if native != c.request {
		return out, ErrConflict
	}
	s := native.snapshot
	o, f := s.owner, s.fragment
	next, err := planSearchFragmentReclaim(s, c.identity)
	if err != nil {
		return out, err
	}
	for _, id := range []stmt.ID{searchReclaimPrefix, searchReclaimLexical, searchReclaimGrams, searchReclaimContentDelete, searchReclaimNoDue, searchReclaimDue, searchReclaimReceiptPut} {
		if _, err := searchPublisherStep(turn, id); err != nil {
			return out, err
		}
	}
	for _, slot := range []struct {
		id   stmt.ID
		text string
	}{{searchReclaimPrefix, f.row.Prefix}, {searchReclaimLexical, f.row.Lexical}, {searchReclaimGrams, f.row.Normalized}} {
		step, _ := searchPublisherStep(turn, slot.id)
		if _, err := turn.exec(ctx, step, "delete", f.id, slot.text); err != nil {
			return out, err
		}
	}
	step, _ := searchPublisherStep(turn, searchReclaimContentDelete)
	row, err := turn.queryRow(ctx, step, f.id, f.publicationID, f.bankID, f.inputGeneration, int64(f.row.Field), f.row.Ordinal, f.row.Revision, f.row.Prefix, f.row.Lexical, f.row.Normalized)
	if err != nil {
		return out, err
	}
	var deleted int64
	if err = row.Scan(&deleted); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return out, ErrConflict
		}
		return out, err
	}
	if deleted != f.id {
		return out, ErrResolveRequired
	}
	id := searchReclaimNoDue
	args := []any{"reclaim", deleted, next.acceptedRevision, c.identity.Sequence, o.id, o.publicationID, o.livePublicationID.Int64, c.identity.Incarnation[:], o.phase, o.revision, o.afterFragmentID, o.lastSequence, o.acknowledgedSequence, o.leaseOwner, o.leaseGeneration, o.leaseExpiresMS, o.attempts}
	if o.nextAttempt.Valid {
		id = searchReclaimDue
		args = append(args, o.nextAttempt.Int64)
	}
	step, _ = searchPublisherStep(turn, id)
	row, err = turn.queryRow(ctx, step, args...)
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
	if accepted != next.acceptedRevision {
		return out, ErrResolveRequired
	}
	out = searchFragmentReclaimResult{JobID: o.id, AcceptedJobRevision: accepted, AppliedMS: ms, PublicationID: o.publicationID, FragmentID: deleted, AfterFragmentID: deleted}
	step, _ = searchPublisherStep(turn, searchReclaimReceiptPut)
	result, err := turn.exec(ctx, step, o.id, c.identity.Incarnation[:], c.identity.Sequence, c.identity.OperationID[:], c.digest[:], searchReclaimFragmentKind, accepted, ms, o.publicationID, deleted, deleted, int64(0))
	if err != nil {
		return out, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return out, err
	}
	if n != 1 {
		return out, ErrResolveRequired
	}
	return out, nil
}
