package writer

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"math"
	"time"
)

// 231 body. Reencode replaces the encoded variants of ONE accepted artwork
// source with a new complete installed three-variant manifest from a newer
// encoder generation. Current owner authority precedes historical receipt
// disclosure; the ORIGINAL saved result is served without any fresh
// source/lease/policy probe. A fresh command re-proves the retained subject job,
// the current entity/person artwork revision and policy, the installed material
// intent and its COMPLETE manifest, then CAS-replaces the source's blob keys and
// encoder revision.
//
// It keeps the retained original bytes (artwork_sources.original_bytes and the
// IP-19 slot 3) unchanged, and it never rewrites artwork_selections,
// metadata_owner_history_artwork or material rows: the source digest is stable,
// so every pinned reference keeps charging the old blobs and original. Readers
// join the source revision instead of being enumerated here.
//
// Every file was verified by the caller before this turn; nothing here touches
// the filesystem, a provider or the network. The request repeats the verified
// installed manifest and the exact previous source facts; a stale encoder
// generation, changed revision, retiring source, missing file or changed
// original is refused whole.

// The exact accepted source facts as the caller last observed them. A reencode
// never changes the accepted source identity, only its encoded variants.
type ipArtworkReencodePrevious struct {
	small, large, ambient, mime                                        string
	width, height, largeEdge, encoderRevision, originalBytes, revision int64
	smallOutlier, largeOutlier                                         bool
}

type ipArtworkReencodeRequest struct {
	owner    ipArtworkMaterialOwner
	spec     ipArtworkMaterialPublishSpec
	previous ipArtworkReencodePrevious
	slots    []ipArtworkMaterialSlot
}

// The saved result is the new source revision head only. Blobs stay in their own
// table and the pinned original is never relabelled as a result.
type ipArtworkReencodeResult struct {
	IntentID, SourceDigest                                       string
	SourceRevision, Slots, Bytes, OriginalBytes, EncoderRevision int64
	PreviousRevision, AppliedMS                                  int64
}

type ipArtworkReencodeCommand struct {
	identity CommandIdentity
	request  ipArtworkReencodeRequest
	digest   RequestDigest
	valid    bool
}

type ipArtworkReencodeCommandResult struct {
	Outcome ReceiptOutcome
	Value   ipArtworkReencodeResult
	Digest  ResultDigest
}

type ipArtworkReencodeOutcome struct {
	decision receiptDecision
	result   ipArtworkReencodeResult
	digest   ResultDigest
}

type ipArtworkReencodeSavedResult struct {
	Kind  CommandID
	Value ipArtworkReencodeResult
}

func (c *ipArtworkReencodeCommand) reference() ReceiptReference {
	return ReceiptReference{Identity: c.identity, RequestDigest: c.digest}
}

func ipArtworkReencodeFlag(n int64) (bool, bool) {
	switch n {
	case 0:
		return false, true
	case 1:
		return true, true
	}
	return false, false
}

func ipArtworkReencodeBit(v bool) int64 {
	if v {
		return 1
	}
	return 0
}

// Every string in the retained request must be bounded before any digest or
// hold. The accepted source identity (digest, mime, dimensions) is fixed by the
// previous facts and may not be restated differently.
func ipArtworkReencodeRequestShape(identity CommandIdentity, q ipArtworkReencodeRequest) error {
	if !ipPolicyCommandIdentityValid(identity) || q.spec.expectedSlots != 3 {
		return ErrInvalid
	}
	if err := ipArtworkMaterialPublishRequestShape(identity, ipArtworkMaterialPublishRequest{
		owner: q.owner, spec: q.spec, slots: q.slots,
	}); err != nil {
		return err
	}
	p := q.previous
	if p.revision <= 0 || p.revision == math.MaxInt64 || p.encoderRevision <= 0 ||
		p.originalBytes < 0 || p.width <= 0 || p.height <= 0 ||
		(p.largeEdge != 1280 && p.largeEdge != 1920 && p.largeEdge != 2560) ||
		!ipArtworkSelectionMaybeDigest(p.small) || p.small == "" ||
		!ipArtworkSelectionMaybeDigest(p.large) || p.large == "" ||
		!ipArtworkSelectionMaybeDigest(p.ambient) || p.ambient == "" ||
		(p.mime != "image/jpeg" && p.mime != "image/png" && p.mime != "image/webp") ||
		q.spec.acceptedMIME != p.mime || q.spec.acceptedWidth != p.width ||
		q.spec.acceptedHeight != p.height {
		return ErrInvalid
	}
	return nil
}

