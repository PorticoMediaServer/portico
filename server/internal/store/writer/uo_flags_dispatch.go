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
	"unicode/utf8"

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
// at release. Purpose 600 uses the ACTIVE-ONLY shared uoRootReclaimRelease (its
// uoFlagsRelease gate re-reads active as the last check) and NEVER the separate
// 654 draining maintenance release: a Fresh flags patch must end on an active
// root, while an authorized replay or retired disclosure may end draining. This
// file mints no developer token, invents no second issuer and accepts no caller
// bool.
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

// uoFlagsTargetProof is the typed result of the same-frame viewing-authority
// proof. It carries the entity/library binding and the membership, profile and
// restriction revisions that authorized it. The dispatcher consumes it before
// Fresh writes, before any retained original is disclosed on replay and again in
// the release recheck; a proof from a different frame, a different target or a
// drifted policy never matches.
type uoFlagsTargetProof struct {
	turn                                                     *commandTurn
	public                                                   [16]byte
	entityID, libraryID                                      int64
	libraryKey                                               string
	entityRevision, admissionRevision                        int64
	membershipRevision, profileRevision, restrictionRevision int64
}

// uoFlagsTargetRow is one public item resolved to its CURRENT entity/library
// row. It is the writer-owned copy of the native read shape, not a second
// catalog service.
type uoFlagsTargetRow struct {
	entityID, libraryID               int64
	kind                              int64
	kindName, libraryKey              string
	playable                          bool
	entityRevision, admissionRevision int64
	ownAge                            sql.NullInt64
}

type uoFlagsViewerFacts struct {
	membershipRevision, profileRevision, restrictionRevision int64
}

// proveUOFlagsTargetVisible is UO-03c's exact target proof for a Fresh patch. It
// reads, in the SAME command frame as the write, the public item -> current
// entity/library row, the current membership/profile library grants and the
// profile restrictions, and requires every one of them to still match the
// validated proof identity and the caller's prepared binding (subject/library/
// revisions). The legacy PUT personal-state path got the library from
// LibraryForItem (playable=1, catalog_libraries.retired=0), re-read the item's
// current library and refused a moved title, then required the viewer's
// membership/profile library access (recordingaccess.directLibraryAllowed) plus
// the catalogue restriction predicate. Wrong, hidden, retired, moved, disabled
// or drifted targets all return the same disclosure-denied class before the
// dispatcher writes anything.
func proveUOFlagsTargetVisible(ctx context.Context, turn *commandTurn, proof *uoRootCurrentProof, in uoPersonalFlagsInput) (out uoFlagsTargetProof, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if proof == nil {
		return out, ErrDisclosureDenied
	}
	if err = in.validate(); err != nil {
		return out, err
	}
	// The personal owner key is derived from the proven actor, never trusted
	// from the caller. This is the #1375 rule applied to the visibility proof.
	if in.profileKey != uoFlagsPersonalOwnerKey(uoFlagsNativeAuthority, proof.identity.account, proof.identity.profile) {
		return out, ErrDisclosureDenied
	}
	if _, err = uoRootProofCurrent(proof, turn); err != nil {
		return out, err
	}
	target, err := readUOFlagsTargetRow(ctx, turn, in.publicID)
	if err != nil {
		return out, err
	}
	// The request's prepared binding must be the item's CURRENT binding. A moved
	// title, a changed source revision or a different admission publication is
	// a refusal, never an invitation to write the new row under the old request.
	if target.entityID != in.subjectID || target.libraryID != in.libraryID || target.libraryKey != in.libraryKey || target.entityRevision != in.entityRevision || target.admissionRevision != in.admissionRevision || !target.playable {
		return out, ErrDisclosureDenied
	}
	viewer, err := validateUOFlagsViewer(ctx, turn, proof.identity, target)
	if err != nil {
		return out, err
	}
	return uoFlagsTargetProof{turn: turn, public: in.publicID, entityID: target.entityID, libraryID: target.libraryID, libraryKey: target.libraryKey, entityRevision: target.entityRevision, admissionRevision: target.admissionRevision, membershipRevision: viewer.membershipRevision, profileRevision: viewer.profileRevision, restrictionRevision: viewer.restrictionRevision}, nil
}

