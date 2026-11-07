package writer

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

type ReclaimReceiptRequest struct {
	PlaybackID string
	Receipt    ReceiptReference
}

type ReclaimResult struct {
	Outcome ReceiptOutcome
	Value   ReclaimValue
	Digest  ResultDigest
}

// Private SAME-turn native observations. This is neither a floor capability nor
// authority. Parent checks current retained-owner authority before every use.
type wcReclaimSnapshot struct {
	owner                       wcReclaimOwner
	slot                        wcReclaimSlot
	resultVersion, controlBytes int64
}

type wcReclaimLookup struct {
	request  ReclaimReceiptRequest
	scope    wcProgressScope
	owner    receiptOwnerFacts
	decision receiptDecision
	snapshot wcReclaimSnapshot
	value    ReclaimValue
	digest   ResultDigest
}

// Explicit parent-selected compiled purpose; never alias ProgressAccept, guess
// an ordinal or derive an admission class/budget from this metadata.
func checkWCReclaimPurpose(purpose CommandID) error {
	d, err := Describe(purpose)
	if err != nil {
		return err
	}
	if d.Name != wcReclaimKind || d.Durability != Normal {
		return ErrInvalid
	}
	return nil
}

func validWCReclaimScope(s wcProgressScope) bool {
	return s.playbackID != "" && len(s.playbackID) <= wcMaxPlaybackIDBytes && utf8.ValidString(s.playbackID) &&
		strings.IndexByte(s.playbackID, 0) < 0 && s.incarnation != [16]byte{} &&
		s.profileKey != "" && s.subjectID > 0 && s.accountID != "" && s.profileID != "" &&
		len(s.memberBirth) == 32 && len(s.profileBirth) == 32 && s.libraryID > 0
}

const (
	wcReclaimCurrentSlot stmt.ID = "wc.reclaim.protocol_current_slot"
	wcReclaimExactSlot   stmt.ID = "wc.reclaim.protocol_exact_slot"
	wcReclaimAckFloor    stmt.ID = "wc.reclaim.protocol_ack_floor"
)

