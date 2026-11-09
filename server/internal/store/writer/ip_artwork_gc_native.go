package writer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"math"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// 229/230 bodies. Artwork GC is two commands over one physical file custody:
// MARK is permission to attempt exactly one externally held unlink under the
// exact source/blob revision, and FINALIZE acknowledges the row only after the
// caller has actually removed that file. Neither command touches the filesystem,
// a provider or the network: every file fact is verified by the caller before the
// turn and repeated here, and the writer refuses any difference from the durable
// row.
//
// Current owner authority precedes historical receipt disclosure; the ORIGINAL
// saved result is served without any fresh source/lease/policy probe. A fresh
// command re-proves the retained subject job under a live lease, then the
// source-level or blob-level fence, before it writes.
//
// E016 v1 custody rules, binding for these commands:
//   - a source may be marked only while all three encoded blobs are ready
//     custody, so no reference can point at an unlinking blob;
//   - a source is marked only when no selection, normalized history pin,
//     retained publish/reencode receipt (selected/previous digest) or live
//     material intent still references it;
//   - a blob is marked only when no source on any encoded slot and no material
//     slot digest still references it;
//   - neither mark nor finalize may run while the existing backup hold exists,
//     so a backup copy never races an unlink;
//   - finalize requires the exact retiring revision, minted token and file byte
//     count, so a stale token or a missing file cannot become a row-ready
//     acknowledgment;
//   - a retiring source/blob is refused as new selection, publish, upload or
//     reencode input by IP-20/21/22, and its own receipt stores the retired
//     digest and token as SCALARS, never as selected/previous FKs.

// Two closed physical targets. Entity and person IDs are separate namespaces,
// so the retained subject job also carries its exact entity/person witnesses.
const (
	ipArtworkGCSource int64 = 1
	ipArtworkGCBlob   int64 = 2
)

// The retained artwork subject job is the command owner and lease witness. The
// accepted source facts are CAS witnesses, never authority.
type ipArtworkGCRequest struct {
	owner    ipArtworkMaterialOwner
	target   int64
	digest   string
	revision int64
	// bytes is the exact on-disk file this command governs: the retained
	// original for a source (0 when the source kept none) or the encoded blob.
	bytes int64
	// token is the exact mark token for finalize and zero for mark.
	token [16]byte
}

// The saved result is the physical custody head only. No file manifest, blob
// body or provider fact is relabelled as a result.
type ipArtworkGCResult struct {
	Target          int64
	Digest          string
	Token           [16]byte
	Revision        int64
	StorageRevision int64
	Bytes           int64
	Complete        bool
	AppliedMS       int64
}

type ipArtworkGCCommand struct {
	kind     CommandID
	identity CommandIdentity
	request  ipArtworkGCRequest
	digest   RequestDigest
	valid    bool
}

type ipArtworkGCCommandResult struct {
	Outcome ReceiptOutcome
	Value   ipArtworkGCResult
	Digest  ResultDigest
}

type ipArtworkGCOutcome struct {
	decision receiptDecision
	result   ipArtworkGCResult
	digest   ResultDigest
}

type ipArtworkGCSavedResult struct {
	Kind  CommandID
	Value ipArtworkGCResult
}

func (c *ipArtworkGCCommand) reference() ReceiptReference {
	return ReceiptReference{Identity: c.identity, RequestDigest: c.digest}
}

func ipArtworkGCKind(kind CommandID) bool {
	return kind == IPArtworkGCMark || kind == IPArtworkGCFinish
}

func ipArtworkGCCommandKind(kind CommandID) string {
	switch kind {
	case IPArtworkGCMark:
		return "gc_mark"
	case IPArtworkGCFinish:
		return "gc_finish"
	}
	return ""
}

func ipArtworkGCTarget(target int64) bool {
	return target == ipArtworkGCSource || target == ipArtworkGCBlob
}

func ipArtworkGCExactDigest(digest string) bool {
	return digest != "" && ipArtworkSelectionMaybeDigest(digest)
}

