package writer

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/artwork"
)

// 225 body. Publish is the point where the immutable installed material becomes
// an accepted artwork source. Current owner authority precedes historical
// receipt disclosure; the ORIGINAL saved result is served without any fresh
// source/lease/policy probe. A fresh command then re-proves the exact retained
// subject job, accepted artwork source, resolved language and policy, the
// installed intent and its COMPLETE slot manifest before it writes any blob,
// source or receipt.
//
// Every file is verified by the caller before this turn; nothing here touches
// the filesystem, a provider or the network. The request repeats the verified
// installed manifest, and the writer refuses any difference from the manifest
// IP-19 recorded, so a missing or changed file cannot be published.

const ipArtworkMaterialPublishRevision int64 = 3

// The source facts are re-declared by the caller and re-proved against the
// installed intent; they are never copied from an earlier provider response.
type ipArtworkMaterialPublishSpec struct {
	intentID, sourceDigest, acceptedMIME string
	// language/policyRevision are current-value witnesses re-derived by the
	// writer from settings, exactly as IP-19 does.
	language                        string
	policyRevision                  int64
	acceptedWidth, acceptedHeight   int64
	largeEdge, encoderRevision      int64
	expectedSlots, intentGeneration int64
	intentRevision                  int64
}

// The complete installed manifest as the caller verified it on disk. Slot 3 is
// the retained original upload when four slots were declared.
type ipArtworkMaterialPublishRequest struct {
	owner ipArtworkMaterialOwner
	spec  ipArtworkMaterialPublishSpec
	slots []ipArtworkMaterialSlot
}

// The saved result is the published intent head only: the blobs and source stay
// in their own tables and are never relabeled as a proof.
type ipArtworkMaterialPublishResult struct {
	IntentID      string
	Revision      int64
	Slots, Bytes  int64
	JobGeneration int64
}

type ipArtworkMaterialPublishCommand struct {
	identity CommandIdentity
	request  ipArtworkMaterialPublishRequest
	digest   RequestDigest
	valid    bool
}

type ipArtworkMaterialPublishCommandResult struct {
	Outcome ReceiptOutcome
	Value   ipArtworkMaterialPublishResult
	Digest  ResultDigest
}

type ipArtworkMaterialPublishOutcome struct {
	decision receiptDecision
	result   ipArtworkMaterialPublishResult
	digest   ResultDigest
}

type ipArtworkMaterialPublishSavedResult struct {
	Kind  CommandID
	Value ipArtworkMaterialPublishResult
}

func (c *ipArtworkMaterialPublishCommand) reference() ReceiptReference {
	return ReceiptReference{Identity: c.identity, RequestDigest: c.digest}
}

// The retained request must be bounded before any digest or hold. The original
// namespace rule (exactly one of entity/person) and the lease witnesses are the
// same ones IP-19 uses; a malformed manifest is refused whole, never truncated.
func ipArtworkMaterialPublishRequestShape(identity CommandIdentity, q ipArtworkMaterialPublishRequest) error {
	if !ipPolicyCommandIdentityValid(identity) {
		return ErrInvalid
	}
	o, s := q.owner, q.spec
	if o.id <= 0 || o.incarnation != identity.Incarnation || (o.entityID > 0) == (o.personID > 0) ||
		o.entityID < 0 || o.personID < 0 || o.sourceIncarnation == [16]byte{} || o.sourceRevision <= 0 ||
		o.revision <= 0 || o.leaseOwner <= 0 || o.leaseGeneration <= 0 || o.leaseUntilMS <= 0 ||
		!ipArtworkDiscoveryText(s.intentID, ipArtworkMaterialMaxIntent) || s.intentID == "" ||
		!ipArtworkDiscoveryText(s.sourceDigest, 64) || !ipArtworkDiscoveryText(s.language, 64) ||
		s.language == "" || s.policyRevision <= 0 || s.acceptedWidth <= 0 || s.acceptedHeight <= 0 ||
		s.largeEdge != 1280 && s.largeEdge != 1920 && s.largeEdge != 2560 ||
		s.encoderRevision <= 0 || s.expectedSlots != 3 && s.expectedSlots != 4 ||
		s.intentGeneration <= 0 || s.intentRevision != 2 ||
		int64(len(q.slots)) != s.expectedSlots {
		return ErrInvalid
	}
	// One pure preparation validates the declaration and the complete manifest
	// against the accepted source; the writer revalidates the same bytes under
	// its own hold against the installed rows.
	prepared, err := artwork.PrepareMaterial(ipArtworkMaterialPreparedSpec(ipArtworkMaterialSpec{
		intentID: s.intentID, sourceDigest: s.sourceDigest, acceptedMIME: s.acceptedMIME,
		acceptedWidth: s.acceptedWidth, acceptedHeight: s.acceptedHeight, largeEdge: s.largeEdge,
		encoderRevision: s.encoderRevision, expectedSlots: s.expectedSlots,
	}), ipArtworkMaterialPreparedSlots(q.slots), artwork.MaterialLimits{
		Slots: artwork.MaterialMaxSlots, TextBytes: 16 << 10, TotalBytes: ipArtworkMaterialMaxBytes,
	})
	if err != nil || !prepared.Valid() {
		return ErrInvalid
	}
	return nil
}