// Called under W preparation custody before any hold. No SQL, no allocation
// beyond the retained request copy and its canonical digest.
func newIPArtworkReencodeCommand(identity CommandIdentity, in ipArtworkReencodeRequest) (*ipArtworkReencodeCommand, error) {
	if err := ipArtworkReencodeRequestShape(identity, in); err != nil {
		return nil, err
	}
	digest, err := canonicalRequestDigest(IPArtworkReencode, 1, identity, in)
	if err != nil {
		return nil, err
	}
	return &ipArtworkReencodeCommand{identity: identity, request: in, digest: digest, valid: true}, nil
}

// Current authority, historical receipt and original result are resolved in the
// same order as the other IP artwork bodies. The receipt is read by this
// command's OWN statement: only the frozen 'reencode' label maps to 231.
func ipArtworkReencodeLookup(ctx context.Context, turn *commandTurn, q ipArtworkReencodeRequest, identity CommandIdentity, digest RequestDigest) (out ipArtworkReencodeOutcome, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != IPArtworkReencode {
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

	rows, err = ipTextRows(ctx, turn, ipARReceiptOctets, q.owner.id, q.owner.incarnation[:], identity.Sequence)
	if err != nil {
		return out, err
	}
	var intentBytes, selectedBytes, operationBytes, requestBytes, resultBytes, tokenBytes int64
	err = ipTextOne(rows, func() error {
		return rows.Scan(&intentBytes, &selectedBytes, &operationBytes, &requestBytes,
			&resultBytes, &tokenBytes)
	})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, errors.Join(ErrResolveRequired, err)
	}
	if err == nil && (intentBytes < 1 || intentBytes > ipArtworkMaterialMaxIntent ||
		selectedBytes != 64 || operationBytes != 16 || requestBytes != 32 ||
		resultBytes != 32 || tokenBytes != 0) {
		return out, ErrResolveRequired
	}
	rows, err = ipTextRows(ctx, turn, ipARReceipt, q.owner.id, q.owner.incarnation[:], identity.Sequence)
	if err != nil {
		return out, err
	}
	var found receiptFacts
	var saved ipArtworkReencodeResult
	var operation, request, result []byte
	var nativeKind, accepted, appliedMS, count, totalBytes, complete int64
	var leaseGeneration, leaseUntil, storageRevision int64
	var intent, selected string
	err = ipTextOne(rows, func() error {
		return rows.Scan(&operation, &request, &result, &nativeKind, &accepted, &appliedMS,
			&count, &totalBytes, &complete, &leaseGeneration, &leaseUntil, &intent,
			&selected, &storageRevision, &tokenBytes)
	})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, errors.Join(ErrResolveRequired, err)
	}
	if err == nil {
		if len(operation) != 16 || len(request) != 32 || len(result) != 32 ||
			CommandID(nativeKind) != IPArtworkReencode || accepted != storageRevision ||
			appliedMS < 0 || count != 3 || totalBytes != ipArtworkMaterialSlotBytes(q.slots) ||
			totalBytes < 1 || totalBytes > ipArtworkMaterialMaxBytes || complete != 1 ||
			leaseGeneration <= 0 || leaseUntil <= 0 || intent != q.spec.intentID ||
			int64(len(intent)) != intentBytes || int64(len(intent)) > ipArtworkMaterialMaxIntent ||
			selected != q.spec.sourceDigest || int64(len(selected)) != selectedBytes ||
			storageRevision != q.previous.revision && storageRevision != q.previous.revision+1 ||
			tokenBytes != 0 {
			return out, ErrResolveRequired
		}
		found.exists = true
		found.kind = IPArtworkReencode
		found.resultCount = count
		actual := receiptIdentity{command: identity}
		copy(actual.command.OperationID[:], operation)
		copy(actual.digest[:], request)
		found.identity = actual
		copy(found.resultDigest[:], result)
		saved = ipArtworkReencodeResult{
			IntentID: intent, SourceDigest: selected, SourceRevision: storageRevision,
			Slots: count, Bytes: totalBytes, OriginalBytes: q.previous.originalBytes,
			EncoderRevision: q.spec.encoderRevision, PreviousRevision: q.previous.revision,
			AppliedMS: appliedMS,
		}
	}
	out.decision, err = classifyReceipt(receiptIdentity{identity, digest}, IPArtworkReencode, owner, found)
	if err != nil {
		return out, err
	}
	if out.decision != receiptReplay {
		return out, nil
	}
	if saved.IntentID != q.spec.intentID || saved.SourceDigest != q.spec.sourceDigest ||
		saved.Slots != 3 || saved.Bytes != ipArtworkMaterialSlotBytes(q.slots) ||
		saved.OriginalBytes != q.previous.originalBytes ||
		saved.EncoderRevision != q.spec.encoderRevision ||
		saved.PreviousRevision != q.previous.revision ||
		(saved.SourceRevision != q.previous.revision && saved.SourceRevision != q.previous.revision+1) {
		return out, ErrResolveRequired
	}
	expected, e := ipArtworkReencodeResultDigest(identity, digest, saved)
	if e != nil || !bytes.Equal(expected[:], result) {
		return out, errors.Join(ErrResolveRequired, e)
	}
	out.result, out.digest = saved, expected
	return out, nil
}

