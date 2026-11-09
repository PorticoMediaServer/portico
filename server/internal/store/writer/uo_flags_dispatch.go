package writer

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/apievents"
)

// UO-03 is the complete root original for native purpose 600: one sparse flags
// patch on personal_current_live plus the viewer/selection heads, saved as one
// closed typed result under the shared lifetime root. It is NORMAL durability,
// which is not authorization.
//
// Every entry consumes the SAME private same-commandTurn proof UO-01 defines and
// UO-02's component already uses: the proof's frame, purpose, lifetime, deadline
// and identity are rechecked before the retained owner/receipt lookup and again
// at release through this purpose's uoFlagsRelease gate (which calls the shared
// release and then keeps Fresh on an active root). This file mints no developer
// token, invents no second issuer and accepts no caller bool.
// UO-01's bindUORoot still refuses every NORMAL proof until Sol's current-session
// mint lands, so the purpose keeps its own proof checks instead of presenting a
// binding that cannot pass.

// The current closed NORMAL proof carries no authority field. The native
// interactive product has exactly one authority, "local"; every personal owner
// key in this database is therefore the [local,account,profile] tuple. A Hosted
// or narrow-grant proof must never be reinterpreted through this constant: it
// needs its own separately reviewed authority-qualified key recipe.
const uoFlagsNativeAuthority = "local"

// uoFlagsPersonalOwnerKey is the lossless persisted owner tuple
// persistence.PersonalOwnerKey writes as personal_items.profile_id and
// identity.PersonalKey delegates to. The bytes are ported locally because
// importing persistence pulls in dbwork/worker, whose init() registers legacy
// SQLite scalar functions and changes the pragma_function_list hash the plan
// goldens bind. Keep them identical; TestUOFlagsPersonalOwnerKeyDrift parses
// the source of both originals to catch drift.
func uoFlagsPersonalOwnerKey(authority, account, profile string) string {
	raw, _ := json.Marshal([3]string{authority, account, profile})
	return "viewer:" + base64.RawURLEncoding.EncodeToString(raw)
}

// bindUOFlagsFreshInput ties a Fresh submission to the proven owner and to the
// reference's request digest. The proof and root bind identity.account and
// identity.profile, but applyUOPersonalFlags still writes the caller-supplied
// profileKey; without this check a valid proof for profile A could commit
// profile B's rows under A's root and receipt (the #1375 case). Plain equality
// with identity.profile would be wrong because the stored key is the lossless
// owner tuple, so the expected key is DERIVED from the proven identity. The
// digest is RECOMPUTED from the complete input, exactly as runUSPolicyDispatch
// does, so one reference cannot authorize a different patch.
func bindUOFlagsFreshInput(proof *uoRootCurrentProof, reference uoRootReference, input uoPersonalFlagsInput) error {
	if proof == nil {
		return ErrDisclosureDenied
	}
	if err := input.validate(); err != nil {
		return err
	}
	if input.profileKey != uoFlagsPersonalOwnerKey(uoFlagsNativeAuthority, proof.identity.account, proof.identity.profile) {
		return ErrDisclosureDenied
	}
	digest, err := canonicalRequestDigest(SetPersonalFlags, 1, reference.identity, input)
	if err != nil {
		return errors.Join(ErrConflict, err)
	}
	if digest != reference.request {
		return ErrConflict
	}
	return nil
}

// appendUOFlagsChangedHint appends the ONE personal.changed hint a real flag
// change owes. The native schema has no personal_change_hint trigger, so this
// dispatcher owns the bounded append (see the assignment in
// uo_personal_flags.go). apievents.Flush derives this profile audience from the
// legacy "personal:"||owner hint, and the legacy viewer_revisions hint names
// the library with the viewer revision, so those exact fields are reused. No
// titles field means "any title in this library", the documented fallback; the
// scalar patch never claims a partial title path.
func appendUOFlagsChangedHint(ctx context.Context, turn *commandTurn, identity uoRootIdentity, libraryKey string, revision, nowMS int64) error {
	if libraryKey == "" || revision <= 0 || nowMS <= 0 {
		return ErrInvalid
	}
	data := fmt.Sprintf(`{"personal":"%d","observation":"0"}`, revision)
	_, err := appendWCAPIEvent(ctx, turn, wcAPIEvent{
		audience:     apievents.ProfileAudience(uoFlagsNativeAuthority, identity.account, identity.profile),
		eventType:    "personal.changed",
		resourceKind: "library",
		resourceID:   libraryKey,
		revision:     strconv.FormatInt(revision, 10),
		data:         data,
		atMS:         nowMS,
	})
	return err
}

