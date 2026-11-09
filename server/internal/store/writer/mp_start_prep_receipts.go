package writer

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"io"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// One MP start receipt root on the preparation owner. The adopted F schema lets
// one owner carry at most one unacknowledged receipt, so claim (701) and seal
// (702) take consecutive sequences and 702 needs the 701 floor acknowledged by
// command 780 (MP-29, not written). ACK never refunds storage; reclaim is a
// separate bounded turn. Saved bytes are returned only after fresh disclosure.

const mpStartPrepPayloadCap = 2048

type mpStartPrepOwner struct {
	incarnation                [16]byte
	last, ack                  int64
	revision, rows, bytes      int64
	control                    int64
	retirement                 string
	device, item, root         string
	part                       int64
	deadlineMS, tokenExpiresMS int64
	createdMS, nowMS           int64
	replaces                   string
	replacesGeneration         int64
	state, execution           string
	lifecycleRevision          int64
	sealed                     bool
	actor                      mpPrepActor
	source                     mpPrepSource
	queue                      mpPrepQueue
	ids                        mpStartPrepStableIDs
	resultDigest               string
	stagedRows, stagedBytes    int64
	physicalExited, cleaned    int64
	cleanupCursor              int64
}

func (o mpStartPrepOwner) receiptFacts() receiptOwnerFacts {
	return receiptOwnerFacts{exists: true, incarnation: o.incarnation,
		lastSequence: o.last, acknowledgedSequence: o.ack}
}

func (o mpStartPrepOwner) valid() bool {
	return o.last >= 0 && o.ack >= 0 && o.ack <= o.last && o.last-o.ack <= 1 &&
		o.revision > 0 && o.rows >= 0 && o.bytes >= 0 && o.control > 0 &&
		(o.rows == 0) == (o.bytes == 0) && mpPrepTexts(o.device, o.item, o.root) &&
		mpPrepText(o.replaces, true) && o.part >= 0 && o.deadlineMS > 0 && o.tokenExpiresMS >= o.deadlineMS &&
		o.createdMS > 0 && mpPrepRevision(o.lifecycleRevision) &&
		(o.retirement == "live" || o.retirement == "draining" || o.retirement == "retired") &&
		o.actor.valid(0) && o.source.valid() && o.queue.valid() && o.ids.valid() &&
		(o.physicalExited == 0 || o.physicalExited == 1) && (o.cleaned == 0 || o.cleaned == 1) && o.cleaned <= o.physicalExited
}

func readMPStartPrepOwner(ctx context.Context, t *commandTurn, id, device string, out *mpStartPrepOwner) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(t), struct{}{}, err) }()
	if out == nil || !mpPrepTexts(id, device) {
		return ErrInvalid
	}
	var incarnation []byte
	row, err := mpPrepRow(ctx, t, "mp.start.owner", id, device)
	if err != nil {
		return err
	}
	err = row.Scan(&incarnation, &out.last, &out.ack, &out.revision, &out.rows, &out.bytes, &out.control,
		&out.retirement, &out.device, &out.item, &out.part, &out.deadlineMS, &out.tokenExpiresMS,
		&out.replaces, &out.replacesGeneration, &out.createdMS, &out.root)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrDisclosureDenied
	}
	if err != nil {
		return err
	}
	if len(incarnation) != 16 || out.device != device {
		return ErrDisclosureDenied
	}
	copy(out.incarnation[:], incarnation)
	row, err = mpPrepRow(ctx, t, "mp.start.lifecycle_row", id)
	if err != nil {
		return err
	}
	var sealed int64
	if err = row.Scan(&out.state, &out.lifecycleRevision, &out.execution, &sealed); err != nil {
		return errors.Join(ErrResolveRequired, err)
	}
	if sealed < 0 || sealed > 1 {
		return ErrResolveRequired
	}
	out.sealed = sealed == 1
	row, err = mpPrepRow(ctx, t, "mp.start.actor_row", id)
	if err != nil {
		return err
	}
	if err = row.Scan(&out.actor.server, &out.actor.authority, &out.actor.account, &out.actor.profile,
		&out.actor.device, &out.actor.family, &out.actor.grant, &out.actor.sessionHash,
		&out.actor.epoch, &out.actor.tokenGeneration, &out.actor.horizonMS); err != nil {
		return errors.Join(ErrResolveRequired, err)
	}
	row, err = mpPrepRow(ctx, t, "mp.start.source_row", id)
	if err != nil {
		return err
	}
	if err = row.Scan(&out.source.library, &out.source.source, &out.source.asset, &out.source.object,
		&out.source.revision, &out.source.root, &out.source.incarnation, &out.source.remote,
		&out.source.configuration, &out.source.policy, &out.source.authority,
		&out.source.libraryGeneration, &out.source.configurationGeneration, &out.source.profileRevision,
		&out.source.size, &out.source.modifiedNS); err != nil {
		return errors.Join(ErrResolveRequired, err)
	}
	row, err = mpPrepRow(ctx, t, "mp.start.queue_row", id)
	if err != nil {
		return err
	}
	if err = row.Scan(&out.queue.id, &out.queue.entry, &out.queue.ownerKind, &out.queue.ownerID,
		&out.queue.revision, &out.queue.custody); err != nil {
		return errors.Join(ErrResolveRequired, err)
	}
	row, err = mpPrepRow(ctx, t, "mp.start.results_row", id)
	if err != nil {
		return err
	}
	if err = row.Scan(&out.ids.session, &out.ids.legacySession, &out.ids.legacyGeneration, &out.ids.snapshot); err != nil {
		return errors.Join(ErrResolveRequired, err)
	}
	if !out.valid() {
		return ErrResolveRequired
	}
	return nil
}