// Called under W preparation custody before any hold. No SQL, no allocation
// beyond the retained request copy and its canonical digest.
func newIPArtworkMaterialPublishCommand(identity CommandIdentity, in ipArtworkMaterialPublishRequest) (*ipArtworkMaterialPublishCommand, error) {
	if err := ipArtworkMaterialPublishRequestShape(identity, in); err != nil {
		return nil, err
	}
	digest, err := canonicalRequestDigest(IPArtworkMaterialPublish, 1, identity, in)
	if err != nil {
		return nil, err
	}
	return &ipArtworkMaterialPublishCommand{identity: identity, request: in, digest: digest, valid: true}, nil
}

// The current authority, historical receipt and original result are resolved
// exactly as IP-19 does, but through THIS command's own receipt statement. The
// frozen E receipt enum has no publish-specific label: 224 already stores
// "material_publish", so a publish row is proved by its selected source digest,
// source revision and revision 3. An IP-19 handoff row at the same key can never
// replay as a publish.
func ipArtworkMaterialPublishLookup(ctx context.Context, turn *commandTurn, q ipArtworkMaterialPublishRequest, identity CommandIdentity, digest RequestDigest) (out ipArtworkMaterialPublishOutcome, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != IPArtworkMaterialPublish {
		return out, ErrInvalid
	}
	rows, err := ipTextRows(ctx, turn, ipAMOwner, q.owner.id)
	if err != nil {
		return out, err
	}
	var owner receiptOwnerFacts
	var incarnation []byte
	if err = ipTextOne(rows, func() error {
		return rows.Scan(&incarnation, &owner.lastSequence, &owner.acknowledgedSequence)
	}); err != nil {
		return out, errors.Join(ErrResolveRequired, ErrOwnerUnavailable, err)
	}
	if len(incarnation) != 16 {
		return out, ErrResolveRequired
	}
	owner.exists = true
	copy(owner.incarnation[:], incarnation)

	rows, err = ipTextRows(ctx, turn, ipAPReceiptOctet, q.owner.id, q.owner.incarnation[:], identity.Sequence)
	if err != nil {
		return out, err
	}
	var intentBytes, selectedBytes, tokenBytes int64
	err = ipTextOne(rows, func() error { return rows.Scan(&intentBytes, &selectedBytes, &tokenBytes) })
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, errors.Join(ErrResolveRequired, err)
	}
	if err == nil && (intentBytes < 1 || intentBytes > ipArtworkMaterialMaxIntent ||
		selectedBytes != 64 || tokenBytes != 0) {
		return out, ErrResolveRequired
	}
	rows, err = ipTextRows(ctx, turn, ipAPReceipt, q.owner.id, q.owner.incarnation[:], identity.Sequence)
	if err != nil {
		return out, err
	}
	var found receiptFacts
	var operation, request, result, token []byte
	var nativeKind, revision, appliedMS, count, bytesTotal, jobGeneration, leaseUntil, storageRevision int64
	var intent, selected string
	var saved ipArtworkMaterialPublishResult
	err = ipTextOne(rows, func() error {
		return rows.Scan(&operation, &request, &result, &nativeKind, &revision, &appliedMS,
			&count, &bytesTotal, &jobGeneration, &leaseUntil, &intent, &selected,
			&storageRevision, &token)
	})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, errors.Join(ErrResolveRequired, err)
	}
	if err == nil {
		if len(operation) != 16 || len(request) != 32 || len(result) != 32 ||
			CommandID(nativeKind) != IPArtworkMaterialPublish ||
			revision != ipArtworkMaterialPublishRevision || appliedMS < 0 ||
			count != q.spec.expectedSlots || count < 3 || count > 4 ||
			bytesTotal != ipArtworkMaterialSlotBytes(q.slots) || bytesTotal < 1 ||
			bytesTotal > ipArtworkMaterialMaxBytes ||
			jobGeneration <= 0 || leaseUntil <= 0 || storageRevision != 1 ||
			len(token) != 0 || selected != q.spec.sourceDigest ||
			len(intent) != int(intentBytes) || intentBytes < 1 || intentBytes > ipArtworkMaterialMaxIntent {
			return out, ErrResolveRequired
		}
		found.exists = true
		found.kind = IPArtworkMaterialPublish
		found.resultCount = count
		actual := receiptIdentity{command: identity}
		copy(actual.command.OperationID[:], operation)
		copy(actual.digest[:], request)
		found.identity = actual
		copy(found.resultDigest[:], result)
		saved.IntentID = intent
		saved.Revision, saved.Slots, saved.Bytes, saved.JobGeneration = revision, count, bytesTotal, jobGeneration
	}
	out.decision, err = classifyReceipt(receiptIdentity{identity, digest}, IPArtworkMaterialPublish, owner, found)
	if err != nil {
		return out, err
	}
	if out.decision != receiptReplay {
		return out, nil
	}
	if saved.IntentID != q.spec.intentID || saved.Slots != q.spec.expectedSlots ||
		saved.Bytes != ipArtworkMaterialSlotBytes(q.slots) ||
		saved.JobGeneration != q.spec.intentGeneration {
		return out, ErrResolveRequired
	}
	expected, e := ipArtworkMaterialPublishResultDigest(identity, digest, saved)
	if e != nil || !bytes.Equal(expected[:], result) {
		return out, errors.Join(ErrResolveRequired, e)
	}
	out.result, out.digest = saved, expected
	return out, nil
}

