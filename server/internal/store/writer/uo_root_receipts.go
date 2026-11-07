package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Current US disclosure precedes this native original lookup. The root FK is
// retained custody, not authority. Birth is composed into the original current
// registration command, never synthesized from a missing retry owner here.
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
	reference                                           uoRootReference
	owner                                               uoRootOwner
	decision                                            receiptDecision
	payloadBytes, variableBytes, resultCount, appliedMS int64
}

func (q uoRootReference) valid() bool {
	_, err := Describe(q.kind)
	return err == nil && len(q.root) == 22 && utf8.ValidString(q.root) && q.identity.Incarnation != [16]byte{} && q.identity.OperationID != [16]byte{} && q.identity.Sequence > 0 && q.request != [32]byte{}
}

func lookupUORootCommand(ctx context.Context, turn *commandTurn, q uoRootReference) (out uoRootLookup, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if !q.valid() {
		return out, ErrInvalid
	}
	if err = turn.checkLifetimes(ctx); err != nil {
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
	// ACKed originals need no receipt payload/header to remain retired.
	if q.identity.Sequence <= owner.facts.acknowledgedSequence {
		decision, err := classifyReceipt(expected, q.kind, owner.facts, receiptFacts{})
		if err != nil {
			return out, err
		}
		return uoRootLookup{turn: turn, reference: q, owner: owner, decision: decision}, nil
	}
	row, err = wc008DraftQuery(ctx, turn, uoRootReceiptMeta, q.identity.OperationID[:], q.request[:], q.root, q.identity.Incarnation[:], q.identity.Sequence)
	if err != nil {
		return out, err
	}
	var kind, version, count, ms, size, variable, operationMatch, requestMatch, digestBytes int64
	err = row.Scan(&kind, &version, &count, &ms, &size, &variable, &operationMatch, &requestMatch, &digestBytes)
	found := receiptFacts{}
	if err == nil {
		if operationMatch != 1 || requestMatch != 1 || kind != int64(q.kind) {
			return out, ErrConflict
		}
		if version != 1 || count < 0 || count > 10000 || ms < 0 || size < 1 || size > uoRootPayloadCap || variable != size+118 || digestBytes != 32 {
			return out, ErrResolveRequired
		}
		found = receiptFacts{exists: true, identity: expected, kind: q.kind, resultCount: count}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	decision, err := classifyReceipt(expected, q.kind, owner.facts, found)
	if err != nil {
		return out, err
	}
	return uoRootLookup{turn: turn, reference: q, owner: owner, decision: decision, payloadBytes: size, variableBytes: variable, resultCount: count, appliedMS: ms}, nil
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
	if lookup.turn != turn || lookup.decision != receiptReplay || lookup.payloadBytes < 1 || lookup.payloadBytes > uoRootPayloadCap || !lookup.reference.valid() {
		return nil, ResultDigest{}, ErrResolveRequired
	}
	q := lookup.reference
	row, err := wc008DraftQuery(ctx, turn, uoRootReceiptPayload, q.root, q.identity.Incarnation[:], q.identity.Sequence, q.identity.OperationID[:], int64(q.kind), q.request[:], lookup.payloadBytes)
	if err != nil {
		return nil, ResultDigest{}, err
	}
	var stored []byte
	if err = row.Scan(&payload, &stored); err != nil {
		return nil, ResultDigest{}, err
	}
	if int64(len(payload)) != lookup.payloadBytes || len(stored) != 32 {
		return nil, ResultDigest{}, ErrResolveRequired
	}
	copy(digest[:], stored)
	return payload, digest, turn.checkLifetimes(ctx)
}

// Fresh-only scalar effect. W's SAME parent turn funds the complete receipt,
// owner/control and quota delta and owns final dispatch/COMMIT. This is not an
// Applied result, an authority issuer, a quota reservation or an ACK refund.
func persistUORootOperation(ctx context.Context, turn *commandTurn, lookup uoRootLookup, result uoOperationResult) (out ResultDigest, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	q, o := lookup.reference, lookup.owner
	if lookup.turn != turn || !q.valid() || lookup.decision != receiptFresh || o.revision == math.MaxInt64 || o.rows == math.MaxInt64 {
		return out, ErrResolveRequired
	}
	if decision, e := classifyReceipt(receiptIdentity{command: q.identity, digest: q.request}, q.kind, o.facts, receiptFacts{}); e != nil || decision != receiptFresh {
		return out, ErrResolveRequired
	}
	payload, digest, err := encodeUORootOperation(q.identity, q.request, uoRootOperationResult{root: q.root, kind: q.kind, result: result})
	if err != nil {
		return out, err
	}
	bytes := int64(len(payload)) + 118
	if bytes > math.MaxInt64-o.variableBytes {
		return out, ErrInvalid
	}
	actual, err := wc008DraftInteger(ctx, turn, uoRootReceiptInsert, q.root, q.identity.Incarnation[:], q.identity.Sequence, q.identity.OperationID[:], int64(q.kind), q.request[:], digest[:], payload, result.appliedMS)
	if err != nil {
		return out, err
	}
	if actual != bytes {
		return out, ErrResolveRequired
	}
	var due any
	if o.due.Valid {
		due = o.due.Int64
	}
	if _, err = wc008DraftInteger(ctx, turn, uoRootOwnerAdvance, q.identity.Sequence, o.rows+1, o.variableBytes+actual, o.revision+1, q.root, q.identity.Incarnation[:], o.facts.lastSequence, o.facts.acknowledgedSequence, o.revision, o.rows, o.variableBytes, o.afterSequence, due); err != nil {
		return out, err
	}
	return digest, turn.checkLifetimes(ctx)
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
	uoRootOwnerPoint     stmt.ID = "uo.root.owner.point"
	uoRootReceiptMeta    stmt.ID = "uo.root.receipt.meta"
	uoRootReceiptPayload stmt.ID = "uo.root.receipt.payload"
	uoRootReceiptInsert  stmt.ID = "uo.root.receipt.insert"
	uoRootOwnerAdvance   stmt.ID = "uo.root.owner.advance"
)

func uoRootDefinitions() []stmt.Definition {
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	d := func(id stmt.ID, kind stmt.Kind, sql string, p ...stmt.Param) stmt.Definition {
		return stmt.Definition{ID: id, Kind: kind, SQL: sql, Params: p, Owner: "store/writer UO shared root", CallSites: []string{"store/writer/uo_root_receipts.go"}, Plan: stmt.Plan{Hot: true}}
	}
	return []stmt.Definition{
		d(uoRootOwnerPoint, stmt.Read, `SELECT command_incarnation,last_command_sequence,acknowledged_command_sequence,revision,receipt_rows,receipt_variable_bytes,receipt_reclaim_after_sequence,receipt_reclaim_due_ms FROM user_mutation_command_owners WHERE root_id=?`, s("root")),
		d(uoRootReceiptMeta, stmt.Read, `SELECT command_kind,result_version,result_count,applied_ms,octet_length(result_payload),variable_bytes,(operation_id=?),(request_digest=?),octet_length(result_digest) FROM user_mutation_command_receipts WHERE root_id=? AND command_incarnation=? AND command_sequence=?`, b("operation"), b("request"), s("root"), b("incarnation"), i("sequence")),
		d(uoRootReceiptPayload, stmt.Read, `SELECT result_payload,result_digest FROM user_mutation_command_receipts WHERE root_id=? AND command_incarnation=? AND command_sequence=? AND operation_id=? AND command_kind=? AND request_digest=? AND octet_length(result_payload)=? AND octet_length(result_digest)=32`, s("root"), b("incarnation"), i("sequence"), b("operation"), i("kind"), b("request"), i("payload_bytes")),
		d(uoRootReceiptInsert, stmt.Write, `INSERT INTO user_mutation_command_receipts(root_id,command_incarnation,command_sequence,operation_id,command_kind,request_digest,result_version,result_count,result_digest,result_payload,applied_ms) VALUES(?,?,?,?,?,?,1,0,?,?,?) RETURNING variable_bytes`, s("root"), b("incarnation"), i("sequence"), b("operation"), i("kind"), b("request"), b("result_digest"), b("payload"), i("applied")),
		d(uoRootOwnerAdvance, stmt.Write, `UPDATE user_mutation_command_owners SET last_command_sequence=?,receipt_rows=?,receipt_variable_bytes=?,revision=? WHERE root_id=? AND command_incarnation=? AND last_command_sequence=? AND acknowledged_command_sequence=? AND revision=? AND receipt_rows=? AND receipt_variable_bytes=? AND receipt_reclaim_after_sequence=? AND receipt_reclaim_due_ms IS ? RETURNING last_command_sequence`, i("next_sequence"), i("next_rows"), i("next_bytes"), i("next_revision"), s("root"), b("incarnation"), i("old_sequence"), i("old_floor"), i("old_revision"), i("old_rows"), i("old_bytes"), i("old_cursor"), stmt.Param{Name: "old_due", Type: stmt.Integer, Nullable: true}),
	}
}
