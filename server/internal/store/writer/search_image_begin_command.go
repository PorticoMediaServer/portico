package writer

import (
	"crypto/rand"
	"database/sql"
	"strings"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchmanifest"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchpolicy"
)

// Native captured facts are fresh CAS expectations, NOT current disclosure.
// W's opaque PublisherDisclosure alone supplies issuer/live permission. The
// dispatcher resolves the ORIGINAL receipt before any fresh vector is checked.
type searchImageBeginBank struct {
	id        int64
	key, role string
	profile   searchpolicy.Profile
}

type searchImageBeginSource struct {
	entityID, libraryID, kind, revision int64
	incarnation                         [16]byte
	title                               string
}

type searchImageBeginOwner struct {
	id, entityID, bankID                            int64
	incarnation                                     [16]byte
	revision, inputGeneration, publishedGeneration  int64
	publishedImage, document                        sql.NullInt64
	sourceIncarnation                               [16]byte
	sourceRevision                                  int64
	recipe, phase                                   string
	afterField, afterOrdinal, afterFragmentID       int64
	expectedFragments, stagedFragments, stagedBytes int64
	contentDigest                                   string
	leaseOwner, leaseGeneration, leaseExpiresMS     int64
	attempts                                        int64
	nextAttempt                                     sql.NullInt64
	lastSequence, acknowledgedSequence              int64
}

type searchImageBeginHeader struct {
	titleFold, titlePrefixTokens, initialPrefixTokens string
	expectedFragments, expectedBytes                  int64
	expectedDigest                                    [32]byte
}

// This complete PRE-allocation request is the sole domain request digest input.
// Image/reclaim IDs, applied time and retirement nonce are generated outputs;
// the disclosure capability and physical evidence are never serialized here.
type searchImageStageRequest struct {
	variant string
	bank    searchImageBeginBank
	source  searchImageBeginSource
	owner   searchImageBeginOwner
	header  searchImageBeginHeader
}

type searchImageStageCommand struct {
	identity              CommandIdentity
	request               searchImageStageRequest
	digest                RequestDigest
	seed                  [32]byte
	retirementIncarnation [16]byte
	valid                 bool
}

func (*searchImageStageCommand) isSearchImageStageCommand() {}

var _ SearchImageStageCommand = (*searchImageStageCommand)(nil)

