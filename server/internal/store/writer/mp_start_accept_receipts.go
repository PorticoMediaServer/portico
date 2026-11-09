package writer

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"io"
)

// One MP start receipt root (the MP-07 mp_start_command_receipts table) carries
// every terminal MP start command. Accept/retire/timeout therefore take the next
// consecutive sequence on the same preparation owner; a predecessor must be
// ACKed by MP-29's command 780 before the next terminal command is admitted.
// ACK never refunds storage; reclaim is a separate bounded turn. Saved bytes are
// returned only after fresh disclosure in the SAME turn.

const mpStartAcceptPayloadCap = 2048

type mpStartAcceptReceipt struct {
	identity     CommandIdentity
	request      RequestDigest
	kind         CommandID
	appliedMS    int64
	digest       ResultDigest
	storedBytes  int64
	payloadBytes int64
	value        mpStartAcceptValue
}

func mpStartAcceptReadReceipt(ctx context.Context, t *commandTurn, id string, owner mpStartPrepOwner, reference ReceiptReference) (out receiptFacts, saved mpStartAcceptReceipt, err error) {
	defer func() {
		x, e := finishCommand(ctx, commandLifetime(t), struct {
			O receiptFacts
			S mpStartAcceptReceipt
		}{out, saved}, err)
		out, saved, err = x.O, x.S, e
	}()
	if !mpPrepTexts(id) {
		return out, saved, ErrInvalid
	}
	identity := reference.Identity
	row, err := mpPrepRow(ctx, t, "mp.accept.receipt_header", id, identity.Incarnation[:], identity.Sequence)
	if err != nil {
		return out, saved, err
	}
	var op, request, digest []byte
	var storedKind CommandID
	var version, count, applied, payloadSize, storedBytes int64
	err = row.Scan(&op, &storedKind, &request, &version, &count, &digest, &applied, &payloadSize, &storedBytes)
	if errors.Is(err, sql.ErrNoRows) {
		return out, saved, nil
	}
	if err != nil {
		return out, saved, err
	}
	if len(op) != 16 || len(request) != 32 || len(digest) != 32 || version != 1 || count != 1 ||
		applied <= 0 || payloadSize <= 0 || payloadSize > mpStartAcceptPayloadCap ||
		storedBytes != int64(len(id)+16+16+32+32)+payloadSize {
		return out, saved, ErrResolveRequired
	}
	out.exists = true
	out.identity.command = identity
	copy(out.identity.command.OperationID[:], op)
	copy(out.identity.digest[:], request)
	out.kind = storedKind
	out.resultCount = count
	copy(out.resultDigest[:], digest)
	saved = mpStartAcceptReceipt{identity: identity, request: RequestDigest(request), kind: storedKind,
		appliedMS: applied, digest: ResultDigest(digest), storedBytes: storedBytes, payloadBytes: payloadSize}
	return out, saved, nil
}

func mpStartAcceptLoadPayload(ctx context.Context, t *commandTurn, id string, saved *mpStartAcceptReceipt) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(t), struct{}{}, err) }()
	if saved == nil || saved.payloadBytes <= 0 || saved.payloadBytes > mpStartAcceptPayloadCap {
		return ErrResolveRequired
	}
	row, err := mpPrepRow(ctx, t, "mp.accept.receipt_payload", id, saved.identity.Incarnation[:], saved.identity.Sequence)
	if err != nil {
		return err
	}
	var payload []byte
	if err = row.Scan(&payload); err != nil {
		return err
	}
	if int64(len(payload)) != saved.payloadBytes {
		return ErrResolveRequired
	}
	value, e := decodeMPStartAcceptPayload(saved.identity, saved.request, payload, saved.digest)
	if e != nil {
		return e
	}
	saved.value = value
	return nil
}

