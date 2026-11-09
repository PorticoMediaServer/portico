package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Private receipt adapter for the profile-restrictions replacement (542). It
// consumes US-01's current-source authority stamp and W's classifier; it mints
// no authority, registers no accepting command and reconstructs no original from
// live target source. Replay reads ONLY the retained header, its one typed
// restriction row and the ordered label cohort.
type usRestrictionsReceiptRequest struct {
	ownerID   string
	kind      CommandID
	reference ReceiptReference
}

type usRestrictionsOwnerFacts struct {
	receiptOwnerFacts
	// originalFamily is retained provenance, not an equality predicate: the
	// owner is addressed by the exact issuer scope instead, so a renewed family
	// of the same installation still resolves.
	originalFamily                     string
	reclaimed, count, bytes, retiredMS int64
}

type usRestrictionsReceiptLookup struct {
	owner    usRestrictionsOwnerFacts
	decision receiptDecision
	result   usRestrictionsSavedResult
	digest   ResultDigest
}

func usRestrictionsReceiptKind(kind CommandID) error {
	if _, err := usRestrictionsCodec(kind); err != nil {
		return err
	}
	return nil
}

// Small fixed-width binding header, before floor/result decoding.
func readUSRestrictionsBinding(ctx context.Context, turn *commandTurn, q usRestrictionsReceiptRequest) (out receiptFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	row, err := usRestrictionsRow(ctx, turn, usRestrictionsReceiptBinding, q.ownerID, q.reference.Identity.Incarnation[:], q.reference.Identity.Sequence)
	if err != nil {
		return out, err
	}
	var op, request, digest []byte
	var kind, count int64
	if err = row.Scan(&op, &request, &kind, &count, &digest); errors.Is(err, sql.ErrNoRows) {
		return out, nil
	} else if err != nil {
		return out, err
	}
	if len(op) != 16 || len(request) != 32 || len(digest) != 32 || kind < 0 || kind > math.MaxUint16 {
		return out, ErrResolveRequired
	}
	out.exists = true
	out.identity.command = q.reference.Identity
	copy(out.identity.command.OperationID[:], op)
	copy(out.identity.digest[:], request)
	copy(out.resultDigest[:], digest)
	out.kind = CommandID(kind)
	out.resultCount = count
	return out, nil
}

// The owner is addressed by BOTH its PK and the exact issuer/scope it was born
// with. A different current issuer cannot read or advance someone else's cohort.
func readUSRestrictionsOwner(ctx context.Context, turn *commandTurn, a *usOwnerAuthorization, ownerID string) (out usRestrictionsOwnerFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if a == nil {
		return out, ErrInvalid
	}
	i := a.proof.witness.issuer
	row, err := usRestrictionsRow(ctx, turn, usRestrictionsOwner, ownerID, i.server, i.account, i.memberIncarnation, i.profile, i.profileLifetime, i.installation)
	if err != nil {
		return out, err
	}
	var inc []byte
	if err = row.Scan(&inc, &out.originalFamily, &out.lastSequence, &out.acknowledgedSequence, &out.reclaimed, &out.count, &out.bytes, &out.retiredMS); errors.Is(err, sql.ErrNoRows) {
		return out, nil
	} else if err != nil {
		return out, err
	}
	if len(inc) != 16 || !usIdentifier(out.originalFamily) || out.lastSequence < 0 || out.acknowledgedSequence < 0 || out.acknowledgedSequence > out.lastSequence || out.lastSequence-out.acknowledgedSequence > 1 || out.reclaimed < 0 || out.reclaimed > out.acknowledgedSequence || out.count < out.lastSequence-out.acknowledgedSequence || out.bytes < 0 || out.retiredMS < 0 || (out.count == 0) != (out.bytes == 0) {
		return out, ErrResolveRequired
	}
	out.exists = true
	copy(out.incarnation[:], inc)
	return out, nil
}

type usRestrictionsCustody struct {
	owner    usRestrictionsOwnerFacts
	decision receiptDecision
	binding  receiptFacts
}