// The accepted source and current settings are re-derived by IP-19's own fresh
// source check, then the installed intent and its complete raw slot manifest are
// read back and compared with the caller's verified files.
func ipArtworkMaterialPublishFresh(ctx context.Context, turn *commandTurn, q ipArtworkMaterialPublishRequest, nowMS int64) (total int64, err error) {
	defer func() {
		v, e := finishCommand(ctx, commandLifetime(turn), total, err)
		total, err = v, e
	}()
	if err = ipArtworkMaterialFreshSource(ctx, turn, ipArtworkMaterialRequest{
		owner: q.owner,
		spec:  ipArtworkMaterialSpec{language: q.spec.language, policyRevision: q.spec.policyRevision},
	}, nowMS); err != nil {
		return 0, err
	}
	if err = ipArtworkMaterialPublishIntent(ctx, turn, q); err != nil {
		return 0, err
	}
	return ipArtworkMaterialManifest(ctx, turn, q.spec.intentID, q.slots)
}

// The declared intent must still be the exact installed generation: bound to
// this owner and generation, complete, source-faithful and not retiring.
func ipArtworkMaterialPublishIntent(ctx context.Context, turn *commandTurn, q ipArtworkMaterialPublishRequest) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	s := q.spec
	rows, err := ipTextRows(ctx, turn, ipAPIntentOctets, s.intentID, q.owner.id, q.owner.incarnation[:])
	if err != nil {
		return err
	}
	var digestBytes, mimeBytes, stateBytes int64
	if err = ipTextOne(rows, func() error { return rows.Scan(&digestBytes, &mimeBytes, &stateBytes) }); err != nil {
		return errors.Join(ErrConflict, err)
	}
	if digestBytes != 64 || (mimeBytes != 9 && mimeBytes != 10 && mimeBytes != 11) ||
		stateBytes != int64(len("installed")) {
		return ErrConflict
	}
	rows, err = ipTextRows(ctx, turn, ipAPIntent, s.intentID, q.owner.id, q.owner.incarnation[:])
	if err != nil {
		return err
	}
	var generation, revision, expected, staged, width, height, edge, encoder, tokenBytes int64
	var state, sourceDigest, mime string
	if err = ipTextOne(rows, func() error {
		return rows.Scan(&generation, &revision, &state, &expected, &staged, &sourceDigest, &mime,
			&width, &height, &edge, &encoder, &tokenBytes)
	}); err != nil {
		return errors.Join(ErrConflict, err)
	}
	if generation != s.intentGeneration || revision != s.intentRevision || state != "installed" ||
		expected != s.expectedSlots || staged != s.expectedSlots || sourceDigest != s.sourceDigest ||
		mime != s.acceptedMIME || width != s.acceptedWidth || height != s.acceptedHeight ||
		edge != s.largeEdge || encoder != s.encoderRevision || tokenBytes != 0 ||
		int64(len(state)) != stateBytes || int64(len(sourceDigest)) != digestBytes ||
		int64(len(mime)) != mimeBytes {
		return ErrConflict
	}
	return nil
}

