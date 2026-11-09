package writer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"math"
	"unicode/utf8"
)

// uoRootFlagsResult is the INERT scalar primitive currently saved for native
// purpose 600. It carries only native facts captured in the SAME freshly
// authorized turn: the acting profile's public subject identity, all three
// sparse flags, the nullable rating (a present zero stays present), the actual
// personal revision, the viewer and selection heads and the applied time.
//
// This is NOT the completed public 600 result. The public original must be the
// complete catalog.PersonalReceipt (used at detail.go:189, defined at
// detail_types.go:162): its identity, library, viewer fence and receipt
// lifetime plus Personal catalog.PersonalState (ContinueDismissed,
// watched/progressSeconds/lastPlayedAt/status/conflicts, all flags, the
// nullable rating and revision) and optional Current, loaded post-effect in the
// SAME frame. A replay may not synthesize a stub or read mutable progress. The
// public adapter stays HELD until that storage/image is adopted; nothing here is
// a wire result, current authority or a public input shape.
type uoRootFlagsResult struct {
	root      string
	public    [16]byte
	effect    uoPersonalFlagsEffect
	appliedMS int64
	// retired marks an ACKed sequence answered as the retired class. It is
	// never encoded into a receipt original.
	retired bool
}

func preflightUORootFlags(v uoRootFlagsResult) error {
	if v.retired {
		return ErrInvalid
	}
	s, e := v.effect.state, v.effect
	if len(v.root) != 22 || !utf8.ValidString(v.root) || v.public == [16]byte{} || v.appliedMS <= 0 || s.revision < 0 || s.rating.Valid && (s.rating.Int64 < 0 || s.rating.Int64 > 10) || !s.rating.Valid && s.rating.Int64 != 0 || e.viewerRevision < 0 || e.selectionRevision < 0 || e.changed && (s.revision == 0 || e.viewerRevision == 0 || e.selectionRevision == 0) || !e.changed && (e.viewerRevision != 0 || e.selectionRevision != 0) {
		return ErrInvalid
	}
	return nil
}

func writeUORootFlags(e *requestEncoder, i CommandIdentity, q RequestDigest, v uoRootFlagsResult) error {
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1}) // semantic result version, independent of CommandID ordinals
	e.text("uo_set_personal_flags")
	e.text("root_flags")
	e.text(v.root)
	e.bytes(i.Incarnation[:])
	e.number(uint64(i.Sequence))
	e.bytes(i.OperationID[:])
	e.bytes(q[:])
	e.bytes(v.public[:])
	e.number(uint64(v.effect.state.revision))
	e.flag(v.effect.state.watchlisted)
	e.flag(v.effect.state.favorite)
	e.flag(v.effect.state.notInterested)
	e.flag(v.effect.state.rating.Valid)
	e.number(uint64(v.effect.state.rating.Int64))
	e.flag(v.effect.changed)
	e.number(uint64(v.effect.viewerRevision))
	e.number(uint64(v.effect.selectionRevision))
	e.number(uint64(v.appliedMS))
	return e.err
}

// The retained original is decoded from the immutable receipt only. The exact
// saved bytes must re-encode to the same canonical digest, so a foreign purpose,
// root, identity, request or a noncanonical boolean can never be relabeled as
// this result.
func decodeUORootFlags(i CommandIdentity, q RequestDigest, root string, p []byte, want ResultDigest) (uoRootFlagsResult, error) {
	var zero uoRootFlagsResult
	if len(p) < 61 || len(p) > uoRootPayloadCap || ResultDigest(sha256.Sum256(p)) != want {
		return zero, ErrResolveRequired
	}
	tail := p[len(p)-61:]
	v := uoRootFlagsResult{root: root}
	copy(v.public[:], tail[:16])
	for _, bit := range []byte{tail[24], tail[25], tail[26], tail[27], tail[36]} {
		if bit > 1 {
			return zero, ErrResolveRequired
		}
	}
	v.effect.state.watchlisted, v.effect.state.favorite, v.effect.state.notInterested, v.effect.state.rating.Valid, v.effect.changed = tail[24] == 1, tail[25] == 1, tail[26] == 1, tail[27] == 1, tail[36] == 1
	for _, field := range []struct {
		offset int
		dst    *int64
	}{{16, &v.effect.state.revision}, {28, &v.effect.state.rating.Int64}, {37, &v.effect.viewerRevision}, {45, &v.effect.selectionRevision}, {53, &v.appliedMS}} {
		n := binary.BigEndian.Uint64(tail[field.offset : field.offset+8])
		if n > uint64(^uint64(0)>>1) {
			return zero, ErrResolveRequired
		}
		*field.dst = int64(n)
	}
	encoded, sum, err := encodeUORootCanonical(i, q, v)
	if err != nil || sum != want || !bytes.Equal(p, encoded) {
		return zero, ErrResolveRequired
	}
	return v, nil
}

