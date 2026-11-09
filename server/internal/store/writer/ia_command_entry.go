package writer

import (
	"context"
	"database/sql"
	"errors"
	"unicode/utf8"
)

type iaEntryAction uint8

const (
	iaEntryResolve iaEntryAction = iota + 1
	iaEntryApply
)

// Inputs are exact comparison witnesses. No token/installation secret or caller
// role is persisted in this original reference. Other IA purposes stay closed
// until their typed owner/result classes have their own reviewed entry variant.
type iaEntryRequest struct {
	purpose              CommandID
	action               iaEntryAction
	reference            ReceiptReference
	libraryID            int64
	libraryKey, sourceID string
}
type iaCommandEntry struct {
	authority *iaEntryAuthorization
	request   iaEntryRequest
	decision  receiptDecision
	owner     receiptOwnerFacts
	header    receiptFacts
}

// This is a SAME-TURN prefix on W's one private connection, not a new executor,
// public result or callback. Later typed bodies use entry.decision, validate the
// COMPLETE saved child cohort/digest before Applied, and recheck before COMMIT.
// Missing-owner birth is possible only on explicit Apply of sequence1 under
// current independent authority. Resolve never creates authority or an owner.
func enterIACommand(ctx context.Context, turn *commandTurn, q iaEntryRequest, p *usOwnerAuthorityProof, lifetime context.Context, budget iaEntryBudget) (out *iaCommandEntry, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	if turn == nil || turn.transaction == nil {
		return nil, ErrInvalid
	}
	resultPurpose, known := iaEntryResultPurpose(turn.transaction.descriptor.ID)
	if !known || q.purpose != resultPurpose || (q.purpose != IAConfigureLibrary && q.purpose != IAConfigureSource) || (q.action != iaEntryResolve && q.action != iaEntryApply) || q.libraryID <= 0 || !iaEntryText(q.libraryKey) || (q.purpose == IAConfigureSource) != (q.sourceID != "") || (q.sourceID != "" && !iaEntryText(q.sourceID)) {
		return nil, ErrInvalid
	}
	ref := q.reference
	if ref.Identity.Incarnation == [16]byte{} || ref.Identity.OperationID == [16]byte{} || ref.Identity.Sequence != 1 || ref.RequestDigest == [32]byte{} {
		return nil, ErrInvalid
	}
	a, err := authorizeIAEntryOwner(ctx, turn, p, lifetime, budget)
	if err != nil {
		return nil, err
	}
	entry := &iaCommandEntry{authority: a, request: q}
	kind, err := iaEntryCommandKind(q.purpose)
	if err != nil {
		return nil, err
	}
	var source any
	if q.sourceID != "" {
		source = q.sourceID
	}
	row, err := wc008DraftQuery(ctx, turn, iaEntryOwner, q.libraryID, q.libraryKey, kind, ref.RequestDigest[:], source, ref.Identity.OperationID[:])
	if err != nil {
		return nil, err
	}
	var incarnation []byte
	var matches int64
	err = row.Scan(&incarnation, &entry.owner.lastSequence, &entry.owner.acknowledgedSequence, &matches)
	if errors.Is(err, sql.ErrNoRows) {
		if q.action != iaEntryApply || ref.Identity.Sequence != 1 {
			return nil, ErrResolveRequired
		}
		entry.owner = receiptOwnerFacts{exists: true, incarnation: ref.Identity.Incarnation}
	} else if err != nil {
		return nil, err
	} else {
		if len(incarnation) != 16 || matches != 1 {
			return nil, ErrConflict
		}
		entry.owner.exists = true
		copy(entry.owner.incarnation[:], incarnation)
	}
	// Retained namespace identity is checked without requiring live eligibility:
	// replay remains possible after source retirement/change under current owner.
	row, err = wc008DraftQuery(ctx, turn, iaEntryNamespace, q.libraryKey, q.libraryID)
	if err != nil {
		return nil, err
	}
	var same, retired int64
	err = row.Scan(&same, &retired)
	if errors.Is(err, sql.ErrNoRows) {
		if q.purpose != IAConfigureLibrary || entry.owner.lastSequence != 0 || q.action != iaEntryApply {
			return nil, ErrConflict
		}
	} else if err != nil {
		return nil, err
	} else if same != 1 {
		return nil, ErrConflict
	}
	row, err = wc008DraftQuery(ctx, turn, iaEntryHeader, ref.Identity.OperationID[:], ref.Identity.Incarnation[:], ref.Identity.Sequence)
	if err != nil {
		return nil, err
	}
	var operation, digest, result []byte
	var purpose, version int64
	err = row.Scan(&operation, &digest, &purpose, &entry.header.resultCount, &result, &version)
	if !errors.Is(err, sql.ErrNoRows) {
		if err != nil {
			return nil, err
		}
		if len(operation) != 16 || len(digest) != 32 || len(result) != 32 || version != 1 {
			return nil, ErrResolveRequired
		}
		entry.header.exists = true
		entry.header.identity.command = ref.Identity
		copy(entry.header.identity.command.OperationID[:], operation)
		copy(entry.header.identity.digest[:], digest)
		entry.header.kind = CommandID(purpose)
		copy(entry.header.resultDigest[:], result)
	}
	entry.decision, err = classifyReceipt(receiptIdentity{ref.Identity, ref.RequestDigest}, q.purpose, entry.owner, entry.header)
	if err != nil {
		return nil, err
	}
	if entry.decision == receiptFresh && retired != 0 {
		return nil, ErrConflict
	}
	// A constructor/earlier stamp can never mask a later same-turn revocation.
	if err = a.recheck(ctx, turn); err != nil {
		return nil, err
	}
	// Fresh is an internal admission classification, not an Applied/queued result.
	// A resolver cannot consume it as permission to execute a missing command.
	if q.action == iaEntryResolve && entry.decision == receiptFresh {
		return nil, ErrResolveRequired
	}
	return entry, nil
}
func iaEntryText(s string) bool { return len(s) > 0 && len(s) <= 128 && utf8.ValidString(s) }

