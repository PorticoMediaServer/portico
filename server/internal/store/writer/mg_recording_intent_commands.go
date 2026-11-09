package writer

import (
	"context"
)

// One accepting outcome. decision is the receipt classification; only Replay
// carries a saved result and only Fresh carries a newly persisted one. Retired
// is a complete zero result, never a reconstructed success.
type mgRecordingAcceptance struct {
	decision receiptDecision
	result   mgRecordingIntentResult
	digest   ResultDigest
}

// mgRecordingAuthorize composes the SAME MG-owned current-session root adapter
// (mgPersonalRootSeam) as the sparse personal commands: the closed purpose and
// NORMAL durability, the caller lifetime, the frozen witness and the exact root
// identity. Until the US mint lands the production seam refuses with
// ErrMGRootUnavailable; tests inject a fake session, never a boolean or issuer
// string. This adds no second adapter.
func mgRecordingAuthorize(ctx context.Context, turn *commandTurn, purpose CommandID, root mgPersonalRootSeam, witness *usOwnerAuthorityProof, lifetime context.Context) (out mgPersonalRootAuthorization, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	if err = mgRecordingPurpose(turn, purpose); err != nil {
		return nil, err
	}
	if lifetime == nil {
		return nil, ErrInvalid
	}
	if err = lifetime.Err(); err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	frozen, err := mgPersonalFreezeWitness(witness)
	if err != nil {
		return nil, err
	}
	if root == nil {
		return nil, ErrMGRootUnavailable
	}
	session, err := root.authorizeUSCurrentSession(ctx, turn, frozen, lifetime)
	if err != nil {
		return nil, err
	}
	if session == nil {
		return nil, ErrMGRootUnavailable
	}
	if len(session.mgPersonalRootID()) != 22 || !session.mgPersonalRootIdentity().complete() {
		return nil, ErrDisclosureDenied
	}
	if err = mgRecordingPurpose(turn, purpose); err != nil {
		return nil, err
	}
	return session, nil
}

// The frozen owner tuple is bound to the validated current session identity: a
// caller cannot schedule, edit, cancel or keep another account/profile's intent.
// The epoch/member/profile revisions are still compared natively by
// mgRecordingCheckOwner on the same turn.
func mgRecordingOwnerBound(session mgPersonalRootAuthorization, owner mgRecordingOwner) error {
	if session == nil || !owner.valid() {
		return ErrDisclosureDenied
	}
	i := session.mgPersonalRootIdentity()
	if owner.account != i.account || owner.profile != i.profile || owner.memberIncarnation != i.memberIncarnation || owner.profileLifetime != i.profileLifetime {
		return ErrDisclosureDenied
	}
	return nil
}

func mgRecordingRootReference(session mgPersonalRootAuthorization, kind CommandID, identity CommandIdentity, digest RequestDigest) (uoRootReference, error) {
	if session == nil {
		return uoRootReference{}, ErrInvalid
	}
	return uoRootReference{root: session.mgPersonalRootID(), kind: kind, identity: identity, request: digest}, nil
}

