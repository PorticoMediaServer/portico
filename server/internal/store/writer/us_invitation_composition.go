package writer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// The 550-552 dispatcher is INERT private composition: no public Writer method,
// no product caller and no exported constructor. It takes an already begun FULL
// private turn and the narrow usInvitationManager authority seam. The seam is
// the only authority input; Sol's production recipe replaces the test-only
// implementation without changing this algorithm.

type usInvitationFrozenDocument struct {
	Actor          string
	SignInKind     string
	SignInRevision int64
	Prepared       usInvitationPrepared
	CodeKeyID      *string
	CodeNonce      []byte
	CodeBody       []byte
}

// usInvitationInvocationScope is the CURRENT entry witness of the trusted
// caller. It is deliberately the only second issuer shape: the invitation
// package reuses Sol's usPolicyIssuer rather than minting a role or authority
// boolean. A fresh submission must carry the same 11 issuer fields the frozen
// preparation retains; replay/Resolve/ACK use only the CURRENT manager plus the
// retained owner's stable columns (see usInvitationReceiptRequest.issuer).
type usInvitationInvocationScope struct {
	issuer usPolicyIssuer
}

type usInvitationCommand struct {
	kind      CommandID
	ownerID   string
	actor     string
	identity  CommandIdentity
	digest    RequestDigest
	prepared  *usInvitationPrepared
	signIn    usInvitationSignInKind
	protected usInvitationProtectedCode // create only; structurally proved before the effect
}

func (c usInvitationCommand) resultCode() usInvitationResultCode {
	if c.kind != usInvitationCreate || !c.protected.present() {
		return usInvitationResultCode{}
	}
	key := c.protected.keyID
	return usInvitationResultCode{keyID: &key, nonce: append([]byte(nil), c.protected.nonce...),
		body: append([]byte(nil), c.protected.body...)}
}

func (usInvitationCommand) String() string   { return "usInvitationCommand{redacted}" }
func (usInvitationCommand) GoString() string { return "usInvitationCommand{redacted}" }
func (usInvitationCommand) Format(state fmt.State, _ rune) {
	formatUSPreparation(state, "usInvitationCommand")
}

func (usInvitationFrozenDocument) String() string   { return "usInvitationFrozenDocument{redacted}" }
func (usInvitationFrozenDocument) GoString() string { return "usInvitationFrozenDocument{redacted}" }
func (usInvitationFrozenDocument) Format(state fmt.State, _ rune) {
	formatUSPreparation(state, "usInvitationFrozenDocument")
}

// usInvitationCommandDigest binds the COMPLETE frozen command including the
// sign-in kind and identity_state revision witness (G3). A later drift therefore
// changes the digest and is also re-proved in-frame before any write.
func usInvitationCommandDigest(c *usInvitationCommand) (RequestDigest, error) {
	if c == nil || c.prepared == nil || !c.signIn.valid() || !usIdentifier(c.actor) || !usIdentifier(c.ownerID) {
		return RequestDigest{}, ErrInvalid
	}
	doc := usInvitationFrozenDocument{Actor: c.actor, SignInKind: c.signIn.kind, SignInRevision: c.signIn.revision, Prepared: *c.prepared}
	if c.kind == usInvitationCreate {
		if !c.protected.present() || !usIdentifier(c.protected.keyID) {
			return RequestDigest{}, ErrInvalid
		}
		key := c.protected.keyID
		doc.CodeKeyID = &key
		doc.CodeNonce = append([]byte(nil), c.protected.nonce...)
		doc.CodeBody = append([]byte(nil), c.protected.body...)
	}
	return canonicalRequestDigest(c.kind, 1, c.identity, doc)
}

type usInvitationInvocation struct {
	request usInvitationReceiptRequest
	input   *usInvitationCommand
	// scope is the CURRENT trusted entry witness. It is compared with the
	// frozen preparation only after replay classification, so a renewed family
	// can still resolve its retained original.
	scope usInvitationInvocationScope
	ack   bool
}