// Definitions include the merged bounded body and just the retained full-key
// owner read from the progress vocabulary. Parent deduplicates shared IDs when
// composing several command sets. No current-user/token opener is installed.
func wcReclaimProtocolDefinitions() []stmt.Definition {
	defs := wcReclaimDefinitions()
	for _, d := range wcProgressProtocolDefinitions() {
		if d.ID == wcProgressNativeOwner {
			defs = append(defs, d)
		}
	}
	params := []stmt.Param{{Name: "playback", Type: stmt.Text}, {Name: "incarnation", Type: stmt.Blob},
		{Name: "profile_key", Type: stmt.Text}, {Name: "subject", Type: stmt.Integer},
		{Name: "account", Type: stmt.Text}, {Name: "profile", Type: stmt.Text},
		{Name: "member_birth", Type: stmt.Text}, {Name: "profile_birth", Type: stmt.Text}, {Name: "library", Type: stmt.Integer}}
	bindings := []any{"playback-音", []byte{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, "viewer-one", int64(11), "account-one", "profile-one", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", int64(7)}
	where := ` WHERE playback_id=? AND command_incarnation=? AND profile_key=? AND item_id=? AND command_authority='local' AND command_account_id=? AND command_profile_id=? AND command_member_incarnation=? AND command_profile_lifetime=? AND command_library_id=?`
	projection := `SELECT reclaim_command_sequence,reclaim_operation_id,reclaim_request_digest,reclaim_command_kind,reclaim_result_version,reclaim_captured_acknowledged_sequence,reclaim_old_after_sequence,reclaim_removed_sequence,reclaim_removed_count,reclaim_removed_variable_bytes,reclaim_result_after_sequence,reclaim_result_no_next,reclaim_applied_ms,reclaim_result_count,reclaim_result_digest FROM playback_personal_claims INDEXED BY sqlite_autoindex_playback_personal_claims_2`
	plan := stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_playback_personal_claims_2"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "playback_personal_claims", Name: "sqlite_autoindex_playback_personal_claims_2", Origin: "u", Columns: []stmt.IndexColumn{{Name: "playback_id", Collation: "BINARY"}, {Name: "command_incarnation", Collation: "BINARY"}}}}}
	for _, exact := range []bool{false, true} {
		id, sqlText := wcReclaimCurrentSlot, projection+where
		p, b := append([]stmt.Param(nil), params...), append([]any(nil), bindings...)
		if exact {
			id, sqlText = wcReclaimExactSlot, sqlText+` AND reclaim_command_sequence=?`
			p, b = append(p, stmt.Param{Name: "sequence", Type: stmt.Integer}), append(b, int64(5))
		}
		defs = append(defs, stmt.Definition{ID: id, Owner: "store/writer WC reclaim protocol", Kind: stmt.Read,
			CallSites: []string{"store/writer/wc_reclaim_protocol.go"}, SQL: sqlText, Params: p,
			Cohorts: []stmt.Cohort{{Name: "retained_full_key", Bindings: b}}, Plan: plan})
	}
	ackParams := []stmt.Param{{Name: "next_floor", Type: stmt.Integer}}
	ackParams = append(ackParams, params...)
	ackParams = append(ackParams, stmt.Param{Name: "last", Type: stmt.Integer}, stmt.Param{Name: "old_floor", Type: stmt.Integer}, stmt.Param{Name: "sequence", Type: stmt.Integer}, stmt.Param{Name: "operation", Type: stmt.Blob}, stmt.Param{Name: "request", Type: stmt.Blob}, stmt.Param{Name: "result", Type: stmt.Blob})
	ackBindings := append([]any{int64(5)}, bindings...)
	ackBindings = append(ackBindings, int64(5), int64(4), int64(5), make([]byte, 16), make([]byte, 32), make([]byte, 32))
	defs = append(defs, stmt.Definition{ID: wcReclaimAckFloor, Owner: "store/writer WC reclaim ACK", Kind: stmt.Write,
		CallSites: []string{"store/writer/wc_reclaim_protocol.go"},
		SQL:       `UPDATE playback_personal_claims INDEXED BY sqlite_autoindex_playback_personal_claims_2 SET command_acknowledged_sequence=?` + where + ` AND command_last_sequence=? AND command_acknowledged_sequence=? AND reclaim_command_sequence=? AND reclaim_operation_id=? AND reclaim_request_digest=? AND reclaim_result_digest=? RETURNING command_acknowledged_sequence`,
		Params:    ackParams, Cohorts: []stmt.Cohort{{Name: "cleanup_floor_only", Bindings: ackBindings}}, Plan: plan})
	return defs
}

func wcReclaimScopeArgs(s wcProgressScope) []any {
	return []any{s.playbackID, s.incarnation[:], s.profileKey, s.subjectID, s.accountID, s.profileID, s.memberBirth, s.profileBirth, s.libraryID}
}

func readWCReclaimOwner(ctx context.Context, t *commandTurn, scope wcProgressScope) (out wcReclaimSnapshot, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if !validWCReclaimScope(scope) {
		return out, ErrInvalid
	}
	s, err := readWCProgressOwner(ctx, t, scope)
	if err != nil {
		return out, err
	}
	out.owner = wcReclaimOwner{scope.playbackID, scope.incarnation, s.Last, s.Acknowledged, s.AfterSequence, s.ReceiptRows, s.ReceiptBytes, s.Due.Valid, s.Due.Int64}
	out.controlBytes = s.ControlBytes
	return out, nil
}

// readWCReclaimSnapshot is preparation only; historical lookup below MUST
// classify the shared floor before reading/decoding any current cleanup scalar.
func readWCReclaimSnapshot(ctx context.Context, t *commandTurn, scope wcProgressScope) (out wcReclaimSnapshot, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	out, err = readWCReclaimOwner(ctx, t, scope)
	if err != nil {
		return out, err
	}
	return readWCReclaimOldSlot(ctx, t, scope, out)
}

func readWCReclaimOldSlot(ctx context.Context, t *commandTurn, scope wcProgressScope, s wcReclaimSnapshot) (out wcReclaimSnapshot, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	out = s
	row, err := wc008DraftQuery(ctx, t, wcReclaimCurrentSlot, wcReclaimScopeArgs(scope)...)
	if err != nil {
		return out, err
	}
	raw, err := scanWCReclaimSlot(row)
	if err != nil {
		return out, err
	}
	out.slot, out.resultVersion, err = decodeWCReclaimSlot(raw, s.owner)
	if err == nil && !validWCReclaimSlot(out.slot, s.owner) {
		err = ErrResolveRequired
	}
	return out, err
}

// Matching request sequence ONLY. An unrelated newer scalar is never supplied
// to the shared classifier as found proof for an older requested command.
func lookupWCReclaim(ctx context.Context, t *commandTurn, purpose CommandID, scope wcProgressScope, q ReclaimReceiptRequest) (out wcReclaimLookup, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = checkWCReclaimPurpose(purpose); err != nil {
		return out, err
	}
	if q.PlaybackID != scope.playbackID || q.Receipt.Identity.Incarnation != scope.incarnation ||
		q.Receipt.Identity.OperationID == [16]byte{} || q.Receipt.RequestDigest == (RequestDigest{}) {
		return out, ErrConflict
	}
	out.scope, out.request = scope, q
	out.snapshot, err = readWCReclaimOwner(ctx, t, scope)
	if err != nil {
		return out, err
	}
	s := out.snapshot.owner
	out.owner = receiptOwnerFacts{true, s.incarnation, s.lastSequence, s.acknowledgedSequence}
	expected := receiptIdentity{q.Receipt.Identity, q.Receipt.RequestDigest}
	if expected.command.Sequence <= s.acknowledgedSequence {
		out.decision, err = classifyReceipt(expected, purpose, out.owner, receiptFacts{})
		return out, err
	}
	args := append(wcReclaimScopeArgs(scope), expected.command.Sequence)
	row, err := wc008DraftQuery(ctx, t, wcReclaimExactSlot, args...)
	if err != nil {
		return out, err
	}
	raw, err := scanWCReclaimSlot(row)
	found := receiptFacts{}
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	} else if err != nil {
		return out, err
	} else {
		slot, version, e := decodeWCReclaimSlotShape(raw, s)
		if e != nil || !slot.present || slot.reference.Identity.Sequence != expected.command.Sequence {
			return out, errors.Join(ErrResolveRequired, e)
		}
		out.snapshot.slot, out.snapshot.resultVersion = slot, version
		out.value, out.digest = raw.value(), ResultDigest(slot.resultDigest)
		found = receiptFacts{true, receiptIdentity{slot.reference.Identity, slot.reference.RequestDigest}, purpose, out.value.ResultCount, slot.resultDigest}
	}
	out.decision, err = classifyReceipt(expected, purpose, out.owner, found)
	if err != nil {
		return out, err
	}
	if out.decision == receiptReplay {
		computed, e := canonicalResultDigest(expected.command, expected.digest, out.value)
		if e != nil || computed != out.digest {
			return out, errors.Join(ErrResolveRequired, e)
		}
	}
	if out.decision == receiptFresh {
		out.snapshot, err = readWCReclaimOldSlot(ctx, t, scope, out.snapshot)
	}
	return out, err
}

// Refund0: advances ONLY the shared floor, retaining scalar, debt, bytes and
// due verbatim. In particular floor advancement alone never schedules cleanup.
func acknowledgeWCReclaim(ctx context.Context, t *commandTurn, purpose CommandID, q ReclaimReceiptRequest, lookup wcReclaimLookup) (floor int64, err error) {
	defer func() { floor, err = finishCommand(ctx, commandLifetime(t), floor, err) }()
	if err = checkWCReclaimPurpose(purpose); err != nil {
		return 0, err
	}
	o, i := lookup.owner, q.Receipt.Identity
	if q != lookup.request || !o.exists || q.PlaybackID != lookup.scope.playbackID || i.Incarnation != o.incarnation || i.Sequence <= 0 ||
		i.OperationID == [16]byte{} || q.Receipt.RequestDigest == (RequestDigest{}) ||
		o.acknowledgedSequence < 0 || o.lastSequence < o.acknowledgedSequence || o.lastSequence-o.acknowledgedSequence > 1 {
		return 0, ErrResolveRequired
	}
	if lookup.decision == receiptRetired && i.Sequence <= o.acknowledgedSequence {
		return o.acknowledgedSequence, nil
	}
	slot := lookup.snapshot.slot
	if lookup.decision != receiptReplay || i.Sequence != o.lastSequence || i.Sequence <= o.acknowledgedSequence ||
		!slot.present || slot.reference != q.Receipt || ResultDigest(slot.resultDigest) != lookup.digest {
		return 0, ErrResolveRequired
	}
	args := append([]any{i.Sequence}, wcReclaimScopeArgs(lookup.scope)...)
	args = append(args, o.lastSequence, o.acknowledgedSequence, i.Sequence, i.OperationID[:], q.Receipt.RequestDigest[:], lookup.digest[:])
	floor, err = wc008DraftInteger(ctx, t, wcReclaimAckFloor, args...)
	if err == nil && floor != i.Sequence {
		err = ErrResolveRequired
	}
	return floor, err
}

// Exact source receipt debit is separate from OLD-to-NEW retained header bytes.
// Header/owner row count stays one; no receipt child is inserted for cleanup.
// Parent charges these and the physical terminal on the SAME admitted turn.
type wcReclaimPhysicalEffect struct {
	removedKey                        wcReclaimKey
	removedRows, removedVariableBytes int64
	oldControlBytes, newControlBytes  int64
}

type wcReclaimApplied struct {
	value  ReclaimValue
	digest ResultDigest
	effect wcReclaimPhysicalEffect
}

// Parent supplies the trusted appliedMS after original classification and owns
// Background/NORMAL admission, memory, accounting, commit and public completion.
func applyWCReclaim(ctx context.Context, t *commandTurn, purpose CommandID, q wcReclaimRequest, reference ReclaimReceiptRequest, lookup wcReclaimLookup, appliedMS int64) (out wcReclaimApplied, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if reference != lookup.request || lookup.decision != receiptFresh || q.Scope != lookup.scope || q.Snapshot != lookup.snapshot || reference.PlaybackID != q.Scope.playbackID || appliedMS < 0 {
		return out, ErrConflict
	}
	request, err := wcReclaimRequestDigest(purpose, reference.Receipt.Identity, q)
	if err != nil || request != reference.Receipt.RequestDigest {
		return out, errors.Join(ErrConflict, err)
	}
	s := q.Snapshot
	body := wc008ReclaimCommand{owner: s.owner, identity: reference.Receipt.Identity, oldSlot: s.slot, expectedOldSlot: s.slot, appliedMS: appliedMS, resultVersion: 1, oldResultVersion: s.resultVersion, oldControlBytes: s.controlBytes}
	effect, err := applyWC008ReclaimChild(ctx, t, body)
	if err != nil {
		return out, err
	}
	p := effect.prepared
	out.value = ReclaimValue{CommandKind: wcReclaimKind, ResultVersion: 1, CapturedAcknowledgedSequence: s.owner.acknowledgedSequence, OldAfterSequence: s.owner.afterSequence, RemovedVariableBytes: p.removedVariableBytes, ResultAfterSequence: p.afterSequence, ResultNoNext: p.noNext, AppliedMS: appliedMS, ResultCount: 1}
	if p.remove {
		out.value.RemovedSequence, out.value.RemovedCount = wcPresent(p.key.sequence), 1
	}
	out.digest, err = canonicalResultDigest(reference.Receipt.Identity, request, out.value)
	if err != nil {
		return out, err
	}
	out.effect.oldControlBytes = s.controlBytes
	out.effect.newControlBytes, err = storeWC008ReclaimResult(ctx, t, body, effect, request, out.digest)
	if err != nil {
		return out, err
	}
	out.effect.removedKey, out.effect.removedRows, out.effect.removedVariableBytes = p.key, out.value.RemovedCount, p.removedVariableBytes
	return out, nil
}
