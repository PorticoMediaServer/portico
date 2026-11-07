package writer

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

// One chosen local input/category, never a gallery. This first effect creates
// an absent matched acquisition job. Existing job replacement requires its
// separate physical-work retirement/custody contract and fails closed here.
// It leaves both matched and locked serving selections completely untouched.
type basicArtworkInput struct {
	source                                                                            basicStreamFactStage
	entityID, ownerSourceRevision, ownerArtworkRevision, selectionRevision            int64
	ownerArtworkIncarnation                                                           [16]byte
	jobID, role, imageID, origin, attribution, locale, effectiveLanguage, sourceFence string
	valid                                                                             bool
}
type basicArtworkProgress struct {
	jobID                          string
	jobRevision, selectionRevision int64
}

func prepareBasicArtwork(in basicArtworkInput) (*basicArtworkInput, error) {
	remaining := 33554432
	for _, s := range [...]string{in.source.assetToken, in.source.runID, in.source.sourceID, in.source.incarnation, in.source.objectID, in.source.objectRevision, in.source.fingerprint, in.jobID, in.role, in.imageID, in.origin, in.attribution, in.locale, in.effectiveLanguage, in.sourceFence} {
		if len(s) > remaining {
			return nil, ErrCap
		}
		remaining -= len(s)
		if !utf8.ValidString(s) {
			return nil, ErrInvalid
		}
	}
	if in.entityID <= 0 || in.ownerSourceRevision <= 0 || in.ownerArtworkRevision <= 0 || in.ownerArtworkIncarnation == ([16]byte{}) || in.selectionRevision < 0 || in.jobID == "" || in.imageID == "" || in.origin == "" || in.sourceFence == "" || in.role == "" || in.role != strings.ToLower(strings.TrimSpace(in.role)) || in.role == "thumb" || in.role == "thumbnail" {
		return nil, ErrInvalid
	}
	in.source = cloneBasicSource(in.source)
	in.jobID = strings.Clone(in.jobID)
	in.role = strings.Clone(in.role)
	in.imageID = strings.Clone(in.imageID)
	in.origin = strings.Clone(in.origin)
	in.attribution = strings.Clone(in.attribution)
	in.locale = strings.Clone(in.locale)
	in.effectiveLanguage = strings.Clone(in.effectiveLanguage)
	in.sourceFence = strings.Clone(in.sourceFence)
	in.valid = true
	return &in, nil
}

func queueBasicLocalArtwork(ctx context.Context, turn *commandTurn, in *basicArtworkInput, appliedMS int64) (value basicArtworkProgress, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	return queueBasicLocalArtworkAtPhase(ctx, turn, in, appliedMS, 0)
}

// queueBasicLocalArtworkForAccept applies local effects before activation in the caller's
// final transaction. The source still names the preactivation asset epoch.
func queueBasicLocalArtworkForAccept(ctx context.Context, turn *commandTurn, in *basicArtworkInput, appliedMS int64) (value basicArtworkProgress, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	return queueBasicLocalArtworkAtPhase(ctx, turn, in, appliedMS, 3)
}

func queueBasicLocalArtworkAtPhase(ctx context.Context, turn *commandTurn, in *basicArtworkInput, appliedMS, phase int64) (value basicArtworkProgress, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if ctx == nil || turn == nil || in == nil || !in.valid || appliedMS < 0 || (phase != 0 && phase != 3) {
		return value, ErrInvalid
	}
	if err = checkBasicInputAtPhase(ctx, turn, in.source, phase); err != nil {
		return value, err
	}
	row, err := basicStageRead(ctx, turn, basicArtworkEntity, in.entityID)
	if err != nil {
		return value, err
	}
	var library, kind, retired, sourceRevision, artworkRevision int64
	var incarnation []byte
	if err = row.Scan(&library, &kind, &retired, &sourceRevision, &artworkRevision, &incarnation); err != nil {
		return value, basicStageError(err)
	}
	if library != in.source.libraryID || kind != 1 || retired != 0 || sourceRevision != in.ownerSourceRevision || artworkRevision != in.ownerArtworkRevision || !bytes.Equal(incarnation, in.ownerArtworkIncarnation[:]) {
		return value, ErrConflict
	}
	row, err = basicStageRead(ctx, turn, basicArtworkSubject, in.entityID)
	if err != nil {
		return value, err
	}
	if err = row.Scan(&library, &kind); err != nil {
		return value, basicStageError(err)
	}
	if library != in.source.libraryID || kind != 1 {
		return value, ErrConflict
	}
	row, err = basicStageRead(ctx, turn, basicArtworkLink, in.entityID, in.source.assetID)
	if err != nil {
		return value, err
	}
	if err = row.Scan(&library); err != nil {
		return value, basicStageError(err)
	}
	if library != in.source.libraryID {
		return value, ErrConflict
	}
	rows, err := basicRows(ctx, turn, basicArtworkSelection, "entity", in.entityID, in.role, "matched")
	if err != nil {
		return value, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	if in.selectionRevision == 0 {
		if rows.Next() {
			return value, ErrConflict
		}
		if err = rows.Err(); err != nil {
			return value, err
		}
	} else if err = basicMediaExactRow(rows, []any{in.selectionRevision}); err != nil {
		return value, err
	}
	if err = rows.Close(); err != nil {
		return value, err
	}
	jobs, err := basicRows(ctx, turn, basicArtworkJob, "entity", in.entityID, in.role, "matched")
	if err != nil {
		return value, err
	}
	defer func() { err = errors.Join(err, jobs.Close()) }()
	if jobs.Next() {
		return value, ErrConflict
	}
	if err = jobs.Err(); err != nil {
		return value, err
	}
	if err = jobs.Close(); err != nil {
		return value, err
	}
	// Timestamp is derived only from W's one private sample. No clock or SQLnow.
	created := time.UnixMilli(appliedMS).UTC().Format(time.RFC3339Nano)
	args := []any{in.jobID, "entity", in.entityID, in.role, "matched", in.imageID, in.attribution, in.locale, in.effectiveLanguage, in.source.objectID, in.source.objectRevision, in.origin, "local", in.sourceFence, in.selectionRevision, "pending", "", "", created, int64(0), int64(0), int64(0), int64(0), int64(0), appliedMS, int64(1)}
	inserted, err := basicRows(ctx, turn, basicArtworkInsert, args...)
	if err != nil {
		return value, err
	}
	if err = basicMediaExactRow(inserted, args); err != nil {
		return value, err
	}
	return basicArtworkProgress{in.jobID, 1, in.selectionRevision}, nil
}
