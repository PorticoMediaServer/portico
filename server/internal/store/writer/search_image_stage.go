package writer

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchmanifest"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchpolicy"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Closed domain entry on W's SAME admitted turn. W establishes current opaque
// and retained native disclosure before invoking it, and alone owns terminal
// permission/COMMIT truth and the public Applied projection. Original history
// precedes every fresh source, memory, profile, physical engine and lease check.
func applySearchImageStage(ctx context.Context, turn *commandTurn, c *searchImageStageCommand) (out SearchImageStageResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if ctx == nil || turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != SearchImageStage || c == nil || !c.valid {
		return out, ErrInvalid
	}
	defer turn.transaction.guardPanic()
	q := PublisherReceiptRequest{JobID: c.request.owner.id, LibraryID: c.request.source.libraryID, EntityID: c.request.source.entityID, Receipt: ReceiptReference{c.identity, c.digest}}
	lookup, err := lookupSearchImageStage(ctx, turn, q)
	if err != nil {
		return out, err
	}
	if lookup.decision == receiptReplay {
		return lookup.result, nil
	}
	if lookup.decision != receiptFresh {
		return out, ErrResolveRequired
	}
	// Fail absent registration BEFORE any fresh read/allocation/effect. No Work is
	// created here; only W's exact immutable registered steps may execute.
	for _, id := range [...]stmt.ID{searchImageSource, searchImageSubject, searchImageBank, searchImageHeads, searchImageLibrary, searchImageJob, searchImageInsert, searchImageSealInsert, searchImageReclaimInsert, searchImageAttachNoHeadNoDue, searchImageAttachNoHeadDue, searchImageAttachHeadNoDue, searchImageAttachHeadDue} {
		if _, err := searchPublisherStep(turn, id); err != nil {
			return out, err
		}
	}
	if _, err := searchPublisherStep(turn, searchPublisherImagePut); err != nil {
		return out, err
	}
	required, err := searchImageFreshRequiredBytes(c)
	if err != nil {
		return out, err
	}
	if err := turn.requireSearchImageMemory(ctx, required); err != nil {
		return out, err
	}
	// The original complete packet is rehashed only within that same memory
	// reservation. Allocated image IDs/time never enter the request digest.
	digest, err := canonicalRequestDigest(SearchImageStage, 1, c.identity, c.request)
	if err != nil || digest != c.digest {
		return out, ErrConflict
	}
	native, err := searchImageStageFreshFacts(ctx, turn, c.request)
	if err != nil {
		return out, err
	}
	actual, err := turn.searchImageEngine(ctx)
	if err != nil {
		return out, err
	}
	if searchpolicy.Compare(native.bank.profile, actual).State != searchpolicy.Current {
		return out, ErrConflict
	}
	appliedMS, err := turn.searchImageTimeMS(ctx, native.owner.leaseExpiresMS)
	if err != nil {
		return out, err
	}
	next, err := nextSearchInputCounter(native.owner.revision, 1)
	if err != nil {
		return out, err
	}
	seed, err := searchmanifest.Seed(searchImageBeginManifestInput(native), native.header.expectedFragments, native.header.expectedBytes)
	if err != nil || seed != c.seed {
		return out, ErrConflict
	}
	if _, err := searchmanifest.Restore(searchmanifest.Algorithm, seed, native.header.expectedDigest, native.header.expectedFragments, native.header.expectedBytes); err != nil {
		return out, ErrConflict
	}
	step, _ := searchPublisherStep(turn, searchImageInsert)
	s, b, h, o := native.source, native.bank, native.header, native.owner
	row, err := turn.queryRow(ctx, step, s.entityID, b.id, s.title, "", s.libraryID, s.kind, o.inputGeneration, s.incarnation[:], s.revision, b.profile.RecipeVersion, h.titleFold, h.titlePrefixTokens, h.initialPrefixTokens)
	if err != nil {
		return out, err
	}
	var imageID int64
	if err := row.Scan(&imageID); err != nil {
		return out, err
	}
	if imageID <= 0 {
		return out, ErrResolveRequired
	}
	for _, effect := range []struct {
		id   stmt.ID
		args []any
	}{
		{searchImageSealInsert, []any{imageID, searchmanifest.Algorithm, seed[:], h.expectedDigest[:], h.expectedFragments, h.expectedBytes, seed[:]}},
		{searchImageReclaimInsert, []any{imageID, imageID, "held", c.retirementIncarnation[:]}},
	} {
		step, _ := searchPublisherStep(turn, effect.id)
		result, err := turn.exec(ctx, step, effect.args...)
		if err != nil {
			return out, err
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return out, err
		}
		if changed != 1 {
			return out, ErrResolveRequired
		}
	}
	id, args := searchImageStageAttachValues(c, imageID, next)
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
	if accepted != next {
		return out, ErrResolveRequired
	}
	out = SearchImageStageResult{JobID: o.id, AcceptedJobRevision: accepted, AppliedMS: appliedMS, ImageID: imageID, InputGeneration: o.inputGeneration, PublishedGeneration: o.publishedGeneration, SealChain: seed, SealAlgorithm: searchmanifest.Algorithm}
	step, _ = searchPublisherStep(turn, searchPublisherImagePut)
	result, err := turn.exec(ctx, step, o.id, c.identity.Incarnation[:], c.identity.Sequence, c.identity.OperationID[:], c.digest[:], searchImageReceiptKind,
		accepted, appliedMS, o.inputGeneration, imageID, nil, int64(0), int64(0), int64(0), seed[:], o.publishedGeneration, int64(0), int64(0), int64(0), int64(0), searchmanifest.Algorithm)
	if err != nil {
		return out, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return out, err
	}
	if changed != 1 {
		return out, ErrResolveRequired
	}
	if err := checkSearchImageTerminal(ctx, turn, c); err != nil {
		return out, err
	}
	return out, nil // ZERO Outcome: a body return is not physical COMMIT truth.
}