func applyIPArtworkMaterialPublish(ctx context.Context, turn *commandTurn, p *usOwnerAuthorityProof, lifetime context.Context, b ipPolicyBudget, c *ipArtworkMaterialPublishCommand) (out ipArtworkMaterialPublishCommandResult, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	var zero ipArtworkMaterialPublishCommandResult
	if c == nil || !c.valid || turn == nil || turn.transaction == nil ||
		turn.transaction.descriptor.ID != IPArtworkMaterialPublish {
		return zero, ErrInvalid
	}
	q := c.request
	if err = ipArtworkMaterialPublishRequestShape(c.identity, q); err != nil {
		return zero, err
	}
	actual, err := canonicalRequestDigest(IPArtworkMaterialPublish, 1, c.identity, q)
	if err != nil || actual != c.digest {
		return zero, errors.Join(ErrConflict, err)
	}
	auth, err := authorizeIPPolicyOwner(ctx, turn, p, lifetime, b)
	if err != nil {
		return zero, err
	}
	lookup, err := ipArtworkMaterialPublishLookup(ctx, turn, q, c.identity, c.digest)
	if err != nil {
		return zero, err
	}
	out.Outcome.Reference = c.reference()
	switch lookup.decision {
	case receiptRetired:
		out.Outcome.State = ResultRetired
		return out, nil
	case receiptReplay:
		out.Outcome.State = Applied
		out.Outcome.Replayed = true
		out.Value, out.Digest = lookup.result, lookup.digest
		return out, nil
	case receiptFresh:
	default:
		return zero, ErrResolveRequired
	}
	if err = auth.ipPolicyAuthorityRecheck(ctx, turn); err != nil {
		return zero, err
	}
	now, err := turn.transaction.clockTime()
	if err != nil {
		return zero, err
	}
	appliedMS := now.UnixMilli()
	if appliedMS < 0 {
		return zero, ErrInvalid
	}
	total, err := ipArtworkMaterialPublishFresh(ctx, turn, q, appliedMS)
	if err != nil {
		return zero, err
	}
	value, err := ipArtworkMaterialPublishBody(ctx, turn, q, appliedMS, total)
	if err != nil {
		return zero, err
	}
	digest, err := ipArtworkMaterialPublishResultDigest(c.identity, c.digest, value)
	if err != nil {
		return zero, err
	}
	if err = ipArtworkMaterialPublishRecord(ctx, turn, q, c.identity, c.digest, digest, value, appliedMS); err != nil {
		return zero, err
	}
	saved, err := ipArtworkMaterialPublishLookup(ctx, turn, q, c.identity, c.digest)
	if err != nil || saved.decision != receiptReplay || saved.digest != digest || saved.result != value {
		return zero, errors.Join(ErrResolveRequired, err)
	}
	out.Value, out.Digest, out.Outcome.State = saved.result, saved.digest, Applied
	return out, nil
}