// uoFlagsRelease is this purpose's final gate before the terminal. UO-01's
// shared release now admits a Fresh 654 maintenance cleanup on a draining root
// (the SOL-USERS reclaim exception); 600 keeps the domain rule unchanged: a
// Fresh flags patch must end on an ACTIVE root, while an authorized replay or
// retired disclosure may end draining. It calls the shared release first so a
// missing/foreign root, a stale incarnation or a finished frame still fails with
// the shared class, then re-reads an active state as the last check.
func uoFlagsRelease(ctx context.Context, turn *commandTurn, proof *uoRootCurrentProof, rootID string, decision receiptDecision) error {
	if err := uoRootReclaimRelease(ctx, turn, proof, rootID, decision); err != nil {
		return err
	}
	if decision != receiptFresh {
		return nil
	}
	binding, err := uoRootProofCurrent(proof, turn)
	if err != nil {
		return err
	}
	state, err := binding.root(ctx, turn, rootID)
	if err != nil {
		return err
	}
	if state != "active" {
		return ErrUORootGone
	}
	return turn.checkLifetimes(ctx)
}

// lookupUORootFlags follows UO-01's skeleton exactly: current authority and the
// root PK precede every retained owner/header read, the owner point is read once,
// the receipt meta point is read before any payload, and W's classifyReceipt is
// the sole receipt classifier. No second sequence comparison is introduced here,
// so a stale incarnation still returns ErrResolveRequired joined with
// ErrOwnerUnavailable.
func lookupUORootFlags(ctx context.Context, turn *commandTurn, proof *uoRootCurrentProof, q uoRootReference) (out uoRootLookup, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if !q.validFor(turn) || q.kind != SetPersonalFlags {
		return out, ErrInvalid
	}
	binding, err := uoRootProofCurrent(proof, turn)
	if err != nil {
		return out, err
	}
	rootState, err := binding.root(ctx, turn, q.root)
	if err != nil {
		return out, err
	}
	row, err := wc008DraftQuery(ctx, turn, uoRootOwnerPoint, q.root)
	if err != nil {
		return out, err
	}
	var owner uoRootOwner
	var incarnation []byte
	err = row.Scan(&incarnation, &owner.facts.lastSequence, &owner.facts.acknowledgedSequence, &owner.revision, &owner.rows, &owner.variableBytes, &owner.afterSequence, &owner.due)
	if errors.Is(err, sql.ErrNoRows) {
		return out, errors.Join(ErrResolveRequired, ErrOwnerUnavailable)
	}
	if err != nil {
		return out, err
	}
	if len(incarnation) != 16 || owner.revision <= 0 || owner.rows < 0 || owner.variableBytes < 0 || (owner.rows == 0) != (owner.variableBytes == 0) || owner.afterSequence < 0 || owner.afterSequence > owner.facts.acknowledgedSequence || owner.due.Valid && owner.due.Int64 < 0 {
		return out, ErrResolveRequired
	}
	copy(owner.facts.incarnation[:], incarnation)
	owner.facts.exists = true
	expected := receiptIdentity{command: q.identity, digest: q.request}
	found := receiptFacts{}
	var size, variable, appliedMS int64
	// ACKed originals need no receipt header to remain retired; the floor is
	// classified from the owner and the row is left at the floor.
	if q.identity.Sequence > owner.facts.acknowledgedSequence {
		row, err = wc008DraftQuery(ctx, turn, uoRootReceiptMeta, q.root, q.identity.Incarnation[:], q.identity.Sequence)
		if err != nil {
			return out, err
		}
		var kind, version, count, digestBytes int64
		var operation, request []byte
		err = row.Scan(&kind, &version, &count, &appliedMS, &size, &variable, &digestBytes, &operation, &request)
		switch {
		case err == nil:
			if kind != int64(q.kind) || !bytes.Equal(operation, q.identity.OperationID[:]) || !bytes.Equal(request, q.request[:]) {
				return out, ErrConflict
			}
			// Exact prospective bounds precede any payload copy. variable_bytes
			// also proves the stored row is the complete native record.
			if version != 1 || count != 0 || appliedMS < 0 || size < 1 || size > uoRootPayloadCap || variable != size+uoRootFixedBytes || digestBytes != 32 {
				return out, ErrResolveRequired
			}
			found = receiptFacts{exists: true, identity: expected, kind: q.kind, resultCount: count}
		case !errors.Is(err, sql.ErrNoRows):
			return out, err
		}
	}
	decision, err := classifyReceipt(expected, q.kind, owner.facts, found)
	if err != nil {
		return out, err
	}
	// A draining root keeps an authorized ORIGINAL replay but admits no Fresh
	// effect.
	if decision == receiptFresh && rootState != "active" {
		return out, ErrUORootGone
	}
	// A constructor-time check cannot outlive a revocation or root change during
	// these reads.
	if err = uoFlagsRelease(ctx, turn, proof, q.root, decision); err != nil {
		return out, err
	}
	return uoRootLookup{turn: turn, binding: binding, reference: q, owner: owner, decision: decision, payloadBytes: size, variableBytes: variable, resultCount: found.resultCount, appliedMS: appliedMS}, nil
}

