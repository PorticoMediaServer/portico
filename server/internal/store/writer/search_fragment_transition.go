package writer

import (
	"database/sql"
	"math"
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchmanifest"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchpolicy"
)

// Captured values only. These become fresh witnesses ONLY when W reads and
// fences them on its same admitted turn AFTER original receipt resolution.
// No generation-driver revision/lease/cursor/statistics are source dependencies.
type searchFragmentSource struct {
	entityID, libraryID, kind, revision int64
	incarnation                         [16]byte
}

type searchFragmentImage struct {
	id    int64
	input searchmanifest.Input
}

type searchFragmentSeal struct {
	algorithm                                         string
	seed, expectedDigest, chain                       [32]byte
	expectedFragments, expectedBytes                  int64
	stagedFragments, stagedBytes                      int64
	verifiedFragments, verifiedBytes                  int64
	afterField, afterOrdinal, afterFragmentID, sealed int64
}

type searchFragmentStageSnapshot struct {
	bank             searchImageBeginBank
	job              searchImageBeginOwner
	source           searchFragmentSource
	image            searchFragmentImage
	seal             searchFragmentSeal
	active, building sql.NullInt64
}

// Prospective scalar transition, never Applied/native IDs/availability. The
// fragment ID and accepted revision must still come from native RETURNING.
// Both staged counters change; verifier chain/totals/cursor remain independent.
type searchFragmentTransition struct {
	jobRevision, stagedFragments, stagedBytes int64
	afterField, afterOrdinal                  int64
	verifiedFragments, verifiedBytes          int64
	sealChain                                 [32]byte
}

func planSearchFragmentStage(s searchFragmentStageSnapshot, p searchPreparedFragment) (searchFragmentTransition, error) {
	if !p.valid {
		return searchFragmentTransition{}, ErrInvalid
	}
	j, b, source, image, seal := s.job, s.bank, s.source, s.image, s.seal
	i := image.input
	if j.id <= 0 || j.revision <= 0 || j.entityID != source.entityID || j.bankID != b.id ||
		j.phase != "stage" || !j.document.Valid || j.document.Int64 != image.id || image.id <= 0 ||
		source.entityID <= 0 || source.libraryID <= 0 || source.kind != 1 || source.revision <= 0 ||
		j.sourceIncarnation != source.incarnation || j.sourceRevision != source.revision ||
		b.id <= 0 || b.key != "v1" || (b.role != "active" && b.role != "building") || !searchpolicy.StoredWellFormed(b.profile) ||
		j.recipe != b.profile.RecipeVersion || j.inputGeneration <= 0 || j.publishedGeneration < 0 || j.publishedGeneration >= j.inputGeneration ||
		j.publishedImage.Valid != (j.publishedGeneration > 0) ||
		(j.publishedImage.Valid && j.publishedImage.Int64 <= 0) || (!j.publishedImage.Valid && j.publishedImage.Int64 != 0) ||
		i.Subject != searchmanifest.Entity || i.SubjectID != source.entityID || i.LibraryID != source.libraryID || i.Kind != source.kind ||
		i.SearchGenerationID != b.id || i.BankKey != b.key || i.Recipe != b.profile.RecipeVersion ||
		i.EngineVersion != b.profile.EngineVersion || i.UnicodeFingerprint != b.profile.UnicodeFingerprint ||
		i.InputGeneration != j.inputGeneration || i.SourceRevision != source.revision || i.SourceIncarnation != source.incarnation {
		return searchFragmentTransition{}, ErrConflict
	}
	if !s.active.Valid || s.active.Int64 <= 0 || s.building.Valid && (s.building.Int64 <= 0 || s.building.Int64 == s.active.Int64) ||
		b.role == "active" && s.active.Int64 != b.id || b.role == "building" && (!s.building.Valid || s.building.Int64 != b.id) {
		return searchFragmentTransition{}, ErrConflict
	}
	// Explicit FIRST source projection selected by INT, NOT generic parsing.
	// The supplied source/image/job facts still require native qualification.
	if p.row.Field != 1 || p.row.Revision != strconv.FormatInt(source.revision, 10) {
		return searchFragmentTransition{}, ErrConflict
	}
	if seal.algorithm != searchmanifest.Algorithm || seal.sealed != 0 || seal.expectedFragments <= 0 || seal.expectedBytes < 0 ||
		j.expectedFragments != seal.expectedFragments || j.stagedFragments != seal.stagedFragments || j.stagedBytes != seal.stagedBytes ||
		seal.stagedFragments < 0 || seal.stagedBytes < 0 || seal.stagedFragments >= seal.expectedFragments || seal.stagedBytes > seal.expectedBytes ||
		seal.verifiedFragments < 0 || seal.verifiedFragments > seal.stagedFragments || seal.verifiedBytes < 0 || seal.verifiedBytes > seal.stagedBytes {
		return searchFragmentTransition{}, ErrConflict
	}
	if seal.stagedFragments == 0 {
		if j.afterField != 0 || j.afterOrdinal != 0 || j.afterFragmentID != 0 || seal.stagedBytes != 0 {
			return searchFragmentTransition{}, ErrConflict
		}
	} else if j.afterField != 1 || j.afterOrdinal != seal.stagedFragments-1 || j.afterFragmentID <= 0 {
		return searchFragmentTransition{}, ErrConflict
	}
	if p.row.Ordinal != seal.stagedFragments {
		return searchFragmentTransition{}, ErrConflict
	}
	seed, err := searchmanifest.Seed(i, seal.expectedFragments, seal.expectedBytes)
	if err != nil || seed != seal.seed {
		return searchFragmentTransition{}, ErrConflict
	}
	if _, err := searchmanifest.Restore(seal.algorithm, seed, seal.expectedDigest, seal.expectedFragments, seal.expectedBytes); err != nil {
		return searchFragmentTransition{}, ErrConflict
	}
	if seal.verifiedFragments == 0 {
		if seal.verifiedBytes != 0 || seal.afterField != 0 || seal.afterOrdinal != 0 || seal.afterFragmentID != 0 || seal.chain != seed {
			return searchFragmentTransition{}, ErrConflict
		}
	} else if seal.afterField != 1 || seal.afterOrdinal != seal.verifiedFragments-1 || seal.afterFragmentID <= 0 || seal.afterFragmentID > j.afterFragmentID {
		return searchFragmentTransition{}, ErrConflict
	}
	if p.bytes > math.MaxInt64 || int64(p.bytes) > seal.expectedBytes-seal.stagedBytes {
		return searchFragmentTransition{}, ErrConflict
	}
	next, err := nextSearchInputCounter(j.revision, 1)
	if err != nil {
		return searchFragmentTransition{}, err
	}
	count, err := nextSearchInputCounter(seal.stagedFragments, 1)
	if err != nil {
		return searchFragmentTransition{}, err
	}
	return searchFragmentTransition{
		jobRevision: next, stagedFragments: count, stagedBytes: seal.stagedBytes + int64(p.bytes),
		afterField: 1, afterOrdinal: p.row.Ordinal,
		verifiedFragments: seal.verifiedFragments, verifiedBytes: seal.verifiedBytes, sealChain: seal.chain,
	}, nil
}