// Every retained value is bounded before any digest or hold. A source states
// exactly one entity/person namespace and one target file; a blob states a
// positive file. Mark carries no caller token; finalize carries exactly one.
func ipArtworkGCRequestShape(kind CommandID, identity CommandIdentity, q ipArtworkGCRequest) error {
	if !ipArtworkGCKind(kind) || !ipPolicyCommandIdentityValid(identity) {
		return ErrInvalid
	}
	o := q.owner
	if o.id <= 0 || o.incarnation != identity.Incarnation || (o.entityID > 0) == (o.personID > 0) ||
		o.entityID < 0 || o.personID < 0 || o.sourceIncarnation == [16]byte{} ||
		o.sourceRevision <= 0 || o.revision <= 0 || o.leaseOwner <= 0 || o.leaseGeneration <= 0 ||
		o.leaseUntilMS <= 0 || !ipArtworkGCTarget(q.target) || !ipArtworkGCExactDigest(q.digest) ||
		q.revision <= 0 || q.revision == math.MaxInt64 || q.bytes < 0 || q.bytes > ipArtworkMaterialMaxSlot {
		return ErrInvalid
	}
	if q.target == ipArtworkGCBlob && q.bytes <= 0 {
		return ErrInvalid
	}
	switch kind {
	case IPArtworkGCMark:
		if q.token != ([16]byte{}) {
			return ErrInvalid
		}
	case IPArtworkGCFinish:
		if q.token == ([16]byte{}) {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}

// Called under W preparation custody before any hold. No SQL, no allocation
// beyond the retained request copy and its canonical digest.
func newIPArtworkGCCommand(kind CommandID, identity CommandIdentity, in ipArtworkGCRequest) (*ipArtworkGCCommand, error) {
	if err := ipArtworkGCRequestShape(kind, identity, in); err != nil {
		return nil, err
	}
	digest, err := canonicalRequestDigest(kind, 1, identity, in)
	if err != nil {
		return nil, err
	}
	return &ipArtworkGCCommand{kind: kind, identity: identity, request: in, digest: digest, valid: true}, nil
}

// The mark token is a deterministic CAS witness over the exact original
// operation and its request digest, so replay re-derives nothing mutable and
// no ABA/stale finalize can delete a replacement marked by a later command.
// It is not a secret and grants no authority.
func ipArtworkGCMarkToken(identity CommandIdentity, request RequestDigest) [16]byte {
	h := sha256.New()
	h.Write([]byte("portico-ip-artwork-gc-mark-token-v1\x00"))
	h.Write(identity.Incarnation[:])
	h.Write(identity.OperationID[:])
	var sequence [8]byte
	binary.BigEndian.PutUint64(sequence[:], uint64(identity.Sequence))
	h.Write(sequence[:])
	h.Write(request[:])
	var token [16]byte
	copy(token[:], h.Sum(nil))
	return token
}

// Current authority, the historical receipt and the original result are resolved
// in the same order as the other IP artwork bodies. The receipt is read by this
// command's OWN statement: only the frozen label 'gc_mark' maps to 229 and only
// 'gc_finish' to 230.
func ipArtworkGCLookup(ctx context.Context, turn *commandTurn, kind CommandID, q ipArtworkGCRequest, identity CommandIdentity, digest RequestDigest) (out ipArtworkGCOutcome, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != kind || !ipArtworkGCKind(kind) {
		return out, ErrInvalid
	}
	rows, err := ipTextRows(ctx, turn, ipAGCOwner, q.owner.id)
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

	rows, err = ipTextRows(ctx, turn, ipAGCReceiptOctets, q.owner.id, q.owner.incarnation[:], identity.Sequence)
	if err != nil {
		return out, err
	}
	var digestBytes, operationBytes, requestBytes, resultBytes, tokenBytes int64
	var intentBytes, selectedBytes, previousBytes, roleBytes, stageID, historyID int64
	err = ipTextOne(rows, func() error {
		return rows.Scan(&digestBytes, &operationBytes, &requestBytes, &resultBytes, &tokenBytes,
			&intentBytes, &selectedBytes, &previousBytes, &roleBytes, &stageID, &historyID)
	})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, errors.Join(ErrResolveRequired, err)
	}
	if err == nil && (digestBytes != 64 || operationBytes != 16 || requestBytes != 32 ||
		resultBytes != 32 || tokenBytes != 16 || intentBytes != 0 || selectedBytes != 0 ||
		previousBytes != 0 || roleBytes != 0 || stageID != 0 || historyID != 0) {
		return out, ErrResolveRequired
	}
	receipt := ipAGCMarkReceipt
	if kind == IPArtworkGCFinish {
		receipt = ipAGCFinishReceipt
	}
	rows, err = ipTextRows(ctx, turn, receipt, q.owner.id, q.owner.incarnation[:], identity.Sequence)
	if err != nil {
		return out, err
	}
	var found receiptFacts
	var saved ipArtworkGCResult
	var operation, request, result, token []byte
	var nativeKind, revision, appliedMS, count, bytesTotal, complete int64
	var leaseGeneration, leaseUntil, target, storageRevision int64
	var blobDigest string
	err = ipTextOne(rows, func() error {
		return rows.Scan(&operation, &request, &result, &nativeKind, &revision, &appliedMS,
			&count, &bytesTotal, &complete, &leaseGeneration, &leaseUntil, &blobDigest,
			&target, &storageRevision, &token)
	})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, errors.Join(ErrResolveRequired, err)
	}
	if err == nil {
		if len(operation) != 16 || len(request) != 32 || len(result) != 32 ||
			CommandID(nativeKind) != kind || revision <= 0 || appliedMS < 0 || count != 1 ||
			bytesTotal < 0 || bytesTotal > ipArtworkMaterialMaxSlot || (complete != 0 && complete != 1) ||
			leaseGeneration <= 0 || leaseUntil <= 0 || !ipArtworkGCTarget(target) ||
			!ipArtworkGCExactDigest(blobDigest) || int64(len(blobDigest)) != digestBytes ||
			storageRevision <= 0 || len(token) != 16 ||
			(kind == IPArtworkGCMark && (complete != 0 || storageRevision != revision+1)) ||
			(kind == IPArtworkGCFinish && (complete != 1 || storageRevision != revision)) {
			return out, ErrResolveRequired
		}
		found.exists = true
		found.kind = kind
		found.resultCount = count
		actual := receiptIdentity{command: identity}
		copy(actual.command.OperationID[:], operation)
		copy(actual.digest[:], request)
		found.identity = actual
		copy(found.resultDigest[:], result)
		saved = ipArtworkGCResult{Target: target, Digest: blobDigest, Revision: revision,
			StorageRevision: storageRevision, Bytes: bytesTotal, Complete: complete == 1,
			AppliedMS: appliedMS}
		copy(saved.Token[:], token)
	}
	out.decision, err = classifyReceipt(receiptIdentity{identity, digest}, kind, owner, found)
	if err != nil {
		return out, err
	}
	if out.decision != receiptReplay {
		return out, nil
	}
	if saved.Target != q.target || saved.Digest != q.digest || saved.Revision != q.revision ||
		saved.Bytes != q.bytes || (kind == IPArtworkGCFinish && saved.Token != q.token) {
		return out, ErrResolveRequired
	}
	expected, e := ipArtworkGCResultDigest(kind, identity, digest, saved)
	if e != nil || !bytes.Equal(expected[:], result) {
		return out, errors.Join(ErrResolveRequired, e)
	}
	out.result, out.digest = saved, expected
	return out, nil
}