func mpStartAcceptWriteReceipt(ctx context.Context, t *commandTurn, id string, owner *mpStartPrepOwner, kind CommandID, reference ReceiptReference, value mpStartAcceptValue, applied int64) (out ResultDigest, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if owner == nil || !mpPrepTexts(id) {
		return out, ErrInvalid
	}
	payload, digest, err := mpStartAcceptPayload(reference.Identity, reference.RequestDigest, value)
	if err != nil {
		return out, err
	}
	expected := int64(len(id) + 16 + 16 + 32 + 32 + len(payload))
	if expected > 65536 || owner.bytes > 65536-expected {
		return out, &Refusal{Code: RefusalCapacity}
	}
	row, err := mpPrepRow(ctx, t, "mp.accept.receipt_insert", id, reference.Identity.Incarnation[:],
		reference.Identity.Sequence, reference.Identity.OperationID[:], int64(kind), reference.RequestDigest[:],
		int64(1), int64(1), digest[:], payload, applied)
	if err != nil {
		return out, err
	}
	var actual int64
	if err = row.Scan(&actual); err != nil {
		return out, err
	}
	if actual != expected {
		return out, ErrResolveRequired
	}
	row, err = mpPrepRow(ctx, t, "mp.accept.debt", reference.Identity.Sequence, actual, id,
		reference.Identity.Incarnation[:], owner.last, owner.ack, owner.revision, owner.rows, owner.bytes, owner.control)
	if err != nil {
		return out, err
	}
	var last, revision, rows, bytes, control int64
	if err = row.Scan(&last, &revision, &rows, &bytes, &control); err != nil {
		return out, err
	}
	if last != reference.Identity.Sequence || revision != owner.revision+1 || rows != owner.rows+1 ||
		bytes != owner.bytes+actual || control != owner.control {
		return out, ErrResolveRequired
	}
	return digest, nil
}

func mpStartAcceptValueKind(state string) CommandID {
	switch state {
	case "accepted":
		return MPStartPrepAccept
	case "retired":
		return MPStartPrepRetire
	}
	return 0
}

func preflightMPStartAcceptValue(v mpStartAcceptValue) error {
	if !v.valid(mpStartAcceptValueKind(v.State)) || v.Revision > 1<<40 || v.DeadlineMS > 1<<44 || v.AppliedMS > 1<<44 ||
		len(v.Preparation) > 256 || len(v.Execution) > 256 || len(v.Custody) > 256 || len(v.Session) > 256 ||
		len(v.LegacySession) > 256 || len(v.Snapshot) > 256 || v.LegacyGeneration < 0 {
		return invalid("mp_start_accept_result")
	}
	return nil
}

func writeMPStartAcceptValue(e *requestEncoder, identity CommandIdentity, request RequestDigest, v mpStartAcceptValue) error {
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1})
	e.text("mp_start_accept")
	e.bytes(identity.Incarnation[:])
	e.number(uint64(identity.Sequence))
	e.bytes(identity.OperationID[:])
	e.bytes(request[:])
	for _, s := range []string{v.Preparation, v.State, v.Execution, v.Custody, v.Session, v.LegacySession, v.Snapshot} {
		e.text(s)
	}
	for _, n := range []int64{v.Revision, v.DeadlineMS, v.LegacyGeneration, v.AppliedMS, v.PhysicalExited, v.Cleaned} {
		e.number(uint64(n))
	}
	return e.err
}

func mpStartAcceptPayload(identity CommandIdentity, request RequestDigest, v mpStartAcceptValue) ([]byte, ResultDigest, error) {
	digest, err := canonicalResultDigest(identity, request, v)
	if err != nil {
		return nil, ResultDigest{}, err
	}
	b := &mpCodecBuffer{}
	e := requestEncoder{h: b}
	if err = encodeCanonicalResult(&e, identity, request, v); err != nil {
		return nil, ResultDigest{}, err
	}
	if b.Len() > mpStartAcceptPayloadCap {
		return nil, ResultDigest{}, ErrCap
	}
	return b.Bytes(), digest, nil
}

