package writer

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchmanifest"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Fresh native values only, after ORIGINAL history/current disclosure and W's
// dedicated memory admission. The image helper here reads six shared source
// witnesses; it supplies NO image entry, purpose resources or acceptance time.
func searchFragmentStageFreshFacts(ctx context.Context, turn *commandTurn, want searchFragmentStageSnapshot) (out searchFragmentStageSnapshot, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	return searchPublisherStageFacts(ctx, turn, SearchFragmentStage, want)
}

// Shared domain witness reads only. Each caller supplies a CLOSED matching
// command purpose; no resource helper, clock, issuer or entry is borrowed.
func searchPublisherStageFacts(ctx context.Context, turn *commandTurn, purpose CommandID, want searchFragmentStageSnapshot) (out searchFragmentStageSnapshot, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if ctx == nil || turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != purpose || (purpose != SearchFragmentStage && purpose != SearchHeadPublish) {
		return out, ErrInvalid
	}
	defer turn.transaction.guardPanic()
	x := searchImageStageRequest{bank: want.bank, owner: want.job,
		source: searchImageBeginSource{entityID: want.source.entityID, libraryID: want.source.libraryID, kind: want.source.kind, revision: want.source.revision, incarnation: want.source.incarnation}}
	actual, err := searchImageStageFreshFacts(ctx, turn, x)
	if err != nil {
		return out, err
	}
	out = want
	out.bank, out.job = actual.bank, actual.owner
	out.source = searchFragmentSource{actual.source.entityID, actual.source.libraryID, actual.source.kind, actual.source.revision, actual.source.incarnation}
	query := func(id stmt.ID, args ...any) (commandRow, error) {
		step, err := searchPublisherStep(turn, id)
		if err != nil {
			return commandRow{}, err
		}
		return turn.queryRow(ctx, step, args...)
	}
	// Re-read the exact pair carried by this command. The shared source
	// helper validates the target bank's role, but intentionally returns no
	// pointer pair. Charge this second head-point use in the complete recipe.
	row, err := query(searchImageHeads, int64(1))
	if err != nil {
		return out, err
	}
	if err := row.Scan(&out.active, &out.building); err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	if out.active != want.active || out.building != want.building {
		return out, ErrConflict
	}
	row, err = query(searchFragmentImageRead, want.image.id, want.bank.profile.RecipeVersion)
	if err != nil {
		return out, err
	}
	image := searchFragmentImage{input: searchmanifest.Input{Subject: searchmanifest.Entity, BankKey: actual.bank.key,
		EngineVersion: actual.bank.profile.EngineVersion, UnicodeFingerprint: actual.bank.profile.UnicodeFingerprint}}
	var incarnation []byte
	in := &image.input
	if err := row.Scan(&image.id, &in.SubjectID, &in.LibraryID, &in.Kind, &in.SearchGenerationID, &in.InputGeneration, &incarnation, &in.SourceRevision, &in.Recipe); err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	if len(incarnation) != 16 {
		return out, ErrResolveRequired
	}
	copy(in.SourceIncarnation[:], incarnation)
	if image != want.image {
		return out, ErrConflict
	}
	out.image = image
	row, err = query(searchFragmentSealRead, image.id, searchmanifest.Algorithm)
	if err != nil {
		return out, err
	}
	var seal searchFragmentSeal
	var seed, expected, chain []byte
	if err := row.Scan(&seal.algorithm, &seed, &expected, &seal.expectedFragments, &seal.expectedBytes, &seal.stagedFragments, &seal.stagedBytes,
		&seal.verifiedFragments, &seal.verifiedBytes, &chain, &seal.afterField, &seal.afterOrdinal, &seal.afterFragmentID, &seal.sealed); err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	if len(seed) != 32 || len(expected) != 32 || len(chain) != 32 {
		return out, ErrResolveRequired
	}
	copy(seal.seed[:], seed)
	copy(seal.expectedDigest[:], expected)
	copy(seal.chain[:], chain)
	if seal != want.seal {
		return out, ErrConflict
	}
	out.seal = seal
	row, err = query(searchFragmentHeldRead, image.id)
	if err != nil {
		return out, err
	}
	var publication int64
	var live sql.NullInt64
	var phase string
	if err := row.Scan(&publication, &live, &phase); err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	if publication != image.id || !live.Valid || live.Int64 != image.id || phase != "held" {
		return out, ErrConflict
	}
	return out, nil
}
