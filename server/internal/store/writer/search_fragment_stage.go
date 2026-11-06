package writer

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchmanifest"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Domain-local projection while W's dedicated public fragment contract is a
// draft. Saved native IDs never become custody FKs. Outcome remains ZERO until
// the sole writer establishes physical COMMIT truth.
type searchFragmentStageResult struct {
	Outcome                                                        ReceiptOutcome
	JobID, AcceptedJobRevision, AppliedMS, ImageID, FragmentID     int64
	InputGeneration, PublishedGeneration                           int64
	AfterField, AfterOrdinal, AfterFragmentID                      int64
	StagedFragments, StagedBytes, VerifiedFragments, VerifiedBytes int64
	SealChain                                                      [32]byte
	SealAlgorithm                                                  string
}

// Effects component, NOT a second entry point/dispatcher. Its sole W caller
// must first resolve ORIGINAL history, qualify current disclosure and native
// source/image/seal/held-custody facts, attest the same physical engine, reserve
// the dedicated fragment memory partition and capture W's acceptance time.
// No image-purpose helpers or caller budget/clock are accepted by a facade.
// W owns invocation, terminal lease/time checks and rollback/COMMIT truth.
func writeSearchFragmentStage(ctx context.Context, turn *commandTurn, c *searchFragmentStageCommand, native searchFragmentStageSnapshot, appliedMS int64) (out searchFragmentStageResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if ctx == nil || turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != SearchFragmentStage || c == nil || !c.valid || appliedMS < 0 {
		return out, ErrInvalid
	}
	defer turn.transaction.guardPanic()
	if native != c.request.snapshot {
		return out, ErrConflict
	}
	if c.payload.row != c.request.row || c.payload.bytes != uint64(len(c.request.row.Prefix)+len(c.request.row.Lexical)+len(c.request.row.Normalized)) {
		return out, ErrConflict
	}
	next, err := planSearchFragmentStage(native, c.payload)
	if err != nil {
		return out, err
	}
	for _, id := range [...]stmt.ID{searchFragmentInsert, searchFragmentPrefix, searchFragmentLexical, searchFragmentGrams, searchFragmentSealTotals, searchFragmentReceiptPut,
		searchFragmentNoHeadNoDue, searchFragmentNoHeadDue, searchFragmentHeadNoDue, searchFragmentHeadDue} {
		if _, err := searchPublisherStep(turn, id); err != nil {
			return out, err
		}
	}
	s, j, image, seal, p := native.source, native.job, native.image, native.seal, c.payload.row
	step, _ := searchPublisherStep(turn, searchFragmentInsert)
	row, err := turn.queryRow(ctx, step, s.entityID, nil, int64(p.Field), p.Ordinal, p.Revision, p.Prefix, p.Lexical, p.Normalized, j.inputGeneration, native.bank.id, image.id)
	if err != nil {
		return out, err
	}
	var fragment int64
	if err := row.Scan(&fragment); err != nil {
		return out, err
	}
	if fragment <= j.afterFragmentID || fragment <= 0 {
		return out, ErrResolveRequired
	}
	// Each independently packed column, INCLUDING empty TEXT, is written to
	// its own fixed native slot on this SAME turn using the actual rowid.
	for _, slot := range []struct {
		id   stmt.ID
		text string
	}{{searchFragmentPrefix, p.Prefix}, {searchFragmentLexical, p.Lexical}, {searchFragmentGrams, p.Normalized}} {
		step, _ := searchPublisherStep(turn, slot.id)
		if _, err := turn.exec(ctx, step, fragment, slot.text); err != nil {
			return out, err
		}
	}
	step, _ = searchPublisherStep(turn, searchFragmentSealTotals)
	result, err := turn.exec(ctx, step, next.stagedFragments, next.stagedBytes, image.id, seal.algorithm, seal.seed[:], seal.expectedDigest[:], seal.expectedFragments, seal.expectedBytes,
		seal.stagedFragments, seal.stagedBytes, seal.verifiedFragments, seal.verifiedBytes, seal.chain[:], seal.afterField, seal.afterOrdinal, seal.afterFragmentID, seal.sealed)
	if err != nil {
		return out, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return out, err
	}
	if changed != 1 {
		return out, ErrConflict
	}
	id, args := searchFragmentJobValues(c, fragment, next)
	step, _ = searchPublisherStep(turn, id)
	row, err = turn.queryRow(ctx, step, args...)
	if err != nil {
		return out, err
	}
	var accepted int64
	if err := row.Scan(&accepted); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return out, ErrConflict
		}
		return out, err
	}
	if accepted != next.jobRevision {
		return out, ErrResolveRequired
	}
	out = searchFragmentStageResult{JobID: j.id, AcceptedJobRevision: accepted, AppliedMS: appliedMS, ImageID: image.id, FragmentID: fragment,
		InputGeneration: j.inputGeneration, PublishedGeneration: j.publishedGeneration, AfterField: next.afterField, AfterOrdinal: next.afterOrdinal, AfterFragmentID: fragment,
		StagedFragments: next.stagedFragments, StagedBytes: next.stagedBytes, VerifiedFragments: seal.verifiedFragments, VerifiedBytes: seal.verifiedBytes, SealChain: seal.chain, SealAlgorithm: seal.algorithm}
	step, _ = searchPublisherStep(turn, searchFragmentReceiptPut)
	result, err = turn.exec(ctx, step, j.id, c.identity.Incarnation[:], c.identity.Sequence, c.identity.OperationID[:], c.digest[:], searchFragmentReceiptKind,
		accepted, appliedMS, j.inputGeneration, image.id, fragment, out.AfterField, out.AfterOrdinal, fragment, seal.chain[:], j.publishedGeneration,
		out.StagedFragments, out.StagedBytes, seal.verifiedFragments, seal.verifiedBytes, searchmanifest.Algorithm)
	if err != nil {
		return out, err
	}
	changed, err = result.RowsAffected()
	if err != nil {
		return out, err
	}
	if changed != 1 {
		return out, ErrResolveRequired
	}
	return out, nil
}

