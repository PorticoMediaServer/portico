package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/bookseries"
)

// IA-14 publishes the accepted local book-series projection with the accepted
// audiobook source. It is a composition inside the existing audio_inventory
// accept (command 111): no new command purpose, no raw tag fallback and no
// provider series. One head per book records the exact accepted source asset,
// info epoch and local policy revision; its members are the complete ordered
// name/position pairs. A newer accepted info epoch or policy invalidates and
// replaces the projection atomically; a stale witness or a divergent cohort in
// the same epoch is a conflict, never a silent overwrite.

// iaBookSeriesAcceptedPairs is the accepted local evidence only: the exact
// folder/tag bytes IA-13 already admitted. A blank name publishes no projection,
// even though the book entity keeps the exact scalar bytes. Nothing here reads
// raw inventory tags or an owner scalar.
func iaBookSeriesAcceptedPairs(r iaBookInventoryRequest) []bookseries.Pair {
	if strings.TrimSpace(r.series) == "" {
		return nil
	}
	return []bookseries.Pair{{Name: r.series, Position: r.seriesPosition}}
}

// iaBookSeriesHeadFacts is the exact retained head, never a caller expectation.
type iaBookSeriesHeadFacts struct {
	libraryID, assetID, infoEpoch, policyRevision, revision, count, memberBytes int64
	sourceRevision                                                              string
	digest                                                                      [32]byte
}

func iaBookSeriesInputError(err error) error {
	var refusal *bookseries.Error
	if errors.As(err, &refusal) {
		cause := ErrInvalid
		if refusal.Limit > 0 {
			cause = ErrCap
		}
		return &PolicyError{Field: "ia_book_series_" + refusal.Field, Limit: refusal.Limit, Cause: cause}
	}
	return err
}

// iaBookSeriesPublish runs inside the accept transaction after the book subject
// and its exact asset witness exist. Every gate is proven before the first
// write; the head and its complete member cohort commit or roll back together.
func iaBookSeriesPublish(ctx context.Context, turn *commandTurn, libraryID, bookID int64, asset iaBookAssetFacts, policyRevision int64, pairs []bookseries.Pair) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if turn == nil || turn.transaction == nil || libraryID <= 0 || bookID <= 0 || asset.id <= 0 || asset.infoEpoch <= 0 ||
		asset.token == "" || policyRevision <= 0 {
		return ErrInvalid
	}
	count, retained, digest, err := bookseries.Cohort(pairs)
	if err != nil {
		return iaBookSeriesInputError(err)
	}
	head, exists, err := iaBookSeriesReadHead(ctx, turn, bookID)
	if err != nil {
		return err
	}
	if !exists {
		if count == 0 {
			return nil
		}
		return iaBookSeriesInsert(ctx, turn, libraryID, bookID, asset, policyRevision, pairs, count, retained, digest)
	}
	if head.libraryID != libraryID {
		return ErrConflict
	}
	// The local policy revision is a run-wide epoch, so it is comparable across
	// every asset of the book. An info epoch is only comparable inside one
	// asset: different parts of a multi-part book each carry their own accepted
	// asset epoch, and a later part legitimately has a lower number than the
	// part that published the head. Applying epoch ordering across assets (or a
	// differing-cohort refusal at equal epoch) blocked the whole IA-13 accept of
	// an unrelated part. Therefore epoch staleness is proven only when the head
	// belongs to the exact same asset.
	sameAsset := head.assetID == asset.id
	if head.policyRevision > policyRevision || sameAsset && head.infoEpoch > asset.infoEpoch {
		return ErrConflict
	}
	// Same asset at the same epoch, and any other asset at the same policy, can
	// only no-op on the exact published cohort. A divergent cohort there is
	// contradictory accepted folder evidence, not an update; it is refused as a
	// typed conflict before any write, so the head keeps its prior source.
	if head.policyRevision == policyRevision && (!sameAsset || head.infoEpoch == asset.infoEpoch) {
		if head.count == count && head.memberBytes == retained && equalBytes(head.digest[:], digest[:]) {
			return nil
		}
		return ErrConflict
	}
	// A newer local policy, or a newer accepted epoch of the same asset,
	// invalidates the old projection and publishes the complete new one,
	// including the accepted empty cohort, in this transaction. A newer policy
	// may arrive from another part of the book; the head then records that part
	// as the exact accepted source.
	return iaBookSeriesReplace(ctx, turn, libraryID, bookID, asset, policyRevision, pairs, count, retained, digest, head)
}