// The retained subject job, then the exact referenced custody, are re-proved
// before either mark or finalize. Finalize deliberately re-runs the reference
// fence so a pin that appeared after the mark cannot be acknowledged away.
func ipArtworkGCFresh(ctx context.Context, turn *commandTurn, q ipArtworkGCRequest, nowMS int64) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if err = ipArtworkGCOwnerLive(ctx, turn, q, nowMS); err != nil {
		return err
	}
	if err = ipArtworkGCBackup(ctx, turn); err != nil {
		return err
	}
	return ipArtworkGCReferences(ctx, turn, q)
}

func ipArtworkGCOwnerLive(ctx context.Context, turn *commandTurn, q ipArtworkGCRequest, nowMS int64) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	o := q.owner
	rows, err := ipTextRows(ctx, turn, ipAGCOwnerLive, o.id, o.incarnation[:], o.entityID, o.personID,
		o.sourceIncarnation[:], o.sourceRevision, o.revision, o.leaseOwner, o.leaseGeneration,
		o.leaseUntilMS, nowMS)
	if err != nil {
		return err
	}
	var one int64
	if err = ipTextOne(rows, func() error { return rows.Scan(&one) }); err != nil {
		return errors.Join(ErrConflict, err)
	}
	return nil
}

// The existing backup hold proof: one row means a backup is copying artwork
// files, so no mark may start and no finalize may race it.
func ipArtworkGCBackup(ctx context.Context, turn *commandTurn) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	return ipArtworkGCMustBeAbsent(ctx, turn, ipAGCBackup)
}

