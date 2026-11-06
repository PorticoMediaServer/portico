package writer

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func searchHeadPublishFreshFacts(ctx context.Context, turn *commandTurn, want searchHeadPublishRequest) (out searchHeadPublishRequest, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != SearchHeadPublish {
		return out, ErrInvalid
	}
	defer turn.transaction.guardPanic()
	stage, err := searchPublisherStageFacts(ctx, turn, SearchHeadPublish, want.snapshot.stage)
	if err != nil {
		return out, err
	}
	out = want
	out.snapshot.stage = stage
	query := func(id stmt.ID, args ...any) (commandRow, error) {
		step, e := searchPublisherStep(turn, id)
		if e != nil {
			return commandRow{}, e
		}
		return turn.queryRow(ctx, step, args...)
	}
	row, err := query(searchHeadClockRead, int64(1))
	if err != nil {
		return out, err
	}
	if err = row.Scan(&out.snapshot.servingRevision); err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	b := stage.bank
	row, err = query(searchHeadPendingRead, b.id, b.key, b.role, b.profile.RecipeVersion, b.profile.EngineVersion, b.profile.UnicodeFingerprint)
	if err != nil {
		return out, err
	}
	if err = row.Scan(&out.snapshot.pending); err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	row, err = query(searchHeadNoNext, stage.image.id, stage.seal.afterFragmentID, int64(1))
	if err != nil {
		return out, err
	}
	var next int64
	err = row.Scan(&next)
	if err == nil {
		return out, ErrConflict
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	out.snapshot.nextStoredFragment = sql.NullInt64{}
	readOwn := func(imageID, generation int64) (searchOwnPrefixImage, error) {
		s := stage.source
		row, e := query(searchHeadOwnRead, imageID, s.entityID, s.libraryID, s.kind, b.id, generation, b.profile.RecipeVersion, "")
		if e != nil {
			return searchOwnPrefixImage{}, e
		}
		var h searchOwnPrefixImage
		e = row.Scan(&h.imageID, &h.prefix, &h.initial)
		return h, e
	}
	out.next, err = readOwn(stage.image.id, stage.job.inputGeneration)
	if err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	if stage.job.publishedImage.Valid {
		out.old, err = readOwn(stage.job.publishedImage.Int64, stage.job.publishedGeneration)
		if err != nil {
			return out, errors.Join(ErrConflict, err)
		}
		row, err = query(searchHeadOldCustody, out.old.imageID)
		if err != nil {
			return out, err
		}
		o := &out.retirement
		var inc []byte
		err = row.Scan(&o.id, &o.publicationID, &o.livePublicationID, &o.phase, &inc, &o.revision, &o.afterFragmentID, &o.lastSequence, &o.acknowledgedSequence, &o.leaseOwner, &o.leaseGeneration, &o.leaseExpiresMS, &o.attempts, &o.nextAttempt)
		if err != nil {
			return out, errors.Join(ErrConflict, err)
		}
		if len(inc) != 16 {
			return out, ErrResolveRequired
		}
		copy(o.incarnation[:], inc)
	}
	if out != want {
		return out, ErrConflict
	}
	return out, nil
}
