package writer

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"math"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Current authority precedes this native original lookup. The root FK is
// retained custody, not authority. Birth is composed into the original current
// registration command, never synthesized from a missing retry owner here.
//
// UO-02/UO-03/UO-05 followers copy this skeleton: valid reference, binding
// check, owner point read, classifyReceipt, then the exact receipt header
// checks. Reclaim slots and per-purpose result schemas stay in their own files.
type uoRootReference struct {
	root     string
	kind     CommandID
	identity CommandIdentity
	request  RequestDigest
}
type uoRootOwner struct {
	facts                                        receiptOwnerFacts
	revision, rows, variableBytes, afterSequence int64
	due                                          sql.NullInt64
}
type uoRootLookup struct {
	turn                                                *commandTurn
	binding                                             *uoRootBinding
	reference                                           uoRootReference
	owner                                               uoRootOwner
	decision                                            receiptDecision
	payloadBytes, variableBytes, resultCount, appliedMS int64
}

// The exact native receipt variable_bytes component of one payload byte:
// root 22 + incarnation 16 + operation 16 + request digest 32 + result digest 32.
const uoRootFixedBytes = 118

func (q uoRootReference) valid() bool {
	_, err := Describe(q.kind)
	return err == nil && len(q.root) == 22 && utf8.ValidString(q.root) && q.identity.Incarnation != [16]byte{} && q.identity.OperationID != [16]byte{} && q.identity.Sequence > 0 && q.request != [32]byte{}
}

// A root receipt belongs to the parent command's own registered purpose. No
// domain number range, caller bool or foreign descriptor can select it.
func (q uoRootReference) validFor(turn *commandTurn) bool {
	return q.valid() && turn != nil && turn.transaction != nil && q.kind == turn.transaction.descriptor.ID
}

func lookupUORootCommand(ctx context.Context, turn *commandTurn, binding *uoRootBinding, q uoRootReference) (out uoRootLookup, err error) {
	defer func() {
		if isUOUserBirthPurpose(q.kind) {
			uoPublicReadingPoison(turn, err)
		}
	}()
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if !q.validFor(turn) || binding == nil {
		return out, ErrInvalid
	}
	if isUOUserBirthPurpose(q.kind) {
		if q.kind != 700 || turn.transaction.birthStart == nil || turn.transaction.birthStart.turn != turn || turn.transaction.birthStart.root.reference != q {
			return out, ErrInvalid
		}
	}
	// Current authority is checked before ANY retained owner/header/payload
	// lookup. A cached stamp or descriptor grants nothing here.
	if err = binding.check(ctx, turn); err != nil {
		return out, err
	}
	// The root PK precedes the command-owner lookup. A missing or foreign root
	// is one ErrUORootGone class, never a retryable or empty result.
	rootState, err := binding.root(ctx, turn, q.root)
	if err != nil {
		return out, err
	}
	row, err := wc008DraftQuery(ctx, turn, uoRootOwnerPoint, q.root)
	if err != nil {
		return out, err
	}
	var owner uoRootOwner
	var incarnation []byte
	err = row.Scan(&incarnation, &owner.facts.lastSequence, &owner.facts.acknowledgedSequence, &owner.revision, &owner.rows, &owner.variableBytes, &owner.afterSequence, &owner.due)
	if errors.Is(err, sql.ErrNoRows) {
		return out, errors.Join(ErrResolveRequired, ErrOwnerUnavailable)
	}
	if err != nil {
		return out, err
	}
	if len(incarnation) != 16 || owner.revision <= 0 || owner.rows < 0 || owner.variableBytes < 0 || (owner.rows == 0) != (owner.variableBytes == 0) || owner.afterSequence < 0 || owner.afterSequence > owner.facts.acknowledgedSequence || owner.due.Valid && owner.due.Int64 < 0 {
		return out, ErrResolveRequired
	}
	copy(owner.facts.incarnation[:], incarnation)
	owner.facts.exists = true
	expected := receiptIdentity{command: q.identity, digest: q.request}
	found := receiptFacts{}
	var size, variable, appliedMS int64
	// ACKed originals need no receipt payload/header to remain retired. UO-02
	// owns the floor movement; this projection only classifies it.
	if q.identity.Sequence > owner.facts.acknowledgedSequence {
		row, err = wc008DraftQuery(ctx, turn, uoRootReceiptMeta, q.root, q.identity.Incarnation[:], q.identity.Sequence)
		if err != nil {
			return out, err
		}
		var kind, version, count, digestBytes int64
		var operation, request []byte
		err = row.Scan(&kind, &version, &count, &appliedMS, &size, &variable, &digestBytes, &operation, &request)
		switch {
		case err == nil:
			if kind != int64(q.kind) || !bytes.Equal(operation, q.identity.OperationID[:]) || !bytes.Equal(request, q.request[:]) {
				return out, ErrConflict
			}
			// Exact prospective bounds precede any payload copy. variable_bytes
			// also proves the stored row is the complete native record.
			if version != 1 || count < 0 || count > 10000 || appliedMS < 0 || size < 1 || size > uoRootPayloadCap || variable != size+uoRootFixedBytes || digestBytes != 32 {
				return out, ErrResolveRequired
			}
			found = receiptFacts{exists: true, identity: expected, kind: q.kind, resultCount: count}
		case !errors.Is(err, sql.ErrNoRows):
			return out, err
		}
	}
	decision, err := classifyReceipt(expected, q.kind, owner.facts, found)
	if err != nil {
		return out, err
	}
	// A draining root keeps an authorized ORIGINAL replay but admits no Fresh
	// effect. This is not a "newest installation root" comparison.
	if decision == receiptFresh && rootState != "active" {
		return out, ErrUORootGone
	}
	// A constructor-time check cannot outlive a revocation or root change during
	// these reads.
	if err = binding.check(ctx, turn); err != nil {
		return out, err
	}
	if rootState, err = binding.root(ctx, turn, q.root); err != nil {
		return out, err
	}
	if decision == receiptFresh && rootState != "active" {
		return out, ErrUORootGone
	}
	return uoRootLookup{turn: turn, binding: binding, reference: q, owner: owner, decision: decision, payloadBytes: size, variableBytes: variable, resultCount: found.resultCount, appliedMS: appliedMS}, nil
}