// Exactly one closed fence by target: every retained reference must be gone.
func ipArtworkGCReferences(ctx context.Context, turn *commandTurn, q ipArtworkGCRequest) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	var probes []stmt.ID
	switch q.target {
	case ipArtworkGCSource:
		probes = []stmt.ID{ipAGCSourceSelect, ipAGCSourceHistory, ipAGCSourceSelected,
			ipAGCSourcePrevious, ipAGCSourceIntent}
	case ipArtworkGCBlob:
		probes = []stmt.ID{ipAGCBlobSmall, ipAGCBlobLarge, ipAGCBlobAmbient, ipAGCBlobSlot}
	default:
		return ErrInvalid
	}
	for _, id := range probes {
		if err = ipArtworkGCMustBeAbsent(ctx, turn, id, q.digest); err != nil {
			return err
		}
	}
	return nil
}

// One point probe that must yield no row. A present row is a reference and is
// refused as a conflict, never collapsed into a missing-owner resolution.
func ipArtworkGCMustBeAbsent(ctx context.Context, turn *commandTurn, id stmt.ID, args ...any) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	rows, err := ipTextRows(ctx, turn, id, args...)
	if err != nil {
		return err
	}
	var one int64
	e := ipTextOne(rows, func() error { return rows.Scan(&one) })
	if errors.Is(e, sql.ErrNoRows) {
		return nil
	}
	if e != nil {
		return errors.Join(ErrResolveRequired, e)
	}
	if one != 1 {
		return ErrResolveRequired
	}
	return ErrConflict
}

// The exact durable custody row and the caller's repeated file facts. The
// source join also proves no encoded blob is already unlinking.
func ipArtworkGCFenceExact(ctx context.Context, turn *commandTurn, q ipArtworkGCRequest) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	switch q.target {
	case ipArtworkGCSource:
		rows, err := ipTextRows(ctx, turn, ipAGCSourceFence, q.digest)
		if err != nil {
			return err
		}
		var revision, retiring, tokenBytes, originalBytes int64
		var token []byte
		if err = ipTextOne(rows, func() error {
			return rows.Scan(&revision, &retiring, &tokenBytes, &token, &originalBytes)
		}); err != nil {
			return errors.Join(ErrConflict, err)
		}
		if len(token) != int(tokenBytes) || originalBytes != q.bytes {
			return ErrConflict
		}
		switch q.token {
		case [16]byte{}:
			if revision != q.revision || retiring != 0 || tokenBytes != 0 || len(token) != 0 {
				return ErrConflict
			}
		default:
			if revision != q.revision || retiring != 1 || tokenBytes != 16 ||
				len(token) != 16 || !bytes.Equal(token, q.token[:]) {
				return ErrConflict
			}
		}
	case ipArtworkGCBlob:
		rows, err := ipTextRows(ctx, turn, ipAGCBlobFence, q.digest)
		if err != nil {
			return err
		}
		var revision, bytesTotal, tokenBytes int64
		var status string
		var token []byte
		if err = ipTextOne(rows, func() error {
			return rows.Scan(&revision, &bytesTotal, &status, &tokenBytes, &token)
		}); err != nil {
			return errors.Join(ErrConflict, err)
		}
		if revision != q.revision || bytesTotal != q.bytes || len(token) != int(tokenBytes) {
			return ErrConflict
		}
		switch q.token {
		case [16]byte{}:
			if status != "ready" || tokenBytes != 0 || len(token) != 0 {
				return ErrConflict
			}
		default:
			if status != "deleting" || tokenBytes != 16 || len(token) != 16 ||
				!bytes.Equal(token, q.token[:]) {
				return ErrConflict
			}
		}
	default:
		return ErrInvalid
	}
	return nil
}