// proveUOFlagsPublicVisible is the replay/retired-side proof: the retained
// original names only the public item id, so this re-reads the item's CURRENT
// library/admission and the viewer's CURRENT membership/profile access before
// any saved payload is disclosed. Losing access after the original is a
// disclosure denial even though the original itself is immutable.
func proveUOFlagsPublicVisible(ctx context.Context, turn *commandTurn, proof *uoRootCurrentProof, public [16]byte) (out uoFlagsTargetProof, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if proof == nil || public == ([16]byte{}) {
		return out, ErrDisclosureDenied
	}
	if _, err = uoRootProofCurrent(proof, turn); err != nil {
		return out, err
	}
	target, err := readUOFlagsTargetRow(ctx, turn, public)
	if err != nil {
		return out, err
	}
	if !target.playable {
		return out, ErrDisclosureDenied
	}
	viewer, err := validateUOFlagsViewer(ctx, turn, proof.identity, target)
	if err != nil {
		return out, err
	}
	return uoFlagsTargetProof{turn: turn, public: public, entityID: target.entityID, libraryID: target.libraryID, libraryKey: target.libraryKey, entityRevision: target.entityRevision, admissionRevision: target.admissionRevision, membershipRevision: viewer.membershipRevision, profileRevision: viewer.profileRevision, restrictionRevision: viewer.restrictionRevision}, nil
}

// readUOFlagsTargetRow copies only the exact current item/library point. A miss,
// a retired item or a retired library is the same denial class as a hidden one.
func readUOFlagsTargetRow(ctx context.Context, turn *commandTurn, public [16]byte) (out uoFlagsTargetRow, err error) {
	if public == ([16]byte{}) {
		return out, ErrDisclosureDenied
	}
	row, err := wc008DraftQuery(ctx, turn, uoFlagsTargetEntity, public[:])
	if err != nil {
		return out, err
	}
	var playable, retired, libraryRetired int64
	err = row.Scan(&out.entityID, &out.libraryID, &out.libraryKey, &out.kind, &out.kindName, &playable, &retired, &out.entityRevision, &out.admissionRevision, &out.ownAge, &libraryRetired)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrDisclosureDenied
	}
	if err != nil {
		return out, err
	}
	if out.entityID <= 0 || out.libraryID <= 0 || out.libraryKey == "" || out.kindName == "" || out.entityRevision <= 0 || out.admissionRevision <= 0 || (playable != 0 && playable != 1) || (retired != 0 && retired != 1) || (libraryRetired != 0 && libraryRetired != 1) || retired != 0 || libraryRetired != 0 {
		return out, ErrDisclosureDenied
	}
	out.playable = playable == 1
	return out, nil
}