// Shared fixed-width custody only. The concrete purpose owns its own native
// result codec/decoder; the reserved US namespace grants no result authority.
func lookupUSRestrictionsCustody(ctx context.Context, turn *commandTurn, a *usOwnerAuthorization, q usRestrictionsReceiptRequest) (out usRestrictionsCustody, err error) {
	defer func() {
		out, err = finishCommand(ctx, commandLifetime(turn), out, err)
		// A final current-source denial REPLACES the body outcome. Joining it
		// would let a benign conflict also match the denial class (or the
		// reverse); the output stays zeroed on denial.
		if denial := a.recheck(ctx, turn); denial != nil {
			out, err = usRestrictionsCustody{}, denial
		}
	}()
	// An earlier prefix in the same frame may have advanced a source row.
	// Recheck native current authority before any original owner or payload read.
	if err = a.recheck(ctx, turn); err != nil {
		return out, err
	}
	if !usIdentifier(q.ownerID) || usRestrictionsReceiptKind(q.kind) != nil || q.kind != turn.transaction.descriptor.ID || q.reference.Identity.Sequence <= 0 || q.reference.Identity.Incarnation == ([16]byte{}) || q.reference.Identity.OperationID == ([16]byte{}) || q.reference.RequestDigest == (RequestDigest{}) {
		return out, ErrInvalid
	}
	if err = a.budget.check(ctx, turn, 0); err != nil {
		return out, err
	}
	out.owner, err = readUSRestrictionsOwner(ctx, turn, a, q.ownerID)
	if err != nil {
		return out, err
	}
	expected := receiptIdentity{q.reference.Identity, q.reference.RequestDigest}
	if !out.owner.exists || out.owner.incarnation != expected.command.Incarnation {
		_, err = classifyReceipt(expected, q.kind, out.owner.receiptOwnerFacts, receiptFacts{})
		return out, err
	}
	binding, err := readUSRestrictionsBinding(ctx, turn, q)
	if err != nil {
		return out, err
	}
	out.decision, err = classifyReceipt(expected, q.kind, out.owner.receiptOwnerFacts, binding)
	if err == nil && out.decision == receiptFresh && out.owner.retiredMS != 0 {
		return out, errors.Join(ErrResolveRequired, ErrOwnerUnavailable)
	}
	if err != nil {
		return out, err
	}
	out.binding = binding
	return out, nil
}