// The meta point precedes copying any payload. SAME-turn immutable snapshot and
// exact native identity predicates bind both points; callers must decode with
// W's sole closed-purpose schema and verify its original canonical digest.
func readUORootPayload(ctx context.Context, turn *commandTurn, lookup uoRootLookup) (payload []byte, digest ResultDigest, err error) {
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
	if err = lookup.binding.check(ctx, turn); err != nil {
		return nil, ResultDigest{}, err
	}
	q := lookup.reference
	if _, err = lookup.binding.root(ctx, turn, q.root); err != nil {
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
	if _, err = lookup.binding.root(ctx, turn, q.root); err != nil {
		return nil, ResultDigest{}, err
	}
	if err = lookup.binding.check(ctx, turn); err != nil {
		return nil, ResultDigest{}, err
	}
	return payload, digest, turn.checkLifetimes(ctx)
}

// Fresh-only scalar effect. W's SAME parent turn funds the complete receipt,
// owner/control and quota delta and owns final dispatch/COMMIT. This is not an
// Applied result, an authority issuer, a quota reservation or an ACK refund.
func persistUORootOperation(ctx context.Context, turn *commandTurn, lookup uoRootLookup, result uoOperationResult) (out ResultDigest, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	q := lookup.reference
	return persistUORootCanonical(ctx, turn, lookup, uoRootOperationResult{root: q.root, kind: q.kind, result: result})
}

// A finite typed result union, always encoded by W's sole canonical dispatcher.
// Unknown purposes/types cannot smuggle arbitrary BLOBs into root custody.
// Followers add exactly one closed case for their own compiled result type when
// that purpose and its ID land; they do not widen this dispatcher.
func persistUORootCanonical(ctx context.Context, turn *commandTurn, lookup uoRootLookup, result any) (out ResultDigest, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	q, o := lookup.reference, lookup.owner
	defer func() {
		if isUOUserBirthPurpose(q.kind) {
			uoPublicReadingPoison(turn, err)
		}
	}()
	if lookup.turn != turn || lookup.binding == nil || !q.validFor(turn) || lookup.decision != receiptFresh || o.revision == math.MaxInt64 || o.rows == math.MaxInt64 {
		return out, ErrResolveRequired
	}
	if err = lookup.binding.check(ctx, turn); err != nil {
		return out, err
	}
	// B3: a draining root never admits a Fresh effect; persistence rechecks the
	// root's state at the moment of the effect.
	rootState, err := lookup.binding.root(ctx, turn, q.root)
	if err != nil {
		return out, err
	}
	if rootState != "active" {
		return out, ErrUORootGone
	}
	if decision, e := classifyReceipt(receiptIdentity{command: q.identity, digest: q.request}, q.kind, o.facts, receiptFacts{}); e != nil || decision != receiptFresh {
		return out, ErrResolveRequired
	}
	var appliedMS int64
	switch v := result.(type) {
	case uoBirthStartResult:
		if v.root != q.root || q.kind != 700 {
			return out, ErrInvalid
		}
		state := turn.transaction.birthStart
		if state == nil || state.turn != turn || state.saved || state.root.reference != q {
			return out, ErrInvalid
		}
		if err = checkUOBirthStartNative(ctx, turn, state, v); err != nil {
			return out, err
		}
		appliedMS = v.appliedMS
	case uoRootOperationResult:
		if v.root != q.root || v.kind != q.kind {
			return out, ErrInvalid
		}
		appliedMS = v.result.appliedMS
	default:
		return out, ErrInvalid
	}
	payload, digest, err := encodeUORootCanonical(q.identity, q.request, result)
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
	// Recheck the root and current authority before release so a revocation or
	// root change during the effects cannot return a digest that looks Applied.
	rootState, err = lookup.binding.root(ctx, turn, q.root)
	if err != nil {
		return out, err
	}
	if rootState != "active" {
		return out, ErrUORootGone
	}
	if err = lookup.binding.check(ctx, turn); err != nil {
		return out, err
	}
	return digest, turn.checkLifetimes(ctx)
}

// A duplicate operation (UNIQUE) or primary key (PK) on the closed receipt
// INSERT is an identity conflict, never a fresh success or a generic SQL error.
func uoRootInsertError(err error) error {
	var coded interface{ Code() int }
	if errors.As(err, &coded) {
		switch coded.Code() {
		case 1555, 2067: // SQLITE_CONSTRAINT_PRIMARYKEY, SQLITE_CONSTRAINT_UNIQUE
			return errors.Join(ErrConflict, err)
		}
	}
	return err
}

func restoreUORootOperation(ctx context.Context, turn *commandTurn, lookup uoRootLookup) (out uoOperationResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	q := lookup.reference
	payload, digest, err := readUORootPayload(ctx, turn, lookup)
	if err != nil {
		return out, err
	}
	result, err := decodeUORootOperation(q.identity, q.request, q.root, q.kind, payload, digest)
	if err != nil {
		return out, err
	}
	if lookup.resultCount != 0 || result.appliedMS != lookup.appliedMS {
		return out, ErrResolveRequired
	}
	return result, turn.checkLifetimes(ctx)
}

const (
	uoRootIdentityPoint    stmt.ID = "uo.root.identity.point"
	uoRootOwnerPoint       stmt.ID = "uo.root.owner.point"
	uoRootReceiptMeta      stmt.ID = "uo.root.receipt.meta"
	uoRootReceiptPayload   stmt.ID = "uo.root.receipt.payload"
	uoRootReceiptInsert    stmt.ID = "uo.root.receipt.insert"
	uoRootOwnerAdvance     stmt.ID = "uo.root.owner.advance"
	uoRootOwnerAdvanceNull stmt.ID = "uo.root.owner.advance_null"
)

func uoRootDefinitions() []stmt.Definition {
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	d := func(id stmt.ID, kind stmt.Kind, sql string, p ...stmt.Param) stmt.Definition {
		return stmt.Definition{ID: id, Kind: kind, SQL: sql, Params: p, Owner: "store/writer UO shared root", CallSites: []string{"store/writer/uo_root_receipts.go"}, Plan: stmt.Plan{Hot: true}}
	}
	root := "abcdefghijklmnopqrstuv"
	incarnation, operation := bytes.Repeat([]byte{1}, 16), bytes.Repeat([]byte{2}, 16)
	request, digest := bytes.Repeat([]byte{3}, 32), bytes.Repeat([]byte{4}, 32)
	definitions := []stmt.Definition{
		d(uoRootIdentityPoint, stmt.Read, `SELECT account_id,profile_id,account_incarnation,profile_incarnation,family_id,installation_incarnation,state FROM user_mutation_roots WHERE id=?`, s("root")),
		d(uoRootOwnerPoint, stmt.Read, `SELECT command_incarnation,last_command_sequence,acknowledged_command_sequence,revision,receipt_rows,receipt_variable_bytes,receipt_reclaim_after_sequence,receipt_reclaim_due_ms FROM user_mutation_command_owners WHERE root_id=?`, s("root")),
		d(uoRootReceiptMeta, stmt.Read, `SELECT command_kind,result_version,result_count,applied_ms,length(result_payload),variable_bytes,length(result_digest),operation_id,request_digest FROM user_mutation_command_receipts WHERE root_id=? AND command_incarnation=? AND command_sequence=?`, s("root"), b("incarnation"), i("sequence")),
		d(uoRootReceiptPayload, stmt.Read, `SELECT result_payload,result_digest FROM user_mutation_command_receipts WHERE root_id=? AND command_incarnation=? AND command_sequence=? AND operation_id=? AND command_kind=? AND request_digest=? AND length(result_payload)=? AND length(result_digest)=32`, s("root"), b("incarnation"), i("sequence"), b("operation"), i("kind"), b("request"), i("payload_bytes")),
		d(uoRootReceiptInsert, stmt.Write, `INSERT INTO user_mutation_command_receipts(root_id,command_incarnation,command_sequence,operation_id,command_kind,request_digest,result_version,result_count,result_digest,result_payload,applied_ms) VALUES(?,?,?,?,?,?,?,?,?,?,?) RETURNING variable_bytes`, s("root"), b("incarnation"), i("sequence"), b("operation"), i("kind"), b("request"), i("result_version"), i("result_count"), b("result_digest"), b("payload"), i("applied")),
		d(uoRootOwnerAdvance, stmt.Write, `UPDATE user_mutation_command_owners SET last_command_sequence=?,receipt_rows=?,receipt_variable_bytes=?,revision=? WHERE root_id=? AND command_incarnation=? AND last_command_sequence=? AND acknowledged_command_sequence=? AND revision=? AND receipt_rows=? AND receipt_variable_bytes=? AND receipt_reclaim_after_sequence=? AND receipt_reclaim_due_ms=? RETURNING last_command_sequence`, i("next_sequence"), i("next_rows"), i("next_bytes"), i("next_revision"), s("root"), b("incarnation"), i("old_sequence"), i("old_floor"), i("old_revision"), i("old_rows"), i("old_bytes"), i("old_cursor"), i("old_due")),
		d(uoRootOwnerAdvanceNull, stmt.Write, `UPDATE user_mutation_command_owners SET last_command_sequence=?,receipt_rows=?,receipt_variable_bytes=?,revision=? WHERE root_id=? AND command_incarnation=? AND last_command_sequence=? AND acknowledged_command_sequence=? AND revision=? AND receipt_rows=? AND receipt_variable_bytes=? AND receipt_reclaim_after_sequence=? AND receipt_reclaim_due_ms IS NULL RETURNING last_command_sequence`, i("next_sequence"), i("next_rows"), i("next_bytes"), i("next_revision"), s("root"), b("incarnation"), i("old_sequence"), i("old_floor"), i("old_revision"), i("old_rows"), i("old_bytes"), i("old_cursor")),
	}
	bindings := [][]any{
		{root},
		{root},
		{root, incarnation, int64(1)},
		{root, incarnation, int64(1), operation, int64(600), request, int64(512)},
		{root, incarnation, int64(1), operation, int64(600), request, int64(1), int64(0), digest, []byte{1, 2, 3}, int64(100)},
		{int64(2), int64(1), int64(800), int64(2), root, incarnation, int64(1), int64(0), int64(1), int64(0), int64(0), int64(0), int64(0)},
		{int64(2), int64(1), int64(800), int64(2), root, incarnation, int64(1), int64(0), int64(1), int64(0), int64(0), int64(0)},
	}
	for n := range definitions {
		definitions[n].Cohorts = []stmt.Cohort{{Name: "root_custody_point", Bindings: bindings[n]}}
	}
	// Native access paths: both TEXT PKs are automatic unique indexes, while the
	// receipt table is WITHOUT ROWID and seeks its PK directly.
	ownerIndex := "sqlite_autoindex_user_mutation_command_owners_1"
	definitions[0].Plan.Indexes, definitions[0].Plan.Seeks = []string{"sqlite_autoindex_user_mutation_roots_1"}, []string{"id=?"}
	definitions[1].Plan.Indexes, definitions[1].Plan.Seeks = []string{ownerIndex}, []string{"root_id=?"}
	definitions[2].Plan.PrimaryKeys = []string{"user_mutation_command_receipts"}
	definitions[3].Plan.Indexes, definitions[3].Plan.Seeks = []string{"sqlite_autoindex_user_mutation_command_receipts_2"}, []string{"root_id=? AND command_incarnation=? AND operation_id=?"}
	definitions[5].Plan.Indexes, definitions[5].Plan.Seeks = []string{"sqlite_autoindex_user_mutation_command_owners_2"}, []string{"root_id=? AND command_incarnation=?"}
	definitions[6].Plan.Indexes, definitions[6].Plan.Seeks = []string{"sqlite_autoindex_user_mutation_command_owners_2"}, []string{"root_id=? AND command_incarnation=?"}
	return definitions
}
