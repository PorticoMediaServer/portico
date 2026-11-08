package writer

import (
	"database/sql"
	"strings"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchmanifest"
)

// Reclaim has its OWN receipt/ACK/lease owner. No mutable publisher/source
// revision or current normalized text substitutes for this retained custody.
type searchFragmentReclaimOwner = searchImageRetirementWitness

type searchRetainedFragment struct {
	id, publicationID, bankID, inputGeneration int64
	entityID, libraryID, kind                  int64
	row                                        searchmanifest.Row
}

type searchFragmentReclaimSnapshot struct {
	owner                     searchFragmentReclaimOwner
	fragment                  searchRetainedFragment
	currentHead, currentStage sql.NullInt64
}

type searchFragmentReclaimTransition struct {
	acceptedRevision, afterFragmentID  int64
	retainedColumnBytes, revisionBytes uint64
	// ONE native deletion per slot, then ONE ordinary row deletion. This is
	// an inventory, NOT a numerical native Work/hold/debt qualification.
	nativeDeletes, contentDeletes int
}

// Copies ORIGINAL persisted strings after complete bounded preflight. It never
// normalizes, re-tokenizes, parses Revision or invents a compatible profile.
// The later dedicated W turn still needs original slot/engine/custody and its
// own nonborrowable resource forecast, including actual native amplification.
func prepareSearchRetainedFragment(in searchRetainedFragment) (searchRetainedFragment, uint64, error) {
	if len(in.row.Prefix) > searchFragmentPackedBytes || len(in.row.Lexical) > searchFragmentPackedBytes || len(in.row.Normalized) > searchFragmentWindowBytes || len(in.row.Revision) > maxCanonicalRequestBytes {
		return searchRetainedFragment{}, 0, ErrInvalid
	}
	total := uint64(len(in.row.Prefix)) + uint64(len(in.row.Lexical)) + uint64(len(in.row.Normalized))
	if total > searchFragmentPreparedBytes || uint64(len(in.row.Revision)) > maxCanonicalRequestBytes-total {
		return searchRetainedFragment{}, 0, ErrInvalid
	}
	total += uint64(len(in.row.Revision))
	for _, text := range []string{in.row.Prefix, in.row.Lexical, in.row.Normalized, in.row.Revision} {
		if !utf8.ValidString(text) {
			return searchRetainedFragment{}, 0, ErrInvalid
		}
	}
	if utf8.RuneCountInString(in.row.Normalized) > searchFragmentWindowRunes {
		return searchRetainedFragment{}, 0, ErrInvalid
	}
	in.row.Prefix, in.row.Lexical = strings.Clone(in.row.Prefix), strings.Clone(in.row.Lexical)
	in.row.Normalized, in.row.Revision = strings.Clone(in.row.Normalized), strings.Clone(in.row.Revision)
	return in, 65536 + 4*total, nil // preparation values only, not a grant
}

func planSearchFragmentReclaim(s searchFragmentReclaimSnapshot, identity CommandIdentity) (searchFragmentReclaimTransition, error) {
	o, f := s.owner, s.fragment
	for _, pointer := range []sql.NullInt64{s.currentHead, s.currentStage} {
		if pointer.Valid && pointer.Int64 <= 0 || !pointer.Valid && pointer.Int64 != 0 {
			return searchFragmentReclaimTransition{}, ErrConflict
		}
	}
	if o.id <= 0 || o.publicationID <= 0 || !o.livePublicationID.Valid || o.livePublicationID.Int64 != o.publicationID || o.revision <= 0 ||
		(o.phase != "ready" && o.phase != "reclaim") || o.afterFragmentID < 0 ||
		o.incarnation != identity.Incarnation || identity.Sequence <= 0 || o.lastSequence < 0 || o.lastSequence != o.acknowledgedSequence ||
		identity.Sequence <= o.lastSequence || identity.Sequence-o.lastSequence != 1 ||
		o.leaseOwner <= 0 || o.leaseGeneration <= 0 || o.leaseExpiresMS <= 0 ||
		o.attempts < 0 || o.nextAttempt.Valid && o.nextAttempt.Int64 < 0 || !o.nextAttempt.Valid && o.nextAttempt.Int64 != 0 ||
		f.id <= o.afterFragmentID || f.publicationID != o.publicationID || f.bankID <= 0 || f.inputGeneration <= 0 ||
		f.entityID <= 0 || f.libraryID <= 0 || f.kind != 1 || f.row.Field != 1 || f.row.Ordinal < 0 ||
		(s.currentHead.Valid && s.currentHead.Int64 == f.publicationID) || (s.currentStage.Valid && s.currentStage.Int64 == f.publicationID) {
		return searchFragmentReclaimTransition{}, ErrConflict
	}
	// An unacked result blocks fresh work. History resolves BEFORE this reducer;
	// the original saved ID/result survives deletion of the native/content row.
	next, err := nextSearchInputCounter(o.revision, 1)
	if err != nil {
		return searchFragmentReclaimTransition{}, err
	}
	return searchFragmentReclaimTransition{acceptedRevision: next, afterFragmentID: f.id,
		retainedColumnBytes: uint64(len(f.row.Prefix)) + uint64(len(f.row.Lexical)) + uint64(len(f.row.Normalized)),
		revisionBytes:       uint64(len(f.row.Revision)), nativeDeletes: 3, contentDeletes: 1}, nil
}