func iaBookSeriesReadHead(ctx context.Context, turn *commandTurn, bookID int64) (head iaBookSeriesHeadFacts, exists bool, err error) {
	defer func() { head, err = finishCommand(ctx, commandLifetime(turn), head, err) }()
	row, err := wc008DraftQuery(ctx, turn, iaBookSeriesHead, bookID)
	if err != nil {
		return head, false, err
	}
	var digest []byte
	err = row.Scan(&head.libraryID, &head.assetID, &head.infoEpoch, &head.sourceRevision, &head.policyRevision,
		&head.revision, &head.count, &head.memberBytes, &digest)
	if errors.Is(err, sql.ErrNoRows) {
		return head, false, nil
	}
	if err != nil {
		return head, false, err
	}
	if head.libraryID <= 0 || head.assetID <= 0 || head.infoEpoch <= 0 || head.sourceRevision == "" ||
		head.policyRevision <= 0 || head.revision <= 0 || head.count < 0 || head.count > bookseries.MaxPairs ||
		head.memberBytes < 0 || len(digest) != 32 {
		return head, false, ErrResolveRequired
	}
	copy(head.digest[:], digest)
	return head, true, nil
}

func iaBookSeriesInsert(ctx context.Context, turn *commandTurn, libraryID, bookID int64, asset iaBookAssetFacts, policyRevision int64, pairs []bookseries.Pair, count, retained int64, digest [32]byte) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	row, err := wc008DraftQuery(ctx, turn, iaBookSeriesHeadInsert, bookID, libraryID, asset.id, asset.infoEpoch,
		asset.token, policyRevision, int64(1), count, retained, digest[:])
	if err != nil {
		return err
	}
	var revision int64
	if err = row.Scan(&revision); err != nil {
		return errors.Join(ErrConflict, err)
	}
	if revision != 1 {
		return ErrConflict
	}
	return iaBookSeriesInsertMembers(ctx, turn, bookID, pairs)
}

func iaBookSeriesReplace(ctx context.Context, turn *commandTurn, libraryID, bookID int64, asset iaBookAssetFacts, policyRevision int64, pairs []bookseries.Pair, count, retained int64, digest [32]byte, head iaBookSeriesHeadFacts) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if head.revision == math.MaxInt64 {
		return ErrCap
	}
	if err = iaBookSeriesDeleteMembers(ctx, turn, bookID, head.count); err != nil {
		return err
	}
	row, err := wc008DraftQuery(ctx, turn, iaBookSeriesHeadCAS, asset.id, asset.infoEpoch, asset.token, policyRevision,
		head.revision+1, count, retained, digest[:],
		bookID, libraryID, head.assetID, head.infoEpoch, head.sourceRevision, head.policyRevision, head.revision,
		head.count, head.memberBytes, head.digest[:])
	if err != nil {
		return err
	}
	var revision int64
	if err = row.Scan(&revision); err != nil {
		return errors.Join(ErrConflict, err)
	}
	if revision != head.revision+1 {
		return ErrConflict
	}
	return iaBookSeriesInsertMembers(ctx, turn, bookID, pairs)
}

// The old member cohort must be deleted completely before the head revision
// advances. A count mismatch is a torn projection and refuses the unit.
func iaBookSeriesDeleteMembers(ctx context.Context, turn *commandTurn, bookID, expected int64) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	step, ok := turn.steps[iaBookSeriesMembersDelete]
	if !ok {
		return invalid("ia_book_series_step")
	}
	result, err := turn.exec(ctx, step.statementStep, bookID)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != expected {
		return ErrConflict
	}
	return turn.checkLifetimes(ctx)
}

func iaBookSeriesInsertMembers(ctx context.Context, turn *commandTurn, bookID int64, pairs []bookseries.Pair) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	for i, pair := range pairs {
		row, err := wc008DraftQuery(ctx, turn, iaBookSeriesMemberInsert, bookID, int64(i), pair.Name, pair.Position)
		if err != nil {
			return err
		}
		var ordinal int64
		if err = row.Scan(&ordinal); err != nil {
			return errors.Join(ErrConflict, err)
		}
		if ordinal != int64(i) {
			return ErrConflict
		}
	}
	return nil
}