// Current stamped authority MUST precede every native owner/receipt read. The
// shared classifier sees only the ORIGINAL identity; fresh target CAS is absent,
// so replay never touches current restrictions.
func lookupUSRestrictions(ctx context.Context, turn *commandTurn, a *usOwnerAuthorization, q usRestrictionsReceiptRequest) (out usRestrictionsReceiptLookup, err error) {
	defer func() {
		out, err = finishCommand(ctx, commandLifetime(turn), out, err)
		if denial := a.recheck(ctx, turn); denial != nil {
			out, err = usRestrictionsReceiptLookup{}, denial
		}
	}()
	if err = usRestrictionsReceiptKind(q.kind); err != nil {
		return out, err
	}
	custody, e := lookupUSRestrictionsCustody(ctx, turn, a, q)
	if e != nil {
		return out, e
	}
	out.owner, out.decision = custody.owner, custody.decision
	if out.decision != receiptReplay {
		return out, nil
	}
	binding := custody.binding
	expected := receiptIdentity{q.reference.Identity, q.reference.RequestDigest}
	if binding.resultCount < 0 || binding.resultCount > usMaxRestrictionLabels {
		return out, ErrResolveRequired
	}
	var bytes int64
	row, err := usRestrictionsRow(ctx, turn, usRestrictionsReceiptOctets, q.ownerID, expected.command.Incarnation[:], expected.command.Sequence)
	if err != nil {
		return out, err
	}
	if err = row.Scan(&bytes); err != nil || bytes <= 0 {
		return out, errors.Join(ErrResolveRequired, err)
	}
	if codec, _ := usRestrictionsCodec(q.kind); bytes != usRestrictionsHeaderBytes(q.ownerID, codec) {
		return out, ErrResolveRequired
	}
	if err = a.budget.check(ctx, turn, bytes); err != nil {
		return out, err
	}
	var rowBytes int64
	row, err = usRestrictionsRow(ctx, turn, usRestrictionsReceiptResultSize, q.ownerID, expected.command.Incarnation[:], expected.command.Sequence)
	if err != nil {
		return out, err
	}
	if err = row.Scan(&rowBytes); err != nil || rowBytes <= 0 || rowBytes > maxCanonicalResultBytes-bytes {
		return out, errors.Join(ErrResolveRequired, err)
	}
	bytes += rowBytes
	if err = a.budget.check(ctx, turn, bytes); err != nil {
		return out, err
	}
	lengths, err := readUSRestrictionsLabelOctets(ctx, turn, q, binding.resultCount)
	if err != nil {
		return out, err
	}
	for _, n := range lengths {
		if n <= 0 || n > maxCanonicalResultBytes-bytes {
			return out, &Refusal{Code: RefusalCapacity}
		}
		bytes += n
	}
	if err = a.budget.check(ctx, turn, bytes); err != nil {
		return out, err
	}
	header, err := readUSRestrictionsHeader(ctx, turn, q, binding.resultCount)
	if err != nil {
		return out, err
	}
	value, err := readUSRestrictionsResultRow(ctx, turn, q)
	if err != nil {
		return out, err
	}
	if int64(len(q.ownerID)+16+len(value.account)+len(value.memberIncarnation)+len(value.profile)+len(value.profileLifetime)+len(value.ratingSystem)+len(value.maximumAgeRating)) != rowBytes {
		return out, ErrResolveRequired
	}
	labels, err := readUSRestrictionsLabels(ctx, turn, q, lengths)
	if err != nil {
		return out, err
	}
	value.labels, value.appliedMS = labels, header.appliedMS
	if err = validateUSRestrictionsResult(value); err != nil {
		return out, err
	}
	out.digest, err = usRestrictionsResultDigest(expected.command, expected.digest, value)
	if err != nil || out.digest != ResultDigest(binding.resultDigest) {
		return out, errors.Join(ErrResolveRequired, err)
	}
	out.result = value
	return out, turn.checkLifetimes(ctx)
}

// usRestrictionsHeaderBytes is the exact fixed charge recorded by the header
// row: owner plus the four BLOBs, the codec and no nullable target/body column.
func usRestrictionsHeaderBytes(ownerID, codec string) int64 {
	return int64(len(ownerID) + 96 + len(codec))
}

func readUSRestrictionsHeader(ctx context.Context, turn *commandTurn, q usRestrictionsReceiptRequest, count int64) (out usRestrictionsSavedResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	row, err := usRestrictionsRow(ctx, turn, usRestrictionsReceiptHeader, q.ownerID, q.reference.Identity.Incarnation[:], q.reference.Identity.Sequence)
	if err != nil {
		return out, err
	}
	var op, request, digest []byte
	var kind, version, actualCount, bytes, appliedMS int64
	var codec string
	var targetAccount, targetMember, targetProfile, targetLifetime, key sql.NullString
	var revision sql.NullInt64
	var nonce, body []byte
	if err = row.Scan(&op, &request, &kind, &version, &codec, &appliedMS, &digest, &actualCount, &bytes, &targetAccount, &targetMember, &targetProfile, &targetLifetime, &revision, &key, &nonce, &body); err != nil {
		return out, errors.Join(ErrResolveRequired, err)
	}
	wantCodec, _ := usRestrictionsCodec(q.kind)
	if len(op) != 16 || len(request) != 32 || len(digest) != 32 || !equalBytes(op, q.reference.Identity.OperationID[:]) || !equalBytes(request, q.reference.RequestDigest[:]) ||
		kind != int64(q.kind) || version != 1 || codec != wantCodec || actualCount != count || count < 0 || count > usMaxRestrictionLabels || bytes <= 0 ||
		targetAccount.Valid || targetMember.Valid || targetProfile.Valid || targetLifetime.Valid || revision.Valid || key.Valid || nonce != nil || body != nil {
		return out, ErrResolveRequired
	}
	if bytes != usRestrictionsHeaderBytes(q.ownerID, codec) {
		return out, ErrResolveRequired
	}
	out.ownerID, out.kind, out.appliedMS = q.ownerID, q.kind, appliedMS
	return out, nil
}

