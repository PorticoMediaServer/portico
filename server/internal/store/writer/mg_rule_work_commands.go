package writer

import "context"

// One accepting outcome. decision is the receipt classification; only Replay
// carries a saved result and only Fresh carries a newly persisted one. Retired
// is a complete zero result, never a reconstructed success.
type mgReconcileAcceptance struct {
	decision receiptDecision
	result   mgReconcileResult
	digest   ResultDigest
}

// mgReconcileAuthorize composes the SAME MG-owned current-session root adapter
// (mgPersonalRootSeam) as the sparse personal, recording and rule commands: the
// closed purpose and NORMAL durability, the caller lifetime, the frozen witness
// and the exact root identity. Until the US mint lands the production seam
// refuses with ErrMGRootUnavailable; tests inject a fake session, never a
// boolean or issuer string. This adds no second adapter.
func mgReconcileAuthorize(ctx context.Context, turn *commandTurn, purpose CommandID, root mgPersonalRootSeam, witness *usOwnerAuthorityProof, lifetime context.Context, owner mgRecordingOwner) (out mgPersonalRootAuthorization, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	if err = mgReconcilePurpose(turn, purpose); err != nil {
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
	if !owner.valid() {
		return nil, ErrDisclosureDenied
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
	if err = mgRecordingOwnerBound(session, owner); err != nil {
		return nil, err
	}
	if err = mgReconcilePurpose(turn, purpose); err != nil {
		return nil, err
	}
	return session, nil
}

func mgReconcileRuleValue(c *mgReconcileRuleCommand, f mgReconcileWorkFacts) mgReconcileResult {
	return mgReconcileResult{
		variant: mgReconcileResultVariant, rule: f.rule, owner: c.owner.key(), generation: f.generation,
		phase: f.phase, cursor: f.cursor, ruleRevision: f.ruleRevision, workRevision: f.jobRevision,
		jobOwner: f.jobOwner, jobGeneration: f.leaseGeneration, jobExpires: f.expires, attempts: f.attempts,
		dueMS: f.dueMS, hasDue: f.hasDue, units: c.request.pageUnits, pending: f.phase != "idle",
	}
}

func mgReconcileScheduledValue(c *mgReconcileScheduledCommand, f mgScheduledReconcileFacts) mgReconcileResult {
	return mgReconcileResult{
		variant: mgScheduledResultVariant, rule: f.work.rule, owner: c.owner.key(), generation: f.work.generation,
		phase: f.work.phase, cursor: f.work.cursor, ruleRevision: f.work.ruleRevision, workRevision: f.work.jobRevision,
		jobOwner: f.work.jobOwner, jobGeneration: f.work.leaseGeneration, jobExpires: f.work.expires,
		attempts: f.work.attempts, dueMS: f.work.dueMS, hasDue: f.work.hasDue, pending: f.pending,
		units: int64(len(f.ids)), ownerRevision: f.ownerRevision, ids: append([]string{}, f.ids...),
	}
}

// Fresh 826: claim/renew/advance ONE bounded page of rule work. A replay never
// re-enters the effect and an ACKed original is retired.
func acceptMGReconcileRule(ctx context.Context, turn *commandTurn, root mgPersonalRootSeam, witness *usOwnerAuthorityProof, lifetime context.Context, reference ReceiptReference, request mgReconcileRuleRequest) (out mgReconcileAcceptance, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	command, err := newMGReconcileRuleCommand(reference.Identity, request)
	if err != nil {
		return out, err
	}
	if command.digest != reference.RequestDigest {
		return out, ErrConflict
	}
	session, err := mgReconcileAuthorize(ctx, turn, MGRuleWorkReconcile, root, witness, lifetime, request.owner)
	if err != nil {
		return out, err
	}
	ref := uoRootReference{root: session.mgPersonalRootID(), kind: MGRuleWorkReconcile, identity: command.identity, request: command.digest}
	lookup, err := mgPersonalRootLookupCommand(ctx, turn, session, ref)
	if err != nil {
		return out, err
	}
	switch lookup.decision {
	case receiptRetired:
		return mgReconcileAcceptance{decision: receiptRetired}, nil
	case receiptReplay:
		payload, digest, err := mgPersonalRootReadPayload(ctx, turn, lookup)
		if err != nil {
			return out, err
		}
		if lookup.resultCount != 1 {
			return out, ErrResolveRequired
		}
		result, err := decodeMGReconcile(command.identity, command.digest, payload, digest)
		if err != nil || result.variant != mgReconcileResultVariant {
			return out, ErrResolveRequired
		}
		return mgReconcileAcceptance{decision: receiptReplay, result: result, digest: digest}, nil
	case receiptFresh:
		facts, err := mgReconcileRuleWork(ctx, turn, request)
		if err != nil {
			return out, err
		}
		result := mgReconcileRuleValue(command, facts)
		digest, err := mgPersonalRootPersist(ctx, turn, lookup, result, 1)
		if err != nil {
			return out, err
		}
		return mgReconcileAcceptance{decision: receiptFresh, result: result, digest: digest}, nil
	default:
		return out, ErrResolveRequired
	}
}

// Fresh 827: detach or cancel ONE bounded page of a retired rule's future
// scheduled intents and co-commit the exact work cursor plus owner revision. A
// replay never re-enters the effect and an ACKed original is retired.
func acceptMGScheduledReconcile(ctx context.Context, turn *commandTurn, root mgPersonalRootSeam, witness *usOwnerAuthorityProof, lifetime context.Context, reference ReceiptReference, request mgReconcileScheduledRequest) (out mgReconcileAcceptance, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	command, err := newMGReconcileScheduledCommand(reference.Identity, request)
	if err != nil {
		return out, err
	}
	if command.digest != reference.RequestDigest {
		return out, ErrConflict
	}
	session, err := mgReconcileAuthorize(ctx, turn, MGScheduledReconcile, root, witness, lifetime, request.owner)
	if err != nil {
		return out, err
	}
	ref := uoRootReference{root: session.mgPersonalRootID(), kind: MGScheduledReconcile, identity: command.identity, request: command.digest}
	lookup, err := mgPersonalRootLookupCommand(ctx, turn, session, ref)
	if err != nil {
		return out, err
	}
	switch lookup.decision {
	case receiptRetired:
		return mgReconcileAcceptance{decision: receiptRetired}, nil
	case receiptReplay:
		payload, digest, err := mgPersonalRootReadPayload(ctx, turn, lookup)
		if err != nil {
			return out, err
		}
		if lookup.resultCount != 1 {
			return out, ErrResolveRequired
		}
		result, err := decodeMGReconcile(command.identity, command.digest, payload, digest)
		if err != nil || result.variant != mgScheduledResultVariant {
			return out, ErrResolveRequired
		}
		return mgReconcileAcceptance{decision: receiptReplay, result: result, digest: digest}, nil
	case receiptFresh:
		facts, err := mgScheduledReconcile(ctx, turn, request)
		if err != nil {
			return out, err
		}
		result := mgReconcileScheduledValue(command, facts)
		digest, err := mgPersonalRootPersist(ctx, turn, lookup, result, 1)
		if err != nil {
			return out, err
		}
		return mgReconcileAcceptance{decision: receiptFresh, result: result, digest: digest}, nil
	default:
		return out, ErrResolveRequired
	}
}