// The retained subject job, current source/policy, installed intent and complete
// manifest are re-proved, then the exact current source facts are compared with
// the caller's previous revision/encoder generation before any blob or source
// byte is written.
func ipArtworkReencodeFresh(ctx context.Context, turn *commandTurn, q ipArtworkReencodeRequest, nowMS int64) (total int64, err error) {
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
	if err = ipArtworkMaterialPublishIntent(ctx, turn, ipArtworkMaterialPublishRequest{
		owner: q.owner, spec: q.spec, slots: q.slots,
	}); err != nil {
		return 0, err
	}
	if total, err = ipArtworkMaterialManifest(ctx, turn, q.spec.intentID, q.slots); err != nil {
		return 0, err
	}
	rows, err := ipTextRows(ctx, turn, ipARSourceExact, q.spec.sourceDigest)
	if err != nil {
		return 0, err
	}
	var mime, small, large, ambient string
	var width, height, edge, encoder, revision, originalBytes, retiring, tokenBytes int64
	var smallOutlier, largeOutlier int64
	if err = ipTextOne(rows, func() error {
		return rows.Scan(&mime, &width, &height, &small, &large, &ambient, &edge,
			&smallOutlier, &largeOutlier, &encoder, &revision, &originalBytes,
			&retiring, &tokenBytes)
	}); err != nil {
		return 0, errors.Join(ErrConflict, err)
	}
	p := q.previous
	smallFlag, smallOK := ipArtworkReencodeFlag(smallOutlier)
	largeFlag, largeOK := ipArtworkReencodeFlag(largeOutlier)
	if !smallOK || !largeOK || mime != p.mime || width != p.width || height != p.height ||
		small != p.small || large != p.large || ambient != p.ambient || edge != p.largeEdge ||
		encoder != p.encoderRevision || revision != p.revision || originalBytes != p.originalBytes ||
		retiring != 0 || tokenBytes != 0 || smallFlag != p.smallOutlier ||
		largeFlag != p.largeOutlier {
		return 0, ErrConflict
	}
	stamp := time.UnixMilli(nowMS).UTC().Format(time.RFC3339Nano)
	for _, slot := range q.slots {
		if err = ipTextExecOne(ctx, turn, ipAPBlob, slot.digest, slot.mime, slot.width,
			slot.height, slot.bytes, stamp); err != nil {
			return 0, errors.Join(ErrConflict, err)
		}
	}
	return total, nil
}

