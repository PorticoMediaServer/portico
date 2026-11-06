package writer

import (
	"database/sql"
	"strings"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchmanifest"
)

// Original native header strings, not live-title normalization or child names.
type searchOwnPrefixImage struct {
	imageID         int64
	prefix, initial string
}

// Eligibility transition witness for the old image's independent reclaimer.
// Head publication preserves its historical result/ACK and lease/cursor fields.
type searchImageRetirementWitness struct {
	id, publicationID, revision, afterFragmentID          int64
	livePublicationID                                     sql.NullInt64
	phase                                                 string
	incarnation                                           [16]byte
	lastSequence, acknowledgedSequence                    int64
	leaseOwner, leaseGeneration, leaseExpiresMS, attempts int64
	nextAttempt                                           sql.NullInt64
}

type searchHeadPublishRequest struct {
	variant    string
	snapshot   searchHeadPublishSnapshot
	old, next  searchOwnPrefixImage
	retirement searchImageRetirementWitness
}

type searchHeadPublishCommand struct {
	identity CommandIdentity
	digest   RequestDigest
	request  searchHeadPublishRequest
	valid    bool
}

// Fixed-shape W canonical wire, excluding nineteen TEXT payloads. The exact
// shared-encoder test pins this before cloning or hashing any request values.
const searchHeadPublishFixedBytes = 8192 // conservative ceiling, independently checked

func searchHeadTexts(r *searchHeadPublishRequest) []*string {
	s := &r.snapshot.stage
	return []*string{&r.variant, &s.bank.key, &s.bank.role, &s.bank.profile.RecipeVersion, &s.bank.profile.EngineVersion, &s.bank.profile.UnicodeFingerprint,
		&s.job.recipe, &s.job.phase, &s.job.contentDigest, &s.image.input.BankKey, &s.image.input.Recipe, &s.image.input.EngineVersion, &s.image.input.UnicodeFingerprint,
		&s.seal.algorithm, &r.old.prefix, &r.old.initial, &r.next.prefix, &r.next.initial, &r.retirement.phase}
}

func searchHeadPreparationBytes(r searchHeadPublishRequest) (uint64, error) {
	wire := uint64(searchHeadPublishFixedBytes)
	for _, value := range searchHeadTexts(&r) {
		if uint64(len(*value)) > maxCanonicalRequestBytes-wire {
			return 0, invalid("head_request_bytes")
		}
		wire += uint64(len(*value))
	}
	return 65536 + 4*wire, nil
}

func newSearchHeadPublishCommand(identity CommandIdentity, r searchHeadPublishRequest) (*searchHeadPublishCommand, error) {
	if _, err := searchHeadPreparationBytes(r); err != nil {
		return nil, err
	}
	j := r.snapshot.stage.job
	if r.variant != "head_publish" || identity.Incarnation != j.incarnation || identity.Sequence <= 0 || j.lastSequence < 0 ||
		j.lastSequence != j.acknowledgedSequence || identity.Sequence <= j.lastSequence || identity.Sequence-j.lastSequence != 1 ||
		j.leaseOwner <= 0 || j.leaseGeneration <= 0 || j.leaseExpiresMS <= 0 || j.attempts < 0 ||
		j.nextAttempt.Valid && j.nextAttempt.Int64 < 0 || !j.nextAttempt.Valid && j.nextAttempt.Int64 != 0 {
		return nil, invalid("head_request")
	}
	if _, err := planSearchHeadPublish(r.snapshot); err != nil {
		return nil, err
	}
	if r.next.imageID != r.snapshot.stage.image.id || r.old.imageID != j.publishedImage.Int64 ||
		!j.publishedImage.Valid && (r.old != (searchOwnPrefixImage{}) || r.retirement != (searchImageRetirementWitness{})) {
		return nil, ErrConflict
	}
	for _, h := range []searchOwnPrefixImage{r.old, r.next} {
		if len(h.prefix) > 65536 || len(h.initial) > 72 || !utf8.ValidString(h.prefix) || !utf8.ValidString(h.initial) {
			return nil, invalid("head_native_bytes")
		}
	}
	// New strings must use the current packed recipe. Old deletion strings
	// remain opaque ORIGINAL native TEXT and are never reconstructed/re-folded.
	for _, prefix := range []string{r.next.prefix, r.next.initial} {
		if _, err := searchFragmentTokenWork(searchmanifest.Row{Prefix: prefix}, 1<<18); err != nil {
			return nil, err
		}
	}
	if strings.Contains(r.next.initial, " ") {
		return nil, invalid("head_initial_token")
	}
	if j.publishedImage.Valid {
		o := r.retirement
		if o.id <= 0 || o.publicationID != r.old.imageID || !o.livePublicationID.Valid || o.livePublicationID.Int64 != o.publicationID ||
			o.phase != "held" || o.revision <= 0 || o.afterFragmentID != 0 || o.lastSequence != o.acknowledgedSequence || o.lastSequence < 0 ||
			o.leaseOwner != 0 || o.leaseGeneration != 0 || o.leaseExpiresMS != 0 || o.attempts < 0 ||
			o.nextAttempt.Valid && o.nextAttempt.Int64 < 0 || !o.nextAttempt.Valid && o.nextAttempt.Int64 != 0 {
			return nil, ErrConflict
		}
		if _, err := nextSearchInputCounter(o.revision, 1); err != nil {
			return nil, err
		}
	}
	for _, value := range searchHeadTexts(&r) {
		if !utf8.ValidString(*value) {
			return nil, invalid("head_request_text")
		}
		*value = strings.Clone(*value)
	}
	digest, err := canonicalRequestDigest(SearchHeadPublish, 1, identity, r)
	if err != nil {
		return nil, err
	}
	return &searchHeadPublishCommand{identity, digest, r, true}, nil
}
