package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"
)

const (
	// An owner retains at most 64 invitation receipts and 1 MiB of variable
	// receipt bytes. This is a bounded-history refusal, not a product user cap.
	usInvitationOwnerReceipts     int64 = 64
	usInvitationOwnerReceiptBytes int64 = 1 << 20
)

// usInvitationReceiptRequest addresses one retained original. It is a comparison
// value, never proof or authority. issuer is the ORIGINAL retained issuer
// context (from the frozen preparation) used for the request binding and the
// FRESH entry-witness comparison only. The owner row is ALWAYS addressed by the
// manager's CURRENT issuer, so replay/Resolve/ACK ignore the retained family,
// token and generations and a renewed sign-in still resolves its original.
type usInvitationReceiptRequest struct {
	ownerID   string
	kind      CommandID
	issuer    usPolicyIssuer
	reference ReceiptReference
}

type usInvitationOwnerFacts struct {
	receiptOwnerFacts
	originalFamily                     string
	reclaimed, count, bytes, retiredMS int64
}

type usInvitationReceiptLookup struct {
	owner    usInvitationOwnerFacts
	decision receiptDecision
	result   usInvitationSavedResult
	digest   ResultDigest
}

func usInvitationReceiptKind(kind CommandID) error {
	_, err := usInvitationResultCodec(kind)
	return err
}

// The owner is addressed by its PK AND the CURRENT manager issuer's six stable
// columns: ingress, server, account, member incarnation, profile, profile
// lifetime and installation. This mirrors readUSPolicyOwner exactly. Family is
// provenance, never an equality predicate, so a renewed/new family of the same
// installation and member incarnation still resolves; another installation or
// member incarnation of the same account does not. The entry-supplied retained
// issuer never participates in the owner predicate.
func readUSInvitationOwner(ctx context.Context, turn *commandTurn, m usInvitationManager, q usInvitationReceiptRequest) (out usInvitationOwnerFacts, err error) {
	defer func() {
		out, err = finishCommand(ctx, commandLifetime(turn), out, err)
		if denial := usInvitationRecheck(ctx, turn, m); denial != nil {
			out, err = usInvitationOwnerFacts{}, denial
		}
	}()
	if turn == nil || m == nil || !usIdentifier(q.ownerID) || q.reference.Identity.Incarnation == ([16]byte{}) {
		return out, ErrInvalid
	}
	current := m.Issuer()
	if !usIdentifier(current.server) || !usIdentifier(current.account) || !usIdentifier(current.profile) ||
		!usIdentifier(current.memberIncarnation) || !usIdentifier(current.profileLifetime) || !usIdentifier(current.installation) {
		return out, ErrInvalid
	}
	if err = m.Recheck(ctx, turn); err != nil {
		return out, err
	}
	row, err := usInvitationRow(ctx, turn, usInvitationOwner, usInvitationOwnerArgs(q, current)...)
	if err != nil {
		return out, err
	}
	var inc []byte
	if err = row.Scan(&inc, &out.originalFamily, &out.lastSequence, &out.acknowledgedSequence, &out.reclaimed, &out.count, &out.bytes, &out.retiredMS); errors.Is(err, sql.ErrNoRows) {
		return out, nil
	} else if err != nil {
		return out, err
	}
	if len(inc) != 16 || !usIdentifier(out.originalFamily) || out.lastSequence < 0 || out.acknowledgedSequence < 0 ||
		out.acknowledgedSequence > out.lastSequence || out.lastSequence-out.acknowledgedSequence > 1 ||
		out.reclaimed < 0 || out.reclaimed > out.acknowledgedSequence || out.count < out.lastSequence-out.acknowledgedSequence ||
		out.bytes < 0 || out.retiredMS < 0 || (out.count == 0) != (out.bytes == 0) {
		return out, ErrResolveRequired
	}
	out.exists = true
	copy(out.incarnation[:], inc)
	return out, nil
}