// applyIPArtworkReencode is the closed command body. A fresh request CAS-replaces
// the source's encoded variant keys and encoder generation, then saves the exact
// source revision; the original digest, accepted facts, selection rows and
// history pins are untouched.
func applyIPArtworkReencode(ctx context.Context, turn *commandTurn, p *usOwnerAuthorityProof, lifetime context.Context, b ipPolicyBudget, c *ipArtworkReencodeCommand) (out ipArtworkReencodeCommandResult, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	var zero ipArtworkReencodeCommandResult
	if c == nil || !c.valid || turn == nil || turn.transaction == nil ||
		turn.transaction.descriptor.ID != IPArtworkReencode {
		return zero, ErrInvalid
	}
	q := c.request
	if err = ipArtworkReencodeRequestShape(c.identity, q); err != nil {
		return zero, err
	}
	actual, err := canonicalRequestDigest(IPArtworkReencode, 1, c.identity, q)
	if err != nil || actual != c.digest {
		return zero, errors.Join(ErrConflict, err)
	}
	auth, err := authorizeIPPolicyOwner(ctx, turn, p, lifetime, b)
	if err != nil {
		return zero, err
	}
	lookup, err := ipArtworkReencodeLookup(ctx, turn, q, c.identity, c.digest)
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
	total, err := ipArtworkReencodeFresh(ctx, turn, q, appliedMS)
	if err != nil {
		return zero, err
	}
	value, err := ipArtworkReencodeBody(ctx, turn, q, appliedMS, total)
	if err != nil {
		return zero, err
	}
	digest, err := ipArtworkReencodeResultDigest(c.identity, c.digest, value)
	if err != nil {
		return zero, err
	}
	if err = ipArtworkReencodeRecord(ctx, turn, q, c.identity, c.digest, digest, value, appliedMS); err != nil {
		return zero, err
	}
	saved, err := ipArtworkReencodeLookup(ctx, turn, q, c.identity, c.digest)
	if err != nil || saved.decision != receiptReplay || saved.digest != digest || saved.result != value {
		return zero, errors.Join(ErrResolveRequired, err)
	}
	out.Value, out.Digest, out.Outcome.State = saved.result, saved.digest, Applied
	return out, nil
}

func ipArtworkReencodeChanged(q ipArtworkReencodeRequest) bool {
	p := q.previous
	return q.slots[0].digest != p.small || q.slots[1].digest != p.large ||
		q.slots[2].digest != p.ambient || q.spec.largeEdge != p.largeEdge ||
		q.spec.encoderRevision != p.encoderRevision ||
		q.slots[0].outlier != p.smallOutlier || q.slots[1].outlier != p.largeOutlier
}

func ipArtworkReencodeBody(ctx context.Context, turn *commandTurn, q ipArtworkReencodeRequest, nowMS, total int64) (out ipArtworkReencodeResult, err error) {
	defer func() {
		v, e := finishCommand(ctx, commandLifetime(turn), out, err)
		out, err = v, e
	}()
	p := q.previous
	revision := p.revision
	if ipArtworkReencodeChanged(q) {
		if err = ipTextExecOne(ctx, turn, ipARSourceUpdate, q.slots[0].digest, q.slots[1].digest,
			q.slots[2].digest, q.spec.largeEdge, slotFlag(q.slots[0]), slotFlag(q.slots[1]),
			q.spec.encoderRevision, q.spec.sourceDigest, p.revision, p.small, p.large,
			p.ambient, p.largeEdge, ipArtworkReencodeBit(p.smallOutlier),
			ipArtworkReencodeBit(p.largeOutlier), p.encoderRevision, p.originalBytes); err != nil {
			return out, errors.Join(ErrConflict, err)
		}
		revision = p.revision + 1
	}
	if err = ipTextExecOne(ctx, turn, ipAPIntentPublish, q.spec.intentID, q.owner.id,
		q.owner.incarnation[:], q.spec.intentGeneration, q.spec.intentRevision,
		q.spec.expectedSlots, q.spec.sourceDigest); err != nil {
		return out, err
	}
	return ipArtworkReencodeResult{
		IntentID: q.spec.intentID, SourceDigest: q.spec.sourceDigest,
		SourceRevision: revision, Slots: 3, Bytes: total, OriginalBytes: p.originalBytes,
		EncoderRevision: q.spec.encoderRevision, PreviousRevision: p.revision,
		AppliedMS: nowMS,
	}, nil
}