// The two closed physical writes. Each is an exact CAS on the revision and file
// byte facts the fence just re-proved. Mark writes the exact retiring revision
// and its minted token; finalize deletes only the exact retiring row after the
// caller repeated the same token and file byte count. Blobs keep independent
// custody: a source finalize NEVER deletes its encoded blobs.
func ipArtworkGCWrite(ctx context.Context, turn *commandTurn, kind CommandID, q ipArtworkGCRequest, token [16]byte) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	switch kind {
	case IPArtworkGCMark:
		switch q.target {
		case ipArtworkGCSource:
			err = ipTextExecOne(ctx, turn, ipAGCSourceMark, token[:], q.digest, q.revision, q.bytes)
		case ipArtworkGCBlob:
			err = ipTextExecOne(ctx, turn, ipAGCBlobMark, token[:], q.digest, q.revision, q.bytes)
		default:
			return ErrInvalid
		}
	case IPArtworkGCFinish:
		switch q.target {
		case ipArtworkGCSource:
			err = ipTextExecOne(ctx, turn, ipAGCSourceFinish, q.digest, q.revision, q.token[:], q.bytes)
		case ipArtworkGCBlob:
			err = ipTextExecOne(ctx, turn, ipAGCBlobFinish, q.digest, q.revision, q.bytes, q.token[:])
		default:
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	if err != nil {
		return errors.Join(ErrConflict, err)
	}
	return nil
}

// The owner CAS is the command allowance and the receipt is saved in the same
// transaction. ACK alone retires the receipt; it does not by itself remove the
// physical custody or the retiring row.
func ipArtworkGCRecord(ctx context.Context, turn *commandTurn, kind CommandID, q ipArtworkGCRequest, identity CommandIdentity, request RequestDigest, result ResultDigest, value ipArtworkGCResult, appliedMS int64) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	o := q.owner
	if err = ipTextExecOne(ctx, turn, ipAGCOwnerCAS, identity.Sequence, o.id, o.incarnation[:], o.revision,
		identity.Sequence-1, identity.Sequence-1, o.entityID, o.personID, o.sourceIncarnation[:],
		o.sourceRevision, o.leaseOwner, o.leaseGeneration, o.leaseUntilMS, appliedMS); err != nil {
		return err
	}
	id := ipAGCSaveMark
	if kind == IPArtworkGCFinish {
		id = ipAGCSaveFinish
	}
	return ipTextExecOne(ctx, turn, id, o.id, o.incarnation[:], identity.Sequence,
		identity.OperationID[:], request[:], appliedMS, result[:], value.Revision, value.Bytes,
		o.leaseGeneration, o.leaseUntilMS, value.Digest, value.Target, value.Token[:],
		value.StorageRevision)
}

// The saved result is the exact custody head this command produced.
func ipArtworkGCResultFor(kind CommandID, q ipArtworkGCRequest, token [16]byte, appliedMS int64) (ipArtworkGCResult, error) {
	switch kind {
	case IPArtworkGCMark:
		return ipArtworkGCResult{Target: q.target, Digest: q.digest, Token: token,
			Revision: q.revision, StorageRevision: q.revision + 1, Bytes: q.bytes,
			AppliedMS: appliedMS}, nil
	case IPArtworkGCFinish:
		return ipArtworkGCResult{Target: q.target, Digest: q.digest, Token: q.token,
			Revision: q.revision, StorageRevision: q.revision, Bytes: q.bytes,
			Complete: true, AppliedMS: appliedMS}, nil
	}
	return ipArtworkGCResult{}, ErrInvalid
}

