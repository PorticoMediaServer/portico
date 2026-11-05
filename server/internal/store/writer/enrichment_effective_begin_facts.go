package writer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// These are transaction-local native facts, not a grant, prepared delta plan,
// disclosure decision or Applied result. The domain entry still needs W's
// current-disclosure bridge, common classifier and complete fresh-source proof.
type effectiveBeginOwner struct {
	receiptOwnerFacts
	entityID, generation                                      int64
	provider, phase, status, lease, leaseUntil                string
	entityIncarnation, policyIncarnation                      [16]byte
	sourceRevision, identityRevision, metadataRevision        int64
	hierarchyRevision, selectionRevision, descriptiveRevision int64
	policyRevision, consentRevision                           int64
	algorithm, refreshMode, inputDigest, selectedID           string
	page                                                      int64
	afterItem                                                 string
	imageID                                                   sql.NullInt64
}

type effectiveBeginReceipt struct {
	exists                                bool
	identity                              receiptIdentity
	kind                                  string
	acceptedRevision, appliedMS, entityID int64
	afterItem                             string
	nextOrdinal                           int64
	publicationID                         sql.NullString
	imageID, imageRevision                sql.NullInt64
	resultCount                           int64
	resultDigest                          [32]byte
}

type effectiveBeginEntity struct {
	id, libraryID, kind, retired, revision             int64
	incarnation                                        [16]byte
	sourceRevision, identityRevision, metadataRevision int64
	language                                           string
	ownerRevision, ownerLocked                         int64
	imageID                                            sql.NullInt64
}

func effectiveBeginOwnerFacts(ctx context.Context, turn *commandTurn, step statementStep, ownerID string) (effectiveBeginOwner, error) {
	var out effectiveBeginOwner
	if step.id != enrichmentEffectiveOwner || turn == nil {
		return out, ErrInvalid
	}
	row, err := turn.queryRow(ctx, step, ownerID)
	if err != nil {
		return out, err
	}
	var incarnation, entityIncarnation, policyIncarnation []byte
	err = row.Scan(&incarnation, &out.lastSequence, &out.acknowledgedSequence,
		&out.entityID, &out.provider, &out.phase, &out.status, &out.generation,
		&out.lease, &out.leaseUntil, &entityIncarnation, &out.sourceRevision,
		&out.identityRevision, &out.metadataRevision, &out.hierarchyRevision,
		&out.selectionRevision, &out.descriptiveRevision, &policyIncarnation,
		&out.policyRevision, &out.consentRevision, &out.algorithm, &out.refreshMode,
		&out.inputDigest, &out.selectedID, &out.page, &out.afterItem, &out.imageID)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil // No owner is never an implicit fresh allocation.
	}
	if err != nil {
		return effectiveBeginOwner{}, err
	}
	if len(incarnation) != 16 || len(entityIncarnation) != 16 || len(policyIncarnation) != 16 {
		return effectiveBeginOwner{}, fmt.Errorf("%w: invalid enrichment owner identity width", ErrResolveRequired)
	}
	copy(out.incarnation[:], incarnation)
	copy(out.entityIncarnation[:], entityIncarnation)
	copy(out.policyIncarnation[:], policyIncarnation)
	out.exists = true
	return out, nil
}

func effectiveBeginReceiptFacts(ctx context.Context, turn *commandTurn, step statementStep, ownerID string, identity CommandIdentity) (effectiveBeginReceipt, error) {
	var out effectiveBeginReceipt
	if step.id != enrichmentEffectiveReceipt || turn == nil {
		return out, ErrInvalid
	}
	row, err := turn.queryRow(ctx, step, ownerID, identity.Incarnation[:], identity.Sequence)
	if err != nil {
		return out, err
	}
	var incarnation, operationID, requestDigest, resultDigest []byte
	err = row.Scan(&incarnation, &out.identity.command.Sequence, &operationID,
		&requestDigest, &out.kind, &out.acceptedRevision, &out.appliedMS, &out.entityID,
		&out.afterItem, &out.nextOrdinal, &out.publicationID, &out.imageID,
		&out.imageRevision, &out.resultCount, &resultDigest)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return effectiveBeginReceipt{}, err
	}
	if len(incarnation) != 16 || len(operationID) != 16 || len(requestDigest) != 32 || len(resultDigest) != 32 {
		return effectiveBeginReceipt{}, fmt.Errorf("%w: invalid enrichment receipt identity width", ErrResolveRequired)
	}
	copy(out.identity.command.Incarnation[:], incarnation)
	copy(out.identity.command.OperationID[:], operationID)
	copy(out.identity.digest[:], requestDigest)
	copy(out.resultDigest[:], resultDigest)
	out.exists = true
	return out, nil
}

func effectiveBeginEntityFacts(ctx context.Context, turn *commandTurn, step statementStep, entityID int64) (effectiveBeginEntity, error) {
	var out effectiveBeginEntity
	if step.id != enrichmentEffectiveEntity || turn == nil {
		return out, ErrInvalid
	}
	row, err := turn.queryRow(ctx, step, entityID)
	if err != nil {
		return out, err
	}
	var incarnation []byte
	err = row.Scan(&out.id, &out.libraryID, &out.kind, &out.retired, &out.revision,
		&incarnation, &out.sourceRevision, &out.identityRevision, &out.metadataRevision,
		&out.language, &out.ownerRevision, &out.ownerLocked, &out.imageID)
	if err != nil {
		return effectiveBeginEntity{}, err
	}
	if len(incarnation) != 16 {
		return effectiveBeginEntity{}, fmt.Errorf("%w: invalid enrichment entity incarnation width", ErrResolveRequired)
	}
	copy(out.incarnation[:], incarnation)
	return out, nil
}