// usInvitationDispatchResult is the closed result of one dispatch call. Code is
// present only on a FRESH create: the sealed artifact is a first-response-only
// value and replay/edit/revoke never project it. DTO is the complete product
// Invitation document without Code on every replay.
type usInvitationDispatchResult struct {
	Outcome ReceiptOutcome
	Value   usInvitationSavedResult
	Digest  ResultDigest
	Code    usInvitationResultCode
	DTO     usInvitationPublicResult

	ChargedBytes int64
	OwnerCount   int64
	OwnerBytes   int64
}

func (usInvitationDispatchResult) String() string   { return "usInvitationDispatchResult{redacted}" }
func (usInvitationDispatchResult) GoString() string { return "usInvitationDispatchResult{redacted}" }
func (usInvitationDispatchResult) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("usInvitationDispatchResult{redacted}"))
}

type usInvitationCompletion struct {
	out        usInvitationDispatchResult
	floor      AckResult
	historical bool
}

func (usInvitationCompletion) String() string   { return "usInvitationCompletion{redacted}" }
func (usInvitationCompletion) GoString() string { return "usInvitationCompletion{redacted}" }
func (usInvitationCompletion) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("usInvitationCompletion{redacted}"))
}

func usInvitationReceiptForCommand(c *usInvitationCommand) (usInvitationReceiptRequest, error) {
	if c == nil || c.prepared == nil || c.kind != usInvitationCreate && c.kind != usInvitationEdit && c.kind != usInvitationRevoke {
		return usInvitationReceiptRequest{}, ErrInvalid
	}
	return usInvitationReceiptRequest{ownerID: c.ownerID, kind: c.kind, issuer: c.prepared.scope.issuer,
		reference: ReceiptReference{Identity: c.identity, RequestDigest: c.digest}}, nil
}

// usInvitationAppliedMS samples the trusted writer clock once for applied_ms,
// the live-cap decision and the result identity.
func usInvitationAppliedMS(turn *commandTurn) (int64, error) {
	if turn == nil || turn.transaction == nil || turn.transaction.writer == nil || turn.transaction.writer.now == nil {
		return 0, ErrInvalid
	}
	tx := turn.transaction
	if tx.done || !tx.begun {
		return 0, ErrClosed
	}
	now := tx.writer.now()
	ms := now.UnixMilli()
	if ms < 0 {
		return 0, ErrInvalid
	}
	if err := tx.observeTime(now); err != nil {
		return 0, err
	}
	if deadline, ok := tx.ctx.Deadline(); !ok || !now.Before(deadline) {
		return 0, context.DeadlineExceeded
	}
	return ms, nil
}

func usInvitationNotes(v sql.NullString) sqlNullString {
	return sqlNullString{String: v.String, Valid: v.Valid}
}

func usInvitationParseLibraries(raw string) ([]string, error) {
	ids, err := decodeUSLibraryIDs(raw)
	if err != nil {
		return nil, err
	}
	if ids == nil {
		ids = []string{}
	}
	return ids, nil
}