// Fresh schedule: current source/head/programme observation, birth-retirement
// floor, one occurrence row plus its reservation/dependency and the saved
// scalar. A replay never re-enters the effect and an ACKed original is retired.
func acceptMGRecordingSchedule(ctx context.Context, turn *commandTurn, root mgPersonalRootSeam, witness *usOwnerAuthorityProof, lifetime context.Context, reference ReceiptReference, request mgRecordingScheduleRequest) (out mgRecordingAcceptance, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	command, err := newMGRecordingScheduleCommand(reference.Identity, request)
	if err != nil {
		return out, err
	}
	if command.digest != reference.RequestDigest {
		return out, ErrConflict
	}
	session, err := mgRecordingAuthorize(ctx, turn, MGScheduleRecording, root, witness, lifetime)
	if err != nil {
		return out, err
	}
	if err = mgRecordingOwnerBound(session, request.owner); err != nil {
		return out, err
	}
	ref, err := mgRecordingRootReference(session, MGScheduleRecording, command.identity, command.digest)
	if err != nil {
		return out, err
	}
	lookup, err := mgPersonalRootLookupCommand(ctx, turn, session, ref)
	if err != nil {
		return out, err
	}
	switch lookup.decision {
	case receiptRetired:
		return mgRecordingAcceptance{decision: receiptRetired}, nil
	case receiptReplay:
		payload, digest, err := mgPersonalRootReadPayload(ctx, turn, lookup)
		if err != nil {
			return out, err
		}
		if lookup.resultCount != 1 {
			return out, ErrResolveRequired
		}
		result, err := decodeMGRecordingIntent(command.identity, command.digest, payload, digest)
		if err != nil {
			return out, err
		}
		return mgRecordingAcceptance{decision: receiptReplay, result: result, digest: digest}, nil
	case receiptFresh:
		facts, err := mgScheduleRecording(ctx, turn, request.owner, request.scope, command.frozen, command.birth())
		if err != nil {
			return out, err
		}
		result := facts.result()
		digest, err := mgPersonalRootPersist(ctx, turn, lookup, result, 1)
		if err != nil {
			return out, err
		}
		return mgRecordingAcceptance{decision: receiptFresh, result: result, digest: digest}, nil
	default:
		return out, ErrResolveRequired
	}
}

// Fresh update: immutable copied programme, exact current source/head facts,
// owner/revision/state fence and exact reservation-window CAS.
func acceptMGRecordingUpdate(ctx context.Context, turn *commandTurn, root mgPersonalRootSeam, witness *usOwnerAuthorityProof, lifetime context.Context, reference ReceiptReference, request mgRecordingUpdateRequest) (out mgRecordingAcceptance, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	command, err := newMGRecordingUpdateCommand(reference.Identity, request)
	if err != nil {
		return out, err
	}
	if command.digest != reference.RequestDigest {
		return out, ErrConflict
	}
	session, err := mgRecordingAuthorize(ctx, turn, MGUpdateRecording, root, witness, lifetime)
	if err != nil {
		return out, err
	}
	if err = mgRecordingOwnerBound(session, request.owner); err != nil {
		return out, err
	}
	ref, err := mgRecordingRootReference(session, MGUpdateRecording, command.identity, command.digest)
	if err != nil {
		return out, err
	}
	lookup, err := mgPersonalRootLookupCommand(ctx, turn, session, ref)
	if err != nil {
		return out, err
	}
	switch lookup.decision {
	case receiptRetired:
		return mgRecordingAcceptance{decision: receiptRetired}, nil
	case receiptReplay:
		payload, digest, err := mgPersonalRootReadPayload(ctx, turn, lookup)
		if err != nil {
			return out, err
		}
		if lookup.resultCount != 1 {
			return out, ErrResolveRequired
		}
		result, err := decodeMGRecordingIntent(command.identity, command.digest, payload, digest)
		if err != nil {
			return out, err
		}
		return mgRecordingAcceptance{decision: receiptReplay, result: result, digest: digest}, nil
	case receiptFresh:
		facts, err := mgUpdateRecording(ctx, turn, request.owner, request.id, request.expected, request.scope, command.frozen)
		if err != nil {
			return out, err
		}
		result := facts.result()
		digest, err := mgPersonalRootPersist(ctx, turn, lookup, result, 1)
		if err != nil {
			return out, err
		}
		return mgRecordingAcceptance{decision: receiptFresh, result: result, digest: digest}, nil
	default:
		return out, ErrResolveRequired
	}
}