// persistUORootFlags saves the complete 600 original on the root receipt PK and
// advances the SAME owner debt/revision. The generic UO-01 persist path is not
// usable here: its binding.check refuses every NORMAL frame until Sol's
// current-session mint lands, so this purpose keeps the identical insert and
// owner-advance skeleton locally and presents the private same-turn proof before
// and after the effects instead. No caller bool, cached role or descriptor can
// stand in for that proof.
func persistUORootFlags(ctx context.Context, turn *commandTurn, proof *uoRootCurrentProof, lookup uoRootLookup, public [16]byte, effect uoPersonalFlagsEffect, appliedMS int64) (out ResultDigest, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	q, o := lookup.reference, lookup.owner
	if lookup.turn != turn || lookup.binding == nil || !q.validFor(turn) || lookup.decision != receiptFresh || o.revision == math.MaxInt64 || o.rows == math.MaxInt64 {
		return out, ErrResolveRequired
	}
	binding, err := uoRootProofCurrent(proof, turn)
	if err != nil {
		return out, err
	}
	rootState, err := binding.root(ctx, turn, q.root)
	if err != nil {
		return out, err
	}
	// A draining root never admits a Fresh effect; persistence rechecks the
	// root's state at the moment of the effect.
	if rootState != "active" {
		return out, ErrUORootGone
	}
	if decision, e := classifyReceipt(receiptIdentity{command: q.identity, digest: q.request}, q.kind, o.facts, receiptFacts{}); e != nil || decision != receiptFresh {
		return out, ErrResolveRequired
	}
	v := uoRootFlagsResult{root: q.root, public: public, effect: effect, appliedMS: appliedMS}
	if err = preflightUORootFlags(v); err != nil {
		return out, err
	}
	payload, digest, err := encodeUORootCanonical(q.identity, q.request, v)
	if err != nil {
		return out, err
	}
	complete := int64(len(payload)) + uoRootFixedBytes
	if complete > math.MaxInt64-o.variableBytes {
		return out, ErrInvalid
	}
	actual, err := wc008DraftInteger(ctx, turn, uoRootReceiptInsert, q.root, q.identity.Incarnation[:], q.identity.Sequence, q.identity.OperationID[:], int64(q.kind), q.request[:], int64(1), int64(0), digest[:], payload, appliedMS)
	if err != nil {
		return out, uoRootInsertError(err)
	}
	if actual != complete {
		return out, ErrResolveRequired
	}
	advance, args := uoRootOwnerAdvanceNull, []any{q.identity.Sequence, o.rows + 1, o.variableBytes + actual, o.revision + 1, q.root, q.identity.Incarnation[:], o.facts.lastSequence, o.facts.acknowledgedSequence, o.revision, o.rows, o.variableBytes, o.afterSequence}
	if o.due.Valid {
		// Closed non-NULL/NULL variants, never IS ? or a sentinel.
		advance = uoRootOwnerAdvance
		args = append(args, o.due.Int64)
	}
	if _, err = wc008DraftInteger(ctx, turn, advance, args...); err != nil {
		return out, err
	}
	// A revocation or root change during the effects cannot return a digest
	// that looks Applied. 600 keeps the domain rule: a Fresh patch must end on
	// an active root, not the 654 maintenance exception.
	if err = uoFlagsRelease(ctx, turn, proof, q.root, receiptFresh); err != nil {
		return out, err
	}
	return digest, turn.checkLifetimes(ctx)
}

