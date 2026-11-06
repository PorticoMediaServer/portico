package writer

import (
	"context"
	"database/sql"
)

// FIRST leaf initialization has no child scalar contribution, authored parent,
// facet, deferred aggregate publication or Basic availability. Every native
// counter used here retains its own meaning and must be read, not aliased.
func applyInventoryFreshAggregates(ctx context.Context, turn *commandTurn, c *inventoryAcceptCommand, f *inventoryFreshFacts, a *inventoryFreshAllocation) error {
	r, file := c.request, c.request.files[0]
	row, err := turn.queryRow(ctx, turn.steps[scanFreshAssetRevision].statementStep, a.assetID, r.scope.libraryID, a.assetToken, file.assetPath, file.size, file.modifiedNS)
	if err != nil {
		return err
	}
	var infoEpoch int64
	if err = row.Scan(&a.fileSizeRevision, &infoEpoch); err != nil {
		return inventoryFreshReadError(err)
	}
	if a.fileSizeRevision <= 0 || infoEpoch != a.infoEpoch {
		return ErrConflict
	}
	row, err = turn.queryRow(ctx, turn.steps[scanFreshEntityFacts].statementStep, a.subjectID, r.scope.libraryID, a.metadata[:], a.entityRevision)
	if err != nil {
		return err
	}
	if err = row.Scan(&a.scalarRevision, &a.aggregateRevision, &a.admissionRevision); err != nil {
		return inventoryFreshReadError(err)
	}
	if a.scalarRevision <= 0 || a.aggregateRevision <= 0 || a.admissionRevision <= 0 {
		return ErrConflict
	}
	row, err = turn.queryRow(ctx, turn.steps[scanFreshEntityInitial].statementStep, file.size, int64(-1), nil, nil, int64(0), int64(0), nil, nil,
		a.subjectID, r.scope.libraryID, a.metadata[:], a.entityRevision, a.scalarRevision, a.aggregateRevision, a.admissionRevision)
	if err != nil {
		return err
	}
	var revision, scalar, aggregate, admission, size, resolution, duration, community, critic, episodes, members, unrated int64
	var latest string
	var allow, known, own, minKnown, publication sql.NullInt64
	if err = row.Scan(&revision, &scalar, &aggregate, &admission, &size, &resolution, &duration, &community, &critic,
		&latest, &episodes, &allow, &known, &own, &members, &unrated, &minKnown, &publication); err != nil {
		return inventoryFreshReadError(err)
	}
	if revision != a.entityRevision || scalar != a.scalarRevision || aggregate != a.aggregateRevision || admission != a.admissionRevision ||
		size != file.size || resolution != 0 || duration != 0 || community != 0 || critic != 0 || latest != "" || episodes != 0 ||
		!allow.Valid || allow.Int64 != -1 || known.Valid || own.Valid || members != 0 || unrated != 0 || minKnown.Valid || publication.Valid {
		return ErrConflict
	}
	// Capture the ACTUAL post-CAS membership sequence. This is one membership
	// event for the entire N1 cohort, never subject ID or a synthesized epoch.
	row, err = turn.queryRow(ctx, turn.steps[scanFreshMembershipCAS].statementStep, f.newMembership, f.membership)
	if err != nil {
		return err
	}
	var sequence int64
	if err = row.Scan(&sequence); err != nil {
		return inventoryFreshReadError(err)
	}
	if sequence != f.newMembership {
		return ErrConflict
	}
	if err = execInventoryFreshOne(ctx, turn, scanFreshScalarInsert, r.scope.libraryID, a.subjectID, a.subjectID, int64(0), a.scalarRevision, sequence,
		nil, nil, nil, nil, nil, nil); err != nil {
		return err
	}
	if err = execInventoryFreshOne(ctx, turn, scanFreshPhysicalInsert, r.scope.libraryID, a.subjectID, a.assetID, int64(1), int64(0), file.size, a.fileSizeRevision); err != nil {
		return err
	}
	// An unrated LEAF has (allow=-1, known=NULL). Mode1 gets NO row or
	// increment. NULL never becomes a -1 bucket that would permit the leaf.
	if f.ageExists {
		err = execInventoryFreshOne(ctx, turn, scanFreshAgeCAS, f.newAgeTotal, r.scope.libraryID, scanNewMovieKind, int64(0), int64(-1), f.ageTotal)
	} else {
		err = execInventoryFreshOne(ctx, turn, scanFreshAgeInsert, r.scope.libraryID, scanNewMovieKind, int64(0), int64(-1), f.newAgeTotal)
	}
	if err != nil {
		return err
	}
	return execInventoryFreshOne(ctx, turn, scanFreshLibraryCAS, f.newData, f.newOrder, f.newMetadata, f.newAgeInput, f.newAgePublished,
		r.scope.libraryKey, f.data, f.order, f.metadata, f.ageInput, f.agePublished, f.agePending, file.metadataLanguage)
}