// Source TEXT was captured/prepared under separate bounded custody. Recheck the
// INT-native metadata incarnation/revision, not a second unbounded title fetch.
// A matching version covers own primary title only in this FIRST closed recipe.
func searchImageStageFreshFacts(ctx context.Context, turn *commandTurn, want searchImageStageRequest) (out searchImageStageRequest, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	out = want
	query := func(id stmt.ID, args ...any) (commandRow, error) {
		step, err := searchPublisherStep(turn, id)
		if err != nil {
			return commandRow{}, err
		}
		return turn.queryRow(ctx, step, args...)
	}
	row, err := query(searchImageJob, want.owner.id, want.owner.recipe, want.owner.phase, want.owner.contentDigest)
	if err != nil {
		return out, err
	}
	var o searchImageBeginOwner
	var inc, source []byte
	err = row.Scan(&o.id, &o.entityID, &o.bankID, &inc, &o.revision, &o.inputGeneration, &o.publishedGeneration, &o.publishedImage, &o.document, &source, &o.sourceRevision,
		&o.recipe, &o.phase, &o.afterField, &o.afterOrdinal, &o.afterFragmentID, &o.expectedFragments, &o.stagedFragments, &o.stagedBytes, &o.contentDigest,
		&o.leaseOwner, &o.leaseGeneration, &o.leaseExpiresMS, &o.attempts, &o.nextAttempt, &o.lastSequence, &o.acknowledgedSequence)
	if err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	if len(inc) != 16 || len(source) != 16 {
		return out, ErrResolveRequired
	}
	copy(o.incarnation[:], inc)
	copy(o.sourceIncarnation[:], source)
	if o != want.owner {
		return out, ErrConflict
	}
	out.owner = o
	row, err = query(searchImageSource, want.source.entityID, want.source.libraryID, want.source.kind)
	if err != nil {
		return out, err
	}
	s := searchImageBeginSource{title: want.source.title}
	var metadata []byte
	if err := row.Scan(&s.entityID, &s.libraryID, &s.kind, &s.revision, &metadata); err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	if len(metadata) != 16 {
		return out, ErrResolveRequired
	}
	copy(s.incarnation[:], metadata)
	if s != want.source {
		return out, ErrConflict
	}
	out.source = s
	row, err = query(searchImageSubject, s.entityID, s.libraryID, s.kind)
	if err != nil {
		return out, err
	}
	var subject int64
	if err := row.Scan(&subject); err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	if subject != s.entityID {
		return out, ErrConflict
	}
	row, err = query(searchImageLibrary, s.libraryID)
	if err != nil {
		return out, err
	}
	var library int64
	if err := row.Scan(&library); err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	if library != s.libraryID {
		return out, ErrConflict
	}
	row, err = query(searchImageBank, want.bank.id, want.bank.key, want.bank.role, want.bank.profile.RecipeVersion, want.bank.profile.EngineVersion, want.bank.profile.UnicodeFingerprint)
	if err != nil {
		return out, err
	}
	var b searchImageBeginBank
	if err := row.Scan(&b.id, &b.key, &b.role, &b.profile.RecipeVersion, &b.profile.EngineVersion, &b.profile.UnicodeFingerprint); err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	// Exact captured tuple; compatibility is checked separately against W's
	// actual same-physical engine. This read cannot manufacture that evidence.
	if b != want.bank {
		return out, ErrConflict
	}
	out.bank = b
	row, err = query(searchImageHeads, int64(1))
	if err != nil {
		return out, err
	}
	var active, building sql.NullInt64
	if err := row.Scan(&active, &building); err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	if !active.Valid || active.Int64 <= 0 || building.Valid && (building.Int64 <= 0 || building.Int64 == active.Int64) {
		return out, ErrConflict
	}
	if b.role == "active" && active.Int64 != b.id || b.role == "building" && (!building.Valid || building.Int64 != b.id) {
		return out, ErrConflict
	}
	return out, nil
}

func searchImageStageAttachValues(c *searchImageStageCommand, image, next int64) (stmt.ID, []any) {
	o, h := c.request.owner, c.request.header
	id := searchImageAttachNoHeadNoDue
	if o.publishedImage.Valid {
		id = searchImageAttachHeadNoDue
	}
	if o.nextAttempt.Valid {
		if o.publishedImage.Valid {
			id = searchImageAttachHeadDue
		} else {
			id = searchImageAttachNoHeadDue
		}
	}
	args := []any{image, "stage", h.expectedFragments, int64(0), int64(0), int64(0), int64(0), int64(0), next, c.identity.Sequence,
		o.id, o.entityID, o.bankID, c.identity.Incarnation[:], o.revision, o.inputGeneration, o.sourceIncarnation[:], o.sourceRevision, o.recipe, o.phase, o.publishedGeneration}
	if o.publishedImage.Valid {
		args = append(args, o.publishedImage.Int64)
	}
	args = append(args, o.afterField, o.afterOrdinal, o.afterFragmentID, o.expectedFragments, o.stagedFragments, o.stagedBytes, o.contentDigest, o.leaseOwner, o.leaseGeneration, o.leaseExpiresMS, o.attempts)
	if o.nextAttempt.Valid {
		args = append(args, o.nextAttempt.Int64)
	}
	args = append(args, o.lastSequence, o.acknowledgedSequence)
	return id, args
}