// validateUOFlagsViewer is the current membership/profile fence plus admission.
// It follows the native read principal fence and admittedItem: disabled member
// or deleted profile, a superseded membership incarnation/profile lifetime, a
// library outside the member's grants or the profile's narrowed set, any stored
// blocked label (that predicate is not represented natively; refuse rather than
// fail open), and the age/unrated admission of the item's own rating.
//
// A stored access_limits document is refused too: native reads do not yet
// represent the account's narrowed ceiling/denied labels, and this proof must not
// silently ignore them.
func validateUOFlagsViewer(ctx context.Context, turn *commandTurn, identity uoRootIdentity, target uoFlagsTargetRow) (out uoFlagsViewerFacts, err error) {
	row, err := wc008DraftQuery(ctx, turn, uoFlagsTargetPrincipal, identity.account, identity.profile)
	if err != nil {
		return out, err
	}
	var role, memberLibraries, memberIncarnation, profileLifetime string
	var membershipRevision, profileRevision int64
	var profileLibraries sql.NullString
	var disabled, deleted int64
	err = row.Scan(&role, &disabled, &memberLibraries, &memberIncarnation, &membershipRevision, &profileLibraries, &deleted, &profileLifetime, &profileRevision)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrDisclosureDenied
	}
	if err != nil {
		return out, err
	}
	if disabled != 0 || deleted != 0 || membershipRevision <= 0 || profileRevision <= 0 || !utf8.ValidString(memberIncarnation) || !utf8.ValidString(profileLifetime) {
		return out, ErrDisclosureDenied
	}
	if role != "owner" && role != "admin" && role != "member" {
		return out, ErrDisclosureDenied
	}
	// The validated root carries the exact membership incarnation/profile
	// lifetime. A superseded pair is a different viewer, not the saved identity.
	if memberIncarnation != identity.memberIncarnation || profileLifetime != identity.profileLifetime {
		return out, ErrDisclosureDenied
	}
	var memberAllowed []string
	if !utf8.ValidString(memberLibraries) {
		return out, ErrDisclosureDenied
	}
	if err = json.Unmarshal([]byte(memberLibraries), &memberAllowed); err != nil || memberAllowed == nil || len(memberAllowed) > 100 || !uoFlagsContains(memberAllowed, target.libraryKey) && role != "owner" && role != "admin" {
		return out, ErrDisclosureDenied
	}
	if profileLibraries.Valid {
		var profileAllowed []string
		if !utf8.ValidString(profileLibraries.String) {
			return out, ErrDisclosureDenied
		}
		if err = json.Unmarshal([]byte(profileLibraries.String), &profileAllowed); err != nil || profileAllowed == nil || len(profileAllowed) > 100 || !uoFlagsContains(profileAllowed, target.libraryKey) {
			return out, ErrDisclosureDenied
		}
	}
	// The native read fence refuses an account with a stored limits document;
	// the writer must not treat the missing native predicate as permission.
	row, err = wc008DraftQuery(ctx, turn, uoFlagsTargetAccessLimits, identity.account)
	if err != nil {
		return out, err
	}
	var limitsAccount string
	switch err = row.Scan(&limitsAccount); {
	case err == nil:
		return out, ErrDisclosureDenied
	case !errors.Is(err, sql.ErrNoRows):
		return out, err
	}
	maxAge, allowUnrated, blockedRaw, restrictionRevision := int64(-1), int64(1), "[]", int64(0)
	row, err = wc008DraftQuery(ctx, turn, uoFlagsTargetRestrictions, identity.profile)
	if err != nil {
		return out, err
	}
	switch err = row.Scan(&maxAge, &allowUnrated, &blockedRaw, &restrictionRevision); {
	case err == nil:
	case errors.Is(err, sql.ErrNoRows):
		maxAge, allowUnrated, blockedRaw, restrictionRevision = -1, 1, "[]", 0
	default:
		return out, err
	}
	if maxAge < -1 || (allowUnrated != 0 && allowUnrated != 1) || restrictionRevision < 0 || !utf8.ValidString(blockedRaw) {
		return out, ErrDisclosureDenied
	}
	var labels []string
	if err = json.Unmarshal([]byte(blockedRaw), &labels); err != nil || labels == nil || len(labels) > 64 {
		return out, ErrDisclosureDenied
	}
	for _, label := range labels {
		if len(label) == 0 || len(label) > 120 {
			return out, ErrDisclosureDenied
		}
	}
	if len(labels) != 0 {
		return out, ErrDisclosureDenied
	}
	// admittedItem: a missing own age is unrated; otherwise the profile's
	// ceiling (negative means no ceiling) admits only children at or below it.
	if !target.ownAge.Valid {
		if allowUnrated == 0 {
			return out, ErrDisclosureDenied
		}
	} else if target.ownAge.Int64 < 0 || maxAge >= 0 && target.ownAge.Int64 > maxAge {
		return out, ErrDisclosureDenied
	}
	return uoFlagsViewerFacts{membershipRevision: membershipRevision, profileRevision: profileRevision, restrictionRevision: restrictionRevision}, nil
}

func uoFlagsContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// appendUOFlagsChangedHint appends the ONE personal.changed hint a real flag
// change owes. The native schema has no personal_change_hint trigger, so this
// dispatcher owns the bounded append (see the assignment in
// uo_personal_flags.go). apievents.Flush derives this profile audience from the
// legacy "personal:"||owner hint, and the legacy viewer_revisions hint names
// the library with the viewer revision, so those exact fields are reused. No
// titles field means "any title in this library", the documented fallback; the
// scalar patch never claims a partial title path.
//
// KNOWN DEVIATION: this hint hardcodes "observation":"0", while the legacy
// viewer_revisions hint emitted the head's observation_revision. The saved
// statement/golden intentionally stay unchanged here; the correct field is
// tracked for the post-D statement change and must not be altered in this
// commit.
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