// Fresh source checks are deliberately separate and cannot run for a saved or
// retired original. They are not performed while disclosing historical results.
func (e *iaCommandEntry) checkFreshSource(ctx context.Context, turn *commandTurn, generation int64, incarnation, root, identity string) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if e == nil || e.authority == nil || e.decision != receiptFresh || e.request.purpose != IAConfigureSource || generation <= 0 || !iaEntryText(incarnation) || len(root) == 0 || len(root) > maxCanonicalRequestBytes || !utf8.ValidString(root) || len(identity) == 0 || len(identity) > maxCanonicalRequestBytes || !utf8.ValidString(identity) {
		return ErrInvalid
	}
	if err = e.authority.recheck(ctx, turn); err != nil {
		return err
	}
	if err = e.authority.budget.check(ctx, turn, int64(len(root)+len(identity))); err != nil {
		return err
	}
	row, err := wc008DraftQuery(ctx, turn, iaEntryFreshSource, e.request.sourceID, e.request.libraryKey, generation, incarnation, root, identity)
	if err != nil {
		return err
	}
	var found int64
	if err = row.Scan(&found); errors.Is(err, sql.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	if found != 1 {
		return ErrResolveRequired
	}
	return e.authority.recheck(ctx, turn)
}

// One persisted result purpose may carry more than one durability descriptor:
// configure_source (NORMAL) and its explicit FULL disable descriptor. Owner,
// header and codec identity always use the result purpose; the compiled
// descriptor fixes durability alone and is never chosen by the caller.
func iaEntryResultPurpose(id CommandID) (CommandID, bool) {
	switch id {
	case IAConfigureLibrary:
		return IAConfigureLibrary, true
	case IAConfigureSource, IAConfigureSourceDisable:
		return IAConfigureSource, true
	}
	return 0, false
}

// Descriptor labels are metadata, not persisted command_kind values. Keep this
// closed map aligned with group C and the typed codecs, independent of IA04's
// prefixed names. Later IA bodies must not persist Describe(...).Name.
func iaEntryCommandKind(purpose CommandID) (string, error) {
	switch purpose {
	case IAConfigureLibrary:
		return "configure_library", nil
	case IAConfigureSource, IAConfigureSourceDisable:
		return "configure_source", nil
	case IARetireLibrary:
		return "retire_library", nil
	case IARetireSource:
		return "retire_source", nil
	case IARetireStep:
		return "retire_step", nil
	case IAAcceptEpisodeInventory:
		return "episode_inventory_accept", nil
	case IAAcceptAudioInventory:
		return "audio_inventory_accept", nil
	case IAAcceptReadingInventory:
		return "reading_inventory_accept", nil
	case IAAcceptExtraInventory:
		return "extra_inventory_accept", nil
	case IAAcceptMembership:
		return "membership_accept", nil
	case IAReconcileObject:
		return "inventory_reconcile", nil
	case IACoverageVerify:
		return "inventory_verify", nil
	default:
		return "", ErrInvalid
	}
}