// readMPStartPrepExecution reads the exact execution commitment. A missing row
// before claim is normal; a missing row after claim is a resolution failure.
func readMPStartPrepExecution(ctx context.Context, t *commandTurn, id string) (out mpStartPrepOwner, found bool, err error) {
	defer func() {
		x, e := finishCommand(ctx, commandLifetime(t), struct {
			O mpStartPrepOwner
			F bool
		}{out, found}, err)
		out, found, err = x.O, x.F, e
	}()
	row, err := mpPrepRow(ctx, t, "mp.start.execution_row", id)
	if err != nil {
		return out, false, err
	}
	err = row.Scan(&out.execution, &out.resultDigest, &out.stagedRows, &out.stagedBytes,
		&out.physicalExited, &out.cleaned, &out.cleanupCursor)
	if errors.Is(err, sql.ErrNoRows) {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	if out.execution == "" || out.stagedRows < 0 || out.stagedRows > 256 || out.stagedBytes < 0 || out.stagedBytes > 1<<20 ||
		out.cleanupCursor < 0 || (out.physicalExited != 0 && out.physicalExited != 1) || (out.cleaned != 0 && out.cleaned != 1) ||
		(out.resultDigest != "" && !lowerHex(out.resultDigest, 64)) {
		return out, false, ErrResolveRequired
	}
	return out, true, nil
}

func mpStartPrepReceiptDefinitions() []stmt.Definition {
	defs := []stmt.Definition{
		mpPrepDefinition("mp.start.receipt_header", `SELECT operation_id,command_kind,request_digest,result_version,result_count,result_digest,applied_ms,length(result_payload),variable_bytes FROM mp_start_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`, "sbi", stmt.Read),
		mpPrepDefinition("mp.start.receipt_payload", `SELECT result_payload FROM mp_start_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`, "sbi", stmt.Read),
		mpPrepDefinition("mp.start.receipt_insert", `INSERT INTO mp_start_command_receipts(owner_id,command_incarnation,command_sequence,operation_id,command_kind,request_digest,result_version,result_count,result_digest,result_payload,applied_ms) VALUES(?,?,?,?,?,?,?,?,?,?,?) RETURNING variable_bytes`, "sbibibiibbi", stmt.Write),
		mpPrepDefinition("mp.start.debt", `UPDATE playback_start_preparations INDEXED BY mp_start_command_owner SET mp_command_last_sequence=?,mp_owner_revision=mp_owner_revision+1,mp_receipt_rows=mp_receipt_rows+1,mp_receipt_variable_bytes=mp_receipt_variable_bytes+? WHERE id=? AND mp_command_incarnation=? AND mp_command_last_sequence=? AND mp_command_acknowledged_sequence=? AND mp_owner_revision=? AND mp_receipt_rows=? AND mp_receipt_variable_bytes=? AND mp_control_variable_bytes=? AND mp_retirement_state='live' RETURNING mp_command_last_sequence,mp_owner_revision,mp_receipt_rows,mp_receipt_variable_bytes,mp_control_variable_bytes`, "iisbiiiiii", stmt.Write),
	}
	for i := range defs {
		switch defs[i].ID {
		case "mp.start.receipt_header", "mp.start.receipt_payload":
			defs[i].Plan = stmt.Plan{Hot: true, PrimaryKeys: []string{"mp_start_command_receipts"}}
		case "mp.start.debt":
			defs[i].Plan = stmt.Plan{Hot: true, Indexes: []string{"mp_start_command_owner"}}
		}
	}
	return defs
}

func mpStartPrepReadReceipt(ctx context.Context, t *commandTurn, id string, owner mpStartPrepOwner, reference ReceiptReference) (out receiptFacts, saved mpStartPrepReceipt, err error) {
	defer func() {
		x, e := finishCommand(ctx, commandLifetime(t), struct {
			O receiptFacts
			S mpStartPrepReceipt
		}{out, saved}, err)
		out, saved, err = x.O, x.S, e
	}()
	if !mpPrepTexts(id) {
		return out, saved, ErrInvalid
	}
	identity := reference.Identity
	row, err := mpPrepRow(ctx, t, "mp.start.receipt_header", id, identity.Incarnation[:], identity.Sequence)
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
		applied <= 0 || payloadSize <= 0 || payloadSize > mpStartPrepPayloadCap ||
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
	saved = mpStartPrepReceipt{identity: identity, request: RequestDigest(request), kind: storedKind,
		appliedMS: applied, digest: ResultDigest(digest), storedBytes: storedBytes, payloadBytes: payloadSize}
	return out, saved, nil
}

func mpStartPrepLoadPayload(ctx context.Context, t *commandTurn, id string, saved *mpStartPrepReceipt) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(t), struct{}{}, err) }()
	if saved == nil || saved.payloadBytes <= 0 || saved.payloadBytes > mpStartPrepPayloadCap {
		return ErrResolveRequired
	}
	row, err := mpPrepRow(ctx, t, "mp.start.receipt_payload", id, saved.identity.Incarnation[:], saved.identity.Sequence)
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
	value, e := decodeMPStartPrepPayload(saved.identity, saved.request, payload, saved.digest)
	if e != nil {
		return e
	}
	saved.value = value
	return nil
}