// usInvitationProspectiveResult builds the complete prospective immutable result
// BEFORE any source effect, so quota and byte admission see the exact charge.
func usInvitationProspectiveResult(q usInvitationReceiptRequest, p *usInvitationPrepared, pre usInvitationSourceRow, code usInvitationResultCode, nowMS int64) (usInvitationSavedResult, error) {
	if p == nil || p.kind != q.kind || nowMS <= 0 {
		return usInvitationSavedResult{}, ErrInvalid
	}
	v := usInvitationSavedResult{kind: q.kind, ownerID: q.ownerID, id: p.id, serverID: p.scope.issuer.server,
		appliedMS: nowMS, code: cloneUSInvitationCode(code)}
	switch q.kind {
	case usInvitationCreate:
		v.notes = usInvitationNotes(p.notes)
		v.role, v.state = p.role, "pending"
		v.createdMS, v.expiresMS = nowMS, nowMS+int64(p.hours)*usInvitationHourMS
		v.revision, v.boundUsername = 1, p.boundUsername
		v.access, v.sourceCodeHash = p.access, p.codeHash
		v.libraries = append([]string(nil), p.libraries...)
	case usInvitationEdit:
		v.notes = usInvitationNotes(p.notes)
		v.role, v.state = p.role, "pending"
		v.createdMS, v.expiresMS = pre.createdMS, p.expiresMS
		v.acceptedMS, v.accountID = sqlNullInt64{Int64: pre.acceptedMS.Int64, Valid: pre.acceptedMS.Valid},
			sqlNullString{String: pre.accountID.String, Valid: pre.accountID.Valid}
		v.revision, v.boundUsername = pre.revision+1, pre.boundUsername
		if p.replaceAccess {
			v.access = p.access
		} else {
			v.access = pre.access()
		}
		v.sourceCodeHash = pre.codeHash
		v.libraries = append([]string(nil), p.libraries...)
	case usInvitationRevoke:
		libraries, err := usInvitationParseLibraries(pre.librariesJSON)
		if err != nil {
			return usInvitationSavedResult{}, err
		}
		v.notes = sqlNullString{String: pre.notes.String, Valid: pre.notes.Valid}
		v.role, v.state = pre.role, "revoked"
		v.createdMS, v.expiresMS = pre.createdMS, pre.expiresMS
		v.acceptedMS, v.accountID = sqlNullInt64{Int64: pre.acceptedMS.Int64, Valid: pre.acceptedMS.Valid},
			sqlNullString{String: pre.accountID.String, Valid: pre.accountID.Valid}
		v.revision, v.boundUsername = pre.revision+1, pre.boundUsername
		v.access, v.sourceCodeHash = pre.access(), pre.codeHash
		v.libraries = libraries
	default:
		return usInvitationSavedResult{}, ErrInvalid
	}
	if v.libraries == nil {
		v.libraries = []string{}
	}
	if err := validateUSInvitationResult(v); err != nil {
		return usInvitationSavedResult{}, err
	}
	return v, nil
}

// usInvitationResultFromSource rebuilds the same result from the POST-effect
// source row. The dispatcher requires its digest to equal the prospective one,
// so a divergent write can never be retained.
func usInvitationResultFromSource(q usInvitationReceiptRequest, p *usInvitationPrepared, row usInvitationSourceRow, code usInvitationResultCode, nowMS int64) (usInvitationSavedResult, error) {
	if p == nil || !row.exists || nowMS <= 0 {
		return usInvitationSavedResult{}, ErrResolveRequired
	}
	libraries, err := usInvitationParseLibraries(row.librariesJSON)
	if err != nil {
		return usInvitationSavedResult{}, errors.Join(ErrResolveRequired, err)
	}
	v := usInvitationSavedResult{kind: q.kind, ownerID: q.ownerID, id: row.id, serverID: p.scope.issuer.server,
		notes: sqlNullString{String: row.notes.String, Valid: row.notes.Valid}, role: row.role, state: row.state,
		createdMS: row.createdMS, expiresMS: row.expiresMS,
		acceptedMS: sqlNullInt64{Int64: row.acceptedMS.Int64, Valid: row.acceptedMS.Valid},
		accountID:  sqlNullString{String: row.accountID.String, Valid: row.accountID.Valid}, revision: row.revision,
		boundUsername: row.boundUsername, access: row.access(), sourceCodeHash: row.codeHash, libraries: libraries,
		appliedMS: nowMS, code: cloneUSInvitationCode(code)}
	if err = validateUSInvitationResult(v); err != nil {
		return usInvitationSavedResult{}, err
	}
	return v, nil
}

// applyUSInvitationEffect executes exactly one US-12 source CAS. Zero rows is a
// same-frame race, never a silent success.
func applyUSInvitationEffect(ctx context.Context, turn *commandTurn, p *usInvitationPrepared, nowMS int64) (id string, revision int64, state string, expires int64, err error) {
	defer func() {
		done, e := finishCommand(ctx, commandLifetime(turn), struct {
			id       string
			revision int64
			state    string
			expires  int64
		}{id, revision, state, expires}, err)
		id, revision, state, expires, err = done.id, done.revision, done.state, done.expires, e
	}()
	if turn == nil || p == nil {
		return "", 0, "", 0, ErrInvalid
	}
	statement, args, err := p.effectBindings(nowMS)
	if err != nil {
		return "", 0, "", 0, err
	}
	row, err := usInvitationRow(ctx, turn, statement, args...)
	if err != nil {
		return "", 0, "", 0, err
	}
	if err = row.Scan(&id, &revision, &state, &expires); errors.Is(err, sql.ErrNoRows) {
		return "", 0, "", 0, ErrConflict
	} else if err != nil {
		return "", 0, "", 0, err
	}
	return id, revision, state, expires, nil
}