func readUSRestrictionsResultRow(ctx context.Context, turn *commandTurn, q usRestrictionsReceiptRequest) (out usRestrictionsSavedResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	row, err := usRestrictionsRow(ctx, turn, usRestrictionsReceiptResult, q.ownerID, q.reference.Identity.Incarnation[:], q.reference.Identity.Sequence)
	if err != nil {
		return out, err
	}
	var unrated, downloads, live, dvr, together int64
	out.ownerID, out.kind = q.ownerID, q.kind
	if err = row.Scan(&out.account, &out.memberIncarnation, &out.profile, &out.profileLifetime, &out.revision, &out.ratingSystem, &out.maximumAgeRating, &out.maximumAge, &unrated, &downloads, &live, &dvr, &together); err != nil {
		return out, errors.Join(ErrResolveRequired, err)
	}
	if (unrated != 0 && unrated != 1) || (downloads != 0 && downloads != 1) || (live != 0 && live != 1) || (dvr != 0 && dvr != 1) || (together != 0 && together != 1) {
		return out, ErrResolveRequired
	}
	out.allowUnrated, out.allowDownloads, out.allowLiveTV, out.allowDVR, out.allowWatchTogether = unrated == 1, downloads == 1, live == 1, dvr == 1, together == 1
	return out, nil
}