// restoreUORootFlags returns the retained ORIGINAL with zero source, revision,
// event or outbox effects. It never re-runs the patch and never reconstructs the
// answer from mutable catalog state. Current VIEWING authority is different from
// those effects: before the payload leaves this function (and, when the caller
// knows the public target, before it is even copied) the item's current library
// and admission plus the viewer's current membership/profile access are proved,
// so a viewer who lost access gets a refusal with no payload. public is the
// retry custody's target when it has one; zero means the decoded original names
// the target, which is still proved before return.
//
// expectedLibrary, when nonempty, is the library from the retry's complete
// validated input. The original is sound where it was applied, but a later MOVE
// means the item is no longer the target that request named, so the replay
// refuses. A zero-input retry has no library to compare and cannot detect the
// move; the held complete result must carry the library for that case.
func restoreUORootFlags(ctx context.Context, turn *commandTurn, proof *uoRootCurrentProof, lookup uoRootLookup, public [16]byte, expectedLibrary string) (out uoRootFlagsResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	q := lookup.reference
	if lookup.turn != turn || q.kind != SetPersonalFlags || lookup.decision != receiptReplay {
		return out, ErrInvalid
	}
	if _, err = uoRootProofCurrent(proof, turn); err != nil {
		return out, err
	}
	var current uoFlagsTargetProof
	if public != ([16]byte{}) {
		if current, err = proveUOFlagsPublicVisible(ctx, turn, proof, public); err != nil {
			return out, err
		}
	}
	payload, digest, err := readUORootFlagsPayload(ctx, turn, proof, lookup)
	if err != nil {
		return out, err
	}
	result, err := decodeUORootFlags(q.identity, q.request, q.root, payload, digest)
	if err != nil {
		return uoRootFlagsResult{}, err
	}
	if lookup.resultCount != 0 || result.appliedMS != lookup.appliedMS {
		return uoRootFlagsResult{}, ErrResolveRequired
	}
	// The original names its own target. A caller-supplied target that differs
	// cannot unlock this receipt; without one, prove the decoded target now and
	// still before the result is returned.
	if public != ([16]byte{}) {
		if result.public != public {
			return uoRootFlagsResult{}, ErrDisclosureDenied
		}
	} else if current, err = proveUOFlagsPublicVisible(ctx, turn, proof, result.public); err != nil {
		return uoRootFlagsResult{}, err
	}
	if expectedLibrary != "" && current.libraryKey != expectedLibrary {
		return uoRootFlagsResult{}, ErrDisclosureDenied
	}
	if err = uoRootReclaimRelease(ctx, turn, proof, q.root, receiptReplay); err != nil {
		return uoRootFlagsResult{}, err
	}
	return result, turn.checkLifetimes(ctx)
}

// verifyUORootFlagsOriginal validates the retained 600 original for ACK without
// re-proving target visibility. ACK discloses no payload and performs no effect:
// it only moves the floor, so requiring the item to remain visible would wedge
// the root forever after an item is retired or hidden (every later Fresh
// sequence would then answer Resolve-required). Root identity, the current
// proof, the receipt header and the canonical original bytes are still proved
// here; only the viewing-authority half is omitted.
func verifyUORootFlagsOriginal(ctx context.Context, turn *commandTurn, proof *uoRootCurrentProof, lookup uoRootLookup) (out uoRootFlagsResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	q := lookup.reference
	if lookup.turn != turn || q.kind != SetPersonalFlags || lookup.decision != receiptReplay {
		return out, ErrInvalid
	}
	if _, err = uoRootProofCurrent(proof, turn); err != nil {
		return out, err
	}
	payload, digest, err := readUORootFlagsPayload(ctx, turn, proof, lookup)
	if err != nil {
		return out, err
	}
	result, err := decodeUORootFlags(q.identity, q.request, q.root, payload, digest)
	if err != nil {
		return uoRootFlagsResult{}, err
	}
	if lookup.resultCount != 0 || result.appliedMS != lookup.appliedMS {
		return uoRootFlagsResult{}, ErrResolveRequired
	}
	return result, nil
}