// Fresh cancel: owner/revision custody only. It consults no guide head, so an
// intent stays cancellable after a source or guide replacement, and it retains
// the generation pin and every capture/allocation/artifact fact.
func acceptMGRecordingCancel(ctx context.Context, turn *commandTurn, root mgPersonalRootSeam, witness *usOwnerAuthorityProof, lifetime context.Context, reference ReceiptReference, request mgRecordingCancelRequest) (out mgRecordingAcceptance, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	command, err := newMGRecordingCancelCommand(reference.Identity, request)
	if err != nil {
		return out, err
	}
	if command.digest != reference.RequestDigest {
		return out, ErrConflict
	}
	session, err := mgRecordingAuthorize(ctx, turn, MGCancelRecording, root, witness, lifetime)
	if err != nil {
		return out, err
	}
	if err = mgRecordingOwnerBound(session, request.owner); err != nil {
		return out, err
	}
	ref, err := mgRecordingRootReference(session, MGCancelRecording, command.identity, command.digest)
	if err != nil {
		return out, err
	}
	lookup, err := mgPersonalRootLookupCommand(ctx, turn, session, ref)
	if err != nil {
		return out, err
	}
	switch lookup.decision {
	case receiptRetired:
		return mgRecordingAcceptance{decision: receiptRetired}, nil
	case receiptReplay:
		payload, digest, err := mgPersonalRootReadPayload(ctx, turn, lookup)
		if err != nil {
			return out, err
		}
		if lookup.resultCount != 1 {
			return out, ErrResolveRequired
		}
		result, err := decodeMGRecordingIntent(command.identity, command.digest, payload, digest)
		if err != nil {
			return out, err
		}
		return mgRecordingAcceptance{decision: receiptReplay, result: result, digest: digest}, nil
	case receiptFresh:
		facts, err := mgCancelRecording(ctx, turn, request.owner, request.id, request.expected)
		if err != nil {
			return out, err
		}
		result := facts.result()
		digest, err := mgPersonalRootPersist(ctx, turn, lookup, result, 1)
		if err != nil {
			return out, err
		}
		return mgRecordingAcceptance{decision: receiptFresh, result: result, digest: digest}, nil
	default:
		return out, ErrResolveRequired
	}
}

// Fresh keep: retention intent only, owner/revision CAS, no guide/head lookup
// and no reservation, allocation, pin or capture-namespace change.
func acceptMGRecordingKeep(ctx context.Context, turn *commandTurn, root mgPersonalRootSeam, witness *usOwnerAuthorityProof, lifetime context.Context, reference ReceiptReference, request mgRecordingKeepRequest) (out mgRecordingAcceptance, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	command, err := newMGRecordingKeepCommand(reference.Identity, request)
	if err != nil {
		return out, err
	}
	if command.digest != reference.RequestDigest {
		return out, ErrConflict
	}
	session, err := mgRecordingAuthorize(ctx, turn, MGKeepRecording, root, witness, lifetime)
	if err != nil {
		return out, err
	}
	if err = mgRecordingOwnerBound(session, request.owner); err != nil {
		return out, err
	}
	ref, err := mgRecordingRootReference(session, MGKeepRecording, command.identity, command.digest)
	if err != nil {
		return out, err
	}
	lookup, err := mgPersonalRootLookupCommand(ctx, turn, session, ref)
	if err != nil {
		return out, err
	}
	switch lookup.decision {
	case receiptRetired:
		return mgRecordingAcceptance{decision: receiptRetired}, nil
	case receiptReplay:
		payload, digest, err := mgPersonalRootReadPayload(ctx, turn, lookup)
		if err != nil {
			return out, err
		}
		if lookup.resultCount != 1 {
			return out, ErrResolveRequired
		}
		result, err := decodeMGRecordingIntent(command.identity, command.digest, payload, digest)
		if err != nil {
			return out, err
		}
		return mgRecordingAcceptance{decision: receiptReplay, result: result, digest: digest}, nil
	case receiptFresh:
		facts, err := mgKeepRecording(ctx, turn, request.owner, request.id, request.expected, request.keep)
		if err != nil {
			return out, err
		}
		result := facts.result()
		digest, err := mgPersonalRootPersist(ctx, turn, lookup, result, 1)
		if err != nil {
			return out, err
		}
		return mgRecordingAcceptance{decision: receiptFresh, result: result, digest: digest}, nil
	default:
		return out, ErrResolveRequired
	}
}