// The three encoded variants are registered by exact byte facts, then the
// accepted source row records their keys, the retained original's bytes and a
// fresh non-retiring revision. The installed material rows are never deleted or
// relabelled, so an installed/unpublished/retiring original stays charged.
func ipArtworkMaterialPublishBody(ctx context.Context, turn *commandTurn, q ipArtworkMaterialPublishRequest, nowMS, total int64) (out ipArtworkMaterialPublishResult, err error) {
	defer func() {
		v, e := finishCommand(ctx, commandLifetime(turn), out, err)
		out, err = v, e
	}()
	s := q.spec
	if total != ipArtworkMaterialSlotBytes(q.slots) || total < 1 {
		return out, ErrConflict
	}
	stamp := time.UnixMilli(nowMS).UTC().Format(time.RFC3339Nano)
	for _, slot := range q.slots[:3] {
		if err = ipTextExecOne(ctx, turn, ipAPBlob, slot.digest, slot.mime, slot.width, slot.height,
			slot.bytes, stamp); err != nil {
			return out, errors.Join(ErrConflict, err)
		}
	}
	var originalBytes int64
	if s.expectedSlots == 4 {
		originalBytes = q.slots[3].bytes
	}
	if err = ipTextExecOne(ctx, turn, ipAPSource, s.sourceDigest, s.acceptedMIME, s.acceptedWidth,
		s.acceptedHeight, q.slots[0].digest, q.slots[1].digest, q.slots[2].digest, s.largeEdge,
		slotFlag(q.slots[0]), slotFlag(q.slots[1]), s.encoderRevision, stamp,
		originalBytes); err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	if err = ipArtworkMaterialPublishSourceFacts(ctx, turn, s, q.slots, originalBytes); err != nil {
		return out, err
	}
	if err = ipTextExecOne(ctx, turn, ipAPIntentPublish, s.intentID, q.owner.id,
		q.owner.incarnation[:], s.intentGeneration, s.intentRevision, s.expectedSlots,
		s.sourceDigest); err != nil {
		return out, err
	}
	return ipArtworkMaterialPublishResult{IntentID: s.intentID, Revision: ipArtworkMaterialPublishRevision,
		Slots: s.expectedSlots, Bytes: total, JobGeneration: s.intentGeneration}, nil
}

// The source readback pins the exact custody the caller may later join: encoded
// blob keys, edge/encoder generation, the retained original bytes and the
// retirement fence.
func ipArtworkMaterialPublishSourceFacts(ctx context.Context, turn *commandTurn, s ipArtworkMaterialPublishSpec, slots []ipArtworkMaterialSlot, originalBytes int64) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	rows, err := ipTextRows(ctx, turn, ipAPSourceFacts, s.sourceDigest)
	if err != nil {
		return err
	}
	var mime, small, large, ambient string
	var width, height, edge, smallOutlier, largeOutlier, encoder, revision, storedOriginal, retiring, tokenBytes int64
	if err = ipTextOne(rows, func() error {
		return rows.Scan(&mime, &width, &height, &small, &large, &ambient, &edge, &smallOutlier,
			&largeOutlier, &encoder, &revision, &storedOriginal, &retiring, &tokenBytes)
	}); err != nil {
		return errors.Join(ErrResolveRequired, err)
	}
	if mime != s.acceptedMIME || width != s.acceptedWidth || height != s.acceptedHeight ||
		small != slots[0].digest || large != slots[1].digest || ambient != slots[2].digest ||
		edge != s.largeEdge || smallOutlier != slotFlag(slots[0]) || largeOutlier != slotFlag(slots[1]) ||
		encoder != s.encoderRevision || revision != 1 || storedOriginal != originalBytes ||
		retiring != 0 || tokenBytes != 0 {
		return ErrResolveRequired
	}
	return nil
}

func slotFlag(slot ipArtworkMaterialSlot) int64 {
	if slot.outlier {
		return 1
	}
	return 0
}

// The owner CAS is the command allowance and the receipt is saved in the same
// transaction. ACK alone does not retire the pinned intent, source or blobs.
func ipArtworkMaterialPublishRecord(ctx context.Context, turn *commandTurn, q ipArtworkMaterialPublishRequest, identity CommandIdentity, request RequestDigest, result ResultDigest, value ipArtworkMaterialPublishResult, appliedMS int64) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	o := q.owner
	if err = ipTextExecOne(ctx, turn, ipAMOwnerCAS, identity.Sequence, o.id, o.incarnation[:], o.revision,
		identity.Sequence-1, identity.Sequence-1, o.entityID, o.personID, o.sourceIncarnation[:],
		o.sourceRevision, o.leaseOwner, o.leaseGeneration, o.leaseUntilMS, appliedMS); err != nil {
		return err
	}
	return ipTextExecOne(ctx, turn, ipAPSave, o.id, o.incarnation[:], identity.Sequence,
		identity.OperationID[:], request[:], appliedMS, result[:], value.Slots, value.Bytes,
		value.JobGeneration, o.leaseUntilMS, value.IntentID, q.spec.sourceDigest)
}

func ipArtworkMaterialPublishResultDigest(identity CommandIdentity, request RequestDigest, value ipArtworkMaterialPublishResult) (ResultDigest, error) {
	return canonicalResultDigest(identity, request, ipArtworkMaterialPublishSavedResult{IPArtworkMaterialPublish, value})
}