// Preparation only, under separately charged custody BEFORE a writer hold.
// A structurally valid packet grants no lease, current source or native-slot
// authority. Fresh execution must rederive the manifest seed from actual native
// facts, attest the physical engine/profile and CAS the exact captured owner.
func newSearchImageStageCommand(identity CommandIdentity, in searchImageStageRequest) (*searchImageStageCommand, error) {
	text := []*string{&in.variant, &in.bank.key, &in.bank.role,
		&in.bank.profile.RecipeVersion, &in.bank.profile.EngineVersion, &in.bank.profile.UnicodeFingerprint,
		&in.source.title, &in.owner.recipe, &in.owner.phase, &in.owner.contentDigest,
		&in.header.titleFold, &in.header.titlePrefixTokens, &in.header.initialPrefixTokens}
	var total uint64
	// Check all lengths before inspecting UTF-8, cloning, deriving or hashing.
	// This codec envelope is preparation, never a production Work forecast.
	for _, p := range text {
		if uint64(len(*p)) > maxCanonicalRequestBytes-total {
			return nil, invalid("search_image_text_bytes")
		}
		total += uint64(len(*p))
	}
	if len(in.source.title) > MaxTitleCharacters*utf8.UTFMax {
		return nil, invalid("search_image_title")
	}
	for _, p := range text {
		if !utf8.ValidString(*p) {
			return nil, invalid("search_image_text")
		}
	}
	if utf8.RuneCountInString(in.source.title) > MaxTitleCharacters {
		return nil, invalid("search_image_title")
	}
	o, s, b := in.owner, in.source, in.bank
	if in.variant != "image_stage" || identity.Sequence <= 0 ||
		o.incarnation != identity.Incarnation || o.lastSequence < 0 || o.lastSequence != o.acknowledgedSequence ||
		identity.Sequence <= o.lastSequence || identity.Sequence-o.lastSequence != 1 ||
		o.id <= 0 || o.entityID != s.entityID || o.bankID != b.id ||
		s.entityID <= 0 || s.libraryID <= 0 || s.kind != 1 || s.revision <= 0 ||
		o.sourceIncarnation != s.incarnation || o.sourceRevision != s.revision ||
		b.id <= 0 || b.key != "v1" || (b.role != "active" && b.role != "building") ||
		!searchpolicy.StoredWellFormed(b.profile) || o.recipe != b.profile.RecipeVersion ||
		o.revision <= 0 || o.inputGeneration <= 0 || o.publishedGeneration < 0 || o.inputGeneration <= o.publishedGeneration ||
		o.publishedImage.Valid != (o.publishedGeneration > 0) ||
		(o.publishedImage.Valid && o.publishedImage.Int64 <= 0) || (!o.publishedImage.Valid && o.publishedImage.Int64 != 0) ||
		o.document != (sql.NullInt64{}) || o.phase != "capture" ||
		o.afterField != 0 || o.afterOrdinal != 0 || o.afterFragmentID != 0 ||
		o.expectedFragments != 0 || o.stagedFragments != 0 || o.stagedBytes != 0 || o.contentDigest != "" ||
		o.leaseOwner <= 0 || o.leaseGeneration <= 0 || o.leaseExpiresMS <= 0 || o.attempts < 0 ||
		(o.nextAttempt.Valid && o.nextAttempt.Int64 < 0) || (!o.nextAttempt.Valid && o.nextAttempt.Int64 != 0) {
		return nil, invalid("search_image_request")
	}
	if _, err := nextSearchInputCounter(o.revision, 1); err != nil {
		return nil, err
	}
	seed, err := searchmanifest.Seed(searchImageBeginManifestInput(in), in.header.expectedFragments, in.header.expectedBytes)
	if err != nil {
		return nil, invalid("search_image_manifest")
	}
	if _, err := searchmanifest.Restore(searchmanifest.Algorithm, seed, in.header.expectedDigest,
		in.header.expectedFragments, in.header.expectedBytes); err != nil {
		return nil, invalid("search_image_manifest")
	}
	for _, p := range text {
		*p = strings.Clone(*p)
	}
	digest, err := canonicalRequestDigest(SearchImageStage, 1, identity, in)
	if err != nil {
		return nil, err
	}
	c := &searchImageStageCommand{identity: identity, request: in, digest: digest, seed: seed, valid: true}
	if _, err := rand.Read(c.retirementIncarnation[:]); err != nil {
		return nil, err
	}
	return c, nil
}

// Called again by the eventual native handler with facts read in the current
// turn; the derived value cached on a prepared command is not source authority.
func searchImageBeginManifestInput(in searchImageStageRequest) searchmanifest.Input {
	return searchmanifest.Input{Subject: searchmanifest.Entity, SubjectID: in.source.entityID,
		LibraryID: in.source.libraryID, Kind: in.source.kind, SearchGenerationID: in.bank.id,
		BankKey: in.bank.key, EngineVersion: in.bank.profile.EngineVersion,
		UnicodeFingerprint: in.bank.profile.UnicodeFingerprint, Recipe: in.bank.profile.RecipeVersion,
		InputGeneration: in.owner.inputGeneration, SourceIncarnation: in.source.incarnation, SourceRevision: in.source.revision}
}

// SearchImageStageReference exposes only copied ORIGINAL identity/digest.
// Wrappers, foreign implementations, nil and zero commands are never unwrapped
// or invoked. Possessing a reference grants no disclosure or effect authority.
func SearchImageStageReference(cmd SearchImageStageCommand) (ReceiptReference, error) {
	c, ok := cmd.(*searchImageStageCommand)
	if !ok || c == nil || !c.valid {
		return ReceiptReference{}, ErrInvalid
	}
	return ReceiptReference{Identity: c.identity, RequestDigest: c.digest}, nil
}