// usInvitationOwnerArgs binds the owner PK and the CURRENT manager's six stable
// columns (never family, never the entry-supplied retained issuer).
func usInvitationOwnerArgs(q usInvitationReceiptRequest, current usPolicyIssuer) []any {
	return []any{q.ownerID, current.server, current.account, current.memberIncarnation, current.profile,
		current.profileLifetime, current.installation}
}

// The write CAS mirrors US policy: the same-frame owner read already proved the
// six current stable columns, so the write addresses the PK and incarnation.
func usInvitationOwnerAdvanceArgs(q usInvitationReceiptRequest, owner usInvitationOwnerFacts, charged int64) []any {
	return []any{q.reference.Identity.Sequence, charged, q.ownerID, q.reference.Identity.Incarnation[:],
		owner.lastSequence, owner.acknowledgedSequence, owner.count, owner.bytes}
}

func usInvitationOwnerAckArgs(q usInvitationReceiptRequest, owner usInvitationOwnerFacts) []any {
	return []any{q.reference.Identity.Sequence, q.ownerID, q.reference.Identity.Incarnation[:],
		owner.lastSequence, owner.acknowledgedSequence}
}

type usInvitationCustody struct {
	owner    usInvitationOwnerFacts
	decision receiptDecision
	binding  receiptFacts
}

