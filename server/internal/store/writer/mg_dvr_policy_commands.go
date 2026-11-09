package writer

import (
	"context"
)

// mgDVRPolicyAuthorize binds the closed NORMAL purpose, the caller lifetime and
// the frozen witness to the ONE current-session root seam. Until the mint is
// merged the production seam refuses with ErrMGRootUnavailable; tests inject a
// fake session through the same interface, never a boolean or issuer string.
func mgDVRPolicyAuthorize(ctx context.Context, turn *commandTurn, purpose CommandID, root mgPersonalRootSeam, witness *usOwnerAuthorityProof, lifetime context.Context) (out mgPersonalRootAuthorization, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	if err = mgDVRPolicyPurpose(turn, purpose); err != nil {
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
	if err = mgDVRPolicyPurpose(turn, purpose); err != nil {
		return nil, err
	}
	return session, nil
}

// Fresh defaults: current singleton observation, expected revision and the
// full-value CAS owned by the leaf effect, then ONE saved scalar under the
// original public root. A replay never re-enters the effect, and an ACKed
// original is retired rather than reconstructed from today's policy.
func acceptMGSetDVRDefaults(ctx context.Context, turn *commandTurn, root mgPersonalRootSeam, witness *usOwnerAuthorityProof, lifetime context.Context, reference ReceiptReference, request mgDVRDefaultsPolicyRequest) (out mgDVRPolicyAcceptance, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	digest, err := request.digest(reference.Identity)
	if err != nil {
		return out, err
	}
	if digest != reference.RequestDigest {
		return out, ErrConflict
	}
	session, err := mgDVRPolicyAuthorize(ctx, turn, MGSetDVRDefaults, root, witness, lifetime)
	if err != nil {
		return out, err
	}
	ref := uoRootReference{root: session.mgPersonalRootID(), kind: MGSetDVRDefaults, identity: reference.Identity, request: digest}
	lookup, err := mgPersonalRootLookupCommand(ctx, turn, session, ref)
	if err != nil {
		return out, err
	}
	switch lookup.decision {
	case receiptRetired:
		return mgDVRPolicyAcceptance{decision: receiptRetired}, nil
	case receiptReplay:
		payload, stored, err := mgPersonalRootReadPayload(ctx, turn, lookup)
		if err != nil {
			return out, err
		}
		if lookup.resultCount != 1 {
			return out, ErrResolveRequired
		}
		result, err := decodeMGDVRPolicyResult(reference.Identity, digest, mgDVRPolicyDefaultsKind, payload, stored)
		if err != nil {
			return out, err
		}
		return mgDVRPolicyAcceptance{decision: receiptReplay, policy: result, digest: stored}, nil
	case receiptFresh:
		current, err := readMGDVRDefaults(ctx, turn)
		if err != nil {
			return out, err
		}
		if current.revision != request.expected {
			return out, ErrConflict
		}
		desired, err := freezeMGDVRDefaults(request.input)
		if err != nil {
			return out, err
		}
		facts, err := setMGDVRDefaults(ctx, turn, current, desired)
		if err != nil {
			return out, err
		}
		result := facts.result()
		saved, err := mgPersonalRootPersist(ctx, turn, lookup, result, 1)
		if err != nil {
			return out, err
		}
		return mgDVRPolicyAcceptance{decision: receiptFresh, policy: result, digest: saved}, nil
	default:
		return out, ErrResolveRequired
	}
}

// Fresh storage policy: the same shape over the storage singleton. A numeric
// floor/cap is still not a readiness certificate; this command never performs
// retention, cleanup, release or recording rewrites.
func acceptMGSetDVRStoragePolicy(ctx context.Context, turn *commandTurn, root mgPersonalRootSeam, witness *usOwnerAuthorityProof, lifetime context.Context, reference ReceiptReference, request mgDVRStoragePolicyRequest) (out mgDVRPolicyAcceptance, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	digest, err := request.digest(reference.Identity)
	if err != nil {
		return out, err
	}
	if digest != reference.RequestDigest {
		return out, ErrConflict
	}
	session, err := mgDVRPolicyAuthorize(ctx, turn, MGSetDVRStoragePolicy, root, witness, lifetime)
	if err != nil {
		return out, err
	}
	ref := uoRootReference{root: session.mgPersonalRootID(), kind: MGSetDVRStoragePolicy, identity: reference.Identity, request: digest}
	lookup, err := mgPersonalRootLookupCommand(ctx, turn, session, ref)
	if err != nil {
		return out, err
	}
	switch lookup.decision {
	case receiptRetired:
		return mgDVRPolicyAcceptance{decision: receiptRetired}, nil
	case receiptReplay:
		payload, stored, err := mgPersonalRootReadPayload(ctx, turn, lookup)
		if err != nil {
			return out, err
		}
		if lookup.resultCount != 1 {
			return out, ErrResolveRequired
		}
		result, err := decodeMGDVRPolicyResult(reference.Identity, digest, mgDVRPolicyStorageKind, payload, stored)
		if err != nil {
			return out, err
		}
		return mgDVRPolicyAcceptance{decision: receiptReplay, policy: result, digest: stored}, nil
	case receiptFresh:
		current, err := readMGDVRStorage(ctx, turn)
		if err != nil {
			return out, err
		}
		if current.revision != request.expected {
			return out, ErrConflict
		}
		desired, err := freezeMGDVRStorage(request.policy)
		if err != nil {
			return out, err
		}
		facts, err := setMGDVRStorage(ctx, turn, current, desired)
		if err != nil {
			return out, err
		}
		result := facts.result()
		saved, err := mgPersonalRootPersist(ctx, turn, lookup, result, 1)
		if err != nil {
			return out, err
		}
		return mgDVRPolicyAcceptance{decision: receiptFresh, policy: result, digest: saved}, nil
	default:
		return out, ErrResolveRequired
	}
}
