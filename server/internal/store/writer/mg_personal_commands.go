package writer

import (
	"context"
)

// One accepting outcome. decision is the receipt classification; only Replay
// carries a saved result and only Fresh carries a newly persisted one. Retired
// is a complete zero result, never a reconstructed success.
type mgPersonalAcceptance struct {
	decision   receiptDecision
	preference mgPersonalPreferenceResult
	reminder   mgPersonalReminderResult
	digest     ResultDigest
}

// The witness is a comparison input, never a capability. A caller-supplied
// cached stamp is rebuilt from its native strings and must match exactly, so a
// forged issuer/timestamp pair cannot be paired with this frame.
func mgPersonalFreezeWitness(witness *usOwnerAuthorityProof) (*usOwnerAuthorityProof, error) {
	if witness == nil {
		return nil, ErrDisclosureDenied
	}
	p, err := prepareUSOwnerAuthority(witness.witness)
	if err != nil || *p != *witness {
		return nil, ErrDisclosureDenied
	}
	return p, nil
}

// mgPersonalAuthorize binds the closed purpose and durability, the caller
// lifetime and the frozen witness to the ONE current-session seam. Until the
// mint is merged the production seam refuses with ErrMGRootUnavailable; tests
// inject a fake session, never a boolean or issuer string.
func mgPersonalAuthorize(ctx context.Context, turn *commandTurn, purpose CommandID, root mgPersonalRootSeam, witness *usOwnerAuthorityProof, lifetime context.Context) (out mgPersonalRootAuthorization, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	if err = mgPersonalPurpose(turn, purpose); err != nil {
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
	if err = mgPersonalPurpose(turn, purpose); err != nil {
		return nil, err
	}
	return session, nil
}

// The frozen owner tuple is a comparison input, never a caller-selected
// identity. acceptMGPersonal* is the parent for these commands, so it binds the
// owner's account/profile to the validated session BEFORE any lookup or effect:
// a session for profile A can never read or write profile B's preference,
// reminder or reminder delete. The session alone owns the member/profile
// revisions, rating and item policy that the native rows recheck.
func mgPersonalOwnerBound(session mgPersonalRootAuthorization, owner mgPersonalOwner) error {
	if session == nil || !owner.valid() {
		return ErrDisclosureDenied
	}
	i := session.mgPersonalRootIdentity()
	if owner.account != i.account || owner.profile != i.profile {
		return ErrDisclosureDenied
	}
	return nil
}

// Fresh preference: current target/head observation, sparse owner CAS, saved
// scalar. A replay never re-enters the effect and an ACKed original is retired.
func acceptMGPersonalPreference(ctx context.Context, turn *commandTurn, root mgPersonalRootSeam, witness *usOwnerAuthorityProof, lifetime context.Context, reference ReceiptReference, request mgPersonalPreferenceRequest) (out mgPersonalAcceptance, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	command, err := newMGPersonalPreferenceCommand(reference.Identity, request)
	if err != nil {
		return out, err
	}
	if command.digest != reference.RequestDigest {
		return out, ErrConflict
	}
	session, err := mgPersonalAuthorize(ctx, turn, MGSetChannelPreference, root, witness, lifetime)
	if err != nil {
		return out, err
	}
	if err = mgPersonalOwnerBound(session, request.owner); err != nil {
		return out, err
	}
	ref := uoRootReference{root: session.mgPersonalRootID(), kind: MGSetChannelPreference, identity: command.identity, request: command.digest}
	lookup, err := mgPersonalRootLookupCommand(ctx, turn, session, ref)
	if err != nil {
		return out, err
	}
	switch lookup.decision {
	case receiptRetired:
		return mgPersonalAcceptance{decision: receiptRetired}, nil
	case receiptReplay:
		payload, digest, err := mgPersonalRootReadPayload(ctx, turn, lookup)
		if err != nil {
			return out, err
		}
		if lookup.resultCount != 1 {
			return out, ErrResolveRequired
		}
		result, err := decodeMGPersonalPreference(command.identity, command.digest, payload, digest)
		if err != nil {
			return out, err
		}
		return mgPersonalAcceptance{decision: receiptReplay, preference: result, digest: digest}, nil
	case receiptFresh:
		if err = session.admitTarget(ctx, turn, mgPersonalAdmissionTarget{kind: request.target.kind, source: request.target.source, channel: request.target.channel, generation: request.target.generation}); err != nil {
			return out, err
		}
		facts, err := setMGPersonalPreference(ctx, turn, request)
		if err != nil {
			return out, err
		}
		result := request.result(facts)
		digest, err := mgPersonalRootPersist(ctx, turn, lookup, result, 1)
		if err != nil {
			return out, err
		}
		return mgPersonalAcceptance{decision: receiptFresh, preference: result, digest: digest}, nil
	default:
		return out, ErrResolveRequired
	}
}

// Fresh reminder: generation-scoped programme/occurrence evidence, per-owner
// revision CAS, one saved intent result. An identical retry is resolved by its
// original root receipt; a different input under the same identity conflicts.
func acceptMGPersonalReminder(ctx context.Context, turn *commandTurn, root mgPersonalRootSeam, witness *usOwnerAuthorityProof, lifetime context.Context, reference ReceiptReference, request mgPersonalReminderRequest) (out mgPersonalAcceptance, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	command, err := newMGPersonalReminderCommand(reference.Identity, request)
	if err != nil {
		return out, err
	}
	if command.digest != reference.RequestDigest {
		return out, ErrConflict
	}
	session, err := mgPersonalAuthorize(ctx, turn, MGSetReminder, root, witness, lifetime)
	if err != nil {
		return out, err
	}
	if err = mgPersonalOwnerBound(session, request.owner); err != nil {
		return out, err
	}
	ref := uoRootReference{root: session.mgPersonalRootID(), kind: MGSetReminder, identity: command.identity, request: command.digest}
	lookup, err := mgPersonalRootLookupCommand(ctx, turn, session, ref)
	if err != nil {
		return out, err
	}
	switch lookup.decision {
	case receiptRetired:
		return mgPersonalAcceptance{decision: receiptRetired}, nil
	case receiptReplay:
		payload, digest, err := mgPersonalRootReadPayload(ctx, turn, lookup)
		if err != nil {
			return out, err
		}
		if lookup.resultCount != 1 {
			return out, ErrResolveRequired
		}
		result, err := decodeMGPersonalReminder(command.identity, command.digest, payload, digest)
		if err != nil {
			return out, err
		}
		return mgPersonalAcceptance{decision: receiptReplay, reminder: result, digest: digest}, nil
	case receiptFresh:
		if err = session.admitTarget(ctx, turn, mgPersonalAdmissionTarget{kind: request.target.kind, source: request.target.source, channel: request.target.channel, generation: request.target.generation, programme: request.programme, item: request.libraryItem}); err != nil {
			return out, err
		}
		facts, err := setMGPersonalReminder(ctx, turn, request)
		if err != nil {
			return out, err
		}
		result := facts.result()
		digest, err := mgPersonalRootPersist(ctx, turn, lookup, result, 1)
		if err != nil {
			return out, err
		}
		return mgPersonalAcceptance{decision: receiptFresh, reminder: result, digest: digest}, nil
	default:
		return out, ErrResolveRequired
	}
}

// Reminder deletion is owner/revision intent CAS. It never consults the current
// guide head, so an intent survives guide replacement and its original result
// remains replayable until the receipt is acknowledged.
func acceptMGPersonalReminderDelete(ctx context.Context, turn *commandTurn, root mgPersonalRootSeam, witness *usOwnerAuthorityProof, lifetime context.Context, reference ReceiptReference, request mgPersonalDeleteRequest) (out mgPersonalAcceptance, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	command, err := newMGPersonalDeleteCommand(reference.Identity, request)
	if err != nil {
		return out, err
	}
	if command.digest != reference.RequestDigest {
		return out, ErrConflict
	}
	session, err := mgPersonalAuthorize(ctx, turn, MGDeleteReminder, root, witness, lifetime)
	if err != nil {
		return out, err
	}
	if err = mgPersonalOwnerBound(session, request.owner); err != nil {
		return out, err
	}
	ref := uoRootReference{root: session.mgPersonalRootID(), kind: MGDeleteReminder, identity: command.identity, request: command.digest}
	lookup, err := mgPersonalRootLookupCommand(ctx, turn, session, ref)
	if err != nil {
		return out, err
	}
	switch lookup.decision {
	case receiptRetired:
		return mgPersonalAcceptance{decision: receiptRetired}, nil
	case receiptReplay:
		payload, digest, err := mgPersonalRootReadPayload(ctx, turn, lookup)
		if err != nil {
			return out, err
		}
		if lookup.resultCount != 1 {
			return out, ErrResolveRequired
		}
		result, err := decodeMGPersonalReminder(command.identity, command.digest, payload, digest)
		if err != nil {
			return out, err
		}
		return mgPersonalAcceptance{decision: receiptReplay, reminder: result, digest: digest}, nil
	case receiptFresh:
		facts, err := deleteMGPersonalReminder(ctx, turn, request)
		if err != nil {
			return out, err
		}
		result := facts.result()
		digest, err := mgPersonalRootPersist(ctx, turn, lookup, result, 1)
		if err != nil {
			return out, err
		}
		return mgPersonalAcceptance{decision: receiptFresh, reminder: result, digest: digest}, nil
	default:
		return out, ErrResolveRequired
	}
}