func lookupUSInvitationCustody(ctx context.Context, turn *commandTurn, m usInvitationManager, q usInvitationReceiptRequest) (out usInvitationCustody, err error) {
	defer func() {
		out, err = finishCommand(ctx, commandLifetime(turn), out, err)
		if denial := usInvitationRecheck(ctx, turn, m); denial != nil {
			out, err = usInvitationCustody{}, denial
		}
	}()
	if m == nil || turn == nil {
		return out, ErrInvalid
	}
	if err = m.Recheck(ctx, turn); err != nil {
		return out, err
	}
	if !usIdentifier(q.ownerID) || usInvitationReceiptKind(q.kind) != nil || q.kind != turn.transaction.descriptor.ID ||
		q.reference.Identity.Sequence <= 0 || q.reference.Identity.Incarnation == ([16]byte{}) ||
		q.reference.Identity.OperationID == ([16]byte{}) || q.reference.RequestDigest == (RequestDigest{}) {
		return out, ErrInvalid
	}
	out.owner, err = readUSInvitationOwner(ctx, turn, m, q)
	if err != nil {
		return out, err
	}
	expected := receiptIdentity{q.reference.Identity, q.reference.RequestDigest}
	if !out.owner.exists || out.owner.incarnation != expected.command.Incarnation {
		_, err = classifyReceipt(expected, q.kind, out.owner.receiptOwnerFacts, receiptFacts{})
		return out, err
	}
	binding, err := readUSInvitationBinding(ctx, turn, q)
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

func readUSInvitationBinding(ctx context.Context, turn *commandTurn, q usInvitationReceiptRequest) (out receiptFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	row, err := usInvitationRow(ctx, turn, usInvitationReceiptBinding, q.ownerID, q.reference.Identity.Incarnation[:], q.reference.Identity.Sequence)
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

// lookupUSInvitation classifies the original and, on replay, decodes ONLY the
// retained header/typed child/cohort. Current authority is re-proved before the
// first payload read and again before returning it.
func lookupUSInvitation(ctx context.Context, turn *commandTurn, m usInvitationManager, q usInvitationReceiptRequest) (out usInvitationReceiptLookup, err error) {
	defer func() {
		out, err = finishCommand(ctx, commandLifetime(turn), out, err)
		if denial := usInvitationRecheck(ctx, turn, m); denial != nil {
			out, err = usInvitationReceiptLookup{}, denial
		}
	}()
	if err = usInvitationReceiptKind(q.kind); err != nil {
		return out, err
	}
	custody, e := lookupUSInvitationCustody(ctx, turn, m, q)
	if e != nil {
		return out, e
	}
	out.owner, out.decision = custody.owner, custody.decision
	if out.decision != receiptReplay {
		return out, nil
	}
	if err = m.Recheck(usInvitationRecheckAt(ctx, usInvitationRecheckBeforePayload), turn); err != nil {
		return out, err
	}
	expected := receiptIdentity{q.reference.Identity, q.reference.RequestDigest}
	header, err := readUSInvitationHeader(ctx, turn, q)
	if err != nil {
		return out, err
	}
	if header.count != custody.binding.resultCount || header.count < 0 || header.count > usInvitationMaxLibraries {
		return out, ErrResolveRequired
	}
	lengths, err := readUSInvitationLibraryOctets(ctx, turn, q, header.count)
	if err != nil {
		return out, err
	}
	result, err := readUSInvitationTyped(ctx, turn, q, header, lengths)
	if err != nil {
		return out, err
	}
	result.code = header.code
	out.digest, err = usInvitationResultDigest(expected.command, expected.digest, result)
	if err != nil || out.digest != ResultDigest(custody.binding.resultDigest) || out.digest != header.digest {
		return out, errors.Join(ErrResolveRequired, err)
	}
	// The sealed Code is retained only to prove the original digest. It is never
	// projected through a replayed Value.
	result.code = usInvitationResultCode{}
	out.result = result
	return out, turn.checkLifetimes(ctx)
}

type usInvitationHeader struct {
	appliedMS int64
	digest    ResultDigest
	count     int64
	bytes     int64
	code      usInvitationResultCode
}

func readUSInvitationHeader(ctx context.Context, turn *commandTurn, q usInvitationReceiptRequest) (out usInvitationHeader, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	row, err := usInvitationRow(ctx, turn, usInvitationReceiptHeader, q.ownerID, q.reference.Identity.Incarnation[:], q.reference.Identity.Sequence)
	if err != nil {
		return out, err
	}
	var op, request, digest []byte
	var kind, version, count int64
	var codec string
	var keyID sql.NullString
	var nonce, body []byte
	if err = row.Scan(&op, &request, &kind, &version, &codec, &out.appliedMS, &digest, &count, &keyID, &nonce, &body, &out.bytes); err != nil {
		return out, errors.Join(ErrResolveRequired, err)
	}
	wantCodec, _ := usInvitationResultCodec(q.kind)
	if len(op) != 16 || len(request) != 32 || len(digest) != 32 || !equalBytes(op, q.reference.Identity.OperationID[:]) ||
		!equalBytes(request, q.reference.RequestDigest[:]) || kind != int64(q.kind) || version != 1 || codec != wantCodec ||
		count < 0 || count > usInvitationMaxLibraries || out.appliedMS < 0 || out.bytes <= 0 {
		return out, ErrResolveRequired
	}
	copy(out.digest[:], digest)
	out.count = count
	switch q.kind {
	case usInvitationCreate:
		if !keyID.Valid || len(keyID.String) < 1 || len(keyID.String) > 128 || len(nonce) != 12 ||
			len(body) != usInvitationLegacyCodeLen+16 {
			return out, ErrResolveRequired
		}
		value := keyID.String
		out.code.keyID = &value
		out.code.nonce = append([]byte(nil), nonce...)
		out.code.body = append([]byte(nil), body...)
	case usInvitationEdit, usInvitationRevoke:
		if keyID.Valid || nonce != nil || body != nil {
			return out, ErrResolveRequired
		}
	}
	return out, nil
}

func readUSInvitationTyped(ctx context.Context, turn *commandTurn, q usInvitationReceiptRequest, header usInvitationHeader, lengths []int64) (out usInvitationSavedResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	row, err := usInvitationRow(ctx, turn, usInvitationReceiptOctets, q.ownerID, q.reference.Identity.Incarnation[:], q.reference.Identity.Sequence)
	if err != nil {
		return out, err
	}
	var sizes [10]int64
	var typedBytes int64
	if err = row.Scan(&sizes[0], &sizes[1], &sizes[2], &sizes[3], &sizes[4], &sizes[5], &sizes[6], &sizes[7], &sizes[8], &sizes[9], &typedBytes); err != nil {
		return out, errors.Join(ErrResolveRequired, err)
	}
	total := int64(len(q.ownerID) + 16)
	for _, n := range sizes {
		if n < 0 || n > maxCanonicalResultBytes-total {
			return out, &Refusal{Code: RefusalCapacity}
		}
		total += n
	}
	if typedBytes != total {
		return out, ErrResolveRequired
	}
	out.kind, out.ownerID, out.appliedMS = q.kind, q.ownerID, header.appliedMS
	row, err = usInvitationRow(ctx, turn, usInvitationReceiptResult, q.ownerID, q.reference.Identity.Incarnation[:], q.reference.Identity.Sequence)
	if err != nil {
		return out, err
	}
	var notes, account sql.NullString
	var accepted sql.NullInt64
	var actualBytes int64
	var remote, tokens int64
	if err = row.Scan(&out.id, &out.serverID, &notes, &out.role, &out.state, &out.createdMS, &out.expiresMS,
		&accepted, &account, &out.revision, &out.boundUsername, &out.access.mode, &out.access.streams,
		&remote, &tokens, &out.access.rating, &out.sourceCodeHash, &actualBytes); err != nil {
		return out, errors.Join(ErrResolveRequired, err)
	}
	if int64(len(out.id)) != sizes[0] || int64(len(out.serverID)) != sizes[1] ||
		int64(len(notes.String)) != sizes[2] || int64(len(out.role)) != sizes[3] || int64(len(out.state)) != sizes[4] ||
		int64(len(account.String)) != sizes[5] || int64(len(out.boundUsername)) != sizes[6] ||
		int64(len(out.access.mode)) != sizes[7] || int64(len(out.access.rating)) != sizes[8] ||
		int64(len(out.sourceCodeHash)) != sizes[9] || actualBytes != typedBytes {
		return out, ErrResolveRequired
	}
	if remote != 0 && remote != 1 || tokens != 0 && tokens != 1 {
		return out, ErrResolveRequired
	}
	out.access.allowRemote, out.access.accessTokens = remote == 1, tokens == 1
	if notes.Valid {
		out.notes = sqlNullString{String: notes.String, Valid: true}
	}
	if account.Valid {
		out.accountID = sqlNullString{String: account.String, Valid: true}
	}
	if accepted.Valid {
		out.acceptedMS = sqlNullInt64{Int64: accepted.Int64, Valid: true}
	} else if accepted.Int64 != 0 {
		return out, ErrResolveRequired
	}
	out.code = header.code
	libraries, err := readUSInvitationLibraries(ctx, turn, q, lengths)
	if err != nil {
		return out, err
	}
	if int64(len(libraries)) != header.count {
		return out, ErrResolveRequired
	}
	out.libraries = libraries
	if err = validateUSInvitationResult(out); err != nil {
		return out, err
	}
	return out, nil
}

func readUSInvitationLibraryOctets(ctx context.Context, turn *commandTurn, q usInvitationReceiptRequest, count int64) (out []int64, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if count < 0 || count > usInvitationMaxLibraries {
		return nil, ErrResolveRequired
	}
	rows, err := usInvitationRows(ctx, turn, usInvitationReceiptLibOctets, q.ownerID, q.reference.Identity.Incarnation[:], q.reference.Identity.Sequence, count+1)
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

func readUSInvitationLibraries(ctx context.Context, turn *commandTurn, q usInvitationReceiptRequest, lengths []int64) (out []string, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	rows, err := usInvitationRows(ctx, turn, usInvitationReceiptLibraries, q.ownerID, q.reference.Identity.Incarnation[:], q.reference.Identity.Sequence, int64(len(lengths)+1))
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	out = make([]string, 0, len(lengths))
	for rows.Next() {
		var ordinal int64
		var id string
		if err = rows.Scan(&ordinal, &id); err != nil {
			return nil, err
		}
		if ordinal != int64(len(out)) || ordinal >= int64(len(lengths)) ||
			int64(len(q.ownerID)+16+len(usInvitationInvitationCohort)+len(id)) != lengths[len(out)] {
			return nil, ErrResolveRequired
		}
		out = append(out, id)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(out) != len(lengths) {
		return nil, ErrResolveRequired
	}
	return out, nil
}

// Native immutable save: receipt header (with the sealed Code on create), typed
// invitation result, ordered library cohort, then the exact owner debt in ONE
// frame. No event, source CAS or ACK here.
func storeUSInvitationResult(ctx context.Context, turn *commandTurn, m usInvitationManager, q usInvitationReceiptRequest, v usInvitationSavedResult, digest ResultDigest) (charged int64, err error) {
	defer func() {
		charged, err = finishCommand(ctx, commandLifetime(turn), charged, err)
		if denial := usInvitationRecheck(ctx, turn, m); denial != nil {
			charged, err = 0, denial
		}
	}()
	if turn == nil || turn.transaction == nil || q.kind != v.kind || turn.transaction.descriptor.ID != q.kind ||
		v.ownerID != q.ownerID || q.reference.Identity.Sequence <= 0 || q.reference.Identity.Incarnation == ([16]byte{}) ||
		q.reference.Identity.OperationID == ([16]byte{}) || q.reference.RequestDigest == (RequestDigest{}) || digest == (ResultDigest{}) {
		return 0, ErrInvalid
	}
	if err = m.Recheck(ctx, turn); err != nil {
		return 0, err
	}
	if err = usInvitationReceiptKind(q.kind); err != nil {
		return 0, err
	}
	custody, err := lookupUSInvitationCustody(ctx, turn, m, q)
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
	codec, _ := usInvitationResultCodec(q.kind)
	expectedBytes, err := usInvitationResultVariableBytes(v)
	if err != nil || expectedBytes <= 0 {
		return 0, errors.Join(ErrResolveRequired, err)
	}
	if actual, err := usInvitationResultDigest(q.reference.Identity, q.reference.RequestDigest, v); err != nil || actual != digest {
		return 0, errors.Join(ErrResolveRequired, err)
	}
	opRow, err := usInvitationRow(ctx, turn, usInvitationReceiptOperation, q.ownerID, q.reference.Identity.Incarnation[:], q.reference.Identity.OperationID[:])
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
	var keyID any
	var nonce, body any
	if q.kind == usInvitationCreate {
		keyID, nonce, body = *v.code.keyID, v.code.nonce, v.code.body
	}
	row, err := usInvitationRow(ctx, turn, usInvitationReceiptInsert, q.ownerID, q.reference.Identity.Incarnation[:], q.reference.Identity.Sequence,
		q.reference.Identity.OperationID[:], q.reference.RequestDigest[:], int64(q.kind), int64(1), codec, v.appliedMS, digest[:], int64(len(v.libraries)), keyID, nonce, body)
	if err != nil {
		return 0, usInvitationUniqueError(err)
	}
	var headerBytes int64
	if err = row.Scan(&headerBytes); err != nil {
		return 0, err
	}
	var notes, account, accepted any
	if v.notes.Valid {
		notes = v.notes.String
	}
	if v.accountID.Valid {
		account = v.accountID.String
	}
	if v.acceptedMS.Valid {
		accepted = v.acceptedMS.Int64
	}
	row, err = usInvitationRow(ctx, turn, usInvitationReceiptResultWrite, q.ownerID, q.reference.Identity.Incarnation[:], q.reference.Identity.Sequence,
		v.id, v.serverID, notes, v.role, v.state, v.createdMS, v.expiresMS, accepted, account, v.revision, v.boundUsername,
		v.access.mode, int64(v.access.streams), usInvitationBool(v.access.allowRemote), usInvitationBool(v.access.accessTokens),
		v.access.rating, v.sourceCodeHash)
	if err != nil {
		return 0, err
	}
	var typedBytes int64
	if err = row.Scan(&typedBytes); err != nil {
		return 0, err
	}
	if headerBytes <= 0 || typedBytes <= 0 || headerBytes+typedBytes > expectedBytes {
		return 0, ErrResolveRequired
	}
	charged = headerBytes + typedBytes
	for ordinal, id := range v.libraries {
		row, err = usInvitationRow(ctx, turn, usInvitationReceiptLibInsert, q.ownerID, q.reference.Identity.Incarnation[:], q.reference.Identity.Sequence,
			usInvitationInvitationCohort, int64(ordinal), id)
		if err != nil {
			return 0, err
		}
		var n int64
		if err = row.Scan(&n); err != nil {
			return 0, err
		}
		if n <= 0 || n > expectedBytes-charged {
			return 0, ErrResolveRequired
		}
		charged += n
	}
	if charged != expectedBytes {
		return 0, ErrResolveRequired
	}
	row, err = usInvitationRow(ctx, turn, usInvitationOwnerAdvance, usInvitationOwnerAdvanceArgs(q, owner, charged)...)
	if err != nil {
		return 0, err
	}
	var last, count, bytes int64
	if err = row.Scan(&last, &count, &bytes); err != nil {
		return 0, errors.Join(ErrConflict, err)
	}
	if last != q.reference.Identity.Sequence || count != owner.count+1 || bytes != owner.bytes+charged {
		return 0, ErrResolveRequired
	}
	return charged, turn.checkLifetimes(ctx)
}

// Floor-only ACK: it advances one durable sequence and refunds/deletes nothing.
func acknowledgeUSInvitationAfterAuthority(ctx context.Context, turn *commandTurn, m usInvitationManager, q usInvitationReceiptRequest) (ack int64, err error) {
	defer func() {
		ack, err = finishCommand(ctx, commandLifetime(turn), ack, err)
		if denial := usInvitationRecheck(ctx, turn, m); denial != nil {
			ack, err = 0, denial
		}
	}()
	lookup, err := lookupUSInvitation(ctx, turn, m, q)
	if err != nil {
		return 0, err
	}
	if lookup.owner.retiredMS != 0 {
		return 0, ErrOwnerUnavailable
	}
	if lookup.decision == receiptRetired {
		return lookup.owner.acknowledgedSequence, nil
	}
	if lookup.decision != receiptReplay || q.reference.Identity.Sequence != lookup.owner.lastSequence {
		return 0, ErrConflict
	}
	row, err := usInvitationRow(ctx, turn, usInvitationOwnerAck, usInvitationOwnerAckArgs(q, lookup.owner)...)
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

func usInvitationQuotaAdmit(owner usInvitationOwnerFacts, charge int64) error {
	if !owner.exists || owner.retiredMS != 0 {
		return ErrOwnerUnavailable
	}
	if charge <= 0 || charge > usInvitationOwnerReceiptBytes {
		return &Refusal{Code: RefusalCapacity}
	}
	if owner.count >= usInvitationOwnerReceipts || owner.bytes > usInvitationOwnerReceiptBytes-charge {
		return &Refusal{Code: RefusalCapacity}
	}
	if owner.lastSequence != owner.acknowledgedSequence {
		return ErrResolveRequired
	}
	return nil
}

func usInvitationRecheck(ctx context.Context, turn *commandTurn, m usInvitationManager) error {
	if m == nil {
		return ErrInvalid
	}
	return m.Recheck(ctx, turn)
}

// usInvitationRecheckSite names the exact dispatcher point that re-proves
// current authority. It travels on the context only so tests can pin each call
// site without counting calls; the production manager ignores it and reads the
// same frame authority as before.
type usInvitationRecheckSite uint8

const (
	usInvitationRecheckEntry usInvitationRecheckSite = iota + 1
	usInvitationRecheckBeforePayload
	usInvitationRecheckRelease
)

type usInvitationRecheckSiteKey struct{}

func usInvitationRecheckAt(ctx context.Context, site usInvitationRecheckSite) context.Context {
	return context.WithValue(ctx, usInvitationRecheckSiteKey{}, site)
}

func usInvitationRecheckSiteOf(ctx context.Context) (usInvitationRecheckSite, bool) {
	if ctx == nil {
		return 0, false
	}
	site, ok := ctx.Value(usInvitationRecheckSiteKey{}).(usInvitationRecheckSite)
	return site, ok
}

func usInvitationBool(v bool) int64 {
	if v {
		return 1
	}
	return 0
}