func applyIPArtworkGC(ctx context.Context, turn *commandTurn, p *usOwnerAuthorityProof, lifetime context.Context, b ipPolicyBudget, c *ipArtworkGCCommand) (out ipArtworkGCCommandResult, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	var zero ipArtworkGCCommandResult
	if c == nil || !c.valid || !ipArtworkGCKind(c.kind) || turn == nil || turn.transaction == nil ||
		turn.transaction.descriptor.ID != c.kind {
		return zero, ErrInvalid
	}
	q := c.request
	if err = ipArtworkGCRequestShape(c.kind, c.identity, q); err != nil {
		return zero, err
	}
	actual, err := canonicalRequestDigest(c.kind, 1, c.identity, q)
	if err != nil || actual != c.digest {
		return zero, errors.Join(ErrConflict, err)
	}
	auth, err := authorizeIPPolicyOwner(ctx, turn, p, lifetime, b)
	if err != nil {
		return zero, err
	}
	lookup, err := ipArtworkGCLookup(ctx, turn, c.kind, q, c.identity, c.digest)
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
	if err = ipArtworkGCFresh(ctx, turn, q, appliedMS); err != nil {
		return zero, err
	}
	if err = ipArtworkGCFenceExact(ctx, turn, q); err != nil {
		return zero, err
	}
	token := q.token
	if c.kind == IPArtworkGCMark {
		token = ipArtworkGCMarkToken(c.identity, c.digest)
	}
	if err = ipArtworkGCWrite(ctx, turn, c.kind, q, token); err != nil {
		return zero, err
	}
	value, err := ipArtworkGCResultFor(c.kind, q, token, appliedMS)
	if err != nil {
		return zero, err
	}
	digest, err := ipArtworkGCResultDigest(c.kind, c.identity, c.digest, value)
	if err != nil {
		return zero, err
	}
	if err = ipArtworkGCRecord(ctx, turn, c.kind, q, c.identity, c.digest, digest, value, appliedMS); err != nil {
		return zero, err
	}
	saved, err := ipArtworkGCLookup(ctx, turn, c.kind, q, c.identity, c.digest)
	if err != nil || saved.decision != receiptReplay || saved.digest != digest || saved.result != value {
		return zero, errors.Join(ErrResolveRequired, err)
	}
	out.Value, out.Digest, out.Outcome.State = saved.result, saved.digest, Applied
	return out, nil
}

func ipArtworkGCResultDigest(kind CommandID, identity CommandIdentity, request RequestDigest, value ipArtworkGCResult) (ResultDigest, error) {
	return canonicalResultDigest(identity, request, ipArtworkGCSavedResult{kind, value})
}

// W result wire: shared identity/request prefix, the native purpose and the
// actual command variant, then the custody head. Rows and files are never
// relabelled as a result.
func preflightIPArtworkGCResult(v ipArtworkGCSavedResult) error {
	r := v.Value
	if !ipArtworkGCKind(v.Kind) || !ipArtworkGCTarget(r.Target) ||
		!ipArtworkGCExactDigest(r.Digest) || r.Token == ([16]byte{}) || r.Revision <= 0 ||
		r.StorageRevision <= 0 || r.Bytes < 0 || r.Bytes > ipArtworkMaterialMaxSlot ||
		r.AppliedMS < 0 || (r.Target == ipArtworkGCBlob && r.Bytes <= 0) {
		return ErrInvalid
	}
	switch v.Kind {
	case IPArtworkGCMark:
		if r.Complete || r.StorageRevision != r.Revision+1 {
			return ErrInvalid
		}
	case IPArtworkGCFinish:
		if !r.Complete || r.StorageRevision != r.Revision {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}

func writeIPArtworkGCResult(e *requestEncoder, i CommandIdentity, q RequestDigest, v ipArtworkGCSavedResult) error {
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1})
	e.text("ip_artwork_gc")
	e.text(ipArtworkGCCommandKind(v.Kind))
	e.text("applied")
	e.bytes(i.Incarnation[:])
	e.number(uint64(i.Sequence))
	e.bytes(i.OperationID[:])
	e.bytes(q[:])
	r := v.Value
	e.number(uint64(r.Target))
	e.text(r.Digest)
	e.bytes(r.Token[:])
	for _, n := range []int64{r.Revision, r.StorageRevision, r.Bytes} {
		e.number(uint64(n))
	}
	e.flag(r.Complete)
	e.number(uint64(r.AppliedMS))
	return e.err
}