type mpStartPrepReceipt struct {
	identity     CommandIdentity
	request      RequestDigest
	kind         CommandID
	appliedMS    int64
	digest       ResultDigest
	storedBytes  int64
	payloadBytes int64
	value        mpStartPrepValue
}

func mpStartPrepWriteReceipt(ctx context.Context, t *commandTurn, id string, owner *mpStartPrepOwner, kind CommandID, reference ReceiptReference, value mpStartPrepValue, applied int64) (out ResultDigest, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if owner == nil || !mpPrepTexts(id) {
		return out, ErrInvalid
	}
	payload, digest, err := mpStartPrepPayload(reference.Identity, reference.RequestDigest, value)
	if err != nil {
		return out, err
	}
	expected := int64(len(id) + 16 + 16 + 32 + 32 + len(payload))
	if expected > 65536 || owner.bytes > 65536-expected {
		return out, &Refusal{Code: RefusalCapacity}
	}
	row, err := mpPrepRow(ctx, t, "mp.start.receipt_insert", id, reference.Identity.Incarnation[:],
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
	row, err = mpPrepRow(ctx, t, "mp.start.debt", reference.Identity.Sequence, actual, id,
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

func mpStartPrepValueKind(state string) CommandID {
	switch state {
	case "pending":
		return MPStartPrepBegin
	case "preparing":
		return MPStartPrepClaim
	case "prepared":
		return MPStartPrepSeal
	}
	return 0
}

func preflightMPStartPrepValue(v mpStartPrepValue) error {
	if !v.valid(mpStartPrepValueKind(v.State)) || v.Revision > 1<<40 || v.DeadlineMS > 1<<44 || v.AppliedMS > 1<<44 ||
		len(v.Preparation) > 256 || len(v.Execution) > 256 || len(v.Session) > 256 ||
		len(v.LegacySession) > 256 || len(v.Snapshot) > 256 || v.LegacyGeneration < 0 {
		return invalid("mp_start_prep_result")
	}
	return nil
}

func writeMPStartPrepValue(e *requestEncoder, identity CommandIdentity, request RequestDigest, v mpStartPrepValue) error {
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1})
	e.text("mp_start_preparation")
	e.bytes(identity.Incarnation[:])
	e.number(uint64(identity.Sequence))
	e.bytes(identity.OperationID[:])
	e.bytes(request[:])
	for _, s := range []string{v.Preparation, v.State, v.Execution, v.Session, v.LegacySession, v.Snapshot} {
		e.text(s)
	}
	for _, n := range []int64{v.Revision, v.DeadlineMS, v.LegacyGeneration, v.AppliedMS} {
		e.number(uint64(n))
	}
	e.flag(v.Sealed)
	return e.err
}

func mpStartPrepPayload(identity CommandIdentity, request RequestDigest, v mpStartPrepValue) ([]byte, ResultDigest, error) {
	digest, err := canonicalResultDigest(identity, request, v)
	if err != nil {
		return nil, ResultDigest{}, err
	}
	b := &mpCodecBuffer{}
	e := requestEncoder{h: b}
	if err = encodeCanonicalResult(&e, identity, request, v); err != nil {
		return nil, ResultDigest{}, err
	}
	if b.Len() > mpStartPrepPayloadCap {
		return nil, ResultDigest{}, ErrCap
	}
	return b.Bytes(), digest, nil
}

func decodeMPStartPrepPayload(identity CommandIdentity, request RequestDigest, p []byte, digest ResultDigest) (mpStartPrepValue, error) {
	var v mpStartPrepValue
	if len(p) == 0 || len(p) > mpStartPrepPayloadCap {
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
	if e != nil || name != "mp_start_preparation" {
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
	for _, s := range []*string{&v.Preparation, &v.State, &v.Execution, &v.Session, &v.LegacySession, &v.Snapshot} {
		*s, e = text()
		if e != nil {
			return mpStartPrepValue{}, ErrResolveRequired
		}
	}
	for _, n := range []*int64{&v.Revision, &v.DeadlineMS, &v.LegacyGeneration, &v.AppliedMS} {
		if binary.Read(r, binary.BigEndian, n) != nil {
			return mpStartPrepValue{}, ErrResolveRequired
		}
	}
	b, e := r.ReadByte()
	if e != nil || b > 1 || r.Len() != 0 {
		return mpStartPrepValue{}, ErrResolveRequired
	}
	v.Sealed = b == 1
	actual, got, e := mpStartPrepPayload(identity, request, v)
	if e != nil || got != digest || !bytes.Equal(actual, p) {
		return mpStartPrepValue{}, ErrResolveRequired
	}
	return v, nil
}

// completeMPStartPrepResult reads the native post-effect row in the SAME turn.
// It is a completion read, never a replay decoder: a saved result always comes
// from the immutable receipt payload.
func completeMPStartPrepResult(ctx context.Context, t *commandTurn, kind CommandID, in mpStartPrepValue) (out mpStartPrepValue, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	row, err := mpPrepRow(ctx, t, "mp.start.result_native", in.Preparation)
	if err != nil {
		return out, err
	}
	var sealed int64
	var root string
	out.Preparation = in.Preparation
	if err = row.Scan(&out.State, &out.Revision, &out.Execution, &sealed, &out.DeadlineMS, &root,
		&out.Session, &out.LegacySession, &out.LegacyGeneration, &out.Snapshot, &out.AppliedMS); err != nil {
		return out, err
	}
	if sealed < 0 || sealed > 1 || !mpPrepTexts(root) {
		return out, ErrResolveRequired
	}
	out.Sealed = sealed == 1
	// Applied is the sampled clock of THIS fresh command, not a row value.
	out.AppliedMS = in.AppliedMS
	if !out.valid(kind) {
		return out, ErrConflict
	}
	if out.State != in.State || out.Revision != in.Revision || out.Execution != in.Execution || out.Sealed != in.Sealed ||
		out.DeadlineMS != in.DeadlineMS || out.Session != in.Session || out.LegacyGeneration != in.LegacyGeneration ||
		out.Snapshot != in.Snapshot {
		return out, ErrConflict
	}
	return out, nil
}

// mpStartPrepBeginDuplicate replays the one live birth for this device/start
// key only when the complete stored input still matches. Head drift or a
// changed duplicate is a conflict, never a second preparation or stale result.
func mpStartPrepBeginDuplicate(ctx context.Context, t *commandTurn, q mpStartPrepBeginInput, incarnation [16]byte) (out mpStartPrepValue, replayed bool, err error) {
	defer func() {
		x, e := finishCommand(ctx, commandLifetime(t), struct {
			V mpStartPrepValue
			R bool
		}{out, replayed}, err)
		out, replayed, err = x.V, x.R, e
	}()
	row, err := mpPrepRow(ctx, t, "mp.start.begin_key", q.actor.device, q.keyDigest)
	if err != nil {
		return out, false, err
	}
	var id, body string
	err = row.Scan(&id, &body)
	if errors.Is(err, sql.ErrNoRows) {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	if id != q.id || body != q.bodyDigest {
		return out, false, ErrConflict
	}
	var owner mpStartPrepOwner
	if err = readMPStartPrepOwner(ctx, t, id, q.actor.device, &owner); err != nil {
		return out, false, err
	}
	if owner.incarnation != incarnation || owner.state != "pending" || owner.lifecycleRevision != 1 ||
		owner.execution != "" || owner.sealed || owner.actor != q.actor || owner.source != q.source ||
		owner.queue != q.queue || owner.item != q.item || owner.part != q.part ||
		owner.deadlineMS != q.deadlineMS || owner.tokenExpiresMS != q.tokenExpiresMS ||
		owner.replaces != q.replaces || owner.replacesGeneration != q.replacesGeneration ||
		owner.ids != q.results || owner.root != q.birth.birth.root {
		return out, false, ErrConflict
	}
	return mpStartPrepValue{Preparation: id, State: "pending", Revision: 1, DeadlineMS: owner.deadlineMS,
		Session: owner.ids.session, LegacySession: owner.ids.legacySession, LegacyGeneration: owner.ids.legacyGeneration,
		Snapshot: owner.ids.snapshot, AppliedMS: owner.createdMS}, true, nil
}
