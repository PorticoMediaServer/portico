package writer

import (
	"context"
	"database/sql"
	"errors"
	"unicode/utf8"
)

type ipEntryAction uint8

const (
	ipEntryResolve ipEntryAction = iota + 1
	ipEntryApply
)

// targetKind0 is a PERSON, never a dummy entity. Other values are canonical
// entity kinds, scoped by retained library identity. These are comparison
// witnesses, not current disclosure or mutation permission.
type ipEntryRequest struct {
	purpose                         CommandID
	action                          ipEntryAction
	reference                       ReceiptReference
	targetID, targetKind, libraryID int64
	libraryKey                      string
}
type ipCommandEntry struct {
	authority *ipEntryAuthorization
	request   ipEntryRequest
	decision  receiptDecision
	ownerID   int64
	owner     receiptOwnerFacts
	header    receiptFacts
}

func ipEntryOwnerKind(kind int64) string {
	switch kind {
	case 0:
		return "person"
	case 2:
		return "show"
	case 3:
		return "season"
	case 5:
		return "artist"
	case 6:
		return "album"
	case 8:
		return "book"
	case 10:
		return "collection"
	case 1, 4, 7, 9, 11, 12, 20, 21:
		return "item"
	}
	return ""
}

// Private SAME-TURN owner-edit prefix. It grants neither provider-worker
// authority nor an Applied result. Body-specific input/source checks, complete
// saved fields/result digest, effects/ACK and installed dispatch remain the
// typed body's job. A sessionless provider driver must never borrow this stamp.
func enterIPCommand(ctx context.Context, turn *commandTurn, q ipEntryRequest, p *usOwnerAuthorityProof, lifetime context.Context, budget ipEntryBudget) (out *ipCommandEntry, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	kind := ipEntryOwnerKind(q.targetKind)
	if turn == nil || turn.transaction == nil || q.purpose != turn.transaction.descriptor.ID || (q.purpose != IPFieldEdit && q.purpose != IPFieldLock && q.purpose != IPFieldRestore) || (q.action != ipEntryApply && q.action != ipEntryResolve) || q.targetID <= 0 || kind == "" {
		return nil, ErrInvalid
	}
	if q.targetKind == 0 {
		if q.libraryID != 0 || q.libraryKey != "" {
			return nil, ErrInvalid
		}
	} else if q.libraryID <= 0 || len(q.libraryKey) == 0 || len(q.libraryKey) > 128 || !utf8.ValidString(q.libraryKey) {
		return nil, ErrInvalid
	}
	ref := q.reference
	if ref.Identity.Incarnation == [16]byte{} || ref.Identity.OperationID == [16]byte{} || ref.Identity.Sequence <= 0 || ref.RequestDigest == [32]byte{} {
		return nil, ErrInvalid
	}
	auth, err := authorizeIPEntryOwner(ctx, turn, p, lifetime, budget)
	if err != nil {
		return nil, err
	}
	entry := &ipCommandEntry{authority: auth, request: q}
	if q.targetKind == 0 {
		if err = usAuthorityPresence(ctx, turn, ipEntryPerson, true, q.targetID); err != nil {
			return nil, err
		}
	} else {
		if err = usAuthorityPresence(ctx, turn, ipEntryLibrary, true, q.libraryKey, q.libraryID); err != nil {
			return nil, err
		}
		row, e := wc008DraftQuery(ctx, turn, ipEntryEntity, q.targetID, q.libraryID)
		if e != nil {
			return nil, e
		}
		var actual int64
		if err = row.Scan(&actual); err != nil {
			return nil, usAuthorityReadError(err)
		}
		if actual != q.targetKind {
			return nil, ErrDisclosureDenied
		}
	}
	id, args := ipEntryEntityOwner, []any{kind, q.targetID}
	if q.targetKind == 0 {
		id, args = ipEntryPersonOwner, []any{q.targetID}
	}
	row, err := wc008DraftQuery(ctx, turn, id, args...)
	if err != nil {
		return nil, err
	}
	var incarnation []byte
	var matched, active int64
	err = row.Scan(&entry.ownerID, &incarnation, &entry.owner.lastSequence, &entry.owner.acknowledgedSequence, &matched, &active)
	if errors.Is(err, sql.ErrNoRows) {
		if q.action != ipEntryApply || ref.Identity.Sequence != 1 {
			return nil, ErrResolveRequired
		}
		entry.owner = receiptOwnerFacts{exists: true, incarnation: ref.Identity.Incarnation}
		active = 1
	} else if err != nil {
		return nil, err
	} else {
		if entry.ownerID <= 0 || len(incarnation) != 16 || matched != 1 {
			return nil, ErrConflict
		}
		entry.owner.exists = true
		copy(entry.owner.incarnation[:], incarnation)
	}
	row, err = wc008DraftQuery(ctx, turn, ipEntryHeader, entry.ownerID, ref.Identity.Incarnation[:], ref.Identity.Sequence)
	if err != nil {
		return nil, err
	}
	var operation, digest, result []byte
	var purpose int64
	err = row.Scan(&operation, &digest, &purpose, &entry.header.resultCount, &result)
	if !errors.Is(err, sql.ErrNoRows) {
		if err != nil {
			return nil, err
		}
		if len(operation) != 16 || len(digest) != 32 || len(result) != 32 || purpose < 240 || purpose > 242 {
			return nil, ErrResolveRequired
		}
		entry.header.exists = true
		entry.header.kind = CommandID(purpose)
		entry.header.identity.command = ref.Identity
		copy(entry.header.identity.command.OperationID[:], operation)
		copy(entry.header.identity.digest[:], digest)
		copy(entry.header.resultDigest[:], result)
	}
	entry.decision, err = classifyReceipt(receiptIdentity{ref.Identity, ref.RequestDigest}, q.purpose, entry.owner, entry.header)
	if err != nil {
		return nil, err
	}
	if entry.decision == receiptFresh && active != 1 {
		return nil, ErrConflict
	}
	if err = auth.recheck(ctx, turn); err != nil {
		return nil, err
	}
	return entry, nil
}
func (e *ipCommandEntry) checkFreshSubject(ctx context.Context, turn *commandTurn, incarnation [16]byte, source, identity, metadata int64, personToken string) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if e == nil || e.authority == nil || e.decision != receiptFresh {
		return ErrInvalid
	}
	if err = e.authority.recheck(ctx, turn); err != nil {
		return err
	}
	q := e.request
	if q.targetKind == 0 {
		if len(personToken) == 0 || len(personToken) > 128 || !utf8.ValidString(personToken) || metadata <= 0 || incarnation != [16]byte{} || source != 0 || identity != 0 {
			return ErrInvalid
		}
		return usAuthorityPresence(ctx, turn, ipEntryFreshPerson, true, q.targetID, personToken, metadata)
	}
	if incarnation == [16]byte{} || source <= 0 || identity <= 0 || metadata <= 0 || personToken != "" {
		return ErrInvalid
	}
	return usAuthorityPresence(ctx, turn, ipEntryFreshEntity, true, q.targetID, q.libraryID, q.targetKind, incarnation[:], source, identity, metadata)
}

// Owner restore of an automatic source must prove actual provider enablement
// and its provider-specific online consent. Local edits do not require online
// consent. This never issues a sessionless worker or provider-body capability.
func (e *ipCommandEntry) checkFreshProvider(ctx context.Context, turn *commandTurn, provider string, incarnation [16]byte, revision, consent int64) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if e == nil || e.authority == nil || e.decision != receiptFresh || e.request.targetKind == 0 || incarnation == [16]byte{} || revision <= 0 || consent <= 0 {
		return ErrInvalid
	}
	if err = e.authority.recheck(ctx, turn); err != nil {
		return err
	}
	var id = ipEntryScreenConsent
	switch provider {
	case "tmdb", "tvdb":
	case "musicbrainz":
		id = ipEntryMusicConsent
	default:
		return ErrInvalid
	}
	if err = usAuthorityPresence(ctx, turn, ipEntryPolicy, true, e.request.libraryID, provider, incarnation[:], revision); err != nil {
		return err
	}
	return usAuthorityPresence(ctx, turn, id, true, consent)
}