func decodeMPStartAcceptPayload(identity CommandIdentity, request RequestDigest, p []byte, digest ResultDigest) (mpStartAcceptValue, error) {
	var v mpStartAcceptValue
	if len(p) == 0 || len(p) > mpStartAcceptPayloadCap {
		return v, ErrResolveRequired
	}
	r := bytes.NewReader(p)
	prefix := make([]byte, len(canonicalResultCodec)+2)
	if _, e := io.ReadFull(r, prefix); e != nil || !bytes.Equal(prefix, append([]byte(canonicalResultCodec), 0, 1)) {
		return v, ErrResolveRequired
	}
	text := func() (string, error) {
		var n uint64
		if binary.Read(r, binary.BigEndian, &n) != nil || n > 4096 || n > uint64(r.Len()) {
			return "", ErrResolveRequired
		}
		b := make([]byte, int(n))
		_, e := io.ReadFull(r, b)
		return string(b), e
	}
	name, e := text()
	if e != nil || name != "mp_start_accept" {
		return v, ErrResolveRequired
	}
	var inc, op [16]byte
	var seq uint64
	var requestBytes [32]byte
	if _, e = io.ReadFull(r, inc[:]); e != nil {
		return v, ErrResolveRequired
	}
	if binary.Read(r, binary.BigEndian, &seq) != nil {
		return v, ErrResolveRequired
	}
	if _, e = io.ReadFull(r, op[:]); e != nil {
		return v, ErrResolveRequired
	}
	if _, e = io.ReadFull(r, requestBytes[:]); e != nil {
		return v, ErrResolveRequired
	}
	if inc != identity.Incarnation || op != identity.OperationID || seq != uint64(identity.Sequence) || requestBytes != [32]byte(request) {
		return v, ErrResolveRequired
	}
	for _, s := range []*string{&v.Preparation, &v.State, &v.Execution, &v.Custody, &v.Session, &v.LegacySession, &v.Snapshot} {
		*s, e = text()
		if e != nil {
			return mpStartAcceptValue{}, ErrResolveRequired
		}
	}
	for _, n := range []*int64{&v.Revision, &v.DeadlineMS, &v.LegacyGeneration, &v.AppliedMS, &v.PhysicalExited, &v.Cleaned} {
		if binary.Read(r, binary.BigEndian, n) != nil {
			return mpStartAcceptValue{}, ErrResolveRequired
		}
	}
	if r.Len() != 0 {
		return mpStartAcceptValue{}, ErrResolveRequired
	}
	actual, got, e := mpStartAcceptPayload(identity, request, v)
	if e != nil || got != digest || !bytes.Equal(actual, p) {
		return mpStartAcceptValue{}, ErrResolveRequired
	}
	return v, nil
}

// completeMPStartAcceptResult reads the native post-effect rows in the SAME turn.
// A saved result always comes from the immutable receipt payload instead.
func completeMPStartAcceptResult(ctx context.Context, t *commandTurn, kind CommandID, in mpStartAcceptValue) (out mpStartAcceptValue, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	row, err := mpPrepRow(ctx, t, "mp.accept.native", in.Preparation)
	if err != nil {
		return out, err
	}
	var sealed int64
	out.Preparation = in.Preparation
	out.AppliedMS = in.AppliedMS
	if err = row.Scan(&out.State, &out.Revision, &out.Execution, &sealed, &out.DeadlineMS, &out.Custody,
		&out.PhysicalExited, &out.Cleaned, &out.Session, &out.LegacySession, &out.LegacyGeneration, &out.Snapshot); err != nil {
		return out, err
	}
	if sealed < 0 || sealed > 1 {
		return out, ErrResolveRequired
	}
	if !out.valid(kind) {
		return out, ErrConflict
	}
	if out.State != in.State || out.Revision != in.Revision || out.Execution != in.Execution || out.Custody != in.Custody ||
		out.DeadlineMS != in.DeadlineMS || out.Session != in.Session || out.LegacyGeneration != in.LegacyGeneration ||
		out.Snapshot != in.Snapshot || out.PhysicalExited != in.PhysicalExited || out.Cleaned != in.Cleaned {
		return out, ErrConflict
	}
	return out, nil
}