// uoFlagsRelease is this purpose's final gate before the terminal. Purpose 600
// uses the ACTIVE-ONLY shared uoRootReclaimRelease and never the separate 654
// draining maintenance release: a Fresh flags patch must end on an ACTIVE root,
// while an authorized replay or retired disclosure may end draining. It calls
// the shared release first so a missing/foreign root, a stale incarnation or a
// finished frame still fails with the shared class, then re-reads an active
// state as the last check so the flags purpose keeps its own rule independently
// of the 654 maintenance path.
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
// UO-03c supplies the viewing-authority half: the item->current-library binding
// and the current membership/profile effective-library/restriction visibility
// are proved in this same frame before any Fresh write, before a retained
// original is disclosed and again immediately before release. The public
// adapter is still HELD on the missing complete result image above.
func runUOFlagsDispatch(ctx context.Context, turn *commandTurn, proof *uoRootCurrentProof, inv uoRootFlagsInvocation) (out uoRootFlagsResult, floor int64, err error) {
	defer func() {
		v, e := finishCommand(ctx, ctx, uoRootFlagsPublished{out, floor}, err)
		out, floor, err = v.out, v.floor, e
	}()
	lookup, err := lookupUORootFlags(ctx, turn, proof, inv.reference)
	if err != nil {
		return withdrawUOFlags(turn, err)
	}
	var targetProof uoFlagsTargetProof
	var targetProven bool
	if inv.ack {
		if inv.input != (uoPersonalFlagsInput{}) {
			return withdrawUOFlags(turn, ErrInvalid)
		}
		floor, err = acknowledgeUORootFlags(ctx, turn, proof, lookup, inv.nowMS)
	} else {
		switch lookup.decision {
		case receiptFresh:
			// UO-03c: the item->current-library binding plus the current
			// membership/profile effective-library/restriction visibility must be
			// proved in this frame BEFORE the owner bind, source effect and receipt
			// insert. The same typed proof is re-checked immediately before release.
			if inv.public != inv.input.publicID || inv.nowMS <= 0 {
				return withdrawUOFlags(turn, ErrInvalid)
			}
			if err = bindUOFlagsFreshInput(proof, inv.reference, inv.input); err != nil {
				return withdrawUOFlags(turn, err)
			}
			if targetProof, err = proveUOFlagsTargetVisible(ctx, turn, proof, inv.input); err != nil {
				return withdrawUOFlags(turn, err)
			}
			targetProven = true
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
			// A retained original is disclosed only to a viewer who may STILL see
			// its item. Prove the public target first when the retry custody names
			// it; restore proves again against the decoded original before it
			// returns anything.
			if inv.public != ([16]byte{}) {
				if targetProof, err = proveUOFlagsPublicVisible(ctx, turn, proof, inv.public); err != nil {
					return withdrawUOFlags(turn, err)
				}
				targetProven = true
			}
			out, err = restoreUORootFlags(ctx, turn, proof, lookup, inv.public)
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
		// UO-03c final recheck: a policy/role/membership/library change after the
		// first proof and before this release must refuse the whole prefix. A
		// retired answer has no payload and no target proof, and Fresh no-ops still
		// carry their retained original, so only Fresh and replay are re-proved.
		if lookup.decision == receiptFresh {
			if !targetProven {
				return withdrawUOFlags(turn, ErrDisclosureDenied)
			}
			fresh, proofErr := proveUOFlagsTargetVisible(ctx, turn, proof, inv.input)
			if proofErr != nil || fresh != targetProof {
				return withdrawUOFlags(turn, ErrDisclosureDenied)
			}
		} else if lookup.decision == receiptReplay && out.public != ([16]byte{}) {
			fresh, proofErr := proveUOFlagsPublicVisible(ctx, turn, proof, out.public)
			if proofErr != nil {
				return withdrawUOFlags(turn, proofErr)
			}
			if targetProven && fresh != targetProof {
				return withdrawUOFlags(turn, ErrDisclosureDenied)
			}
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