// executeUSInvitationInvocation is the complete original/Resolve/ACK algorithm.
// It returns Pending for a fresh effect: only the physical terminal may declare
// Applied.
func executeUSInvitationInvocation(ctx context.Context, turn *commandTurn, m usInvitationManager, inv *usInvitationInvocation) (out usInvitationDispatchResult, floor AckResult, historical bool, err error) {
	defer func() {
		v, e := finishCommand(ctx, commandLifetime(turn), usInvitationCompletion{out, floor, historical}, err)
		out, floor, historical, err = v.out, v.floor, v.historical, e
	}()
	if turn == nil || turn.transaction == nil || m == nil || inv == nil {
		return out, floor, false, ErrInvalid
	}
	kind := turn.transaction.descriptor.ID
	q := inv.request
	if q.kind != kind || usInvitationReceiptKind(kind) != nil || q.reference.Identity.Incarnation == ([16]byte{}) ||
		q.reference.Identity.OperationID == ([16]byte{}) || q.reference.Identity.Sequence <= 0 || q.reference.RequestDigest == (RequestDigest{}) {
		return out, floor, false, ErrInvalid
	}
	if err = m.Recheck(usInvitationRecheckAt(ctx, usInvitationRecheckEntry), turn); err != nil {
		return out, floor, false, err
	}
	// The verified issuer must be the frozen actor before ANY owner, payload or
	// source read. A foreign issuer is refused with no receipt, not reclassified
	// as an absent owner.
	if inv.input != nil {
		if inv.input.prepared == nil || !usIdentifier(inv.input.actor) ||
			inv.input.actor != inv.input.prepared.scope.issuer.account || m.Issuer() != inv.input.actor {
			return out, floor, false, ErrDisclosureDenied
		}
	}
	if inv.ack {
		if inv.input != nil {
			return out, floor, false, ErrInvalid
		}
		floor.AcknowledgedThrough, err = acknowledgeUSInvitationAfterAuthority(ctx, turn, m, q)
		return out, floor, true, err
	}
	if inv.input != nil {
		quota, e := readUSInvitationOwner(ctx, turn, m, q)
		if e != nil {
			return out, floor, false, e
		}
		if quota.exists && quota.retiredMS != 0 && q.reference.Identity.Sequence > quota.lastSequence {
			return out, floor, false, ErrOwnerUnavailable
		}
	}
	lookup, err := lookupUSInvitation(ctx, turn, m, q)
	if err != nil {
		return out, floor, false, err
	}
	out.Outcome.Reference = q.reference
	out.OwnerCount, out.OwnerBytes = lookup.owner.count, lookup.owner.bytes
	switch lookup.decision {
	case receiptRetired:
		out.Outcome.State = ResultRetired
		return out, floor, true, nil
	case receiptReplay:
		out.Outcome.State = Applied
		out.Outcome.Replayed = true
		out.Value, out.Digest = lookup.result, lookup.digest
		out.DTO = lookup.result.usInvitationPublic("")
		return out, floor, true, nil
	case receiptFresh:
		out.Outcome.State = Pending
	default:
		return out, floor, false, ErrResolveRequired
	}
	if inv.input == nil {
		return out, floor, false, nil
	}
	input := inv.input
	if input.kind != q.kind || input.ownerID != q.ownerID || input.identity != q.reference.Identity || input.digest != q.reference.RequestDigest {
		return out, floor, false, ErrConflict
	}
	digest, err := usInvitationCommandDigest(input)
	if err != nil || digest != input.digest {
		return out, floor, false, errors.Join(ErrConflict, err)
	}
	p := input.prepared
	if p == nil || p.kind != q.kind || !usIdentifier(input.actor) || input.actor != p.scope.issuer.account || m.Issuer() != input.actor {
		return out, floor, false, ErrDisclosureDenied
	}
	// FULL-ISSUER FRESH BINDING: the CURRENT trusted entry witness must equal the
	// frozen preparation in all 11 issuer fields. This runs after replay
	// classification, so a renewed family still resolves its retained original.
	// The frozen document (including prepared.scope.issuer) is already covered
	// by the canonical request digest, so this is a current-frame comparison,
	// not a second digest.
	if inv.scope.issuer != p.scope.issuer {
		return out, floor, false, ErrConflict
	}
	// The sealed Code is carried as the protected type. Prove its exact manifest
	// BEFORE any effect; a same-length body sealed for another invitation has a
	// different manifest and is refused here.
	if q.kind == usInvitationCreate && !input.protected.valid(p) {
		return out, floor, false, ErrConflict
	}
	// G3: the frozen sign-in-kind/revision witness is current-frame evidence. A
	// drifted kind or revision is a denial, never a stale-witness success.
	if err = checkUSInvitationFreshKind(ctx, turn, input.signIn); err != nil {
		return out, floor, false, err
	}
	nowMS, err := usInvitationAppliedMS(turn)
	if err != nil {
		return out, floor, false, err
	}
	var pre usInvitationSourceRow
	switch q.kind {
	case usInvitationCreate:
		if !m.ManagesTier(p.role) {
			return out, floor, false, ErrDisclosureDenied
		}
		if err = checkUSInvitationCreateAdmission(ctx, turn, p, nowMS); err != nil {
			return out, floor, false, err
		}
	case usInvitationEdit, usInvitationRevoke:
		pre, err = readUSInvitationSource(ctx, turn, p.id)
		if err != nil {
			return out, floor, false, err
		}
		if !pre.exists {
			return out, floor, false, ErrInvitationNotFound
		}
		// Legacy order: not-found, tier, state/expiry, then revision (edit).
		// Tier must therefore precede classify so a lower-tier manager sees a
		// denial rather than a revision/expired conflict from the row.
		if !m.ManagesTier(pre.role) {
			return out, floor, false, ErrDisclosureDenied
		}
		if q.kind == usInvitationEdit && !m.ManagesTier(p.role) {
			return out, floor, false, ErrDisclosureDenied
		}
		if err = classifyUSInvitationSource(p, pre, nowMS); err != nil {
			return out, floor, false, err
		}
		if q.kind == usInvitationEdit {
			// B2: requested libraries must exist on edit exactly as on create.
			// An empty cohort means all libraries and needs no row.
			if err = checkUSInvitationLibraries(ctx, turn, p.libraries); err != nil {
				return out, floor, false, err
			}
		}
	}
	code := usInvitationResultCode{}
	if q.kind == usInvitationCreate {
		code = cloneUSInvitationCode(input.resultCode())
	}
	prospective, err := usInvitationProspectiveResult(q, p, pre, code, nowMS)
	if err != nil {
		return out, floor, false, err
	}
	charge, err := usInvitationResultVariableBytes(prospective)
	if err != nil {
		return out, floor, false, err
	}
	if err = usInvitationQuotaAdmit(lookup.owner, charge); err != nil {
		return out, floor, false, err
	}
	id, revision, state, expires, err := applyUSInvitationEffect(ctx, turn, p, nowMS)
	if err != nil {
		return out, floor, false, usInvitationUniqueError(err)
	}
	if id != p.id || revision != prospective.revision || state != prospective.state || expires != prospective.expiresMS {
		return out, floor, false, ErrResolveRequired
	}
	post, err := readUSInvitationSource(ctx, turn, p.id)
	if err != nil {
		return out, floor, false, err
	}
	if !post.exists || q.kind == usInvitationCreate && post.createdBy != input.actor {
		return out, floor, false, ErrResolveRequired
	}
	final, err := usInvitationResultFromSource(q, p, post, code, nowMS)
	if err != nil {
		return out, floor, false, err
	}
	out.Digest, err = usInvitationResultDigest(q.reference.Identity, q.reference.RequestDigest, final)
	if err != nil {
		return out, floor, false, err
	}
	if want, err := usInvitationResultDigest(q.reference.Identity, q.reference.RequestDigest, prospective); err != nil || want != out.Digest {
		return out, floor, false, errors.Join(ErrResolveRequired, err)
	}
	charged, err := storeUSInvitationResult(ctx, turn, m, q, final, out.Digest)
	if err != nil || charged != charge {
		return out, floor, false, errors.Join(ErrResolveRequired, err)
	}
	// The sealed Code is FIRST-RESPONSE-ONLY: the immutable Value carries only
	// public fields and out.Code below is its ONLY projection.
	final.code = usInvitationResultCode{}
	out.Value, out.ChargedBytes = final, charged
	out.OwnerCount, out.OwnerBytes = lookup.owner.count+1, lookup.owner.bytes+charged
	if q.kind == usInvitationCreate {
		out.Code = cloneUSInvitationCode(input.resultCode())
	}
	out.DTO = final.usInvitationPublic("")
	// Legacy projects serverId only on a FRESH create from a LOCAL sign-in
	// (LocalLinkServerIDTx is empty otherwise). Edit, revoke and every replay
	// therefore carry none.
	if q.kind == usInvitationCreate && input.signIn.kind == "local" {
		out.DTO.ServerID = p.scope.issuer.server
	}
	return out, floor, false, turn.checkLifetimes(ctx)
}