func readUSRestrictionsLabelOctets(ctx context.Context, turn *commandTurn, q usRestrictionsReceiptRequest, count int64) (out []int64, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	rows, err := usRestrictionsRows(ctx, turn, usRestrictionsReceiptLabelSize, q.ownerID, q.reference.Identity.Incarnation[:], q.reference.Identity.Sequence, count+1)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	out = make([]int64, 0, int(count))
	for rows.Next() {
		var ordinal, n int64
		if err = rows.Scan(&ordinal, &n); err != nil {
			return nil, err
		}
		if ordinal != int64(len(out)) || ordinal >= count || n <= 0 {
			return nil, ErrResolveRequired
		}
		out = append(out, n)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if int64(len(out)) != count {
		return nil, ErrResolveRequired
	}
	return out, nil
}

func readUSRestrictionsLabels(ctx context.Context, turn *commandTurn, q usRestrictionsReceiptRequest, lengths []int64) (out []string, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	rows, err := usRestrictionsRows(ctx, turn, usRestrictionsReceiptLabels, q.ownerID, q.reference.Identity.Incarnation[:], q.reference.Identity.Sequence, int64(len(lengths)+1))
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	out = make([]string, 0, len(lengths))
	for rows.Next() {
		var ordinal int64
		var label string
		if err = rows.Scan(&ordinal, &label); err != nil {
			return nil, err
		}
		if ordinal != int64(len(out)) || ordinal >= int64(len(lengths)) || int64(len(q.ownerID)+16+len(label)) != lengths[len(out)] {
			return nil, ErrResolveRequired
		}
		out = append(out, label)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(out) != len(lengths) {
		return nil, ErrResolveRequired
	}
	return out, nil
}

// Native immutable save: receipt header, the typed restriction row, the ordered
// label cohort, then the exact owner byte/count debt in ONE frame. No event or
// source CAS here; the dispatcher owns the replacement and the final release.
func storeUSRestrictionsResult(ctx context.Context, turn *commandTurn, a *usOwnerAuthorization, q usRestrictionsReceiptRequest, v usRestrictionsSavedResult, digest ResultDigest) (charged int64, err error) {
	defer func() {
		charged, err = finishCommand(ctx, commandLifetime(turn), charged, err)
		if denial := a.recheck(ctx, turn); denial != nil {
			charged, err = 0, denial
		}
	}()
	if turn == nil || turn.transaction == nil || q.kind != v.kind || turn.transaction.descriptor.ID != q.kind || v.ownerID != q.ownerID || q.reference.Identity.Sequence <= 0 || q.reference.Identity.Incarnation == ([16]byte{}) || q.reference.Identity.OperationID == ([16]byte{}) || q.reference.RequestDigest == (RequestDigest{}) || digest == (ResultDigest{}) {
		return 0, ErrInvalid
	}
	if err = a.recheck(ctx, turn); err != nil {
		return 0, err
	}
	if err = usRestrictionsReceiptKind(q.kind); err != nil {
		return 0, err
	}
	// Owner facts are never caller-supplied. Re-read the owner under the CURRENT
	// issuer scope and require the original identity to classify fresh in the
	// same frame, so a stale prefix or a foreign issuer cannot write.
	custody, err := lookupUSRestrictionsCustody(ctx, turn, a, q)
	if err != nil {
		return 0, err
	}
	if custody.decision != receiptFresh {
		return 0, ErrConflict
	}
	owner := custody.owner
	if !owner.exists || owner.incarnation != q.reference.Identity.Incarnation {
		return 0, ErrResolveRequired
	}
	codec, _ := usRestrictionsCodec(q.kind)
	expectedBytes, err := usRestrictionsResultVariableBytes(v)
	if err != nil || expectedBytes <= 0 {
		return 0, errors.Join(ErrResolveRequired, err)
	}
	if actual, err := usRestrictionsResultDigest(q.reference.Identity, q.reference.RequestDigest, v); err != nil || actual != digest {
		return 0, errors.Join(ErrResolveRequired, err)
	}
	// The SAME operation may not re-store its result at another retained
	// sequence. Detect that explicitly so a UNIQUE violation never surfaces as a
	// raw SQLite error, and never overwrite the original receipt.
	opRow, err := usRestrictionsRow(ctx, turn, usRestrictionsReceiptOperation, q.ownerID, q.reference.Identity.Incarnation[:], q.reference.Identity.OperationID[:])
	if err != nil {
		return 0, err
	}
	var opSequence int64
	switch scanErr := opRow.Scan(&opSequence); {
	case scanErr == nil:
		if opSequence == q.reference.Identity.Sequence {
			return 0, ErrResolveRequired
		}
		return 0, ErrConflict
	case errors.Is(scanErr, sql.ErrNoRows):
	default:
		return 0, scanErr
	}
	row, err := usRestrictionsRow(ctx, turn, usRestrictionsReceiptInsert, q.ownerID, q.reference.Identity.Incarnation[:], q.reference.Identity.Sequence,
		q.reference.Identity.OperationID[:], q.reference.RequestDigest[:], int64(q.kind), int64(1), codec, v.appliedMS, digest[:], int64(len(v.labels)))
	if err != nil {
		return 0, err
	}
	if err = row.Scan(&charged); err != nil {
		return 0, err
	}
	if want := usRestrictionsHeaderBytes(q.ownerID, codec); charged != want {
		return 0, ErrResolveRequired
	}
	row, err = usRestrictionsRow(ctx, turn, usRestrictionsResultInsert, q.ownerID, q.reference.Identity.Incarnation[:], q.reference.Identity.Sequence,
		v.account, v.memberIncarnation, v.profile, v.profileLifetime, v.revision, v.ratingSystem, v.maximumAgeRating, v.maximumAge,
		usRestrictionsBool(v.allowUnrated), usRestrictionsBool(v.allowDownloads), usRestrictionsBool(v.allowLiveTV), usRestrictionsBool(v.allowDVR), usRestrictionsBool(v.allowWatchTogether))
	if err != nil {
		return 0, err
	}
	var n int64
	if err = row.Scan(&n); err != nil {
		return 0, err
	}
	if want := int64(len(q.ownerID) + 16 + len(v.account) + len(v.memberIncarnation) + len(v.profile) + len(v.profileLifetime) + len(v.ratingSystem) + len(v.maximumAgeRating)); n != want || n > math.MaxInt64-charged {
		return 0, ErrResolveRequired
	}
	charged += n
	for ordinal, label := range v.labels {
		row, err = usRestrictionsRow(ctx, turn, usRestrictionsLabelInsert, q.ownerID, q.reference.Identity.Incarnation[:], q.reference.Identity.Sequence, int64(ordinal), label)
		if err != nil {
			return 0, err
		}
		if err = row.Scan(&n); err != nil {
			return 0, err
		}
		if n <= 0 || n != int64(len(q.ownerID)+16+len(label)) || n > math.MaxInt64-charged {
			return 0, ErrResolveRequired
		}
		charged += n
	}
	row, err = usRestrictionsRow(ctx, turn, usRestrictionsOwnerAdvance, q.reference.Identity.Sequence, charged, q.ownerID, q.reference.Identity.Incarnation[:], owner.lastSequence, owner.acknowledgedSequence, owner.count, owner.bytes)
	if err != nil {
		return 0, err
	}
	var last, count, bytes int64
	if err = row.Scan(&last, &count, &bytes); err != nil {
		return 0, errors.Join(ErrConflict, err)
	}
	if last != q.reference.Identity.Sequence || count != owner.count+1 || bytes != owner.bytes+charged || charged != expectedBytes {
		return 0, ErrResolveRequired
	}
	return charged, turn.checkLifetimes(ctx)
}

// Floor-only ACK: it advances one durable sequence and refunds/deletes nothing.
// It cannot prove or return the original result; retrieval stays a separate
// replay classification before and after the floor.
func acknowledgeUSRestrictionsAfterAuthority(ctx context.Context, turn *commandTurn, a *usOwnerAuthorization, q usRestrictionsReceiptRequest) (ack int64, err error) {
	defer func() {
		ack, err = finishCommand(ctx, commandLifetime(turn), ack, err)
		if denial := a.recheck(ctx, turn); denial != nil {
			ack, err = 0, denial
		}
	}()
	lookup, err := lookupUSRestrictions(ctx, turn, a, q)
	if err != nil {
		return 0, err
	}
	// Retirement is a retention boundary. An ACK may not resurrect a retired
	// owner, and its floor stays frozen at the retirement-time value.
	if lookup.owner.retiredMS != 0 {
		return 0, ErrOwnerUnavailable
	}
	if lookup.decision == receiptRetired {
		return lookup.owner.acknowledgedSequence, nil
	}
	if lookup.decision != receiptReplay || q.reference.Identity.Sequence != lookup.owner.lastSequence {
		return 0, ErrConflict
	}
	row, err := usRestrictionsRow(ctx, turn, usRestrictionsOwnerAck, q.reference.Identity.Sequence, q.ownerID, q.reference.Identity.Incarnation[:], lookup.owner.lastSequence, lookup.owner.acknowledgedSequence)
	if err != nil {
		return 0, err
	}
	if err = row.Scan(&ack); err != nil {
		return 0, errors.Join(ErrConflict, err)
	}
	if ack != q.reference.Identity.Sequence {
		return 0, ErrResolveRequired
	}
	return ack, turn.checkLifetimes(ctx)
}

func usRestrictionsRows(ctx context.Context, turn *commandTurn, id stmt.ID, args ...any) (*commandRows, error) {
	if turn == nil {
		return nil, ErrInvalid
	}
	step, ok := turn.steps[id]
	if !ok {
		return nil, invalid("us_restrictions_statement")
	}
	return turn.query(ctx, step.statementStep, args...)
}