// uoRootFlagsInvocation is one patch or ACK request. The reference is a
// comparison value supplied by the caller's retry custody, never proof.
type uoRootFlagsInvocation struct {
	reference uoRootReference
	public    [16]byte
	input     uoPersonalFlagsInput
	nowMS     int64
	ack       bool
}

// uoRootFlagsPublished is the pre-terminal release packet of the closed dispatch
// entry.
type uoRootFlagsPublished struct {
	out   uoRootFlagsResult
	floor int64
}

// runUOFlagsDispatch is the closed test/harness entry for 600. It mirrors
// runUSPolicyDispatch on main: the body is released through finishCommand BEFORE
// the physical terminal (which cancels its own lifetime), a release-time denial
// REPLACES the body and rolls the prefix back, an unknown COMMIT is
// Resolve-required with zero output, and a final deferred publication on the
// CALLER's context withdraws a result that committed before a late cancel.
//
// The result saved here is the INERT scalar primitive (flags, revision, heads,
// applied time), NOT the completed public 600. The public original must be the
// complete catalog.PersonalReceipt (used at detail.go:189, defined at
// detail_types.go:162): its identity, library, viewer fence and receipt lifetime
// plus Personal catalog.PersonalState (ContinueDismissed,
// watched/progressSeconds/lastPlayedAt/status/conflicts, every flag, the
// nullable rating and revision) and optional Current, loaded post-effect in this
// same frame. Replay may not synthesize a stub or read mutable progress. The
// public adapter stays HELD until that storage/image is adopted.
//
// Viewing authority is also still missing: the item->current-library binding and
// the current membership/profile effective-library/restriction visibility must
// be proved before any Fresh write, before retained payload disclosure and
// re-proved before release (UO-03c). 600 is NOT wire-able until that task lands.
func runUOFlagsDispatch(ctx context.Context, turn *commandTurn, proof *uoRootCurrentProof, inv uoRootFlagsInvocation) (out uoRootFlagsResult, floor int64, err error) {
	defer func() {
		v, e := finishCommand(ctx, ctx, uoRootFlagsPublished{out, floor}, err)
		out, floor, err = v.out, v.floor, e
	}()
	lookup, err := lookupUORootFlags(ctx, turn, proof, inv.reference)
	if err != nil {
		return withdrawUOFlags(turn, err)
	}
	if inv.ack {
		if inv.input != (uoPersonalFlagsInput{}) {
			return withdrawUOFlags(turn, ErrInvalid)
		}
		floor, err = acknowledgeUORootFlags(ctx, turn, proof, lookup, inv.nowMS)
	} else {
		switch lookup.decision {
		case receiptFresh:
			// UO-03c plugs target viewing authority in HERE, before bind and any
			// source effect: the item->current-library binding plus the current
			// membership/profile effective-library/restriction visibility, and the
			// same proof must guard retained payload disclosure and release.
			if inv.public != inv.input.publicID || inv.nowMS <= 0 {
				return withdrawUOFlags(turn, ErrInvalid)
			}
			if err = bindUOFlagsFreshInput(proof, inv.reference, inv.input); err != nil {
				return withdrawUOFlags(turn, err)
			}
			var effect uoPersonalFlagsEffect
			if effect, err = applyUOPersonalFlags(ctx, turn, inv.input); err == nil {
				// A nonempty personal prefix is rolled back with the whole turn if
				// the root original cannot be saved.
				if _, err = persistUORootFlags(ctx, turn, proof, lookup, inv.public, effect, inv.nowMS); err == nil {
					// The native schema has no personal_change_hint trigger on
					// viewer_revisions; a real change owes exactly one bounded
					// personal.changed hint. No-op, replay and refusal append none.
					if effect.changed {
						err = appendUOFlagsChangedHint(ctx, turn, lookup.binding.identity, inv.input.libraryKey, effect.viewerRevision, inv.nowMS)
					}
					if err == nil {
						out = uoRootFlagsResult{root: inv.reference.root, public: inv.public, effect: effect, appliedMS: inv.nowMS}
					}
				}
			}
		case receiptReplay:
			out, err = restoreUORootFlags(ctx, turn, proof, lookup)
		case receiptRetired:
			// A submission to an ACKed sequence is the retired class, never a
			// Resolve-required loop. No payload is disclosed and nothing is written.
			out = uoRootFlagsResult{root: inv.reference.root, retired: true}
		default:
			out, err = uoRootFlagsResult{}, ErrResolveRequired
		}
	}
	if err != nil {
		return withdrawUOFlags(turn, err)
	}
	// Final current authority/root before the terminal. A release-time denial
	// replaces the body outcome and rolls back the whole prefix.
	if out != (uoRootFlagsResult{}) {
		decision := receiptFresh
		if lookup.decision == receiptReplay || lookup.decision == receiptRetired {
			decision = receiptReplay
		}
		if err = uoFlagsRelease(ctx, turn, proof, inv.reference.root, decision); err != nil {
			return withdrawUOFlags(turn, err)
		}
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return withdrawUOFlags(turn, ctxErr)
	}
	release, releaseErr := finishCommand(ctx, commandLifetime(turn), uoRootFlagsPublished{out, floor}, nil)
	if releaseErr != nil {
		return withdrawUOFlags(turn, releaseErr)
	}
	out, floor = release.out, release.floor
	if rowsErr := turn.closeRows(); rowsErr != nil {
		return withdrawUOFlags(turn, rowsErr)
	}
	physical, commitErr := turn.transaction.commit()
	switch physical.state {
	case committed:
		// finishCommand's release rule: no error, including a late cancel or a
		// committed safety overrun, may carry output from the private frame.
		if commitErr != nil {
			return uoRootFlagsResult{}, 0, commitErr
		}
		return out, floor, nil
	case commitUnknown:
		_, _ = turn.transaction.rollback(commitErr)
		return uoRootFlagsResult{}, 0, errors.Join(ErrResolveRequired, commitErr)
	default:
		_, _ = turn.transaction.rollback(commitErr)
		if commitErr == nil {
			commitErr = ErrResolveRequired
		}
		return uoRootFlagsResult{}, 0, commitErr
	}
}

func withdrawUOFlags(turn *commandTurn, err error) (uoRootFlagsResult, int64, error) {
	if turn != nil && turn.transaction != nil {
		_, _ = turn.transaction.rollback(err)
	}
	return uoRootFlagsResult{}, 0, err
}