// Decodes ONLY immutable historical scalars. It does not join live fragments,
// images or heads and cannot reconstruct a retired/replayed result from them.
func readSearchFragmentStageResult(ctx context.Context, turn *commandTurn, q PublisherReceiptRequest) (out searchFragmentStageResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if ctx == nil || turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != SearchFragmentStage || q.JobID <= 0 {
		return out, ErrInvalid
	}
	step, err := searchPublisherStep(turn, searchFragmentReceiptRead)
	if err != nil {
		return out, err
	}
	row, err := turn.queryRow(ctx, step, q.JobID, q.Receipt.Identity.Incarnation[:], q.Receipt.Identity.Sequence, searchmanifest.Algorithm)
	if err != nil {
		return out, err
	}
	var image, fragment sql.NullInt64
	var chain []byte
	err = row.Scan(&out.AcceptedJobRevision, &out.AppliedMS, &out.InputGeneration, &image, &fragment,
		&out.AfterField, &out.AfterOrdinal, &out.AfterFragmentID, &chain, &out.PublishedGeneration,
		&out.StagedFragments, &out.StagedBytes, &out.VerifiedFragments, &out.VerifiedBytes, &out.SealAlgorithm)
	if err != nil {
		return out, errors.Join(ErrResolveRequired, err)
	}
	if out.AcceptedJobRevision <= 0 || out.AppliedMS < 0 || out.InputGeneration <= 0 || !image.Valid || image.Int64 <= 0 || !fragment.Valid || fragment.Int64 <= 0 ||
		out.AfterField != 1 || out.AfterOrdinal < 0 || out.AfterOrdinal >= out.StagedFragments || out.AfterOrdinal != out.StagedFragments-1 || out.AfterFragmentID != fragment.Int64 ||
		out.PublishedGeneration < 0 || out.PublishedGeneration >= out.InputGeneration || out.StagedFragments <= 0 || out.StagedBytes < 0 ||
		out.VerifiedFragments < 0 || out.VerifiedFragments > out.StagedFragments || out.VerifiedBytes < 0 || out.VerifiedBytes > out.StagedBytes || len(chain) != 32 || out.SealAlgorithm != searchmanifest.Algorithm {
		return out, ErrResolveRequired
	}
	out.JobID, out.ImageID, out.FragmentID = q.JobID, image.Int64, fragment.Int64
	copy(out.SealChain[:], chain)
	return out, nil
}