// runUSInvitationDispatch is the closed test/harness entry. It owns the final
// current disclosure and the FULL physical terminal; the caller supplies only
// the already admitted turn and the authority seam.
func runUSInvitationDispatch(ctx context.Context, turn *commandTurn, m usInvitationManager, inv *usInvitationInvocation) (out usInvitationDispatchResult, floor AckResult, err error) {
	var historical bool
	defer func() {
		v, e := finishCommand(ctx, ctx, usInvitationCompletion{out, floor, historical}, err)
		out, floor, err = v.out, v.floor, e
	}()
	out, floor, historical, err = executeUSInvitationInvocation(ctx, turn, m, inv)
	if err != nil {
		return withdrawUSInvitation(turn, err)
	}
	// Final current disclosure before the terminal: a release-time denial
	// REPLACES the body outcome and rolls the whole prefix back.
	if denial := usInvitationRecheck(usInvitationRecheckAt(ctx, usInvitationRecheckRelease), turn, m); denial != nil {
		return withdrawUSInvitation(turn, denial)
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return withdrawUSInvitation(turn, ctxErr)
	}
	// The release rule runs BEFORE the terminal: the transaction cancels its
	// own lifetime at commit/rollback, so this release uses the live frame
	// lifetime. The deferred final publication rechecks the CALLER's context
	// after physical return. Do NOT call Recheck there: current authority denies
	// once transaction.done is set.
	release, releaseErr := finishCommand(ctx, commandLifetime(turn), usInvitationCompletion{out, floor, historical}, nil)
	if releaseErr != nil {
		return withdrawUSInvitation(turn, releaseErr)
	}
	out, floor = release.out, release.floor
	if rowsErr := turn.closeRows(); rowsErr != nil {
		return withdrawUSInvitation(turn, rowsErr)
	}
	physical, commitErr := turn.transaction.commit()
	switch physical.state {
	case committed:
		if commitErr != nil {
			return usInvitationDispatchResult{}, AckResult{}, commitErr
		}
		if inv.ack {
			return usInvitationDispatchResult{}, floor, nil
		}
		if historical {
			return out, floor, nil
		}
		if inv.input != nil && out.Outcome.State == Pending {
			out.Outcome.State = Applied
		}
		return out, floor, nil
	case commitUnknown:
		_, _ = turn.transaction.rollback(commitErr)
		return usInvitationDispatchResult{}, AckResult{}, errors.Join(ErrResolveRequired, commitErr)
	default:
		_, _ = turn.transaction.rollback(commitErr)
		if commitErr == nil {
			commitErr = ErrResolveRequired
		}
		return usInvitationDispatchResult{}, AckResult{}, commitErr
	}
}

func withdrawUSInvitation(turn *commandTurn, err error) (usInvitationDispatchResult, AckResult, error) {
	if turn != nil && turn.transaction != nil {
		_, _ = turn.transaction.rollback(err)
	}
	return usInvitationDispatchResult{}, AckResult{}, err
}
