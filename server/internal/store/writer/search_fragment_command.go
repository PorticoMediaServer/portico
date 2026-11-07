package writer

import (
	"strings"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchmanifest"
)

// Complete PRE-allocation intent. Native fragment ID, accepted time and actual
// post-CAS result are outputs, not rewritten request fields. No capability,
// caller clock, engine attestation or grant is transported in this packet.
type searchFragmentStageRequest struct {
	variant  string
	snapshot searchFragmentStageSnapshot
	row      searchmanifest.Row
}

type searchFragmentStageCommand struct {
	identity CommandIdentity
	request  searchFragmentStageRequest
	digest   RequestDigest
	payload  searchPreparedFragment
	valid    bool
}

func (*searchFragmentStageCommand) isSearchFragmentStageCommand() {}

// Pinned by the SHARED W encoder with an all-empty-TEXT request. No duplicate
// reflection/encoding routine or caller size declaration is used in production.
const searchFragmentStageFixedBytes = 5813

const (
	searchFragmentPreparedBytes = 6144
	searchFragmentTokenWorkCap  = 8192
	searchFragmentPackedBytes   = 1024
	searchFragmentWindowBytes   = 4096
	searchFragmentWindowRunes   = 1024
	searchFirstRevisionBytes    = 19 // positive native int64 → canonical decimal
)

// Allocation-free complete packet reservation, before UTF8/token work/clones.
// This is preparation data, not W's native/common reserve or permission.
func searchFragmentStagePreparationBytes(in searchFragmentStageRequest) (uint64, error) {
	s := in.snapshot
	text := [...]string{in.variant, s.bank.key, s.bank.role,
		s.bank.profile.RecipeVersion, s.bank.profile.EngineVersion, s.bank.profile.UnicodeFingerprint,
		s.job.recipe, s.job.phase, s.job.contentDigest,
		s.image.input.BankKey, s.image.input.EngineVersion, s.image.input.UnicodeFingerprint, s.image.input.Recipe,
		s.seal.algorithm, in.row.Revision, in.row.Prefix, in.row.Lexical, in.row.Normalized}
	wire := uint64(searchFragmentStageFixedBytes)
	for _, value := range text {
		if uint64(len(value)) > maxCanonicalRequestBytes-wire {
			return 0, invalid("fragment_request_bytes")
		}
		wire += uint64(len(value))
	}
	return 65536 + 4*wire, nil
}

// Preparation only, outside the writer. FIRST admits only primary-title rows
// of a captured compatible-v1 image. This packet is not current source proof;
// the same native values/recipe/engine/lease must be rechecked AFTER history.
func newSearchFragmentStageCommand(identity CommandIdentity, in searchFragmentStageRequest) (*searchFragmentStageCommand, error) {
	required, err := searchFragmentStagePreparationBytes(in)
	if err != nil {
		return nil, err
	}
	j := in.snapshot.job
	if in.variant != "fragment_stage" || identity.Incarnation != j.incarnation ||
		identity.Sequence <= 0 || j.lastSequence < 0 || j.lastSequence != j.acknowledgedSequence ||
		identity.Sequence <= j.lastSequence || identity.Sequence-j.lastSequence != 1 ||
		j.leaseOwner <= 0 || j.leaseGeneration <= 0 || j.leaseExpiresMS <= 0 || j.attempts < 0 ||
		(j.nextAttempt.Valid && j.nextAttempt.Int64 < 0) || (!j.nextAttempt.Valid && j.nextAttempt.Int64 != 0) {
		return nil, invalid("fragment_request")
	}
	if len(in.row.Prefix) > searchFragmentPackedBytes || len(in.row.Lexical) > searchFragmentPackedBytes ||
		len(in.row.Normalized) > searchFragmentWindowBytes || len(in.row.Revision) > searchFirstRevisionBytes {
		return nil, invalid("fragment_column_envelope")
	}
	if utf8.RuneCountInString(in.row.Normalized) > searchFragmentWindowRunes {
		return nil, invalid("fragment_normalized_window")
	}
	// No partial stream, clipped token or shortened normalized window is ever
	// accepted. Empty columns still form one ordinary/native fragment row.
	payload, err := prepareSearchFragment(in.row, searchFragmentPreparationBudget{
		bytes: searchFragmentPreparedBytes, revisionBytes: searchFirstRevisionBytes,
		tokenWork: searchFragmentTokenWorkCap, preparationBytes: required,
	})
	if err != nil {
		return nil, err
	}
	if _, err := planSearchFragmentStage(in.snapshot, payload); err != nil {
		return nil, err
	}
	// Freeze every retained native TEXT, including duplicate profile fields
	// and legacy content_digest. Its old value is preserved, never new proof.
	s := &in.snapshot
	text := [...]*string{&in.variant, &s.bank.key, &s.bank.role,
		&s.bank.profile.RecipeVersion, &s.bank.profile.EngineVersion, &s.bank.profile.UnicodeFingerprint,
		&s.job.recipe, &s.job.phase, &s.job.contentDigest,
		&s.image.input.BankKey, &s.image.input.EngineVersion, &s.image.input.UnicodeFingerprint, &s.image.input.Recipe,
		&s.seal.algorithm}
	for _, p := range text {
		if !utf8.ValidString(*p) {
			return nil, invalid("fragment_request_text")
		}
		*p = strings.Clone(*p)
	}
	in.row = payload.row
	digest, err := canonicalRequestDigest(SearchFragmentStage, 1, identity, in)
	if err != nil {
		return nil, err
	}
	return &searchFragmentStageCommand{identity: identity, request: in, digest: digest, payload: payload, valid: true}, nil
}
