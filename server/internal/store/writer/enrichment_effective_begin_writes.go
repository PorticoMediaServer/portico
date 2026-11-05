package writer

import (
	"context"
	"strings"
	"unicode/utf8"
)

type effectiveBeginImage struct{ id, revision int64 }

// This is one private effect step, not an accepting entry. The closed handler
// must first run W's current disclosure/receipt protocol, full fresh-native
// proof and complete admission. Its caller owns rollback until a typed receipt
// and all effects pass W's terminal path. No Applied is returned here.
func effectiveBeginInsertHiddenHeader(ctx context.Context, turn *commandTurn, step statementStep, h effectiveBeginHeader, request RequestDigest, retirementIncarnation [16]byte) (effectiveBeginImage, error) {
	var image effectiveBeginImage
	if step.id != enrichmentEffectiveInsert || turn == nil {
		return image, ErrInvalid
	}
	if h.entityID <= 0 || h.sourceRevision <= 0 || h.identityRevision <= 0 || h.metadataRevision <= 0 ||
		h.ownerRevision <= 0 || h.policyRevision < 0 || h.consentRevision < 0 || h.expectedCount < 0 || h.expectedBytes < 0 ||
		(h.expectedCount == 0) != (h.expectedBytes == 0) || !utf8.ValidString(h.language) || strings.IndexByte(h.language, 0) >= 0 {
		return image, invalid("effective_begin_intent")
	}
	seed := effectiveBeginBuildSeed(h)
	if h.expectedCount == 0 && h.expectedManifest != seed {
		return image, invalid("effective_begin_empty_intent")
	}
	row, err := turn.queryRow(ctx, step, effectiveBeginHeaderValues(h, request, seed, retirementIncarnation)...)
	if err != nil {
		return image, err
	}
	if err := row.Scan(&image.id, &image.revision); err != nil {
		return effectiveBeginImage{}, err
	}
	if image.id <= 0 || image.revision <= 0 {
		return effectiveBeginImage{}, invalid("effective_begin_returned_identity")
	}
	return image, nil
}

func effectiveBeginAttachHiddenHeader(ctx context.Context, turn *commandTurn, step statementStep, ownerID string, owner effectiveBeginOwner, identity CommandIdentity, image effectiveBeginImage) error {
	if step.id != enrichmentEffectiveAttach || turn == nil || !owner.exists || image.id <= 0 || image.revision <= 0 {
		return ErrInvalid
	}
	// This is a scalar effect precondition, not a second receipt classifier.
	// W must already have established receiptFresh for this ORIGINAL request.
	if owner.lastSequence < 0 || identity.Sequence <= 0 || owner.incarnation != identity.Incarnation || owner.lastSequence != owner.acknowledgedSequence ||
		identity.Sequence <= owner.lastSequence || identity.Sequence-owner.lastSequence != 1 || owner.imageID.Valid {
		return ErrConflict
	}
	result, err := turn.exec(ctx, step, image.id, identity.Sequence, ownerID, owner.incarnation[:],
		owner.lastSequence, owner.acknowledgedSequence, owner.entityID, owner.generation,
		owner.phase, owner.status, owner.lease, owner.leaseUntil, owner.page, owner.afterItem)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return ErrConflict
	}
	return nil
}
