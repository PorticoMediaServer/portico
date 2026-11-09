package writer

import (
	"context"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

// One accepting outcome. Only Replay carries a saved result and only Fresh
// carries a newly persisted one. Retired is a complete zero result, never a
// reconstructed claim.
type mgCaptureAcceptance struct {
	decision receiptDecision
	result   mgCaptureResult
	digest   ResultDigest
}

// Purpose and durability bind these NORMAL capture effects to the exact closed
// command. A FULL, foreign or unverified frame cannot borrow them.
func mgCapturePurpose(t *commandTurn, purpose CommandID) error {
	if t == nil || t.transaction == nil || t.transaction.descriptor.ID != purpose ||
		t.transaction.descriptor.Durability != Normal || !t.transaction.policyVerified ||
		!t.transaction.begun || t.transaction.done ||
		t.transaction.settings.Synchronous != int(sqliteconn.Normal) {
		return ErrInvalid
	}
	switch purpose {
	case MGCaptureClaim, MGRenewCapture, MGCheckpointCapture:
		return nil
	}
	return ErrInvalid
}

// mgCaptureAuthorize composes the SAME MG-owned current-session root adapter as
// the sparse personal and recording-intent commands: the closed purpose and
// NORMAL durability, the caller lifetime, the frozen witness and the exact root
// identity. Until the US mint lands production refuses with
// ErrMGRootUnavailable; tests inject a fake session, never a boolean or issuer.
func mgCaptureAuthorize(ctx context.Context, turn *commandTurn, purpose CommandID, root mgPersonalRootSeam, witness *usOwnerAuthorityProof, lifetime context.Context) (out mgPersonalRootAuthorization, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	if err = mgCapturePurpose(turn, purpose); err != nil {
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
	if err = mgCapturePurpose(turn, purpose); err != nil {
		return nil, err
	}
	return session, nil
}

// Fresh claim: current owner/source/allocation/launch observation, one native
// capture namespace write on the exact fresh intent and the original saved
// scalar. No worker starts and no physical custody is proven.
func acceptMGCaptureClaim(ctx context.Context, turn *commandTurn, root mgPersonalRootSeam, witness *usOwnerAuthorityProof, lifetime context.Context, reference ReceiptReference, request mgCaptureClaimRequest) (out mgCaptureAcceptance, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	command, err := newMGCaptureClaimCommand(reference.Identity, request)
	if err != nil {
		return out, err
	}
	if command.digest != reference.RequestDigest {
		return out, ErrConflict
	}
	session, err := mgCaptureAuthorize(ctx, turn, MGCaptureClaim, root, witness, lifetime)
	if err != nil {
		return out, err
	}
	if err = mgRecordingOwnerBound(session, request.owner); err != nil {
		return out, err
	}
	ref, err := mgRecordingRootReference(session, MGCaptureClaim, command.identity, command.digest)
	if err != nil {
		return out, err
	}
	lookup, err := mgPersonalRootLookupCommand(ctx, turn, session, ref)
	if err != nil {
		return out, err
	}
	switch lookup.decision {
	case receiptRetired:
		return mgCaptureAcceptance{decision: receiptRetired}, nil
	case receiptReplay:
		payload, digest, err := mgPersonalRootReadPayload(ctx, turn, lookup)
		if err != nil {
			return out, err
		}
		if lookup.resultCount != 1 {
			return out, ErrResolveRequired
		}
		result, err := decodeMGCaptureResult(command.identity, command.digest, payload, digest)
		if err != nil {
			return out, err
		}
		return mgCaptureAcceptance{decision: receiptReplay, result: result, digest: digest}, nil
	case receiptFresh:
		row, err := mgReadCapture(ctx, turn, request.id)
		if err != nil {
			return out, err
		}
		facts, err := mgClaimCapture(ctx, turn, row, command.command(), request)
		if err != nil {
			return out, err
		}
		result := facts.row.result()
		digest, err := mgPersonalRootPersist(ctx, turn, lookup, result, 1)
		if err != nil {
			return out, err
		}
		return mgCaptureAcceptance{decision: receiptFresh, result: result, digest: digest}, nil
	default:
		return out, ErrResolveRequired
	}
}

// Fresh renewal: exact live claim and allocation, extended logical lease only.
func acceptMGCaptureRenew(ctx context.Context, turn *commandTurn, root mgPersonalRootSeam, witness *usOwnerAuthorityProof, lifetime context.Context, reference ReceiptReference, request mgCaptureRenewRequest) (out mgCaptureAcceptance, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	command, err := newMGCaptureRenewCommand(reference.Identity, request)
	if err != nil {
		return out, err
	}
	if command.digest != reference.RequestDigest {
		return out, ErrConflict
	}
	session, err := mgCaptureAuthorize(ctx, turn, MGRenewCapture, root, witness, lifetime)
	if err != nil {
		return out, err
	}
	if err = mgRecordingOwnerBound(session, request.owner); err != nil {
		return out, err
	}
	ref, err := mgRecordingRootReference(session, MGRenewCapture, command.identity, command.digest)
	if err != nil {
		return out, err
	}
	lookup, err := mgPersonalRootLookupCommand(ctx, turn, session, ref)
	if err != nil {
		return out, err
	}
	switch lookup.decision {
	case receiptRetired:
		return mgCaptureAcceptance{decision: receiptRetired}, nil
	case receiptReplay:
		payload, digest, err := mgPersonalRootReadPayload(ctx, turn, lookup)
		if err != nil {
			return out, err
		}
		if lookup.resultCount != 1 {
			return out, ErrResolveRequired
		}
		result, err := decodeMGCaptureResult(command.identity, command.digest, payload, digest)
		if err != nil {
			return out, err
		}
		return mgCaptureAcceptance{decision: receiptReplay, result: result, digest: digest}, nil
	case receiptFresh:
		row, err := mgReadCapture(ctx, turn, request.id)
		if err != nil {
			return out, err
		}
		facts, err := mgRenewCapture(ctx, turn, row, command.command(), request)
		if err != nil {
			return out, err
		}
		result := facts.row.result()
		digest, err := mgPersonalRootPersist(ctx, turn, lookup, result, 1)
		if err != nil {
			return out, err
		}
		return mgCaptureAcceptance{decision: receiptFresh, result: result, digest: digest}, nil
	default:
		return out, ErrResolveRequired
	}
}

// Fresh checkpoint: native captured progress on the exact live claim. It never
// retires the writer, records a physical exit, releases bytes or writes
// checkpoint_json.
func acceptMGCaptureCheckpoint(ctx context.Context, turn *commandTurn, root mgPersonalRootSeam, witness *usOwnerAuthorityProof, lifetime context.Context, reference ReceiptReference, request mgCaptureCheckpointRequest) (out mgCaptureAcceptance, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	command, err := newMGCaptureCheckpointCommand(reference.Identity, request)
	if err != nil {
		return out, err
	}
	if command.digest != reference.RequestDigest {
		return out, ErrConflict
	}
	session, err := mgCaptureAuthorize(ctx, turn, MGCheckpointCapture, root, witness, lifetime)
	if err != nil {
		return out, err
	}
	if err = mgRecordingOwnerBound(session, request.owner); err != nil {
		return out, err
	}
	ref, err := mgRecordingRootReference(session, MGCheckpointCapture, command.identity, command.digest)
	if err != nil {
		return out, err
	}
	lookup, err := mgPersonalRootLookupCommand(ctx, turn, session, ref)
	if err != nil {
		return out, err
	}
	switch lookup.decision {
	case receiptRetired:
		return mgCaptureAcceptance{decision: receiptRetired}, nil
	case receiptReplay:
		payload, digest, err := mgPersonalRootReadPayload(ctx, turn, lookup)
		if err != nil {
			return out, err
		}
		if lookup.resultCount != 1 {
			return out, ErrResolveRequired
		}
		result, err := decodeMGCaptureResult(command.identity, command.digest, payload, digest)
		if err != nil {
			return out, err
		}
		return mgCaptureAcceptance{decision: receiptReplay, result: result, digest: digest}, nil
	case receiptFresh:
		row, err := mgReadCapture(ctx, turn, request.id)
		if err != nil {
			return out, err
		}
		facts, err := mgCheckpointCapture(ctx, turn, row, command.command(), request)
		if err != nil {
			return out, err
		}
		result := facts.row.result()
		digest, err := mgPersonalRootPersist(ctx, turn, lookup, result, 1)
		if err != nil {
			return out, err
		}
		return mgCaptureAcceptance{decision: receiptFresh, result: result, digest: digest}, nil
	default:
		return out, ErrResolveRequired
	}
}
