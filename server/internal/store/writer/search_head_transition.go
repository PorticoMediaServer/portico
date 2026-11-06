package writer

import (
	"database/sql"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchmanifest"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchpolicy"
)

// Captured values, not permission or an indexed no-next certificate. The actual
// command reads these in W's same protected turn, after ORIGINAL history. It
// must also perform the separately closed own-token native replacement before
// committing this head/accounting transition. No per-child foreign-name copies.
type searchHeadPublishSnapshot struct {
	stage                    searchFragmentStageSnapshot
	nextStoredFragment       sql.NullInt64
	servingRevision, pending int64
}

type searchHeadPublishTransition struct {
	acceptedJobRevision, servingRevision, pending int64
	imageID, inputGeneration, oldImageID          int64
}

func planSearchHeadPublish(s searchHeadPublishSnapshot) (searchHeadPublishTransition, error) {
	x := s.stage
	j, b, source, image, seal := x.job, x.bank, x.source, x.image, x.seal
	in := image.input
	if j.id <= 0 || j.revision <= 0 || source.entityID <= 0 || source.libraryID <= 0 || source.kind != 1 || source.revision <= 0 ||
		j.entityID != source.entityID || j.bankID != b.id || j.sourceRevision != source.revision || j.sourceIncarnation != source.incarnation ||
		j.phase != "publish" || !j.document.Valid || j.document.Int64 != image.id || image.id <= 0 ||
		j.inputGeneration <= 0 || j.publishedGeneration < 0 || j.publishedGeneration >= j.inputGeneration ||
		j.publishedImage.Valid != (j.publishedGeneration > 0) ||
		(j.publishedImage.Valid && (j.publishedImage.Int64 <= 0 || j.publishedImage.Int64 == image.id)) ||
		(!j.publishedImage.Valid && j.publishedImage.Int64 != 0) ||
		b.id <= 0 || b.key != "v1" || (b.role != "active" && b.role != "building") || !searchpolicy.StoredWellFormed(b.profile) || j.recipe != b.profile.RecipeVersion ||
		in.Subject != searchmanifest.Entity || in.SubjectID != source.entityID || in.LibraryID != source.libraryID || in.Kind != source.kind ||
		in.SearchGenerationID != b.id || in.BankKey != b.key || in.Recipe != b.profile.RecipeVersion || in.EngineVersion != b.profile.EngineVersion || in.UnicodeFingerprint != b.profile.UnicodeFingerprint ||
		in.SourceIncarnation != source.incarnation || in.SourceRevision != source.revision || in.InputGeneration != j.inputGeneration {
		return searchHeadPublishTransition{}, ErrConflict
	}
	if !x.active.Valid || x.active.Int64 <= 0 || x.building.Valid && (x.building.Int64 <= 0 || x.building.Int64 == x.active.Int64) ||
		b.role == "active" && b.id != x.active.Int64 || b.role == "building" && (!x.building.Valid || b.id != x.building.Int64) ||
		s.servingRevision <= 0 || s.pending <= 0 || s.nextStoredFragment.Valid || s.nextStoredFragment.Int64 != 0 {
		return searchHeadPublishTransition{}, ErrConflict
	}
	if seal.algorithm != searchmanifest.Algorithm || seal.sealed != 1 || seal.expectedFragments < 0 || seal.expectedBytes < 0 ||
		j.expectedFragments != seal.expectedFragments || j.stagedFragments != seal.expectedFragments || j.stagedBytes != seal.expectedBytes ||
		seal.stagedFragments != seal.expectedFragments || seal.verifiedFragments != seal.expectedFragments ||
		seal.stagedBytes != seal.expectedBytes || seal.verifiedBytes != seal.expectedBytes || seal.chain != seal.expectedDigest ||
		j.afterField != seal.afterField || j.afterOrdinal != seal.afterOrdinal || j.afterFragmentID != seal.afterFragmentID {
		return searchHeadPublishTransition{}, ErrConflict
	}
	if seal.expectedFragments == 0 {
		if seal.expectedBytes != 0 || seal.afterField != 0 || seal.afterOrdinal != 0 || seal.afterFragmentID != 0 {
			return searchHeadPublishTransition{}, ErrConflict
		}
	} else if seal.afterField != 1 || seal.afterOrdinal != seal.expectedFragments-1 || seal.afterFragmentID <= 0 {
		return searchHeadPublishTransition{}, ErrConflict
	}
	seed, err := searchmanifest.Seed(in, seal.expectedFragments, seal.expectedBytes)
	if err != nil || seed != seal.seed || (seal.expectedFragments == 0 && seal.chain != seed) {
		return searchHeadPublishTransition{}, ErrConflict
	}
	nextJob, err := nextSearchInputCounter(j.revision, 1)
	if err != nil {
		return searchHeadPublishTransition{}, err
	}
	// Only an active-bank head changes serving-search revision. A building
	// head is immutable prepared work; global activation owns its later flip.
	serving := s.servingRevision
	if b.role == "active" {
		serving, err = nextSearchInputCounter(serving, 1)
		if err != nil {
			return searchHeadPublishTransition{}, err
		}
	}
	return searchHeadPublishTransition{nextJob, serving, s.pending - 1, image.id, j.inputGeneration, j.publishedImage.Int64}, nil
}