// The owner CAS is the command allowance and the receipt is saved in the same
// transaction. ACK alone does not retire the pinned original, selection or
// history.
func ipArtworkReencodeRecord(ctx context.Context, turn *commandTurn, q ipArtworkReencodeRequest, identity CommandIdentity, request RequestDigest, result ResultDigest, value ipArtworkReencodeResult, appliedMS int64) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	o := q.owner
	if err = ipTextExecOne(ctx, turn, ipAMOwnerCAS, identity.Sequence, o.id, o.incarnation[:],
		o.revision, identity.Sequence-1, identity.Sequence-1, o.entityID, o.personID,
		o.sourceIncarnation[:], o.sourceRevision, o.leaseOwner, o.leaseGeneration,
		o.leaseUntilMS, appliedMS); err != nil {
		return err
	}
	return ipTextExecOne(ctx, turn, ipARSave, o.id, o.incarnation[:], identity.Sequence,
		identity.OperationID[:], request[:], appliedMS, result[:], value.SourceRevision,
		value.Slots, value.Bytes, o.leaseGeneration, o.leaseUntilMS, value.IntentID,
		value.SourceDigest, value.SourceRevision)
}

func ipArtworkReencodeResultDigest(identity CommandIdentity, request RequestDigest, value ipArtworkReencodeResult) (ResultDigest, error) {
	return canonicalResultDigest(identity, request, ipArtworkReencodeSavedResult{IPArtworkReencode, value})
}

// W result wire: shared identity/request prefix, the native purpose and the
// actual command variant, then the new source revision head. The pinned original
// and its bytes are never relabelled as a result.
func preflightIPArtworkReencodeResult(v ipArtworkReencodeSavedResult) error {
	r := v.Value
	if v.Kind != IPArtworkReencode || len(r.IntentID) == 0 ||
		len(r.IntentID) > ipArtworkMaterialMaxIntent || !ipArtworkSelectionMaybeDigest(r.SourceDigest) ||
		r.SourceDigest == "" || r.SourceRevision <= 0 || r.Slots != 3 ||
		r.Bytes < 1 || r.Bytes > ipArtworkMaterialMaxBytes || r.OriginalBytes < 0 ||
		r.EncoderRevision <= 0 || r.PreviousRevision <= 0 ||
		(r.SourceRevision != r.PreviousRevision && r.SourceRevision != r.PreviousRevision+1) ||
		r.AppliedMS < 0 {
		return ErrInvalid
	}
	return nil
}

func writeIPArtworkReencodeResult(e *requestEncoder, i CommandIdentity, q RequestDigest, v ipArtworkReencodeSavedResult) error {
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1})
	e.text("ip_artwork_reencode")
	e.text("reencode")
	e.text("applied")
	e.bytes(i.Incarnation[:])
	e.number(uint64(i.Sequence))
	e.bytes(i.OperationID[:])
	e.bytes(q[:])
	r := v.Value
	e.text(r.IntentID)
	e.text(r.SourceDigest)
	for _, n := range []int64{r.SourceRevision, r.Slots, r.Bytes, r.OriginalBytes, r.EncoderRevision, r.PreviousRevision, r.AppliedMS} {
		e.number(uint64(n))
	}
	return e.err
}