// acknowledgeUORootFlags moves only the root ACK floor. A replay first proves
// the complete typed original; a retired sequence is answered by the shared
// wrapper, whose generic restore is only needed for a replayable original.
// ACK never refunds the retained bytes.
func acknowledgeUORootFlags(ctx context.Context, turn *commandTurn, proof *uoRootCurrentProof, lookup uoRootLookup, nowMS int64) (floor int64, err error) {
	defer func() { floor, err = finishCommand(ctx, commandLifetime(turn), floor, err) }()
	if lookup.reference.kind != SetPersonalFlags {
		return 0, ErrInvalid
	}
	switch lookup.decision {
	case receiptReplay:
		// UO-01's acknowledgeUORootOperation validates a replay through its
		// GENERIC uoRootOperationResult decoder, so it cannot accept purpose
		// 600's typed original. Verify that original here and reuse the shared
		// purpose-agnostic floor body. This checks root/receipt identity only:
		// ACK discloses nothing, so target visibility must not gate it.
		if _, err := verifyUORootFlagsOriginal(ctx, turn, proof, lookup); err != nil {
			return 0, err
		}
		return acknowledgeUORootVerified(ctx, turn, proof, lookup, nowMS)
	case receiptRetired:
		// A repeat ACK needs no restore and must return the already durable floor
		// with zero writes. Call the scalar Verified body directly, the same shape
		// acknowledgeUORootReclaim uses; acknowledgeUORootOperation adds nothing
		// for the retired arm.
		return acknowledgeUORootVerified(ctx, turn, proof, lookup, nowMS)
	default:
		return 0, ErrResolveRequired
	}
}

// readUORootFlagsPayload copies only the exact receipt PK row after current
// authority and the native root are re-proved. The header length predicate
// precedes the row copy and the returned digest is the stored one.
func readUORootFlagsPayload(ctx context.Context, turn *commandTurn, proof *uoRootCurrentProof, lookup uoRootLookup) (payload []byte, digest ResultDigest, err error) {
	defer func() {
		v, e := finishCommand(ctx, commandLifetime(turn), struct {
			p []byte
			d ResultDigest
		}{payload, digest}, err)
		payload, digest, err = v.p, v.d, e
	}()
	if lookup.turn != turn || lookup.binding == nil || lookup.decision != receiptReplay || lookup.payloadBytes < 1 || lookup.payloadBytes > uoRootPayloadCap || !lookup.reference.validFor(turn) {
		return nil, ResultDigest{}, ErrResolveRequired
	}
	binding, err := uoRootProofCurrent(proof, turn)
	if err != nil {
		return nil, ResultDigest{}, err
	}
	q := lookup.reference
	if _, err = binding.root(ctx, turn, q.root); err != nil {
		return nil, ResultDigest{}, err
	}
	row, err := wc008DraftQuery(ctx, turn, uoRootReceiptPayload, q.root, q.identity.Incarnation[:], q.identity.Sequence, q.identity.OperationID[:], int64(q.kind), q.request[:], lookup.payloadBytes)
	if err != nil {
		return nil, ResultDigest{}, err
	}
	var stored []byte
	if err = row.Scan(&payload, &stored); errors.Is(err, sql.ErrNoRows) {
		return nil, ResultDigest{}, ErrResolveRequired
	} else if err != nil {
		return nil, ResultDigest{}, err
	}
	if int64(len(payload)) != lookup.payloadBytes || len(stored) != 32 {
		return nil, ResultDigest{}, ErrResolveRequired
	}
	copy(digest[:], stored)
	// The payload is not released while current authority or the root is stale.
	if _, err = binding.root(ctx, turn, q.root); err != nil {
		return nil, ResultDigest{}, err
	}
	if err = uoRootReclaimRelease(ctx, turn, proof, q.root, receiptReplay); err != nil {
		return nil, ResultDigest{}, err
	}
	return payload, digest, turn.checkLifetimes(ctx)
}
